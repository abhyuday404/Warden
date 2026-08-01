package payment

import (
	"context"

	"github.com/abhyuday404/prava-hack/internal/domain"
)

type Authorizer interface {
	Name() string
	Authorize(context.Context, domain.Plan, string) (domain.BudgetAuthorization, error)
	Poll(context.Context, domain.BudgetAuthorization) (domain.BudgetAuthorization, error)
	Doctor(context.Context) error
}
