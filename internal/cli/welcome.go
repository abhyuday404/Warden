package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/chzyer/readline"
	"github.com/spf13/cobra"
)

const (
	ansiReset  = "\x1b[0m"
	ansiGold   = "\x1b[38;5;220m"
	ansiCyan   = "\x1b[38;5;81m"
	ansiGreen  = "\x1b[38;5;82m"
	ansiMuted  = "\x1b[38;5;245m"
	wideCutoff = 72
	midCutoff  = 46
)

var (
	wardenMascot = []string{
		"    .-====-.",
		"   /| .--. |\\",
		"  /_|/o  o\\|_\\",
		"    |  /\\  |",
		"    | [__] |",
		"    '._||_.'",
		"      /||\\",
		"     /_||_\\",
	}
	wardenWordmark = []string{
		"__        ___    ____  ____  _____ _   _",
		"\\ \\      / / \\  |  _ \\|  _ \\| ____| \\ | |",
		" \\ \\ /\\ / / _ \\ | |_) | | | |  _| |  \\| |",
		"  \\ V  V / ___ \\|  _ <| |_| | |___| |\\  |",
		"   \\_/\\_/_/   \\_\\_| \\_\\____/|_____|_| \\_|",
		"",
		"   DEPLOY WITH INTENT. OPERATE WITH LIMITS.",
		"   agentic deployments, bounded by policy",
	}
)

type terminalUI struct {
	TTY   bool
	Width int
	Color bool
}

type welcomeView struct {
	Version    string
	Workspace  string
	Project    string
	Model      string
	Reasoning  string
	AgentReady bool
}

func detectTerminalUI(cmd *cobra.Command) terminalUI {
	input, inputIsFile := cmd.InOrStdin().(*os.File)
	output, outputIsFile := cmd.OutOrStdout().(*os.File)
	tty := inputIsFile && outputIsFile && input == os.Stdin && output == os.Stdout &&
		readline.IsTerminal(int(input.Fd())) && readline.IsTerminal(int(output.Fd()))
	if !tty {
		return terminalUI{}
	}
	width := readline.GetScreenWidth()
	if width <= 0 {
		width = 80
	}
	color := os.Getenv("NO_COLOR") == "" && os.Getenv("CLICOLOR") != "0" && !strings.EqualFold(os.Getenv("TERM"), "dumb")
	return terminalUI{TTY: true, Width: width, Color: color}
}

func renderTerminalWelcome(view welcomeView, width int, color bool) string {
	if width <= 0 {
		width = 80
	}
	switch {
	case width >= wideCutoff:
		return renderWideWelcome(view, width, color)
	case width >= midCutoff:
		return renderMidWelcome(view, width, color)
	default:
		return renderNarrowWelcome(view, width, color)
	}
}

