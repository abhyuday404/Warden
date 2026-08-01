package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/abhyuday404/prava-hack/internal/domain"
	"github.com/abhyuday404/prava-hack/internal/execx"
)

type Vercel struct{ Runner execx.Runner }

func (v Vercel) Info(context.Context) domain.ProviderInfo {
	_, reason := commandAvailable(v.Runner, "vercel")
	return domain.ProviderInfo{
		ID: "vercel", Name: "Vercel", Description: "Static, frontend, and supported serverless framework deployments.",
		Capabilities: []domain.Capability{domain.CapabilityStatic, domain.CapabilityFunctions, domain.CapabilityCustomDomain},
		Billing:      domain.BillingMetered, MerchantURL: "https://vercel.com", Country: "US",
		Available: reason == "", UnavailableReason: reason,
	}
}

func (v Vercel) Estimate(context.Context, domain.ProjectSpec, map[string]string) (domain.CostEstimate, []string, error) {
	return estimateUnknown("USD", "Vercel usage and plan charges are settled through the user's existing Vercel account."), []string{"The budget is a Prava authorization ceiling, not a guaranteed Vercel invoice."}, nil
}

func (v Vercel) Deploy(ctx context.Context, plan domain.Plan, options DeployOptions) (domain.Deployment, error) {
	dep := newDeployment(plan)
	if options.DryRun {
		return finishDeployment(dep, domain.DeploymentPlanned, "dry run: vercel deploy would run"), nil
	}
	args := []string{"deploy", plan.Project.Root, "--yes", "--no-clipboard"}
	if strings.EqualFold(plan.ProviderConfig["production"], "true") {
		args = append(args, "--prod")
	}
	if scope := plan.ProviderConfig["scope"]; scope != "" {
		args = append(args, "--scope", scope)
	}
	result, err := run(ctx, v.Runner, "vercel", plan.Project.Root, args...)
	if err != nil {
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}
	dep.URL = lastURL(result.Stdout + "\n" + result.Stderr)
	dep.ProviderRef = dep.URL
	if dep.URL == "" {
		return finishDeployment(dep, domain.DeploymentDegraded, "deployed, but no deployment URL was found"), nil
	}
	return finishDeployment(dep, domain.DeploymentLive, "Vercel deployment completed"), nil
}

func (v Vercel) Status(ctx context.Context, dep domain.Deployment) (domain.Deployment, error) {
	if dep.ProviderRef == "" {
		return dep, fmt.Errorf("deployment URL is missing")
	}
	result, err := run(ctx, v.Runner, "vercel", "", "inspect", dep.ProviderRef)
	if err != nil {
		return finishDeployment(dep, domain.DeploymentDegraded, err.Error()), err
	}
	if strings.Contains(strings.ToLower(result.Stdout), "ready") {
		return finishDeployment(dep, domain.DeploymentLive, "Vercel reports Ready"), nil
	}
	return finishDeployment(dep, domain.DeploymentProvisioning, strings.TrimSpace(result.Stdout)), nil
}

func (v Vercel) Destroy(ctx context.Context, dep domain.Deployment, dryRun bool) (domain.Deployment, error) {
	if dep.ProviderRef == "" {
		return dep, fmt.Errorf("deployment URL is missing")
	}
	if dryRun {
		return finishDeployment(dep, domain.DeploymentDestroying, "dry run: Vercel deployment would be removed"), nil
	}
	if _, err := run(ctx, v.Runner, "vercel", "", "remove", dep.ProviderRef, "--yes"); err != nil {
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}
	return finishDeployment(dep, domain.DeploymentDestroyed, "Vercel deployment removed"), nil
}
