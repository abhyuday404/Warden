package policy

import (
	"fmt"
	"strings"

	"github.com/abhyuday404/prava-hack/internal/config"
	"github.com/abhyuday404/prava-hack/internal/domain"
)

func ValidatePlan(plan domain.Plan, cfg config.Policy) error {
	if err := ValidateProvider(plan, cfg); err != nil {
		return err
	}
	if plan.BudgetRequired && zero(plan.Budget.Amount) {
		return fmt.Errorf("provider %s requires a non-zero approved budget", plan.Provider.ID)
	}
	if strings.EqualFold(plan.ProviderConfig["production"], "true") && !cfg.AllowProduction {
		return fmt.Errorf("production deployment is blocked by policy.allow_production")
	}
	return nil
}

func ValidateProvider(plan domain.Plan, cfg config.Policy) error {
	if contains(cfg.DeniedProviders, plan.Provider.ID) {
		return fmt.Errorf("provider %s is denied by policy", plan.Provider.ID)
	}
	if len(cfg.AllowedProviders) > 0 && !contains(cfg.AllowedProviders, plan.Provider.ID) {
		return fmt.Errorf("provider %s is not in policy.allowed_providers", plan.Provider.ID)
	}
	return nil
}

func ValidateAuthorization(plan domain.Plan, auth *domain.BudgetAuthorization) error {
	if !plan.BudgetRequired {
		return nil
	}
	if auth == nil {
		return fmt.Errorf("deployment requires a Prava budget authorization")
	}
	if auth.Status != domain.PaymentAuthorized {
		return fmt.Errorf("budget authorization is %s, not authorized", auth.Status)
	}
	if auth.PlanID != plan.ID {
		return fmt.Errorf("budget authorization belongs to another plan")
	}
	if auth.ProviderID != plan.Provider.ID {
		return fmt.Errorf("budget authorization belongs to another provider")
	}
	if auth.Budget != plan.Budget {
		return fmt.Errorf("authorized budget does not match the plan budget")
	}
	return nil
}

func contains(values []string, candidate string) bool {
	for _, v := range values {
		if strings.EqualFold(v, candidate) {
			return true
		}
	}
	return false
}

func zero(value string) bool { return value == "" || value == "0" || value == "0.0" || value == "0.00" }
