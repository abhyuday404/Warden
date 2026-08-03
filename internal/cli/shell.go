package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/abhyuday404/Warden/internal/agent"
	"github.com/abhyuday404/Warden/internal/app"
	"github.com/abhyuday404/Warden/internal/config"
	"github.com/abhyuday404/Warden/internal/domain"
	"github.com/abhyuday404/Warden/internal/execx"
	"github.com/abhyuday404/Warden/internal/payment"
	"github.com/chzyer/readline"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type shellTurn struct {
	Role string
	Text string
}

type interactiveShell struct {
	options    *options
	command    *cobra.Command
	service    *app.Service
	gate       *interactiveGate
	session    *agent.Session
	model      string
	reasoning  string
	baseURL    string
	transcript []shellTurn
	reader     shellLineReader
}

type shellLineReader interface {
	Readline() (string, error)
	SetPrompt(string)
	Close() error
}

type basicLineReader struct {
	input  *bufio.Reader
	output io.Writer
	prompt string
}

func (r *basicLineReader) Readline() (string, error) {
	if _, err := fmt.Fprint(r.output, r.prompt); err != nil {
		return "", err
	}
	line, err := r.input.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (r *basicLineReader) SetPrompt(prompt string) { r.prompt = prompt }
func (r *basicLineReader) Close() error            { return nil }

type readlineAdapter struct{ instance *readline.Instance }

func (r readlineAdapter) Readline() (string, error) { return r.instance.Readline() }
func (r readlineAdapter) SetPrompt(prompt string)   { r.instance.SetPrompt(prompt) }
func (r readlineAdapter) Close() error              { return r.instance.Close() }

func (o *options) runInteractive(cmd *cobra.Command) error {
	if o.json {
		return fmt.Errorf("--json requires a subcommand; interactive mode emits terminal output")
	}
	svc, err := o.service()
	if err != nil {
		return err
	}
	reader, err := newShellLineReader(cmd)
	if err != nil {
		return err
	}
	defer reader.Close()

	model := envOr("OPENAI_MODEL", agent.DefaultModel)
	reasoning := envOr("OPENAI_REASONING_EFFORT", "medium")
	baseURL := envOr("OPENAI_BASE_URL", "https://api.openai.com/v1")
	gate := &interactiveGate{in: cmd.InOrStdin(), out: cmd.ErrOrStderr()}
	handler := agent.Tools{Service: svc, Gate: gate}
	client := agent.Client{APIKey: os.Getenv("OPENAI_API_KEY"), BaseURL: baseURL, Model: model, ReasoningEffort: reasoning}
	shell := &interactiveShell{
		options: o, command: cmd, service: svc, gate: gate, model: model, reasoning: reasoning, baseURL: baseURL, reader: reader,
		session: agent.NewSession(client, agent.Instructions, handler),
	}
	return shell.run(cmd.Context())
}

func newShellLineReader(cmd *cobra.Command) (shellLineReader, error) {
	input, inputIsFile := cmd.InOrStdin().(*os.File)
	output, outputIsFile := cmd.OutOrStdout().(*os.File)
	if inputIsFile && outputIsFile && input == os.Stdin && output == os.Stdout && readline.IsTerminal(int(input.Fd())) && readline.IsTerminal(int(output.Fd())) {
		instance, err := readline.NewEx(&readline.Config{
			Prompt:            "ward › ",
			HistoryLimit:      200,
			HistorySearchFold: true,
			InterruptPrompt:   "^C",
			EOFPrompt:         "exit",
			AutoComplete: readline.NewPrefixCompleter(
				readline.PcItem("/help"), readline.PcItem("/inspect"), readline.PcItem("/init"),
				readline.PcItem("/providers"), readline.PcItem("/plan"), readline.PcItem("/authorize"),
				readline.PcItem("/poll"), readline.PcItem("/deploy"), readline.PcItem("/status"),
				readline.PcItem("/destroy"), readline.PcItem("/payment"), readline.PcItem("/doctor"),
				readline.PcItem("/model"), readline.PcItem("/reasoning"), readline.PcItem("/execute"),
				readline.PcItem("/yes"), readline.PcItem("/context"), readline.PcItem("/history"),
				readline.PcItem("/root"), readline.PcItem("/clear"), readline.PcItem("/exit"),
			),
			Stdin: input, Stdout: output, Stderr: cmd.ErrOrStderr(),
		})
		if err != nil {
			return nil, err
		}
		return readlineAdapter{instance: instance}, nil
	}
	return &basicLineReader{input: bufio.NewReader(cmd.InOrStdin()), output: cmd.OutOrStdout(), prompt: "ward › "}, nil
}

func (s *interactiveShell) run(ctx context.Context) error {
	s.printWelcome()
	for {
		s.reader.SetPrompt(s.prompt())
		line, err := s.reader.Readline()
		if errors.Is(err, readline.ErrInterrupt) {
			continue
		}
		if errors.Is(err, io.EOF) {
			_, _ = fmt.Fprintln(s.command.OutOrStdout(), "")
			return nil
		}
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			exit, commandErr := s.runSlashCommand(ctx, line)
			if commandErr != nil {
				s.printError(commandErr)
			}
			if exit {
				return nil
			}
			continue
		}
		if err := s.runAgentTurn(ctx, line); err != nil {
			s.printError(err)
		}
	}
}

