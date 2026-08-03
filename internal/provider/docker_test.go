package provider

import (
	"context"
	"testing"

	"github.com/abhyuday404/Warden/internal/domain"
	"github.com/abhyuday404/Warden/internal/execx"
)

type recordingRunner struct {
	requests []execx.Request
	result   execx.Result
}

func (r *recordingRunner) LookPath(name string) (string, error) { return name, nil }
func (r *recordingRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, req)
	return r.result, nil
}

func TestDockerDryRunPersistsRemoteContext(t *testing.T) {
	runner := &recordingRunner{}
	driver := Docker{Runner: runner}
	plan := domain.Plan{ID: "plan_12345678", Project: domain.ProjectSpec{Name: "demo", Root: t.TempDir(), Build: domain.BuildDockerfile}, Provider: domain.ProviderInfo{ID: "docker"}, ProviderConfig: map[string]string{"docker_context": "prod"}}
	dep, err := driver.Deploy(context.Background(), plan, DeployOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if dep.Metadata["docker_context"] != "prod" || len(runner.requests) != 0 {
		t.Fatalf("unexpected dry run: %#v", dep)
	}
}
