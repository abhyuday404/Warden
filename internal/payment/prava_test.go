package payment

import (
	"context"
	"strings"
	"testing"

	"github.com/abhyuday404/Warden/internal/domain"
	"github.com/abhyuday404/Warden/internal/execx"
)

type fakeRunner struct {
	requests []execx.Request
	outputs  []execx.Result
}

func (f *fakeRunner) LookPath(name string) (string, error) { return name, nil }
func (f *fakeRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	f.requests = append(f.requests, req)
	if len(f.outputs) == 0 {
		return execx.Result{}, nil
	}
	result := f.outputs[0]
	f.outputs = f.outputs[1:]
	return result, nil
}

func TestPravaMandateFlow(t *testing.T) {
	runner := &fakeRunner{outputs: []execx.Result{
		{Stdout: `{"mandate_id":"m_1","approval_url":"https://pay.prava.space/approve","status":"pending"}`},
		{Stdout: `{"mandate_id":"m_1","status":"active"}`},
	}}
	client := PravaCLI{Runner: runner}
	plan := domain.Plan{ID: "plan_1", Project: domain.ProjectSpec{Name: `demo "api"`}, Provider: domain.ProviderInfo{ID: "fly", Name: "Fly.io", MerchantURL: "https://fly.io", Country: "US"}, Budget: domain.Money{Amount: "25.00", Currency: "USD"}}
	auth, err := client.Authorize(context.Background(), plan, "monthly")
	if err != nil {
		t.Fatal(err)
	}
	if auth.Status != domain.PaymentPending || auth.ExternalID != "m_1" || auth.ApprovalURL == "" {
		t.Fatalf("unexpected auth: %#v", auth)
	}
	joined := strings.Join(runner.requests[0].Args, " ")
	if !strings.Contains(joined, `Infrastructure budget for demo \"api\"`) {
		t.Fatalf("product JSON was not safely encoded: %s", joined)
	}
	auth, err = client.Poll(context.Background(), auth)
	if err != nil {
		t.Fatal(err)
	}
	if auth.Status != domain.PaymentAuthorized {
		t.Fatalf("unexpected status: %s", auth.Status)
	}
}

func TestPravaDoctorUsesSupportedHumanReadableStatus(t *testing.T) {
	runner := &fakeRunner{outputs: []execx.Result{{Stdout: "Agent: Warden (aa_test)\nStatus: active\n"}}}
	if err := (PravaCLI{Runner: runner}).Doctor(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(runner.requests[0].Args, " "); got != "status" {
		t.Fatalf("unexpected status invocation: %q", got)
	}
}

func TestPravaDoctorRecognizesUnlinkedAgent(t *testing.T) {
	runner := &fakeRunner{outputs: []execx.Result{{Stdout: `No agent configured. Run: prava setup --name "<name>"`}}}
	err := (PravaCLI{Runner: runner}).Doctor(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ward payment setup") {
		t.Fatalf("unexpected error: %v", err)
	}
}
