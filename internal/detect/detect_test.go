package detect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/abhyuday404/prava-hack/internal/domain"
)

func TestInspectViteProject(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{"name":"demo-ui","scripts":{"build":"vite build"},"dependencies":{"react":"1","vite":"1"}}`)
	write(t, root, ".env.example", "API_URL=\n# ignored\nPUBLIC_MODE=demo\n")
	spec, err := Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "demo-ui" || spec.Kind != domain.ProjectStatic || spec.Framework != "vite" || spec.OutputDir != "dist" {
		t.Fatalf("unexpected spec: %#v", spec)
	}
	if len(spec.Environment) != 2 || spec.Environment[0] != "API_URL" {
		t.Fatalf("unexpected environment keys: %v", spec.Environment)
	}
}

func TestDockerfileIsPortabilityBoundary(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/demo\n\ngo 1.24\n")
	write(t, root, "main.go", "package main\nfunc main() {}\n")
	write(t, root, "Dockerfile", "FROM scratch\nEXPOSE 9090\n")
	spec, err := Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Kind != domain.ProjectContainer || spec.Build != domain.BuildDockerfile || spec.Port != 9090 {
		t.Fatalf("unexpected spec: %#v", spec)
	}
}

func write(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
