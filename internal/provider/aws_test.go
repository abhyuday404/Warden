package provider

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/abhyuday404/Warden/internal/domain"
	"github.com/abhyuday404/Warden/internal/execx"
)

type awsRunnerStep struct {
	result execx.Result
	err    error
}

type awsRunner struct {
	missing  map[string]bool
	requests []execx.Request
	steps    []awsRunnerStep
}

func (r *awsRunner) LookPath(name string) (string, error) {
	if r.missing[name] {
		return "", errors.New("not found")
	}
	return name, nil
}

func (r *awsRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, request)
	if len(r.steps) == 0 {
		return execx.Result{}, nil
	}
	step := r.steps[0]
	r.steps = r.steps[1:]
	return step.result, step.err
}

func TestAWSInfoReportsAllMissingPrerequisites(t *testing.T) {
	runner := &awsRunner{missing: map[string]bool{"aws": true, "lightsailctl": true}}
	info := (AWS{Runner: runner}).Info(context.Background())
	if info.Available {
		t.Fatal("expected AWS provider to be unavailable")
	}
	if !strings.Contains(info.UnavailableReason, "aws, lightsailctl") {
		t.Fatalf("unexpected reason: %q", info.UnavailableReason)
	}
	if !reflect.DeepEqual(info.Capabilities, []domain.Capability{domain.CapabilityContainer, domain.CapabilityRegions, domain.CapabilityCustomDomain}) {
		t.Fatalf("unexpected capabilities: %#v", info.Capabilities)
	}
}

