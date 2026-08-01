package provider

import (
	"context"

	"github.com/abhyuday404/prava-hack/internal/domain"
)

type DeployOptions struct {
	DryRun bool
}

type Driver interface {
	Info(context.Context) domain.ProviderInfo
	Estimate(context.Context, domain.ProjectSpec, map[string]string) (domain.CostEstimate, []string, error)
	Deploy(context.Context, domain.Plan, DeployOptions) (domain.Deployment, error)
	Status(context.Context, domain.Deployment) (domain.Deployment, error)
	Destroy(context.Context, domain.Deployment, bool) (domain.Deployment, error)
}

type Registry struct{ drivers map[string]Driver }

func NewRegistry(drivers ...Driver) *Registry {
	r := &Registry{drivers: map[string]Driver{}}
	for _, d := range drivers {
		info := d.Info(context.Background())
		r.drivers[info.ID] = d
	}
	return r
}

func (r *Registry) Get(id string) (Driver, bool) { d, ok := r.drivers[id]; return d, ok }

func (r *Registry) All() []Driver {
	result := make([]Driver, 0, len(r.drivers))
	for _, d := range r.drivers {
		result = append(result, d)
	}
	return result
}