func renderWideWelcome(view welcomeView, width int, color bool) string {
	var b strings.Builder
	contentWidth := minInt(width-2, 92)
	b.WriteByte('\n')
	for i := range wardenMascot {
		b.WriteString("  ")
		b.WriteString(paint(ansiGold, padRight(wardenMascot[i], 16), color))
		b.WriteString("  ")
		b.WriteString(paint(ansiCyan, wardenWordmark[i], color))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString("  ")
	b.WriteString(paint(ansiMuted, strings.Repeat("─", contentWidth), color))
	b.WriteByte('\n')
	b.WriteString(welcomeRow("version", "Warden "+view.Version, contentWidth, color, false))
	b.WriteString(welcomeRow("workspace", view.Workspace, contentWidth, color, true))
	b.WriteString(welcomeRow("project", view.Project, contentWidth, color, false))
	b.WriteString(welcomeRow("model", view.Model+" · reasoning "+view.Reasoning, contentWidth, color, false))
	agent := paint(ansiGold, "API key required", color)
	if view.AgentReady {
		agent = paint(ansiGreen, "● ready", color)
	}
	b.WriteString(welcomeRowRaw("agent", agent+" · plan-only safety", contentWidth, color))
	b.WriteString("  ")
	b.WriteString(paint(ansiMuted, strings.Repeat("─", contentWidth), color))
	b.WriteString("\n\n  ")
	b.WriteString(paint(ansiCyan, "›", color))
	b.WriteString(" Describe what you want to deploy, or type ")
	b.WriteString(paint(ansiGold, "/help", color))
	b.WriteString(".\n\n")
	return b.String()
}

func renderMidWelcome(view welcomeView, width int, color bool) string {
	var b strings.Builder
	lines := []struct {
		mascot string
		text   string
	}{
		{"  .-====-.", "WARDEN " + view.Version},
		{" /| o  o |\\", "deploy with intent"},
		{"/_|  /\\  |_\\", view.Project},
		{"  '._||_.'", agentMode(view.AgentReady)},
	}
	b.WriteByte('\n')
	for i, line := range lines {
		b.WriteString(paint(ansiGold, padRight(line.mascot, 15), color))
		text := ellipsize(line.text, width-15)
		if i == 0 {
			text = paint(ansiCyan, text, color)
		}
		b.WriteString(text)
		b.WriteByte('\n')
	}
	b.WriteString("\n")
	b.WriteString(paint(ansiMuted, ellipsizeMiddle(view.Workspace, width-2), color))
	b.WriteString("\n")
	b.WriteString(paint(ansiCyan, "›", color))
	b.WriteString(" Ask what to deploy, or type ")
	b.WriteString(paint(ansiGold, "/help", color))
	b.WriteString(".\n\n")
	return b.String()
}

func renderNarrowWelcome(view welcomeView, width int, color bool) string {
	var b strings.Builder
	b.WriteByte('\n')
	b.WriteString(paint(ansiCyan, ellipsize("WARDEN "+view.Version, width), color))
	b.WriteByte('\n')
	b.WriteString(ellipsize(view.Project, width))
	b.WriteByte('\n')
	b.WriteString(paint(ansiMuted, ellipsize(agentMode(view.AgentReady), width), color))
	b.WriteString("\n\n")
	b.WriteString(paint(ansiGold, ellipsize("/help for commands", width), color))
	b.WriteString("\n\n")
	return b.String()
}

func renderPlainWelcome(view welcomeView) string {
	agent := "unavailable until OPENAI_API_KEY is set"
	if view.AgentReady {
		agent = "ready"
	}
	return fmt.Sprintf("\nWarden %s\nWorkspace: %s\nProject:   %s\nModel:     %s · reasoning %s\nAgent:     %s\nMode:      plan-only; use /execute on to permit approved actions\n\nDescribe what you want in plain English, or type /help for commands.\n\n",
		view.Version, view.Workspace, view.Project, view.Model, view.Reasoning, agent)
}

func welcomeRow(label, value string, width int, color, middle bool) string {
	prefix := fmt.Sprintf("  %-10s", label)
	available := maxInt(width-runeLen(prefix), 1)
	if middle {
		value = ellipsizeMiddle(value, available)
	} else {
		value = ellipsize(value, available)
	}
	return paint(ansiMuted, prefix, color) + value + "\n"
}

func welcomeRowRaw(label, value string, width int, color bool) string {
	prefix := fmt.Sprintf("  %-10s", label)
	plainValue := stripANSI(value)
	if runeLen(prefix)+runeLen(plainValue) > width {
		value = ellipsize(plainValue, maxInt(width-runeLen(prefix), 1))
	}
	return paint(ansiMuted, prefix, color) + value + "\n"
}

func agentMode(ready bool) string {
	if ready {
		return "● agent ready · plan-only"
	}
	return "agent needs OPENAI_API_KEY · plan-only"
}

func paint(code, text string, enabled bool) string {
	if !enabled || text == "" {
		return text
	}
	return code + text + ansiReset
}

func padRight(value string, width int) string {
	if missing := width - runeLen(value); missing > 0 {
		return value + strings.Repeat(" ", missing)
	}
	return value
}

func ellipsize(value string, width int) string {
	runes := []rune(value)
	if width <= 0 {
		return ""
	}
	if len(runes) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

func ellipsizeMiddle(value string, width int) string {
	runes := []rune(value)
	if width <= 0 {
		return ""
	}
	if len(runes) <= width {
		return value
	}
	if width < 5 {
		return ellipsize(value, width)
	}
	left := (width - 1) / 2
	right := width - left - 1
	return string(runes[:left]) + "…" + string(runes[len(runes)-right:])
}

func stripANSI(value string) string {
	for _, code := range []string{ansiReset, ansiGold, ansiCyan, ansiGreen, ansiMuted} {
		value = strings.ReplaceAll(value, code, "")
	}
	return value
}

func runeLen(value string) int { return len([]rune(value)) }
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