func TestAWSDryRunValidatesAndPersistsConfiguration(t *testing.T) {
	runner := &awsRunner{}
	plan := awsTestPlan(t)
	plan.ProviderConfig = map[string]string{
		"region": "ap-south-1", "profile": "staging", "service": "demo-service",
		"container": "web", "image_label": "demo-image", "power": "micro",
		"scale": "2", "health_check_path": "/healthz",
	}
	dep, err := (AWS{Runner: runner}).Deploy(context.Background(), plan, DeployOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if dep.Status != domain.DeploymentPlanned || dep.ProviderRef != "demo-service" {
		t.Fatalf("unexpected deployment: %#v", dep)
	}
	for key, want := range map[string]string{
		"aws_region": "ap-south-1", "aws_profile": "staging", "container": "web",
		"power": "micro", "scale": "2", "port": "8080", "health_check_path": "/healthz",
		"dry_run": "true",
	} {
		if got := dep.Metadata[key]; got != want {
			t.Errorf("metadata[%q] = %q, want %q", key, got, want)
		}
	}
	if len(runner.requests) != 0 {
		t.Fatalf("dry run executed commands: %#v", runner.requests)
	}
}

func TestAWSDeployCreatesServicePushesImageAndStartsDeployment(t *testing.T) {
	runner := &awsRunner{steps: []awsRunnerStep{
		{},
		{result: execx.Result{Stdout: "null\n"}},
		{result: execx.Result{Stdout: `{"containerServiceName":"demo-service","state":"PENDING","url":"demo.example"}`}},
		{result: execx.Result{Stdout: `{"containerServiceName":"demo-service","arn":"arn:aws:lightsail:ap-south-1:123:ContainerService/test","state":"READY","url":"demo.example"}`}},
		{result: execx.Result{Stdout: ":demo-service.demo.1\n"}},
		{result: execx.Result{Stdout: `{"containerServiceName":"demo-service","state":"DEPLOYING","url":"demo.example","nextDeployment":{"version":2,"state":"ACTIVATING"}}`}},
	}}
	plan := awsTestPlan(t)
	plan.ProviderConfig = map[string]string{"region": "ap-south-1", "service": "demo-service"}
	driver := AWS{Runner: runner, PollInterval: time.Millisecond, PollTimeout: time.Second}
	dep, err := driver.Deploy(context.Background(), plan, DeployOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if dep.Status != domain.DeploymentProvisioning || dep.ProviderRef != "demo-service" || dep.URL != "https://demo.example" {
		t.Fatalf("unexpected deployment: %#v", dep)
	}
	if dep.Metadata["image"] != ":demo-service.demo.1" || dep.Metadata["service_created"] != "true" {
		t.Fatalf("unexpected deployment metadata: %#v", dep.Metadata)
	}
	if dep.Metadata["deployment_version"] != "2" {
		t.Fatalf("deployment version = %q, want 2", dep.Metadata["deployment_version"])
	}
	if len(runner.requests) != 6 {
		t.Fatalf("got %d commands, want 6: %#v", len(runner.requests), runner.requests)
	}
	assertRequest(t, runner.requests[0], "docker", []string{"build", "-t", "warden/demo:12345678", plan.Project.Root})
	assertAWSOperation(t, runner.requests[1], "get-container-services")
	assertAWSOperation(t, runner.requests[2], "create-container-service")
	assertAWSOperation(t, runner.requests[3], "get-container-services")
	assertAWSOperation(t, runner.requests[4], "push-container-image")
	assertAWSOperation(t, runner.requests[5], "create-container-service-deployment")

	containersRaw := argumentAfter(t, runner.requests[5].Args, "--containers")
	var containers map[string]struct {
		Image string            `json:"image"`
		Ports map[string]string `json:"ports"`
	}
	if err := json.Unmarshal([]byte(containersRaw), &containers); err != nil {
		t.Fatal(err)
	}
	if containers["app"].Image != ":demo-service.demo.1" || containers["app"].Ports["8080"] != "HTTP" {
		t.Fatalf("unexpected container payload: %s", containersRaw)
	}
	endpointRaw := argumentAfter(t, runner.requests[5].Args, "--public-endpoint")
	var endpoint struct {
		ContainerName string `json:"containerName"`
		ContainerPort int    `json:"containerPort"`
		HealthCheck   struct {
			Path string `json:"path"`
		} `json:"healthCheck"`
	}
	if err := json.Unmarshal([]byte(endpointRaw), &endpoint); err != nil {
		t.Fatal(err)
	}
	if endpoint.ContainerName != "app" || endpoint.ContainerPort != 8080 || endpoint.HealthCheck.Path != "/" {
		t.Fatalf("unexpected public endpoint payload: %s", endpointRaw)
	}
}

func TestAWSStatusMapsProviderStateAndDestroyUsesPersistedRegion(t *testing.T) {
	runner := &awsRunner{steps: []awsRunnerStep{
		{result: execx.Result{Stdout: `{"containerServiceName":"demo-service","state":"RUNNING","url":"https://demo.example","currentDeployment":{"version":3,"state":"ACTIVE"}}`}},
		{},
	}}
	driver := AWS{Runner: runner}
	dep := domain.Deployment{
		ID: "dep_1", ProviderID: "aws", ProviderRef: "demo-service", Status: domain.DeploymentProvisioning,
		Metadata: map[string]string{"aws_region": "ap-south-1", "aws_profile": "staging"},
	}
	updated, err := driver.Status(context.Background(), dep)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != domain.DeploymentLive || updated.Metadata["deployment_version"] != "3" {
		t.Fatalf("unexpected status: %#v", updated)
	}
	destroyed, err := driver.Destroy(context.Background(), updated, false)
	if err != nil {
		t.Fatal(err)
	}
	if destroyed.Status != domain.DeploymentDestroyed || destroyed.URL != "" {
		t.Fatalf("unexpected destroyed deployment: %#v", destroyed)
	}
	if region := argumentAfter(t, runner.requests[1].Args, "--region"); region != "ap-south-1" {
		t.Fatalf("destroy region = %q", region)
	}
	if profile := argumentAfter(t, runner.requests[1].Args, "--profile"); profile != "staging" {
		t.Fatalf("destroy profile = %q", profile)
	}
}

func TestAWSStatusReportsFailedDeployment(t *testing.T) {
	runner := &awsRunner{steps: []awsRunnerStep{{result: execx.Result{Stdout: `{"containerServiceName":"demo-service","state":"READY","currentDeployment":{"version":2,"state":"FAILED"},"stateDetail":{"message":"health check failed"}}`}}}}
	dep := domain.Deployment{ID: "dep_1", ProviderID: "aws", ProviderRef: "demo-service", Metadata: map[string]string{"aws_region": "ap-south-1"}}
	updated, err := (AWS{Runner: runner}).Status(context.Background(), dep)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != domain.DeploymentFailed || !strings.Contains(updated.Message, "health check failed") {
		t.Fatalf("unexpected status: %#v", updated)
	}
}

func TestAWSStatusReportsFailedNextDeployment(t *testing.T) {
	runner := &awsRunner{steps: []awsRunnerStep{{result: execx.Result{Stdout: `{"containerServiceName":"demo-service","state":"RUNNING","currentDeployment":{"version":1,"state":"ACTIVE"},"nextDeployment":{"version":2,"state":"FAILED"},"stateDetail":{"message":"new deployment failed"}}`}}}}
	dep := domain.Deployment{ID: "dep_1", ProviderID: "aws", ProviderRef: "demo-service", Metadata: map[string]string{"aws_region": "ap-south-1"}}
	updated, err := (AWS{Runner: runner}).Status(context.Background(), dep)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != domain.DeploymentFailed || updated.Metadata["deployment_version"] != "2" || !strings.Contains(updated.Message, "next deployment state: failed") {
		t.Fatalf("unexpected status: %#v", updated)
	}
}

func TestAWSAcceptsSingleCharacterServiceName(t *testing.T) {
	runner := &awsRunner{}
	plan := awsTestPlan(t)
	plan.ProviderConfig = map[string]string{"region": "ap-south-1", "service": "a"}
	dep, err := (AWS{Runner: runner}).Deploy(context.Background(), plan, DeployOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if dep.ProviderRef != "a" || dep.Status != domain.DeploymentPlanned {
		t.Fatalf("unexpected deployment: %#v", dep)
	}
}

func TestAWSRejectsInvalidConfigurationWithoutExecutingCommands(t *testing.T) {
	runner := &awsRunner{}
	plan := awsTestPlan(t)
	plan.ProviderConfig = map[string]string{"region": "ap-south-1", "scale": "21"}
	dep, err := (AWS{Runner: runner}).Deploy(context.Background(), plan, DeployOptions{DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "scale") {
		t.Fatalf("expected scale error, got deployment=%#v error=%v", dep, err)
	}
	if len(runner.requests) != 0 {
		t.Fatalf("invalid configuration executed commands: %#v", runner.requests)
	}
}

func awsTestPlan(t *testing.T) domain.Plan {
	t.Helper()
	return domain.Plan{
		ID: "plan_12345678abcdef",
		Project: domain.ProjectSpec{
			Name: "demo", Root: t.TempDir(), Kind: domain.ProjectContainer,
			Build: domain.BuildDockerfile, Port: 8080,
		},
		Provider: domain.ProviderInfo{ID: "aws"},
	}
}

func assertRequest(t *testing.T, request execx.Request, command string, args []string) {
	t.Helper()
	if request.Command != command || !reflect.DeepEqual(request.Args, args) {
		t.Fatalf("request = %#v, want command=%q args=%#v", request, command, args)
	}
}

func assertAWSOperation(t *testing.T, request execx.Request, operation string) {
	t.Helper()
	if request.Command != "aws" || len(request.Args) < 2 || request.Args[0] != "lightsail" || request.Args[1] != operation {
		t.Fatalf("unexpected AWS request: %#v", request)
	}
	if argumentAfter(t, request.Args, "--region") != "ap-south-1" {
		t.Fatalf("AWS request has wrong region: %#v", request.Args)
	}
}

func argumentAfter(t *testing.T, args []string, name string) string {
	t.Helper()
	for index := 0; index+1 < len(args); index++ {
		if args[index] == name {
			return args[index+1]
		}
	}
	t.Fatalf("argument %s not found in %#v", name, args)
	return ""
}
