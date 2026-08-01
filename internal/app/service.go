package app

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/abhyuday404/prava-hack/internal/config"
	"github.com/abhyuday404/prava-hack/internal/detect"
	"github.com/abhyuday404/prava-hack/internal/domain"
	"github.com/abhyuday404/prava-hack/internal/payment"
	"github.com/abhyuday404/prava-hack/internal/planner"
	"github.com/abhyuday404/prava-hack/internal/policy"
	"github.com/abhyuday404/prava-hack/internal/provider"
	"github.com/abhyuday404/prava-hack/internal/state"
)

type Service struct {
	Root     string
	Registry *provider.Registry
	Planner  planner.Planner
	Payments payment.Authorizer
	Store    *state.Store
}

func New(root string, registry *provider.Registry, payments payment.Authorizer) *Service {
	return &Service{Root: root, Registry: registry, Planner: planner.Planner{Registry: registry}, Payments: payments, Store: state.New(state.PathFor(root))}
}

func (s *Service) Inspect() (domain.ProjectSpec, config.Manifest, string, error) {
	spec, err := detect.Inspect(s.Root)
	if err != nil {
		return domain.ProjectSpec{}, config.Manifest{}, "", err
	}
	manifest, path, err := config.Load(spec.Root)
	if err != nil {
		return domain.ProjectSpec{}, config.Manifest{}, "", err
	}
	if err := config.Validate(manifest); err != nil {
		return domain.ProjectSpec{}, config.Manifest{}, "", err
	}
	return config.Apply(spec, manifest), manifest, path, nil
}

func (s *Service) Providers(ctx context.Context) ([]planner.Candidate, error) {
	spec, _, _, err := s.Inspect()
	if err != nil {
		return nil, err
	}
	return s.Planner.Candidates(ctx, spec), nil
}

func (s *Service) CreatePlan(ctx context.Context, providerID string, budget domain.Money) (domain.Plan, error) {
	spec, manifest, _, err := s.Inspect()
	if err != nil {
		return domain.Plan{}, err
	}
	plan, err := s.Planner.Create(ctx, spec, manifest, providerID, budget)
	if err != nil {
		return domain.Plan{}, err
	}
	if err := policy.ValidateProvider(plan, manifest.Policy); err != nil {
		return domain.Plan{}, err
	}
	if err := s.Store.Update(func(j *domain.Journal) error { j.Plans[plan.ID] = plan; return nil }); err != nil {
		return domain.Plan{}, err
	}
	return plan, nil
}

func (s *Service) GetPlan(id string) (domain.Plan, error) {
	j, err := s.Store.Load()
	if err != nil {
		return domain.Plan{}, err
	}
	if id == "" {
		return latestPlan(j)
	}
	plan, ok := j.Plans[id]
	if !ok {
		return domain.Plan{}, fmt.Errorf("plan %s not found", id)
	}
	return plan, nil
}

func (s *Service) Authorize(ctx context.Context, planID, cadence string) (domain.BudgetAuthorization, error) {
	plan, err := s.GetPlan(planID)
	if err != nil {
		return domain.BudgetAuthorization{}, err
	}
	if !plan.BudgetRequired {
		now := time.Now().UTC()
		return domain.BudgetAuthorization{PlanID: plan.ID, ProviderID: plan.Provider.ID, Budget: plan.Budget, Status: domain.PaymentNotRequired, CreatedAt: now, UpdatedAt: now}, nil
	}
	_, manifest, _, err := s.Inspect()
	if err != nil {
		return domain.BudgetAuthorization{}, err
	}
	if err := policy.ValidatePlan(plan, manifest.Policy); err != nil {
		return domain.BudgetAuthorization{}, err
	}
	if s.Payments == nil {
		return domain.BudgetAuthorization{}, fmt.Errorf("payment authorizer is not configured")
	}
	auth, err := s.Payments.Authorize(ctx, plan, cadence)
	if err != nil {
		return domain.BudgetAuthorization{}, err
	}
	if err := s.Store.Update(func(j *domain.Journal) error { j.Authorizations[auth.ID] = auth; return nil }); err != nil {
		return domain.BudgetAuthorization{}, err
	}
	return auth, nil
}

func (s *Service) PollAuthorization(ctx context.Context, authID string) (domain.BudgetAuthorization, error) {
	j, err := s.Store.Load()
	if err != nil {
		return domain.BudgetAuthorization{}, err
	}
	auth, ok := j.Authorizations[authID]
	if !ok {
		return domain.BudgetAuthorization{}, fmt.Errorf("authorization %s not found", authID)
	}
	updated, err := s.Payments.Poll(ctx, auth)
	if saveErr := s.Store.Update(func(j *domain.Journal) error { j.Authorizations[updated.ID] = updated; return nil }); saveErr != nil && err == nil {
		err = saveErr
	}
	return updated, err
}

