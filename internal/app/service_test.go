package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abhyuday404/Warden/internal/domain"
	"github.com/abhyuday404/Warden/internal/payment"
	"github.com/abhyuday404/Warden/internal/provider"
)

type serviceDriver struct{}

func (serviceDriver) Info(context.Context) domain.ProviderInfo {
	return domain.ProviderInfo{ID: "cloud", Name: "Cloud", Available: true, Billing: domain.BillingMetered, MerchantURL: "https://cloud.example", Country: "US", Capabilities: []domain.Capability{domain.CapabilityContainer}}
}
func (serviceDriver) Estimate(context.Context, domain.ProjectSpec, map[string]string) (domain.CostEstimate, []string, error) {
	return domain.CostEstimate{Kind: "unknown"}, nil, nil
}
func (serviceDriver) Deploy(_ context.Context, plan domain.Plan, options provider.DeployOptions) (domain.Deployment, error) {
	status := domain.DeploymentLive
	if options.DryRun {
		status = domain.DeploymentPlanned
	}
	now := time.Now().UTC()
	return domain.Deployment{ID: "dep_test", PlanID: plan.ID, ProviderID: "cloud", Status: status, URL: "https://app.example", CreatedAt: now, UpdatedAt: now}, nil
}
func (serviceDriver) Status(_ context.Context, dep domain.Deployment) (domain.Deployment, error) {
	return dep, nil
}
func (serviceDriver) Destroy(_ context.Context, dep domain.Deployment, _ bool) (domain.Deployment, error) {
	dep.Status = domain.DeploymentDestroyed
	return dep, nil
}

func TestServiceRequiresMatchingAuthorizationBeforeDeploy(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch\nEXPOSE 8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := provider.NewRegistry(serviceDriver{})
	svc := New(root, registry, payment.Manual{})
	plan, err := svc.CreatePlan(context.Background(), "cloud", domain.Money{Amount: "20.00", Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Deploy(context.Background(), plan.ID, false); err == nil {
		t.Fatal("expected deployment to require authorization")
	}
	auth, err := svc.Authorize(context.Background(), plan.ID, "monthly")
	if err != nil {
		t.Fatal(err)
	}
	if auth.Status != domain.PaymentAuthorized {
		t.Fatalf("unexpected authorization: %#v", auth)
	}
	dep, err := svc.Deploy(context.Background(), plan.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if dep.Status != domain.DeploymentLive || dep.AuthorizationID != auth.ID {
		t.Fatalf("unexpected deployment: %#v", dep)
	}
}

func TestDryRunDoesNotRequireBudgetAuthorization(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch\nEXPOSE 8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := New(root, provider.NewRegistry(serviceDriver{}), payment.Manual{})
	plan, err := svc.CreatePlan(context.Background(), "cloud", domain.Money{Amount: "0.00", Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := svc.Deploy(context.Background(), plan.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if dep.Status != domain.DeploymentPlanned {
		t.Fatalf("unexpected deployment: %#v", dep)
	}
}
