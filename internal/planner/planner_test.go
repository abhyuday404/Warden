package planner

import (
	"context"
	"testing"

	"github.com/abhyuday404/prava-hack/internal/config"
	"github.com/abhyuday404/prava-hack/internal/domain"
	"github.com/abhyuday404/prava-hack/internal/provider"
)

type fakeDriver struct{ info domain.ProviderInfo }

func (f fakeDriver) Info(context.Context) domain.ProviderInfo { return f.info }
func (f fakeDriver) Estimate(context.Context, domain.ProjectSpec, map[string]string) (domain.CostEstimate, []string, error) {
	return domain.CostEstimate{}, nil, nil
}
func (f fakeDriver) Deploy(context.Context, domain.Plan, provider.DeployOptions) (domain.Deployment, error) {
	return domain.Deployment{}, nil
}
func (f fakeDriver) Status(context.Context, domain.Deployment) (domain.Deployment, error) {
	return domain.Deployment{}, nil
}
func (f fakeDriver) Destroy(context.Context, domain.Deployment, bool) (domain.Deployment, error) {
	return domain.Deployment{}, nil
}

func TestAutoSelectsCompatibleAvailableProvider(t *testing.T) {
	registry := provider.NewRegistry(
		fakeDriver{info: domain.ProviderInfo{ID: "vercel", Available: true, Capabilities: []domain.Capability{domain.CapabilityStatic}, Billing: domain.BillingMetered}},
		fakeDriver{info: domain.ProviderInfo{ID: "fly", Available: true, Capabilities: []domain.Capability{domain.CapabilityContainer}, Billing: domain.BillingMetered}},
	)
	p := Planner{Registry: registry}
	manifest := config.Defaults()
	spec := domain.ProjectSpec{Kind: domain.ProjectStatic, Build: domain.BuildStatic}
	plan, err := p.Create(context.Background(), spec, manifest, "auto", domain.Money{Amount: "10.00", Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Provider.ID != "vercel" || !plan.BudgetRequired {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestRejectsIncompatibleProvider(t *testing.T) {
	registry := provider.NewRegistry(fakeDriver{info: domain.ProviderInfo{ID: "vercel", Available: true, Capabilities: []domain.Capability{domain.CapabilityStatic}}})
	p := Planner{Registry: registry}
	_, err := p.Create(context.Background(), domain.ProjectSpec{Kind: domain.ProjectContainer, Build: domain.BuildDockerfile}, config.Defaults(), "vercel", domain.Money{Amount: "10", Currency: "USD"})
	if err == nil {
		t.Fatal("expected incompatibility error")
	}
}
