package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/abhyuday404/prava-hack/internal/domain"
	"github.com/abhyuday404/prava-hack/internal/execx"
)

type Fly struct{ Runner execx.Runner }

func (f Fly) binary() (string, string) { return commandAvailable(f.Runner, "flyctl", "fly") }

func (f Fly) Info(context.Context) domain.ProviderInfo {
	_, reason := f.binary()
	return domain.ProviderInfo{
		ID: "fly", Name: "Fly.io", Description: "Container and buildpack services on Fly Machines.",
		Capabilities: []domain.Capability{domain.CapabilityContainer, domain.CapabilityBuildpack, domain.CapabilityTCP, domain.CapabilityVolume, domain.CapabilityRegions, domain.CapabilityCustomDomain},
		Billing:      domain.BillingMetered, MerchantURL: "https://fly.io", Country: "US",
		Available: reason == "", UnavailableReason: reason,
	}
}

func (f Fly) Estimate(context.Context, domain.ProjectSpec, map[string]string) (domain.CostEstimate, []string, error) {
	return estimateUnknown("USD", "Fly.io bills provisioned resources and usage monthly in the user's organization."), []string{"The budget is an authorization ceiling; actual usage may vary."}, nil
}

func (f Fly) Deploy(ctx context.Context, plan domain.Plan, options DeployOptions) (domain.Deployment, error) {
	dep := newDeployment(plan)
	app := plan.ProviderConfig["app"]
	if options.DryRun {
		dep.ProviderRef = app
		return finishDeployment(dep, domain.DeploymentPlanned, "dry run: fly deploy would run"), nil
	}
	binary, reason := f.binary()
	if reason != "" {
		return finishDeployment(dep, domain.DeploymentFailed, reason), fmt.Errorf("%s", reason)
	}
	args := []string{"deploy", "--remote-only", "--yes"}
	if app != "" {
		args = append(args, "--app", app)
	}
	if configPath := plan.ProviderConfig["config"]; configPath != "" {
		args = append(args, "--config", configPath)
	}
	result, err := run(ctx, f.Runner, binary, plan.Project.Root, args...)
	if err != nil {
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}
	dep.ProviderRef = app
	dep.URL = lastURL(result.Stdout + "\n" + result.Stderr)
	if dep.ProviderRef == "" && dep.URL != "" {
		dep.ProviderRef = strings.TrimSuffix(strings.TrimPrefix(dep.URL, "https://"), ".fly.dev")
	}
	return finishDeployment(dep, domain.DeploymentLive, "Fly.io deployment completed"), nil
}

func (f Fly) Status(ctx context.Context, dep domain.Deployment) (domain.Deployment, error) {
	if dep.ProviderRef == "" {
		return dep, fmt.Errorf("Fly app name is missing; set deploy.config.app")
	}
	binary, reason := f.binary()
	if reason != "" {
		return dep, fmt.Errorf("%s", reason)
	}
	result, err := run(ctx, f.Runner, binary, "", "status", "--app", dep.ProviderRef)
	if err != nil {
		return finishDeployment(dep, domain.DeploymentDegraded, err.Error()), err
	}
	return finishDeployment(dep, domain.DeploymentLive, strings.TrimSpace(result.Stdout)), nil
}

func (f Fly) Destroy(ctx context.Context, dep domain.Deployment, dryRun bool) (domain.Deployment, error) {
	if dep.ProviderRef == "" {
		return dep, fmt.Errorf("Fly app name is missing")
	}
	if dryRun {
		return finishDeployment(dep, domain.DeploymentDestroying, "dry run: Fly app would be destroyed"), nil
	}
	binary, reason := f.binary()
	if reason != "" {
		return dep, fmt.Errorf("%s", reason)
	}
	if _, err := run(ctx, f.Runner, binary, "", "apps", "destroy", dep.ProviderRef, "--yes"); err != nil {
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}
	return finishDeployment(dep, domain.DeploymentDestroyed, "Fly app destroyed"), nil
}