func (s *Service) Deploy(ctx context.Context, planID string, dryRun bool) (domain.Deployment, error) {
	plan, err := s.GetPlan(planID)
	if err != nil {
		return domain.Deployment{}, err
	}
	_, manifest, _, err := s.Inspect()
	if err != nil {
		return domain.Deployment{}, err
	}
	if dryRun {
		if err := policy.ValidateProvider(plan, manifest.Policy); err != nil {
			return domain.Deployment{}, err
		}
	} else if err := policy.ValidatePlan(plan, manifest.Policy); err != nil {
		return domain.Deployment{}, err
	}
	auth := s.authorizationForPlan(plan.ID)
	if !dryRun {
		if err := policy.ValidateAuthorization(plan, auth); err != nil {
			return domain.Deployment{}, err
		}
	}
	driver, ok := s.Registry.Get(plan.Provider.ID)
	if !ok {
		return domain.Deployment{}, fmt.Errorf("provider %s is not registered", plan.Provider.ID)
	}
	dep, err := driver.Deploy(ctx, plan, provider.DeployOptions{DryRun: dryRun})
	if auth != nil {
		dep.AuthorizationID = auth.ID
	}
	if saveErr := s.Store.Update(func(j *domain.Journal) error { j.Deployments[dep.ID] = dep; return nil }); saveErr != nil && err == nil {
		err = saveErr
	}
	return dep, err
}

func (s *Service) Status(ctx context.Context, deploymentID string) (domain.Deployment, error) {
	dep, err := s.GetDeployment(deploymentID)
	if err != nil {
		return domain.Deployment{}, err
	}
	driver, ok := s.Registry.Get(dep.ProviderID)
	if !ok {
		return dep, fmt.Errorf("provider %s is not registered", dep.ProviderID)
	}
	updated, err := driver.Status(ctx, dep)
	if updated.ID != "" {
		if saveErr := s.Store.Update(func(j *domain.Journal) error { j.Deployments[updated.ID] = updated; return nil }); saveErr != nil && err == nil {
			err = saveErr
		}
	}
	return updated, err
}

func (s *Service) Destroy(ctx context.Context, deploymentID string, dryRun bool) (domain.Deployment, error) {
	dep, err := s.GetDeployment(deploymentID)
	if err != nil {
		return domain.Deployment{}, err
	}
	driver, ok := s.Registry.Get(dep.ProviderID)
	if !ok {
		return dep, fmt.Errorf("provider %s is not registered", dep.ProviderID)
	}
	updated, err := driver.Destroy(ctx, dep, dryRun)
	if updated.ID != "" {
		if saveErr := s.Store.Update(func(j *domain.Journal) error { j.Deployments[updated.ID] = updated; return nil }); saveErr != nil && err == nil {
			err = saveErr
		}
	}
	return updated, err
}

func (s *Service) GetDeployment(id string) (domain.Deployment, error) {
	j, err := s.Store.Load()
	if err != nil {
		return domain.Deployment{}, err
	}
	if id == "" {
		return latestDeployment(j)
	}
	dep, ok := j.Deployments[id]
	if !ok {
		return domain.Deployment{}, fmt.Errorf("deployment %s not found", id)
	}
	return dep, nil
}

func (s *Service) authorizationForPlan(planID string) *domain.BudgetAuthorization {
	j, err := s.Store.Load()
	if err != nil {
		return nil
	}
	var selected *domain.BudgetAuthorization
	for _, auth := range j.Authorizations {
		if auth.PlanID != planID {
			continue
		}
		copy := auth
		if selected == nil || copy.UpdatedAt.After(selected.UpdatedAt) {
			selected = &copy
		}
	}
	return selected
}

func latestPlan(j domain.Journal) (domain.Plan, error) {
	plans := make([]domain.Plan, 0, len(j.Plans))
	for _, plan := range j.Plans {
		plans = append(plans, plan)
	}
	if len(plans) == 0 {
		return domain.Plan{}, fmt.Errorf("no plans found")
	}
	sort.Slice(plans, func(i, k int) bool { return plans[i].CreatedAt.After(plans[k].CreatedAt) })
	return plans[0], nil
}

func latestDeployment(j domain.Journal) (domain.Deployment, error) {
	deps := make([]domain.Deployment, 0, len(j.Deployments))
	for _, dep := range j.Deployments {
		deps = append(deps, dep)
	}
	if len(deps) == 0 {
		return domain.Deployment{}, fmt.Errorf("no deployments found")
	}
	sort.Slice(deps, func(i, k int) bool { return deps[i].CreatedAt.After(deps[k].CreatedAt) })
	return deps[0], nil
}
