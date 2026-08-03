package payment

import (
	"context"
	"time"

	"github.com/abhyuday404/Warden/internal/domain"
	"github.com/abhyuday404/Warden/internal/ids"
)

// Manual is an explicit development authorizer. It never contacts a payment
// network and must not be presented as a Prava authorization.
type Manual struct{}

func (Manual) Name() string                 { return "manual-development" }
func (Manual) Doctor(context.Context) error { return nil }

func (Manual) Authorize(_ context.Context, plan domain.Plan, cadence string) (domain.BudgetAuthorization, error) {
	now := time.Now().UTC()
	return domain.BudgetAuthorization{
		ID: ids.New("authdev"), PlanID: plan.ID, ProviderID: plan.Provider.ID,
		MerchantURL: plan.Provider.MerchantURL, Budget: plan.Budget, Cadence: cadence,
		Status: domain.PaymentAuthorized, ExternalID: "development-only",
		IdempotencyKey: "development-" + plan.ID, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (Manual) Poll(_ context.Context, auth domain.BudgetAuthorization) (domain.BudgetAuthorization, error) {
	auth.Status, auth.UpdatedAt = domain.PaymentAuthorized, time.Now().UTC()
	return auth, nil
}
