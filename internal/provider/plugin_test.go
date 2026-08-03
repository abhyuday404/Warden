package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/abhyuday404/Warden/internal/domain"
	"github.com/abhyuday404/Warden/internal/execx"
)

type pluginRunner struct{ request execx.Request }

func (r *pluginRunner) LookPath(name string) (string, error) { return name, nil }
func (r *pluginRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	r.request = req
	return execx.Result{Stdout: `{"estimate":{"kind":"fixed","monthly_min":{"amount":"5.00","currency":"USD"},"monthly_max":{"amount":"5.00","currency":"USD"},"confidence":"high"}}`}, nil
}

func TestPluginManifestAndEnvironmentIsolation(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "example.provider.json")
	manifest := map[string]any{
		"protocol": PluginProtocolV1, "id": "example", "name": "Example", "description": "test",
		"command": "example-provider", "environment": []string{"EXAMPLE_TOKEN"},
		"capabilities": []string{"container"}, "billing": "metered", "merchant_url": "https://example.com", "country": "US",
	}
	b, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXAMPLE_TOKEN", "scoped")
	t.Setenv("UNRELATED_SECRET", "must-not-be-forwarded")
	runner := &pluginRunner{}
	drivers, err := LoadPlugins([]string{manifestPath}, runner)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = drivers[0].Estimate(context.Background(), domain.ProjectSpec{Name: "demo"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !runner.request.CleanEnv || len(runner.request.Env) != 1 || runner.request.Env[0] != "EXAMPLE_TOKEN=scoped" {
		t.Fatalf("plugin environment was not scoped: %#v", runner.request)
	}
}

func TestPluginRejectsUnknownProtocol(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.provider.json")
	if err := os.WriteFile(path, []byte(`{"protocol":"v999","id":"bad","name":"Bad","command":"bad","capabilities":["static"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPlugins([]string{path}, &pluginRunner{}); err == nil {
		t.Fatal("expected protocol rejection")
	}
}

func TestPluginAcceptsLegacyProtocolAndUsesItForRequests(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "legacy.provider.json")
	manifest := map[string]any{
		"protocol": legacyPluginProtocolV1, "id": "legacy", "name": "Legacy", "description": "v0.1",
		"command": "legacy-provider", "capabilities": []string{"container"}, "billing": "none",
	}
	b, _ := json.Marshal(manifest)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &pluginRunner{}
	drivers, err := LoadPlugins([]string{path}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := drivers[0].Estimate(context.Background(), domain.ProjectSpec{Name: "demo"}, nil); err != nil {
		t.Fatal(err)
	}
	var request pluginRequest
	if err := json.NewDecoder(runner.request.Stdin).Decode(&request); err != nil {
		t.Fatal(err)
	}
	if request.Protocol != legacyPluginProtocolV1 {
		t.Fatalf("legacy plugin received protocol %q", request.Protocol)
	}
}
