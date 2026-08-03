package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/abhyuday404/Warden/internal/domain"
	"github.com/abhyuday404/Warden/internal/execx"
)

const (
	PluginProtocolV1       = "warden.provider/v1"
	legacyPluginProtocolV1 = "prava-deploy.provider/v1"
)

type PluginManifest struct {
	Protocol     string              `json:"protocol"`
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Description  string              `json:"description"`
	Command      string              `json:"command"`
	Args         []string            `json:"args,omitempty"`
	Environment  []string            `json:"environment,omitempty"`
	Capabilities []domain.Capability `json:"capabilities"`
	Billing      domain.BillingMode  `json:"billing"`
	MerchantURL  string              `json:"merchant_url,omitempty"`
	Country      string              `json:"country,omitempty"`
	manifestPath string
}

type Plugin struct {
	Manifest PluginManifest
	Runner   execx.Runner
}

type pluginRequest struct {
	Protocol   string              `json:"protocol"`
	Method     string              `json:"method"`
	Project    *domain.ProjectSpec `json:"project,omitempty"`
	Plan       *domain.Plan        `json:"plan,omitempty"`
	Deployment *domain.Deployment  `json:"deployment,omitempty"`
	DryRun     bool                `json:"dry_run,omitempty"`
	Config     map[string]string   `json:"config,omitempty"`
}

type pluginResponse struct {
	Estimate   *domain.CostEstimate `json:"estimate,omitempty"`
	Warnings   []string             `json:"warnings,omitempty"`
	Deployment *domain.Deployment   `json:"deployment,omitempty"`
	Error      string               `json:"error,omitempty"`
}

func LoadPlugins(paths []string, runner execx.Runner) ([]Driver, error) {
	var files []string
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("inspect provider plugin %s: %w", path, err)
		}
		if !info.IsDir() {
			files = append(files, path)
			continue
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, fmt.Errorf("read provider plugin directory %s: %w", path, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".provider.json") {
				files = append(files, filepath.Join(path, entry.Name()))
			}
		}
	}
	sort.Strings(files)
	drivers := make([]Driver, 0, len(files))
	seen := map[string]bool{}
	for _, path := range files {
		manifest, err := loadPluginManifest(path)
		if err != nil {
			return nil, err
		}
		if seen[manifest.ID] {
			return nil, fmt.Errorf("duplicate provider plugin ID %q", manifest.ID)
		}
		seen[manifest.ID] = true
		drivers = append(drivers, Plugin{Manifest: manifest, Runner: runner})
	}
	return drivers, nil
}

