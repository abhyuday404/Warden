package cli

import (
	"strings"
	"testing"
)

func testWelcomeView() welcomeView {
	return welcomeView{
		Version: "0.3.0", Workspace: `F:\PersonalRepos\Warden`, Project: "demo · node next.js service",
		Model: "gpt-5.6-sol", Reasoning: "medium", AgentReady: true,
	}
}

func TestRenderWideWelcome(t *testing.T) {
	output := renderTerminalWelcome(testWelcomeView(), 90, false)
	for _, expected := range []string{".-====-.", "___    ____", "DEPLOY WITH INTENT", "Warden 0.3.0", "● ready", "/help"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("wide welcome missing %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "\x1b[") {
		t.Fatal("color-disabled welcome contains ANSI escapes")
	}
	assertWelcomeWidth(t, output, 90)
}

func TestRenderMidWelcomeFitsTerminal(t *testing.T) {
	output := renderTerminalWelcome(testWelcomeView(), 52, false)
	for _, expected := range []string{".-====-.", "WARDEN 0.3.0", "deploy with intent", "agent ready"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("mid welcome missing %q:\n%s", expected, output)
		}
	}
	assertWelcomeWidth(t, output, 52)
}

func TestRenderNarrowWelcomeFitsTerminal(t *testing.T) {
	output := renderTerminalWelcome(testWelcomeView(), 34, false)
	if !strings.Contains(output, "WARDEN 0.3.0") || strings.Contains(output, ".-====-.") {
		t.Fatalf("unexpected narrow welcome:\n%s", output)
	}
	assertWelcomeWidth(t, output, 34)
}

func TestRenderWelcomeUsesColorOnlyWhenEnabled(t *testing.T) {
	colored := renderTerminalWelcome(testWelcomeView(), 90, true)
	if !strings.Contains(colored, ansiCyan) || !strings.Contains(colored, ansiGold) || !strings.Contains(colored, ansiReset) {
		t.Fatal("color-enabled welcome is missing palette escapes")
	}
	plain := renderPlainWelcome(testWelcomeView())
	if strings.Contains(plain, "\x1b[") || strings.Contains(plain, ".-====-.") {
		t.Fatalf("non-TTY welcome should remain plain:\n%s", plain)
	}
}

func assertWelcomeWidth(t *testing.T, output string, width int) {
	t.Helper()
	for lineNumber, line := range strings.Split(stripANSI(output), "\n") {
		if got := runeLen(line); got > width {
			t.Fatalf("line %d has width %d, want <= %d: %q", lineNumber+1, got, width, line)
		}
	}
}
