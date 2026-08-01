package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/abhyuday404/prava-hack/internal/domain"
)

type RenderHook struct{ Client *http.Client }

func (r RenderHook) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (r RenderHook) Info(context.Context) domain.ProviderInfo {
	available := os.Getenv("RENDER_DEPLOY_HOOK") != ""
	reason := ""
	if !available {
		reason = "set RENDER_DEPLOY_HOOK for an existing Render service"
	}
	return domain.ProviderInfo{
		ID: "render", Name: "Render", Description: "Trigger a deployment for an existing Git-connected Render service.",
		Capabilities: []domain.Capability{domain.CapabilityStatic, domain.CapabilityBuildpack, domain.CapabilityWorker, domain.CapabilityCustomDomain},
		Billing:      domain.BillingMetered, MerchantURL: "https://render.com", Country: "US",
		Available: available, UnavailableReason: reason,
	}
}

func (r RenderHook) Estimate(context.Context, domain.ProjectSpec, map[string]string) (domain.CostEstimate, []string, error) {
	return estimateUnknown("USD", "Render charges the existing service according to its configured plan and usage."), []string{"Deploy hooks operate only on an already configured Render service."}, nil
}

func (r RenderHook) Deploy(ctx context.Context, plan domain.Plan, options DeployOptions) (domain.Deployment, error) {
	dep := newDeployment(plan)
	hook := plan.ProviderConfig["deploy_hook"]
	if hook == "" {
		hook = os.Getenv("RENDER_DEPLOY_HOOK")
	}
	if hook == "" {
		return finishDeployment(dep, domain.DeploymentFailed, "Render deploy hook is missing"), fmt.Errorf("set RENDER_DEPLOY_HOOK or deploy.config.deploy_hook")
	}
	if options.DryRun {
		return finishDeployment(dep, domain.DeploymentPlanned, "dry run: Render deploy hook would be triggered"), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook, nil)
	if err != nil {
		return dep, err
	}
	resp, err := r.client().Do(req)
	if err != nil {
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("Render deploy hook returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
		return finishDeployment(dep, domain.DeploymentFailed, err.Error()), err
	}
	dep.ProviderRef = strings.TrimSpace(string(body))
	return finishDeployment(dep, domain.DeploymentProvisioning, "Render accepted the deploy hook"), nil
}

func (r RenderHook) Status(_ context.Context, dep domain.Deployment) (domain.Deployment, error) {
	return dep, fmt.Errorf("Render deploy hooks do not expose status; configure a Render API adapter for status checks")
}

func (r RenderHook) Destroy(_ context.Context, dep domain.Deployment, _ bool) (domain.Deployment, error) {
	return dep, fmt.Errorf("Render deploy hooks cannot destroy services")
}
