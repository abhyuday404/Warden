package planner

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/abhyuday404/Warden/internal/config"
	"github.com/abhyuday404/Warden/internal/domain"
	"github.com/abhyuday404/Warden/internal/ids"
	"github.com/abhyuday404/Warden/internal/provider"
)

type Candidate struct {
	Provider   domain.ProviderInfo `json:"provider"`
	Compatible bool                `json:"compatible"`
	Score      int                 `json:"score"`
	Missing    []domain.Capability `json:"missing,omitempty"`
}

type Planner struct{ Registry *provider.Registry }

func (p Planner) Candidates(ctx context.Context, spec domain.ProjectSpec) []Candidate {
	required := requiredCapabilities(spec)
	var result []Candidate
	for _, driver := range p.Registry.All() {
		info := driver.Info(ctx)
		missing := missingCapabilities(required, info.Capabilities)
		score := providerScore(info.ID, spec)
		if !info.Available {
			score -= 50
		}
		result = append(result, Candidate{Provider: info, Compatible: len(missing) == 0, Missing: missing, Score: score})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Compatible != result[j].Compatible {
			return result[i].Compatible
		}
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		return result[i].Provider.ID < result[j].Provider.ID
	})
	return result
}

func (p Planner) Create(ctx context.Context, spec domain.ProjectSpec, manifest config.Manifest, requestedProvider string, budget domain.Money) (domain.Plan, error) {
	if p.Registry == nil {
		return domain.Plan{}, fmt.Errorf("provider registry is required")
	}
	if requestedProvider == "" {
		requestedProvider = manifest.Deploy.Provider
	}
	if requestedProvider == "" {
		requestedProvider = "auto"
	}
	if budget.Amount == "" {
		budget.Amount = manifest.Budget.MonthlyMax
	}
	if budget.Currency == "" {
		budget.Currency = manifest.Budget.Currency
	}
	budget.Currency = strings.ToUpper(budget.Currency)
	if budget.Amount != "" && !regexp.MustCompile(`^(0|[1-9]\d*)(\.\d{1,2})?$`).MatchString(budget.Amount) {
		return domain.Plan{}, fmt.Errorf("budget amount must be a non-negative decimal with at most two fractional digits")
	}
	if len(budget.Currency) != 3 {
		return domain.Plan{}, fmt.Errorf("currency must be a three-letter ISO 4217 code")
	}

	candidates := p.Candidates(ctx, spec)
	providerID := requestedProvider
	if providerID == "auto" {
		providerID = ""
		for _, c := range candidates {
			if c.Compatible && c.Provider.Available {
				providerID = c.Provider.ID
				break
			}
		}
		if providerID == "" {
			return domain.Plan{}, fmt.Errorf("no compatible and available provider found; run providers for diagnostics")
		}
	}
	driver, ok := p.Registry.Get(providerID)
	if !ok {
		return domain.Plan{}, fmt.Errorf("unknown provider %q", providerID)
	}
	info := driver.Info(ctx)
	missing := missingCapabilities(requiredCapabilities(spec), info.Capabilities)
	if len(missing) > 0 {
		return domain.Plan{}, fmt.Errorf("provider %s lacks required capabilities: %v", providerID, missing)
	}
	if !info.Available {
		return domain.Plan{}, fmt.Errorf("provider %s is unavailable: %s", providerID, info.UnavailableReason)
	}
	providerConfig := cloneMap(manifest.Deploy.Config)
	if manifest.Deploy.Region != "" && providerConfig["region"] == "" {
		providerConfig["region"] = manifest.Deploy.Region
	}
	cost, warnings, err := driver.Estimate(ctx, spec, providerConfig)
	if err != nil {
		return domain.Plan{}, fmt.Errorf("estimate %s: %w", providerID, err)
	}
	requiresBudget := info.Billing != domain.BillingNone && manifest.Policy.RequireBudgetApproval
	if requiresBudget && (budget.Amount == "" || budget.Amount == "0" || budget.Amount == "0.00") {
		warnings = append(warnings, "External deployment is blocked until a non-zero monthly budget is configured.")
	}
	return domain.Plan{
		ID: ids.New("plan"), CreatedAt: time.Now().UTC(), Project: spec, Provider: info,
		Required: requiredCapabilities(spec), Cost: cost, Budget: budget,
		BudgetRequired: requiresBudget, AuthorizationMode: "prava_mandate",
		Warnings: warnings, ProviderConfig: providerConfig,
	}, nil
}

func requiredCapabilities(spec domain.ProjectSpec) []domain.Capability {
	switch {
	case spec.Kind == domain.ProjectStatic:
		return []domain.Capability{domain.CapabilityStatic}
	case spec.Build == domain.BuildDockerfile || spec.Kind == domain.ProjectContainer:
		return []domain.Capability{domain.CapabilityContainer}
	case spec.Kind == domain.ProjectWorker:
		return []domain.Capability{domain.CapabilityWorker}
	default:
		return []domain.Capability{domain.CapabilityBuildpack}
	}
}

func missingCapabilities(required, offered []domain.Capability) []domain.Capability {
	set := map[domain.Capability]bool{}
	for _, c := range offered {
		set[c] = true
	}
	var missing []domain.Capability
	for _, c := range required {
		if !set[c] {
			missing = append(missing, c)
		}
	}
	return missing
}

func providerScore(id string, spec domain.ProjectSpec) int {
	score := 50
	if spec.Kind == domain.ProjectStatic && id == "vercel" {
		score += 30
	}
	if (spec.Kind == domain.ProjectContainer || spec.Build == domain.BuildDockerfile) && id == "fly" {
		score += 25
	}
	if spec.Build == domain.BuildBuildpack && id == "fly" {
		score += 20
	}
	if id == "docker" {
		score -= 10
	}
	if id == "render" {
		score -= 5
	}
	return score
}

func cloneMap(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
