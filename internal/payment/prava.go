package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/abhyuday404/Warden/internal/domain"
	"github.com/abhyuday404/Warden/internal/execx"
	"github.com/abhyuday404/Warden/internal/ids"
)

type PravaCLI struct{ Runner execx.Runner }

func (PravaCLI) Name() string { return "prava" }

func (p PravaCLI) command() (string, []string, error) {
	if path, err := p.Runner.LookPath("prava"); err == nil {
		return path, nil, nil
	}
	if path, err := p.Runner.LookPath("npx"); err == nil {
		return path, []string{"--yes", "@prava-sdk/cli"}, nil
	}
	return "", nil, fmt.Errorf("Prava CLI is unavailable; install Node.js 20+ and run npm install -g @prava-sdk/cli")
}

func (p PravaCLI) run(ctx context.Context, args ...string) (execx.Result, error) {
	command, prefix, err := p.command()
	if err != nil {
		return execx.Result{}, err
	}
	return p.Runner.Run(ctx, execx.Request{Command: command, Args: append(prefix, args...), Sensitive: true})
}

func (p PravaCLI) Doctor(ctx context.Context) error {
	// Prava CLI 3.1 exposes human-readable status output but no --json flag.
	result, err := p.run(ctx, "status")
	output := result.Stdout + "\n" + result.Stderr
	lower := strings.ToLower(output)
	if strings.Contains(lower, "not linked") || strings.Contains(lower, "no agent configured") {
		return fmt.Errorf("Prava agent is not linked; run ward payment setup")
	}
	if err != nil {
		return err
	}
	status := extractString(output, "status", "state")
	if status == "" {
		return fmt.Errorf("Prava returned an unrecognized link status")
	}
	if !strings.EqualFold(status, "active") && !strings.EqualFold(status, "linked") && !strings.EqualFold(status, "ready") {
		return fmt.Errorf("Prava agent is not linked: %s", status)
	}
	return nil
}

func (p PravaCLI) Setup(ctx context.Context, name, platform, description string) (string, error) {
	args := []string{"setup", "--name", name, "--platform", platform}
	if description != "" {
		args = append(args, "--description", description)
	}
	result, err := p.run(ctx, args...)
	if err != nil {
		return "", err
	}
	return extractURL(result.Stdout), nil
}

func (p PravaCLI) SetupPoll(ctx context.Context) error {
	_, err := p.run(ctx, "setup", "poll")
	return err
}

func (p PravaCLI) Authorize(ctx context.Context, plan domain.Plan, cadence string) (domain.BudgetAuthorization, error) {
	if plan.Provider.MerchantURL == "" || plan.Provider.Country == "" {
		return domain.BudgetAuthorization{}, fmt.Errorf("provider %s has no merchant identity for Prava", plan.Provider.ID)
	}
	if cadence == "" {
		cadence = "monthly"
	}
	if cadence != "monthly" && cadence != "one_time" {
		return domain.BudgetAuthorization{}, fmt.Errorf("unsupported Prava mandate cadence %q", cadence)
	}
	key := "budget-" + plan.ID
	args := []string{
		"mandate", "create", "--merchant-name", plan.Provider.Name,
		"--merchant-url", plan.Provider.MerchantURL, "--merchant-country", plan.Provider.Country,
		"--amount", plan.Budget.Amount, "--currency", plan.Budget.Currency,
		"--frequency", cadence, "--scope", "listed",
		"--product", productJSON(plan.Project.Name, plan.Budget.Amount),
		"-y", "--json",
	}
	result, err := p.run(ctx, args...)
	if err != nil {
		return domain.BudgetAuthorization{}, err
	}
	now := time.Now().UTC()
	status := parsePaymentStatus(extractString(result.Stdout, "status", "state"))
	if status == "" {
		status = domain.PaymentPending
	}
	return domain.BudgetAuthorization{
		ID: ids.New("auth"), PlanID: plan.ID, ProviderID: plan.Provider.ID,
		MerchantURL: plan.Provider.MerchantURL, Budget: plan.Budget, Cadence: cadence,
		Status: status, ApprovalURL: extractString(result.Stdout, "approval_url", "payment_url", "url"),
		ExternalID: extractString(result.Stdout, "mandate_id", "id"), IdempotencyKey: key,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func productJSON(name, amount string) string {
	b, _ := json.Marshal(map[string]any{
		"description": "Infrastructure budget for " + name,
		"unit_price":  amount,
		"quantity":    1,
	})
	return string(b)
}

func (p PravaCLI) Poll(ctx context.Context, auth domain.BudgetAuthorization) (domain.BudgetAuthorization, error) {
	result, err := p.run(ctx, "mandate", "poll", "--merchant", auth.MerchantURL, "--amount", auth.Budget.Amount, "--json")
	if err != nil {
		auth.Status, auth.UpdatedAt = domain.PaymentFailed, time.Now().UTC()
		return auth, err
	}
	status := parsePaymentStatus(extractString(result.Stdout, "status", "state"))
	if status == "" {
		status = domain.PaymentAuthorized
	}
	auth.Status, auth.UpdatedAt = status, time.Now().UTC()
	if auth.ExternalID == "" {
		auth.ExternalID = extractString(result.Stdout, "mandate_id", "id")
	}
	return auth, nil
}

func extractString(output string, keys ...string) string {
	var value any
	trimmed := strings.TrimSpace(output)
	if start := strings.Index(trimmed, "{"); start >= 0 {
		if json.Unmarshal([]byte(trimmed[start:]), &value) == nil {
			if found := findKey(value, keys); found != "" {
				return found
			}
		}
	}
	for _, key := range keys {
		re := regexp.MustCompile(`(?mi)["']?` + regexp.QuoteMeta(key) + `["']?\s*[:=]\s*["']?([^\s,"'}]+)`)
		if m := re.FindStringSubmatch(output); len(m) == 2 {
			return strings.TrimSpace(m[1])
		}
	}
	if contains(keys, "url") || contains(keys, "approval_url") || contains(keys, "payment_url") {
		return extractURL(output)
	}
	return ""
}

func findKey(value any, keys []string) string {
	switch v := value.(type) {
	case map[string]any:
		for _, key := range keys {
			if raw, ok := v[key]; ok {
				switch x := raw.(type) {
				case string:
					return x
				case json.Number:
					return x.String()
				}
			}
		}
		for _, raw := range v {
			if found := findKey(raw, keys); found != "" {
				return found
			}
		}
	case []any:
		for _, raw := range v {
			if found := findKey(raw, keys); found != "" {
				return found
			}
		}
	}
	return ""
}

func extractURL(output string) string {
	re := regexp.MustCompile(`https://[^\s]+`)
	if match := re.FindString(output); match != "" {
		return strings.TrimRight(match, ").,;\"'")
	}
	return ""
}

func parsePaymentStatus(status string) domain.PaymentStatus {
	switch strings.ToLower(status) {
	case "active", "approved", "authorized", "ready":
		return domain.PaymentAuthorized
	case "pending", "awaiting_approval", "created":
		return domain.PaymentPending
	case "expired":
		return domain.PaymentExpired
	case "declined", "rejected":
		return domain.PaymentDeclined
	case "failed", "error":
		return domain.PaymentFailed
	default:
		return ""
	}
}

func contains(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