func (s *interactiveShell) prompt() string {
	mode := "plan"
	if s.gate.execute {
		mode = "exec"
	}
	return fmt.Sprintf("ward [%s] › ", mode)
}

func (s *interactiveShell) printWelcome() {
	out := s.command.OutOrStdout()
	_, _ = fmt.Fprintf(out, "\nWarden %s\n", Version)
	_, _ = fmt.Fprintf(out, "Workspace: %s\n", s.options.root)
	if spec, _, _, err := s.service.Inspect(); err == nil {
		description := strings.Join(nonEmpty(spec.Runtime, spec.Framework, string(spec.Kind)), " ")
		_, _ = fmt.Fprintf(out, "Project:   %s (%s)\n", spec.Name, description)
	} else {
		_, _ = fmt.Fprintf(out, "Project:   inspection unavailable: %v\n", err)
	}
	_, _ = fmt.Fprintf(out, "Model:     %s · reasoning %s\n", s.model, s.reasoning)
	if os.Getenv("OPENAI_API_KEY") == "" {
		_, _ = fmt.Fprintln(out, "Agent:     unavailable until OPENAI_API_KEY is set")
	} else {
		_, _ = fmt.Fprintln(out, "Agent:     ready")
	}
	_, _ = fmt.Fprintln(out, "Mode:      plan-only; use /execute on to permit approved actions")
	_, _ = fmt.Fprint(out, "\nDescribe what you want in plain English, or type /help for commands.\n\n")
}

