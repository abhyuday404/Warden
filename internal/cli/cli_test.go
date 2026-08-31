package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abhyuday404/Warden/internal/agent"
)

func TestInspectCommandJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := New()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--json", "inspect"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v, stderr=%s", err, stderr.String())
	}
	var output struct {
		Project struct {
			Kind string `json:"kind"`
		} `json:"project"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output.Project.Kind != "static" {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestProvidersCommandIncludesBuiltInAWS(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := New()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root, "--json", "providers"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v, stderr=%s", err, stderr.String())
	}
	var candidates []struct {
		Provider struct {
			ID string `json:"id"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &candidates); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if candidate.Provider.ID == "aws" {
			return
		}
	}
	t.Fatalf("AWS provider missing from output: %s", stdout.String())
}

func TestInitCommandWritesManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := New()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--root", root, "init"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "warden.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "monthly_max: \"10.00\"") {
		t.Fatalf("unexpected manifest:\n%s", b)
	}
}

func TestManualPaymentRequiresDevelopmentFlag(t *testing.T) {
	cmd := New()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--payment", "manual", "inspect"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "development-only") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInteractiveGateRequiresExecuteAndConfirmation(t *testing.T) {
	gate := &interactiveGate{in: strings.NewReader("yes\n"), out: &bytes.Buffer{}}
	if err := gate.Approve(context.Background(), agent.Action{Kind: "deploy"}); err == nil {
		t.Fatal("expected execute gate")
	}
	gate.execute = true
	if err := gate.Approve(context.Background(), agent.Action{Kind: "deploy", Summary: "deploy test"}); err != nil {
		t.Fatal(err)
	}
}

func TestRootCommandIsWard(t *testing.T) {
	cmd := New()
	if cmd.Use != "ward" {
		t.Fatalf("unexpected root command: %q", cmd.Use)
	}
}

func TestNoArgumentCommandLaunchesInteractiveShell(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_API_KEY", "")
	cmd := New()
	var stdout, stderr bytes.Buffer
	cmd.SetIn(strings.NewReader("/context\n/execute on\n/context\n/inspect\n/exit\n"))
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v, stderr=%s", err, stderr.String())
	}
	output := stdout.String()
	for _, expected := range []string{"Warden", "ward [plan] ›", "execute: false", "execute: true", "kind: static", "Describe what you want"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("interactive output missing %q:\n%s", expected, output)
		}
	}
}

func TestInteractiveAgentErrorDoesNotCloseShell(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_API_KEY", "")
	cmd := New()
	var stdout, stderr bytes.Buffer
	cmd.SetIn(strings.NewReader("please inspect this\n/context\n/exit\n"))
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", root})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "OPENAI_API_KEY is required") || !strings.Contains(stdout.String(), "conversation_items: 0") {
		t.Fatalf("stdout=%s\nstderr=%s", stdout.String(), stderr.String())
	}
}

func TestInteractiveModeRejectsJSONWithoutSubcommand(t *testing.T) {
	cmd := New()
	cmd.SetIn(strings.NewReader("/exit\n"))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "requires a subcommand") {
		t.Fatalf("unexpected error: %v", err)
	}
}