func loadPluginManifest(path string) (PluginManifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return PluginManifest{}, fmt.Errorf("read provider plugin manifest: %w", err)
	}
	var manifest PluginManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return PluginManifest{}, fmt.Errorf("decode provider plugin manifest %s: %w", path, err)
	}
	if manifest.Protocol != PluginProtocolV1 && manifest.Protocol != legacyPluginProtocolV1 {
		return PluginManifest{}, fmt.Errorf("plugin %s uses unsupported protocol %q", path, manifest.Protocol)
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`).MatchString(manifest.ID) {
		return PluginManifest{}, fmt.Errorf("plugin %s has invalid ID %q", path, manifest.ID)
	}
	if manifest.Name == "" || manifest.Command == "" || len(manifest.Capabilities) == 0 {
		return PluginManifest{}, fmt.Errorf("plugin %s requires name, command, and capabilities", path)
	}
	if manifest.MerchantURL != "" {
		u, err := url.Parse(manifest.MerchantURL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return PluginManifest{}, fmt.Errorf("plugin %s merchant_url must be an absolute HTTPS URL", path)
		}
	}
	if manifest.Country != "" && !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(manifest.Country) {
		return PluginManifest{}, fmt.Errorf("plugin %s country must be an uppercase ISO 3166-1 alpha-2 code", path)
	}
	validBilling := map[domain.BillingMode]bool{
		domain.BillingNone: true, domain.BillingExistingAccount: true, domain.BillingPrepaidCredits: true,
		domain.BillingFixedCheckout: true, domain.BillingMetered: true,
	}
	if !validBilling[manifest.Billing] {
		return PluginManifest{}, fmt.Errorf("plugin %s has invalid billing mode %q", path, manifest.Billing)
	}
	if !filepath.IsAbs(manifest.Command) && strings.ContainsAny(manifest.Command, `/\`) {
		manifest.Command = filepath.Join(filepath.Dir(path), manifest.Command)
	}
	for _, name := range manifest.Environment {
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(name) {
			return PluginManifest{}, fmt.Errorf("plugin %s has invalid environment name %q", path, name)
		}
	}
	manifest.manifestPath = path
	return manifest, nil
}

func (p Plugin) Info(context.Context) domain.ProviderInfo {
	reason := ""
	if _, err := p.Runner.LookPath(p.Manifest.Command); err != nil {
		reason = "plugin executable unavailable: " + err.Error()
	}
	return domain.ProviderInfo{
		ID: p.Manifest.ID, Name: p.Manifest.Name, Description: p.Manifest.Description,
		Capabilities: p.Manifest.Capabilities, Billing: p.Manifest.Billing,
		MerchantURL: p.Manifest.MerchantURL, Country: p.Manifest.Country,
		Available: reason == "", UnavailableReason: reason,
	}
}

func (p Plugin) Estimate(ctx context.Context, project domain.ProjectSpec, config map[string]string) (domain.CostEstimate, []string, error) {
	resp, err := p.call(ctx, pluginRequest{Protocol: p.Manifest.Protocol, Method: "estimate", Project: &project, Config: config})
	if err != nil {
		return domain.CostEstimate{}, nil, err
	}
	if resp.Estimate == nil {
		return domain.CostEstimate{}, resp.Warnings, fmt.Errorf("provider plugin %s returned no estimate", p.Manifest.ID)
	}
	return *resp.Estimate, resp.Warnings, nil
}

func (p Plugin) Deploy(ctx context.Context, plan domain.Plan, options DeployOptions) (domain.Deployment, error) {
	base := newDeployment(plan)
	resp, err := p.call(ctx, pluginRequest{Protocol: p.Manifest.Protocol, Method: "deploy", Plan: &plan, DryRun: options.DryRun})
	if err != nil {
		return finishDeployment(base, domain.DeploymentFailed, err.Error()), err
	}
	if resp.Deployment == nil {
		return finishDeployment(base, domain.DeploymentFailed, "plugin returned no deployment"), fmt.Errorf("provider plugin %s returned no deployment", p.Manifest.ID)
	}
	dep := *resp.Deployment
	dep.ID, dep.PlanID, dep.ProviderID, dep.CreatedAt = base.ID, plan.ID, p.Manifest.ID, base.CreatedAt
	if dep.UpdatedAt.IsZero() {
		dep.UpdatedAt = base.UpdatedAt
	}
	if dep.Metadata == nil {
		dep.Metadata = map[string]string{}
	}
	return dep, nil
}

func (p Plugin) Status(ctx context.Context, dep domain.Deployment) (domain.Deployment, error) {
	resp, err := p.call(ctx, pluginRequest{Protocol: p.Manifest.Protocol, Method: "status", Deployment: &dep})
	if err != nil {
		return dep, err
	}
	if resp.Deployment == nil {
		return dep, fmt.Errorf("provider plugin %s returned no deployment", p.Manifest.ID)
	}
	updated := *resp.Deployment
	updated.ID, updated.PlanID, updated.ProviderID, updated.CreatedAt = dep.ID, dep.PlanID, dep.ProviderID, dep.CreatedAt
	return updated, nil
}

func (p Plugin) Destroy(ctx context.Context, dep domain.Deployment, dryRun bool) (domain.Deployment, error) {
	resp, err := p.call(ctx, pluginRequest{Protocol: p.Manifest.Protocol, Method: "destroy", Deployment: &dep, DryRun: dryRun})
	if err != nil {
		return dep, err
	}
	if resp.Deployment == nil {
		return dep, fmt.Errorf("provider plugin %s returned no deployment", p.Manifest.ID)
	}
	updated := *resp.Deployment
	updated.ID, updated.PlanID, updated.ProviderID, updated.CreatedAt = dep.ID, dep.PlanID, dep.ProviderID, dep.CreatedAt
	return updated, nil
}

func (p Plugin) call(ctx context.Context, request pluginRequest) (pluginResponse, error) {
	input, err := json.Marshal(request)
	if err != nil {
		return pluginResponse{}, err
	}
	env := make([]string, 0, len(p.Manifest.Environment))
	for _, name := range p.Manifest.Environment {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	result, err := p.Runner.Run(ctx, execx.Request{
		Command: p.Manifest.Command, Args: p.Manifest.Args, Stdin: bytes.NewReader(input), Env: env,
		CleanEnv: true, Sensitive: len(env) > 0,
	})
	if err != nil {
		return pluginResponse{}, fmt.Errorf("provider plugin %s: %w", p.Manifest.ID, err)
	}
	var response pluginResponse
	if err := json.Unmarshal([]byte(result.Stdout), &response); err != nil {
		return pluginResponse{}, fmt.Errorf("decode provider plugin %s response: %w", p.Manifest.ID, err)
	}
	if response.Error != "" {
		return response, fmt.Errorf("provider plugin %s: %s", p.Manifest.ID, response.Error)
	}
	return response, nil
}