func nonEmpty(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func (s *interactiveShell) runAgentTurn(ctx context.Context, message string) error {
	if os.Getenv("OPENAI_API_KEY") == "" {
		return fmt.Errorf("OPENAI_API_KEY is required for plain-English agent mode; slash commands remain available")
	}
	// Refresh the key in case the shell was launched by an embedding process that
	// updates its environment before the first turn.
	s.session.Client.APIKey = os.Getenv("OPENAI_API_KEY")
	s.transcript = append(s.transcript, shellTurn{Role: "you", Text: message})
	out := s.command.OutOrStdout()
	_, _ = fmt.Fprint(out, "warden › ")
	streamed := false
	answer, err := s.session.SendStream(ctx, message, func(delta string) {
		streamed = true
		_, _ = fmt.Fprint(out, delta)
	})
	if err != nil {
		_, _ = fmt.Fprintln(out)
		return err
	}
	if !streamed {
		_, _ = fmt.Fprint(out, answer)
	}
	s.transcript = append(s.transcript, shellTurn{Role: "warden", Text: answer})
	_, err = fmt.Fprint(out, "\n\n")
	return err
}

func (s *interactiveShell) runSlashCommand(ctx context.Context, line string) (bool, error) {
	name, rawArgs := splitSlashCommand(line)
	args := strings.Fields(rawArgs)
	switch name {
	case "/exit", "/quit", "/q":
		return true, nil
	case "/help", "/commands":
		s.printHelp()
	case "/inspect":
		return false, s.inspect()
	case "/init":
		if len(args) > 1 || (len(args) == 1 && args[0] != "--force") {
			return false, fmt.Errorf("usage: /init [--force]")
		}
		return false, s.initManifest(len(args) == 1)
	case "/providers":
		return false, s.providers(ctx)
	case "/plan":
		return false, s.plan(ctx, args)
	case "/authorize":
		return false, s.authorize(ctx, args)
	case "/poll":
		return false, s.poll(ctx, args)
	case "/deploy":
		return false, s.deploy(ctx, args)
	case "/status":
		return false, s.status(ctx, args)
	case "/destroy":
		return false, s.destroy(ctx, args)
	case "/payment":
		return false, s.payment(ctx, args)
	case "/doctor":
		return false, s.doctor(ctx)
	case "/execute":
		return false, setShellBoolean("/execute", "execute", args, &s.gate.execute, s.command.OutOrStdout())
	case "/yes":
		return false, setShellBoolean("/yes", "automatic terminal approval", args, &s.gate.yes, s.command.OutOrStdout())
	case "/model":
		return false, s.setModel(rawArgs)
	case "/reasoning":
		return false, s.setReasoning(rawArgs)
	case "/root":
		return false, s.setRoot(rawArgs)
	case "/context":
		s.printContext()
	case "/history":
		s.printHistory()
	case "/clear":
		s.session.Reset()
		s.transcript = nil
		_, _ = fmt.Fprintln(s.command.OutOrStdout(), "Conversation cleared. Deployment state was not changed.")
	default:
		return false, fmt.Errorf("unknown command %s; type /help", name)
	}
	return false, nil
}

func splitSlashCommand(line string) (string, string) {
	line = strings.TrimSpace(line)
	if before, after, found := strings.Cut(line, " "); found {
		return strings.ToLower(before), strings.TrimSpace(after)
	}
	return strings.ToLower(line), ""
}

func (s *interactiveShell) printHelp() {
	_, _ = fmt.Fprint(s.command.OutOrStdout(), `
Plain English
  Any input without a leading slash is sent to the Warden agent.

Workspace
  /inspect                         Detect the current project
  /init [--force]                  Generate warden.yaml
  /providers                       Compare provider compatibility
  /root [path]                     Show or switch workspace and clear chat

Deployment
  /plan [provider] [budget] [USD]  Create a deterministic plan
  /authorize [plan-id] [cadence]   Request Prava budget approval
  /poll <authorization-id>         Wait for Prava mandate approval
  /deploy [plan-id] [--dry-run]    Deploy the latest or selected plan
  /status [deployment-id]          Refresh latest or selected deployment
  /destroy [id] [--dry-run]        Destroy a deployment after approval

Session
  /execute [on|off]                Permit agent-requested external actions
  /yes [on|off]                    Toggle automatic terminal approvals
  /model [model-id]                Show or change model; clears conversation
  /reasoning [effort]              Show or change reasoning effort
  /context                         Show session settings
  /history                         Show this session's visible conversation
  /clear                           Clear conversation, not deployment state
  /doctor                          Check local prerequisites
  /payment status|setup|setup-poll Manage Prava linking
  /exit                            Leave Warden

Existing one-shot commands remain available outside the shell, for example:
  ward inspect
  ward plan --provider fly --budget 20 --currency USD
  ward agent "inspect this project"
`)
}

func (s *interactiveShell) inspect() error {
	spec, manifest, path, err := s.service.Inspect()
	if err != nil {
		return err
	}
	return writeShellYAML(s.command.OutOrStdout(), map[string]any{"project": spec, "manifest": manifest, "manifest_path": path})
}

func (s *interactiveShell) initManifest(force bool) error {
	spec, _, existing, err := s.service.Inspect()
	if err != nil {
		return err
	}
	if existing != "" && !force {
		return fmt.Errorf("manifest already exists at %s; use /init --force to replace it", existing)
	}
	b, err := config.Render(spec)
	if err != nil {
		return err
	}
	path := filepath.Join(s.options.root, "warden.yaml")
	if err := writeExclusive(path, b, force); err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.command.OutOrStdout(), "Created %s\n", path)
	return err
}

