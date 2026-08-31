package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/abhyuday404/Warden/internal/domain"
	"github.com/abhyuday404/Warden/internal/execx"
)

const (
	defaultAWSPower        = "nano"
	defaultAWSScale        = 1
	defaultAWSPort         = 8080
	defaultAWSPollInterval = 5 * time.Second
	defaultAWSPollTimeout  = 10 * time.Minute
)

var awsResourceName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

// AWS deploys Dockerfile-based projects to Amazon Lightsail Container Services.
// Authentication remains owned by the AWS CLI; Warden records only resource
// identifiers needed for status checks and cleanup.
type AWS struct {
	Runner       execx.Runner
	PollInterval time.Duration
	PollTimeout  time.Duration
}

type awsSettings struct {
	Region          string
	Profile         string
	Service         string
	Container       string
	ImageLabel      string
	Power           string
	Scale           int
	Port            int
	HealthCheckPath string
}

type awsContainerDeployment struct {
	Version int    `json:"version"`
	State   string `json:"state"`
}

type awsContainerService struct {
	ContainerServiceName string                  `json:"containerServiceName"`
	ARN                  string                  `json:"arn"`
	State                string                  `json:"state"`
	URL                  string                  `json:"url"`
	CurrentDeployment    *awsContainerDeployment `json:"currentDeployment"`
	NextDeployment       *awsContainerDeployment `json:"nextDeployment"`
	StateDetail          *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"stateDetail"`
}

func (a AWS) Info(context.Context) domain.ProviderInfo {
	var missing []string
	for _, command := range []string{"aws", "docker", "lightsailctl"} {
		if _, err := a.Runner.LookPath(command); err != nil {
			missing = append(missing, command)
		}
	}
	reason := ""
	if len(missing) > 0 {
		reason = "install required AWS deployment tools: " + strings.Join(missing, ", ")
	}
	return domain.ProviderInfo{
		ID: "aws", Name: "AWS Lightsail", Description: "Docker containers on Amazon Lightsail Container Services.",
		Capabilities: []domain.Capability{domain.CapabilityContainer, domain.CapabilityRegions, domain.CapabilityCustomDomain},
		Billing:      domain.BillingMetered, MerchantURL: "https://aws.amazon.com", Country: "US",
		Available: reason == "", UnavailableReason: reason,
	}
}

func (a AWS) Estimate(_ context.Context, _ domain.ProjectSpec, config map[string]string) (domain.CostEstimate, []string, error) {
	power := strings.TrimSpace(config["power"])
	if power == "" {
		power = defaultAWSPower
	}
	return estimateUnknown("USD", fmt.Sprintf("AWS bills the Lightsail %s container service and related usage through the user's AWS account.", power)), []string{
		"The estimate does not include data transfer, custom domains, or other AWS resources.",
		"The budget is an authorization ceiling; actual AWS charges may vary.",
	}, nil
}

func (a AWS) Deploy(ctx context.Context, plan domain.Plan, options DeployOptions) (domain.Deployment, error) {
	dep := newDeployment(plan)
	settings, err := a.settings(ctx, plan)
	if err != nil {
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}
	setAWSMetadata(&dep, settings)
	dep.ProviderRef = settings.Service

	if plan.Project.Build != domain.BuildDockerfile {
		err := fmt.Errorf("AWS Lightsail requires a Dockerfile build strategy")
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}
	if options.DryRun {
		dep.Metadata["dry_run"] = "true"
		return finishDeployment(dep, domain.DeploymentPlanned, "dry run: Docker image would be built and deployed to AWS Lightsail"), nil
	}
	if info := a.Info(ctx); !info.Available {
		err := fmt.Errorf("%s", info.UnavailableReason)
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}

	image := "warden/" + safeName(plan.Project.Name) + ":" + stableSuffix(plan.ID)
	dep.Metadata["local_image"] = image
	if _, err := run(ctx, a.Runner, "docker", plan.Project.Root, "build", "-t", image, plan.Project.Root); err != nil {
		err = fmt.Errorf("build AWS deployment image: %w", err)
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}

	service, exists, err := a.getService(ctx, settings)
	if err != nil {
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}
	if !exists {
		result, createErr := a.runAWS(ctx, settings,
			"lightsail", "create-container-service",
			"--service-name", settings.Service,
			"--power", settings.Power,
			"--scale", strconv.Itoa(settings.Scale),
			"--query", "containerService",
			"--output", "json",
		)
		if createErr != nil {
			err = fmt.Errorf("create AWS Lightsail container service: %w", createErr)
			return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
		}
		dep.Metadata["service_created"] = "true"
		if created, parseErr := decodeAWSService(result.Stdout); parseErr == nil && created != nil {
			service = created
			applyAWSService(&dep, created)
		}
	} else {
		dep.Metadata["service_created"] = "false"
		applyAWSService(&dep, service)
	}

	service, err = a.waitForService(ctx, settings)
	if err != nil {
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}
	applyAWSService(&dep, service)

	result, err := a.runAWS(ctx, settings,
		"lightsail", "push-container-image",
		"--service-name", settings.Service,
		"--label", settings.ImageLabel,
		"--image", image,
		"--query", "containerImage.image",
		"--output", "text",
	)
	if err != nil {
		err = fmt.Errorf("push image to AWS Lightsail: %w", err)
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}
	remoteImage := lastNonEmptyLine(result.Stdout)
	if remoteImage == "" || remoteImage == "None" {
		err = fmt.Errorf("push image to AWS Lightsail returned no image reference")
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}
	dep.Metadata["image"] = remoteImage

	containers, _ := json.Marshal(map[string]any{
		settings.Container: map[string]any{
			"image": remoteImage,
			"ports": map[string]string{strconv.Itoa(settings.Port): "HTTP"},
		},
	})
	publicEndpoint, _ := json.Marshal(map[string]any{
		"containerName": settings.Container,
		"containerPort": settings.Port,
		"healthCheck":   map[string]string{"path": settings.HealthCheckPath},
	})
	result, err = a.runAWS(ctx, settings,
		"lightsail", "create-container-service-deployment",
		"--service-name", settings.Service,
		"--containers", string(containers),
		"--public-endpoint", string(publicEndpoint),
		"--query", "containerService",
		"--output", "json",
	)
	if err != nil {
		err = fmt.Errorf("create AWS Lightsail deployment: %w", err)
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}
	if deploying, parseErr := decodeAWSService(result.Stdout); parseErr == nil {
		applyAWSService(&dep, deploying)
	}

	return finishDeployment(dep, domain.DeploymentProvisioning, "AWS Lightsail accepted the container deployment; use status to follow activation"), nil
}

func (a AWS) Status(ctx context.Context, dep domain.Deployment) (domain.Deployment, error) {
	if dep.ProviderRef == "" {
		return dep, fmt.Errorf("AWS Lightsail service name is missing")
	}
	if dep.Metadata == nil {
		dep.Metadata = map[string]string{}
	}
	settings, err := a.settingsForDeployment(ctx, dep)
	if err != nil {
		return dep, err
	}
	service, exists, err := a.getService(ctx, settings)
	if err != nil {
		return finishDeployment(dep, domain.DeploymentDegraded, err.Error()), err
	}
	if !exists {
		dep.URL = ""
		return finishDeployment(dep, domain.DeploymentDestroyed, "AWS Lightsail container service no longer exists"), nil
	}
	applyAWSService(&dep, service)
	return finishDeployment(dep, awsDeploymentStatus(service), awsServiceMessage(service)), nil
}

func (a AWS) Destroy(ctx context.Context, dep domain.Deployment, dryRun bool) (domain.Deployment, error) {
	if dep.ProviderRef == "" {
		return dep, fmt.Errorf("AWS Lightsail service name is missing")
	}
	if dep.Metadata == nil {
		dep.Metadata = map[string]string{}
	}
	if dryRun {
		return finishDeployment(dep, domain.DeploymentDestroying, "dry run: AWS Lightsail container service would be deleted"), nil
	}
	settings, err := a.settingsForDeployment(ctx, dep)
	if err != nil {
		return dep, err
	}
	if _, err := a.runAWS(ctx, settings, "lightsail", "delete-container-service", "--service-name", dep.ProviderRef); err != nil {
		err = fmt.Errorf("delete AWS Lightsail container service: %w", err)
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}
	dep.URL = ""
	return finishDeployment(dep, domain.DeploymentDestroyed, "AWS Lightsail service deletion requested; local Docker image retained"), nil
}

func (a AWS) settings(ctx context.Context, plan domain.Plan) (awsSettings, error) {
	config := plan.ProviderConfig
	settings := awsSettings{
		Region:          strings.TrimSpace(config["region"]),
		Profile:         strings.TrimSpace(config["profile"]),
		Service:         strings.TrimSpace(config["service"]),
		Container:       strings.TrimSpace(config["container"]),
		ImageLabel:      strings.TrimSpace(config["image_label"]),
		Power:           strings.TrimSpace(config["power"]),
		HealthCheckPath: strings.TrimSpace(config["health_check_path"]),
		Scale:           defaultAWSScale,
		Port:            plan.Project.Port,
	}
	if settings.Region == "" {
		settings.Region, _ = os.LookupEnv("AWS_REGION")
	}
	if settings.Region == "" {
		settings.Region, _ = os.LookupEnv("AWS_DEFAULT_REGION")
	}
	if settings.Region == "" {
		settings.Region = a.configuredRegion(ctx, settings.Profile)
	}
	if settings.Service == "" {
		settings.Service = safeName(plan.Project.Name) + "-" + stableSuffix(plan.ID)
	}
	if settings.Container == "" {
		settings.Container = "app"
	}
	if settings.ImageLabel == "" {
		settings.ImageLabel = safeName(plan.Project.Name)
	}
	if settings.Power == "" {
		settings.Power = defaultAWSPower
	}
	if raw := strings.TrimSpace(config["scale"]); raw != "" {
		scale, err := strconv.Atoi(raw)
		if err != nil {
			return awsSettings{}, fmt.Errorf("deploy.config.scale must be an integer from 1 to 20")
		}
		settings.Scale = scale
	}
	if settings.Port <= 0 {
		settings.Port = defaultAWSPort
	}
	if settings.HealthCheckPath == "" {
		settings.HealthCheckPath = "/"
	}
	if err := validateAWSSettings(settings); err != nil {
		return awsSettings{}, err
	}
	return settings, nil
}

func (a AWS) settingsForDeployment(ctx context.Context, dep domain.Deployment) (awsSettings, error) {
	settings := awsSettings{
		Region:  strings.TrimSpace(dep.Metadata["aws_region"]),
		Profile: strings.TrimSpace(dep.Metadata["aws_profile"]),
		Service: dep.ProviderRef,
	}
	if settings.Region == "" {
		settings.Region, _ = os.LookupEnv("AWS_REGION")
	}
	if settings.Region == "" {
		settings.Region, _ = os.LookupEnv("AWS_DEFAULT_REGION")
	}
	if settings.Region == "" {
		settings.Region = a.configuredRegion(ctx, settings.Profile)
	}
	if settings.Region == "" {
		return awsSettings{}, fmt.Errorf("AWS region is missing from deployment metadata; set AWS_REGION")
	}
	return settings, nil
}

func validateAWSSettings(settings awsSettings) error {
	if settings.Region == "" {
		return fmt.Errorf("AWS region is required; set deploy.region, AWS_REGION, or an AWS CLI default region")
	}
	if len(settings.Service) > 63 || !awsResourceName.MatchString(settings.Service) {
		return fmt.Errorf("deploy.config.service must be 1-63 lowercase letters, digits, or interior hyphens")
	}
	if len(settings.Container) > 53 || !awsResourceName.MatchString(settings.Container) {
		return fmt.Errorf("deploy.config.container must be 1-53 lowercase letters, digits, or interior hyphens")
	}
	if len(settings.ImageLabel) > 53 || !awsResourceName.MatchString(settings.ImageLabel) {
		return fmt.Errorf("deploy.config.image_label must be 1-53 lowercase letters, digits, or interior hyphens")
	}
	validPower := map[string]bool{"nano": true, "micro": true, "small": true, "medium": true, "large": true, "xlarge": true}
	if !validPower[settings.Power] {
		return fmt.Errorf("deploy.config.power must be one of nano, micro, small, medium, large, or xlarge")
	}
	if settings.Scale < 1 || settings.Scale > 20 {
		return fmt.Errorf("deploy.config.scale must be an integer from 1 to 20")
	}
	if settings.Port < 1 || settings.Port > 65535 {
		return fmt.Errorf("AWS Lightsail container port must be from 1 to 65535")
	}
	if !strings.HasPrefix(settings.HealthCheckPath, "/") {
		return fmt.Errorf("deploy.config.health_check_path must start with /")
	}
	return nil
}

func (a AWS) configuredRegion(ctx context.Context, profile string) string {
	args := []string{"configure", "get", "region"}
	if profile != "" {
		args = append(args, "--profile", profile)
	}
	result, err := run(ctx, a.Runner, "aws", "", args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}

func (a AWS) runAWS(ctx context.Context, settings awsSettings, args ...string) (execx.Result, error) {
	if settings.Region != "" {
		args = append(args, "--region", settings.Region)
	}
	if settings.Profile != "" {
		args = append(args, "--profile", settings.Profile)
	}
	args = append(args, "--no-cli-pager")
	return run(ctx, a.Runner, "aws", "", args...)
}

func (a AWS) getService(ctx context.Context, settings awsSettings) (*awsContainerService, bool, error) {
	result, err := a.runAWS(ctx, settings,
		"lightsail", "get-container-services",
		"--service-name", settings.Service,
		"--query", "containerServices[0]",
		"--output", "json",
	)
	if err != nil {
		return nil, false, fmt.Errorf("get AWS Lightsail container service: %w", err)
	}
	service, err := decodeAWSService(result.Stdout)
	if err != nil {
		return nil, false, fmt.Errorf("decode AWS Lightsail container service: %w", err)
	}
	return service, service != nil, nil
}

func decodeAWSService(output string) (*awsContainerService, error) {
	output = strings.TrimSpace(output)
	if output == "" || output == "null" || output == "None" {
		return nil, nil
	}
	var service awsContainerService
	if err := json.Unmarshal([]byte(output), &service); err != nil {
		return nil, err
	}
	return &service, nil
}

func (a AWS) waitForService(ctx context.Context, settings awsSettings) (*awsContainerService, error) {
	interval := a.PollInterval
	if interval <= 0 {
		interval = defaultAWSPollInterval
	}
	timeout := a.PollTimeout
	if timeout <= 0 {
		timeout = defaultAWSPollTimeout
	}
	deadline := time.Now().Add(timeout)
	for {
		service, exists, err := a.getService(ctx, settings)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("AWS Lightsail container service %s was not found after creation", settings.Service)
		}
		switch strings.ToUpper(service.State) {
		case "READY", "RUNNING":
			return service, nil
		case "DELETING", "DISABLED":
			return nil, fmt.Errorf("AWS Lightsail container service %s entered state %s", settings.Service, service.State)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for AWS Lightsail container service %s to become ready (last state: %s)", settings.Service, service.State)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func setAWSMetadata(dep *domain.Deployment, settings awsSettings) {
	dep.Metadata["aws_region"] = settings.Region
	dep.Metadata["service"] = settings.Service
	dep.Metadata["container"] = settings.Container
	dep.Metadata["power"] = settings.Power
	dep.Metadata["scale"] = strconv.Itoa(settings.Scale)
	dep.Metadata["port"] = strconv.Itoa(settings.Port)
	dep.Metadata["health_check_path"] = settings.HealthCheckPath
	if settings.Profile != "" {
		dep.Metadata["aws_profile"] = settings.Profile
	}
}

func applyAWSService(dep *domain.Deployment, service *awsContainerService) {
	if service == nil {
		return
	}
	if service.ARN != "" {
		dep.Metadata["service_arn"] = service.ARN
	}
	if service.URL != "" {
		dep.URL = service.URL
		if !strings.Contains(dep.URL, "://") {
			dep.URL = "https://" + dep.URL
		}
	}
	if service.NextDeployment != nil {
		dep.Metadata["deployment_version"] = strconv.Itoa(service.NextDeployment.Version)
	} else if service.CurrentDeployment != nil {
		dep.Metadata["deployment_version"] = strconv.Itoa(service.CurrentDeployment.Version)
	}
}

func awsDeploymentStatus(service *awsContainerService) domain.DeploymentStatus {
	if service == nil {
		return domain.DeploymentDestroyed
	}
	if service.NextDeployment != nil {
		switch strings.ToUpper(service.NextDeployment.State) {
		case "FAILED":
			return domain.DeploymentFailed
		case "ACTIVATING":
			return domain.DeploymentProvisioning
		}
	}
	if service.CurrentDeployment != nil && strings.EqualFold(service.CurrentDeployment.State, "FAILED") {
		return domain.DeploymentFailed
	}
	switch strings.ToUpper(service.State) {
	case "RUNNING":
		if service.CurrentDeployment != nil && strings.EqualFold(service.CurrentDeployment.State, "ACTIVE") {
			return domain.DeploymentLive
		}
		return domain.DeploymentProvisioning
	case "PENDING", "DEPLOYING", "UPDATING":
		return domain.DeploymentProvisioning
	case "DELETING":
		return domain.DeploymentDestroying
	case "READY", "DISABLED":
		return domain.DeploymentDegraded
	default:
		return domain.DeploymentDegraded
	}
}

func awsServiceMessage(service *awsContainerService) string {
	if service == nil {
		return "AWS Lightsail container service no longer exists"
	}
	message := "AWS Lightsail service state: " + strings.ToLower(service.State)
	if service.CurrentDeployment != nil {
		message += "; current deployment state: " + strings.ToLower(service.CurrentDeployment.State)
	}
	if service.NextDeployment != nil {
		message += "; next deployment state: " + strings.ToLower(service.NextDeployment.State)
	}
	if service.StateDetail != nil {
		detail := strings.TrimSpace(service.StateDetail.Message)
		if detail == "" {
			detail = strings.TrimSpace(service.StateDetail.Code)
		}
		if detail != "" {
			message += "; " + detail
		}
	}
	return message
}

func stableSuffix(id string) string {
	id = strings.TrimSpace(id)
	if index := strings.IndexByte(id, '_'); index >= 0 {
		id = id[index+1:]
	}
	id = safeName(id)
	if len(id) > 8 {
		id = id[:8]
	}
	return id
}

func lastNonEmptyLine(output string) string {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
			return line
		}
	}
	return ""
}
