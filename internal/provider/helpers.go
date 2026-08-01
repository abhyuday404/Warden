package provider

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/abhyuday404/prava-hack/internal/domain"
	"github.com/abhyuday404/prava-hack/internal/execx"
	"github.com/abhyuday404/prava-hack/internal/ids"
)

func commandAvailable(r execx.Runner, names ...string) (string, string) {
	for _, name := range names {
		if path, err := r.LookPath(name); err == nil {
			return path, ""
		}
	}
	return "", fmt.Sprintf("install one of: %s", strings.Join(names, ", "))
}

func estimateUnknown(currency string, notes ...string) domain.CostEstimate {
	if currency == "" {
		currency = "USD"
	}
	return domain.CostEstimate{
		Kind: "provider_metered", Confidence: "unknown",
		MonthlyMin: domain.Money{Amount: "0.00", Currency: currency},
		MonthlyMax: domain.Money{Amount: "0.00", Currency: currency}, Notes: notes,
	}
}

func newDeployment(plan domain.Plan) domain.Deployment {
	now := time.Now().UTC()
	return domain.Deployment{
		ID: ids.New("dep"), PlanID: plan.ID, ProviderID: plan.Provider.ID,
		Status: domain.DeploymentProvisioning, CreatedAt: now, UpdatedAt: now,
		Metadata: map[string]string{},
	}
}

func finishDeployment(dep domain.Deployment, status domain.DeploymentStatus, message string) domain.Deployment {
	dep.Status, dep.Message, dep.UpdatedAt = status, message, time.Now().UTC()
	return dep
}

func run(ctx context.Context, r execx.Runner, command, dir string, args ...string) (execx.Result, error) {
	return r.Run(ctx, execx.Request{Command: command, Args: args, Dir: dir})
}

func lastURL(output string) string {
	re := regexp.MustCompile(`https://[^\s]+`)
	matches := re.FindAllString(output, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		candidate := strings.TrimRight(matches[i], ").,;\"'")
		if _, err := url.ParseRequestURI(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func safeName(s string) string {
	s = strings.ToLower(filepath.Base(s))
	s = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 36 {
		s = s[:36]
	}
	if s == "" {
		return "app"
	}
	return s
}