func (s *interactiveShell) providers(ctx context.Context) error {
	candidates, err := s.service.Providers(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(s.command.OutOrStdout(), 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "PROVIDER\tAVAILABLE\tCOMPATIBLE\tSCORE\tDETAIL")
	for _, candidate := range candidates {
		detail := candidate.Provider.UnavailableReason
		if len(candidate.Missing) > 0 {
			detail = "missing: " + fmt.Sprint(candidate.Missing)
		}
		_, _ = fmt.Fprintf(w, "%s\t%t\t%t\t%d\t%s\n", candidate.Provider.ID, candidate.Provider.Available, candidate.Compatible, candidate.Score, detail)
	}
	return w.Flush()
}

func (s *interactiveShell) plan(ctx context.Context, args []string) error {
	if len(args) > 3 {
		return fmt.Errorf("usage: /plan [provider] [budget] [currency]")
	}
	providerID, budget, currency := "", "", ""
	if len(args) > 0 {
		providerID = args[0]
	}
	if len(args) > 1 {
		budget = args[1]
	}
	if len(args) > 2 {
		currency = args[2]
	}
	plan, err := s.service.CreatePlan(ctx, providerID, domain.Money{Amount: budget, Currency: currency})
	if err != nil {
		return err
	}
	return writeShellYAML(s.command.OutOrStdout(), plan)
}

func (s *interactiveShell) authorize(ctx context.Context, args []string) error {
	if len(args) > 2 {
		return fmt.Errorf("usage: /authorize [plan-id] [monthly|one_time]")
	}
	planID, cadence := "", "monthly"
	if len(args) > 0 {
		planID = args[0]
	}
	if len(args) > 1 {
		cadence = args[1]
	}
	plan, err := s.service.GetPlan(planID)
	if err != nil {
		return err
	}
	if plan.BudgetRequired {
		gate := &interactiveGate{execute: true, yes: s.gate.yes, in: s.command.InOrStdin(), out: s.command.ErrOrStderr()}
		if err := gate.Approve(ctx, agent.Action{Kind: "authorize_budget", Summary: fmt.Sprintf("Authorize %s %s per %s for %s", plan.Budget.Amount, plan.Budget.Currency, cadence, plan.Provider.Name), External: true, Cost: &plan.Budget}); err != nil {
			return err
		}
	}
	auth, err := s.service.Authorize(ctx, plan.ID, cadence)
	if err != nil {
		return err
	}
	return writeShellYAML(s.command.OutOrStdout(), auth)
}

func (s *interactiveShell) poll(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: /poll <authorization-id>")
	}
	auth, err := s.service.PollAuthorization(ctx, args[0])
	if printErr := writeShellYAML(s.command.OutOrStdout(), auth); err == nil {
		err = printErr
	}
	return err
}

func (s *interactiveShell) deploy(ctx context.Context, args []string) error {
	id, dryRun, err := optionalIDAndDryRun(args)
	if err != nil {
		return err
	}
	plan, err := s.service.GetPlan(id)
	if err != nil {
		return err
	}
	if !dryRun {
		gate := &interactiveGate{execute: true, yes: s.gate.yes, in: s.command.InOrStdin(), out: s.command.ErrOrStderr()}
		if err := gate.Approve(ctx, agent.Action{Kind: "deploy", Summary: fmt.Sprintf("Deploy %s to %s", plan.Project.Name, plan.Provider.Name), External: plan.Provider.Billing != domain.BillingNone, Cost: &plan.Budget}); err != nil {
			return err
		}
	}
	dep, err := s.service.Deploy(ctx, plan.ID, dryRun)
	if printErr := writeShellYAML(s.command.OutOrStdout(), dep); err == nil {
		err = printErr
	}
	return err
}

func (s *interactiveShell) status(ctx context.Context, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: /status [deployment-id]")
	}
	id := ""
	if len(args) == 1 {
		id = args[0]
	}
	dep, err := s.service.Status(ctx, id)
	if printErr := writeShellYAML(s.command.OutOrStdout(), dep); err == nil {
		err = printErr
	}
	return err
}

func (s *interactiveShell) destroy(ctx context.Context, args []string) error {
	id, dryRun, err := optionalIDAndDryRun(args)
	if err != nil {
		return err
	}
	dep, err := s.service.GetDeployment(id)
	if err != nil {
		return err
	}
	if !dryRun {
		gate := &interactiveGate{execute: true, yes: s.gate.yes, in: s.command.InOrStdin(), out: s.command.ErrOrStderr()}
		if err := gate.Approve(ctx, agent.Action{Kind: "destroy", Summary: "Destroy deployment " + dep.ID, Destructive: true, External: true}); err != nil {
			return err
		}
	}
	updated, err := s.service.Destroy(ctx, dep.ID, dryRun)
	if printErr := writeShellYAML(s.command.OutOrStdout(), updated); err == nil {
		err = printErr
	}
	return err
}

func optionalIDAndDryRun(args []string) (string, bool, error) {
	id, dryRun := "", false
	for _, arg := range args {
		switch arg {
		case "--dry-run":
			dryRun = true
		default:
			if id != "" {
				return "", false, fmt.Errorf("expected at most one ID and --dry-run")
			}
			id = arg
		}
	}
	return id, dryRun, nil
}

func (s *interactiveShell) payment(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: /payment status|setup|setup-poll")
	}
	client := payment.PravaCLI{Runner: execx.OSRunner{}}
	switch args[0] {
	case "status":
		if err := client.Doctor(ctx); err != nil {
			return err
		}
		_, err := fmt.Fprintln(s.command.OutOrStdout(), "Prava agent is linked and ready.")
		return err
	case "setup":
		url, err := client.Setup(ctx, "Warden", "custom", "Agentic provider-agnostic deployment budget controller")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(s.command.OutOrStdout(), "Approve Warden in Prava: %s\nThen run /payment setup-poll\n", url)
		return err
	case "setup-poll":
		if err := client.SetupPoll(ctx); err != nil {
			return err
		}
		_, err := fmt.Fprintln(s.command.OutOrStdout(), "Prava agent linked.")
		return err
	default:
		return fmt.Errorf("usage: /payment status|setup|setup-poll")
	}
}

