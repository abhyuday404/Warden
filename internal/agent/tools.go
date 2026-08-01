package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abhyuday404/prava-hack/internal/app"
	"github.com/abhyuday404/prava-hack/internal/domain"
)

type Action struct {
	Kind        string
	Summary     string
	Destructive bool
	External    bool
	Cost        *domain.Money
}

type Gate interface {
	Approve(context.Context, Action) error
}

type Tools struct {
	Service *app.Service
	Gate    Gate
}

func (t Tools) Tools() []FunctionTool {
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": append([]string{}, required...), "additionalProperties": false}
	}
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	boolean := func(description string) map[string]any {
		return map[string]any{"type": "boolean", "description": description}
	}
	return []FunctionTool{
		{Type: "function", Name: "inspect_project", Description: "Inspect the current repository and return its normalized runtime and build requirements. Read-only.", Parameters: object(map[string]any{}), Strict: true},
		{Type: "function", Name: "list_providers", Description: "List provider compatibility and local availability for the inspected project. Read-only.", Parameters: object(map[string]any{}), Strict: true},
		{Type: "function", Name: "create_plan", Description: "Create and persist a deterministic deployment plan. This does not deploy or spend money.", Parameters: object(map[string]any{
			"provider": str("Provider ID or auto."), "budget_amount": str("Maximum authorized amount as a decimal string; use 0.00 only for no-cost local deployment."), "currency": str("Three-letter ISO 4217 currency code."),
		}, "provider", "budget_amount", "currency"), Strict: true},
		{Type: "function", Name: "authorize_budget", Description: "Create a Prava merchant-scoped mandate for a plan's budget. Requires host-side user approval and does not itself charge the cloud provider.", Parameters: object(map[string]any{
			"plan_id": str("Deployment plan ID."), "cadence": str("monthly or one_time."),
		}, "plan_id", "cadence"), Strict: true},
		{Type: "function", Name: "poll_budget_authorization", Description: "Wait for the owner to approve a previously created Prava budget mandate.", Parameters: object(map[string]any{"authorization_id": str("Budget authorization ID.")}, "authorization_id"), Strict: true},
		{Type: "function", Name: "deploy_project", Description: "Provision the selected plan. The non-dry-run path requires authorization and host-side approval.", Parameters: object(map[string]any{
			"plan_id": str("Deployment plan ID."), "dry_run": boolean("When true, validate without external provisioning."),
		}, "plan_id", "dry_run"), Strict: true},
		{Type: "function", Name: "deployment_status", Description: "Read the latest status of a deployment from its provider.", Parameters: object(map[string]any{"deployment_id": str("Deployment ID.")}, "deployment_id"), Strict: true},
		{Type: "function", Name: "destroy_deployment", Description: "Destroy a deployment. This is destructive and always requires a host-side approval gate unless it is a dry run.", Parameters: object(map[string]any{
			"deployment_id": str("Deployment ID."), "dry_run": boolean("When true, report what would be destroyed."),
		}, "deployment_id", "dry_run"), Strict: true},
	}
}

func (t Tools) Call(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	if t.Service == nil {
		return nil, fmt.Errorf("agent service is not configured")
	}
	switch name {
	case "inspect_project":
		spec, manifest, path, err := t.Service.Inspect()
		return map[string]any{"project": spec, "manifest": manifest, "manifest_path": path}, err
	case "list_providers":
		return t.Service.Providers(ctx)
	case "create_plan":
		var args struct {
			Provider     string `json:"provider"`
			BudgetAmount string `json:"budget_amount"`
			Currency     string `json:"currency"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		return t.Service.CreatePlan(ctx, args.Provider, domain.Money{Amount: args.BudgetAmount, Currency: args.Currency})
	case "authorize_budget":
		var args struct {
			PlanID  string `json:"plan_id"`
			Cadence string `json:"cadence"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		plan, err := t.Service.GetPlan(args.PlanID)
		if err != nil {
			return nil, err
		}
		if err := t.approve(ctx, Action{Kind: "authorize_budget", Summary: fmt.Sprintf("Authorize %s %s per %s for %s", plan.Budget.Amount, plan.Budget.Currency, args.Cadence, plan.Provider.Name), External: true, Cost: &plan.Budget}); err != nil {
			return nil, err
		}
		return t.Service.Authorize(ctx, args.PlanID, args.Cadence)
	case "poll_budget_authorization":
		var args struct {
			AuthorizationID string `json:"authorization_id"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		return t.Service.PollAuthorization(ctx, args.AuthorizationID)
	case "deploy_project":
		var args struct {
			PlanID string `json:"plan_id"`
			DryRun bool   `json:"dry_run"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		if !args.DryRun {
			plan, err := t.Service.GetPlan(args.PlanID)
			if err != nil {
				return nil, err
			}
			if err := t.approve(ctx, Action{Kind: "deploy", Summary: fmt.Sprintf("Deploy %s to %s", plan.Project.Name, plan.Provider.Name), External: plan.Provider.Billing != domain.BillingNone, Cost: &plan.Budget}); err != nil {
				return nil, err
			}
		}
		return t.Service.Deploy(ctx, args.PlanID, args.DryRun)
	case "deployment_status":
		var args struct {
			DeploymentID string `json:"deployment_id"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		return t.Service.Status(ctx, args.DeploymentID)
	case "destroy_deployment":
		var args struct {
			DeploymentID string `json:"deployment_id"`
			DryRun       bool   `json:"dry_run"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		if !args.DryRun {
			if err := t.approve(ctx, Action{Kind: "destroy", Summary: "Destroy deployment " + args.DeploymentID, Destructive: true, External: true}); err != nil {
				return nil, err
			}
		}
		return t.Service.Destroy(ctx, args.DeploymentID, args.DryRun)
	default:
		return nil, fmt.Errorf("unknown agent tool %q", name)
	}
}

func (t Tools) approve(ctx context.Context, action Action) error {
	if t.Gate == nil {
		return fmt.Errorf("action %s requires a host-side approval gate", action.Kind)
	}
	return t.Gate.Approve(ctx, action)
}

func decodeArgs(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	return nil
}
