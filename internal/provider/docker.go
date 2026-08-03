package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/abhyuday404/Warden/internal/domain"
	"github.com/abhyuday404/Warden/internal/execx"
)

type Docker struct{ Runner execx.Runner }

func (d Docker) Info(context.Context) domain.ProviderInfo {
	_, reason := commandAvailable(d.Runner, "docker")
	return domain.ProviderInfo{
		ID: "docker", Name: "Docker", Description: "Deploy to the local daemon or a configured remote Docker context.",
		Capabilities: []domain.Capability{domain.CapabilityContainer, domain.CapabilityTCP},
		Billing:      domain.BillingNone, Available: reason == "", UnavailableReason: reason,
	}
}

func (d Docker) Estimate(context.Context, domain.ProjectSpec, map[string]string) (domain.CostEstimate, []string, error) {
	return estimateUnknown("USD", "Docker itself is not billed; the daemon host may incur separate infrastructure charges."), nil, nil
}

func (d Docker) Deploy(ctx context.Context, plan domain.Plan, options DeployOptions) (domain.Deployment, error) {
	dep := newDeployment(plan)
	name := safeName(plan.Project.Name) + "-" + strings.TrimPrefix(dep.ID, "dep_")[:8]
	image := "warden/" + safeName(plan.Project.Name) + ":" + strings.TrimPrefix(plan.ID, "plan_")[:8]
	contextName := plan.ProviderConfig["docker_context"]
	prefix := []string{}
	if contextName != "" {
		prefix = append(prefix, "--context", contextName)
	}
	if options.DryRun {
		dep.ProviderRef = name
		dep.Metadata["image"] = image
		dep.Metadata["dry_run"] = "true"
		if contextName != "" {
			dep.Metadata["docker_context"] = contextName
		}
		return finishDeployment(dep, domain.DeploymentPlanned, "dry run: docker image would be built and started"), nil
	}
	if plan.Project.Build != domain.BuildDockerfile {
		return finishDeployment(dep, domain.DeploymentFailed, "Docker requires a Dockerfile build strategy"), fmt.Errorf("docker provider requires a Dockerfile")
	}
	args := append(append([]string{}, prefix...), "build", "-t", image, plan.Project.Root)
	if _, err := run(ctx, d.Runner, "docker", plan.Project.Root, args...); err != nil {
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}
	port := plan.Project.Port
	if port <= 0 {
		port = 8080
	}
	publish := fmt.Sprintf("127.0.0.1::%d", port)
	if contextName != "" {
		publish = fmt.Sprintf("%d", port)
	}
	args = append(append([]string{}, prefix...), "run", "-d", "--name", name, "-p", publish, image)
	result, err := run(ctx, d.Runner, "docker", plan.Project.Root, args...)
	if err != nil {
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}
	dep.ProviderRef = strings.TrimSpace(result.Stdout)
	dep.Metadata["container_name"], dep.Metadata["image"] = name, image
	if contextName != "" {
		dep.Metadata["docker_context"] = contextName
	}
	portArgs := append(append([]string{}, prefix...), "port", name, fmt.Sprintf("%d/tcp", port))
	portResult, _ := run(ctx, d.Runner, "docker", plan.Project.Root, portArgs...)
	if m := regexp.MustCompile(`:(\d+)\s*$`).FindStringSubmatch(strings.TrimSpace(portResult.Stdout)); len(m) == 2 {
		host := "127.0.0.1"
		if contextName != "" {
			host = plan.ProviderConfig["public_host"]
		}
		if host != "" {
			dep.URL = "http://" + host + ":" + m[1]
		}
	}
	return finishDeployment(dep, domain.DeploymentLive, "container is running"), nil
}

func (d Docker) Status(ctx context.Context, dep domain.Deployment) (domain.Deployment, error) {
	name := dep.Metadata["container_name"]
	if name == "" {
		name = dep.ProviderRef
	}
	args := append(dockerContextArgs(dep), "inspect", "--format", "{{.State.Status}}", name)
	result, err := run(ctx, d.Runner, "docker", "", args...)
	if err != nil {
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}
	status := strings.TrimSpace(result.Stdout)
	if status == "running" {
		return finishDeployment(dep, domain.DeploymentLive, "container is running"), nil
	}
	return finishDeployment(dep, domain.DeploymentDegraded, "container status: "+status), nil
}

func (d Docker) Destroy(ctx context.Context, dep domain.Deployment, dryRun bool) (domain.Deployment, error) {
	name := dep.Metadata["container_name"]
	if name == "" {
		name = dep.ProviderRef
	}
	if dryRun {
		return finishDeployment(dep, domain.DeploymentDestroying, "dry run: container would be removed"), nil
	}
	dep.Status = domain.DeploymentDestroying
	args := append(dockerContextArgs(dep), "rm", "-f", name)
	if _, err := run(ctx, d.Runner, "docker", "", args...); err != nil {
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}
	return finishDeployment(dep, domain.DeploymentDestroyed, "container removed; image retained for recovery"), nil
}

func dockerContextArgs(dep domain.Deployment) []string {
	if contextName := dep.Metadata["docker_context"]; contextName != "" {
		return []string{"--context", contextName}
	}
	return nil
}