func (s *interactiveShell) doctor(ctx context.Context) error {
	candidates, inspectErr := s.service.Providers(ctx)
	paymentErr := s.service.Payments.Doctor(ctx)
	result := map[string]any{
		"workspace": s.options.root, "model": s.model, "openai_api_key": os.Getenv("OPENAI_API_KEY") != "",
		"providers": candidates, "project_error": errorString(inspectErr), "payment_error": errorString(paymentErr),
	}
	return writeShellYAML(s.command.OutOrStdout(), result)
}

func setShellBoolean(command, label string, args []string, target *bool, out io.Writer) error {
	if len(args) == 0 {
		_, err := fmt.Fprintf(out, "%s: %t\n", label, *target)
		return err
	}
	value := strings.ToLower(args[0])
	if len(args) != 1 || (value != "on" && value != "off") {
		return fmt.Errorf("usage: %s on|off", command)
	}
	*target = value == "on"
	_, err := fmt.Fprintf(out, "%s: %t\n", label, *target)
	return err
}

func (s *interactiveShell) setModel(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		_, err := fmt.Fprintf(s.command.OutOrStdout(), "model: %s\n", s.model)
		return err
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return fmt.Errorf("model ID cannot contain whitespace")
	}
	s.model = value
	s.session.Client.Model = value
	s.session.Reset()
	s.transcript = nil
	_, err := fmt.Fprintf(s.command.OutOrStdout(), "Model changed to %s. Conversation cleared.\n", value)
	return err
}

func (s *interactiveShell) setReasoning(value string) error {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		_, err := fmt.Fprintf(s.command.OutOrStdout(), "reasoning: %s\n", s.reasoning)
		return err
	}
	valid := map[string]bool{"none": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true}
	if !valid[value] {
		return fmt.Errorf("reasoning must be none, minimal, low, medium, high, or xhigh")
	}
	s.reasoning = value
	s.session.Client.ReasoningEffort = value
	_, err := fmt.Fprintf(s.command.OutOrStdout(), "Reasoning effort changed to %s.\n", value)
	return err
}

func (s *interactiveShell) setRoot(value string) error {
	value = strings.Trim(strings.TrimSpace(value), `"`)
	if value == "" {
		_, err := fmt.Fprintf(s.command.OutOrStdout(), "workspace: %s\n", s.options.root)
		return err
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace is not a directory: %s", abs)
	}
	oldRoot := s.options.root
	s.options.root = abs
	service, err := s.options.service()
	if err != nil {
		s.options.root = oldRoot
		return err
	}
	if _, _, _, err := service.Inspect(); err != nil {
		s.options.root = oldRoot
		return err
	}
	s.service = service
	s.session.Handler = agent.Tools{Service: service, Gate: s.gate}
	s.session.Reset()
	s.transcript = nil
	_, err = fmt.Fprintf(s.command.OutOrStdout(), "Workspace changed to %s. Conversation cleared.\n", abs)
	return err
}

func (s *interactiveShell) printContext() {
	_, _ = fmt.Fprintf(s.command.OutOrStdout(), `workspace: %s
model: %s
reasoning: %s
execute: %t
automatic_approval: %t
openai_api_key: %t
conversation_items: %d
`, s.options.root, s.model, s.reasoning, s.gate.execute, s.gate.yes, os.Getenv("OPENAI_API_KEY") != "", s.session.HistoryItems())
}

func (s *interactiveShell) printHistory() {
	if len(s.transcript) == 0 {
		_, _ = fmt.Fprintln(s.command.OutOrStdout(), "No visible conversation in this session.")
		return
	}
	for _, turn := range s.transcript {
		_, _ = fmt.Fprintf(s.command.OutOrStdout(), "\n%s › %s\n", turn.Role, turn.Text)
	}
	_, _ = fmt.Fprintln(s.command.OutOrStdout())
}

func (s *interactiveShell) printError(err error) {
	_, _ = fmt.Fprintf(s.command.ErrOrStderr(), "error: %v\n", err)
}

func writeShellYAML(out io.Writer, value any) error {
	b, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(out, string(b))
	return err
}
