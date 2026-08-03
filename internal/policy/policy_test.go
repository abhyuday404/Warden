package policy

import (
	"testing"

	"github.com/abhyuday404/Warden/internal/config"
	"github.com/abhyuday404/Warden/internal/domain"
)

func TestAuthorizationMustMatchPlan(t *testing.T) {
	plan := domain.Plan{ID: "plan_1", Provider: domain.ProviderInfo{ID: "fly"}, Budget: domain.Money{Amount: "20.00", Currency: "USD"}, BudgetRequired: true}
	auth := &domain.BudgetAuthorization{PlanID: plan.ID, ProviderID: "fly", Budget: plan.Budget, Status: domain.PaymentAuthorized}
	if err := ValidateAuthorization(plan, auth); err != nil {
		t.Fatal(err)
	}
	auth.Budget.Amount = "10.00"
	if err := ValidateAuthorization(plan, auth); err == nil {
		t.Fatal("expected mismatched budget to fail")
	}
}

func TestProviderPolicyIsIndependentFromBudget(t *testing.T) {
	plan := domain.Plan{Provider: domain.ProviderInfo{ID: "fly"}, BudgetRequired: true}
	if err := ValidateProvider(plan, config.Policy{DeniedProviders: []string{"fly"}}); err == nil {
		t.Fatal("expected denied provider")
	}
}

func TestProductionRequiresExplicitPolicy(t *testing.T) {
	plan := domain.Plan{Provider: domain.ProviderInfo{ID: "vercel"}, ProviderConfig: map[string]string{"production": "true"}}
	if err := ValidatePlan(plan, config.Policy{}); err == nil {
		t.Fatal("expected production policy error")
	}
	if err := ValidatePlan(plan, config.Policy{AllowProduction: true}); err != nil {
		t.Fatal(err)
	}
}
