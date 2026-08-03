package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/abhyuday404/Warden/internal/agent"
	"github.com/abhyuday404/Warden/internal/app"
	"github.com/abhyuday404/Warden/internal/config"
	"github.com/abhyuday404/Warden/internal/domain"
	"github.com/abhyuday404/Warden/internal/execx"
	"github.com/abhyuday404/Warden/internal/payment"
	"github.com/abhyuday404/Warden/internal/provider"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var Version = "dev"

type options struct {
	root        string
	json        bool
	payment     string
	development bool
	out         io.Writer
	errOut      io.Writer
	in          io.Reader
}

func New() *cobra.Command {
	o := &options{out: os.Stdout, errOut: os.Stderr, in: os.Stdin, payment: "prava"}
	root := &cobra.Command{
		Use: "ward", Short: "Warden: agentic, provider-agnostic deployment with Prava budget authorization",
		SilenceUsage: true, SilenceErrors: true,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return o.runInteractive(cmd)
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Name() == "version" {
				return nil
			}
			abs, err := filepath.Abs(o.root)
			if err != nil {
				return err
			}
			o.root = abs
			return nil
		},
	}
	root.SetOut(o.out)
	root.SetErr(o.errOut)
	root.SetIn(o.in)
	root.PersistentFlags().StringVar(&o.root, "root", ".", "project root")
	root.PersistentFlags().BoolVar(&o.json, "json", false, "emit JSON")
	root.PersistentFlags().StringVar(&o.payment, "payment", "prava", "budget authorizer: prava or manual")
	root.PersistentFlags().BoolVar(&o.development, "development", false, "allow development-only adapters")

	root.AddCommand(
		o.versionCommand(), o.inspectCommand(), o.initCommand(), o.providersCommand(), o.planCommand(),
		o.deployCommand(), o.statusCommand(), o.destroyCommand(), o.paymentCommand(), o.agentCommand(), o.doctorCommand(),
	)
	return root
}

func Execute() error { return New().Execute() }

func (o *options) service() (*app.Service, error) {
	runner := execx.OSRunner{}
	drivers := []provider.Driver{provider.Docker{Runner: runner}, provider.Vercel{Runner: runner}, provider.Fly{Runner: runner}, provider.RenderHook{}}
	raw := os.Getenv("WARD_PROVIDER_PLUGINS")
	if raw == "" {
		raw = os.Getenv("PRAVA_DEPLOY_PROVIDER_PLUGINS") // Legacy v0.1 compatibility.
	}
	if raw != "" {
		plugins, err := provider.LoadPlugins(filepath.SplitList(raw), runner)
		if err != nil {
			return nil, err
		}
		builtin := map[string]bool{"docker": true, "vercel": true, "fly": true, "render": true}
		for _, plugin := range plugins {
			if id := plugin.Info(context.Background()).ID; builtin[id] {
				return nil, fmt.Errorf("provider plugin ID %q conflicts with a built-in provider", id)
			}
		}
		drivers = append(drivers, plugins...)
	}
	registry := provider.NewRegistry(drivers...)
	var authorizer payment.Authorizer
	switch o.payment {
	case "prava":
		authorizer = payment.PravaCLI{Runner: runner}
	case "manual":
		if !o.development {
			return nil, fmt.Errorf("manual authorization is development-only; add --development explicitly")
		}
		authorizer = payment.Manual{}
	default:
		return nil, fmt.Errorf("unknown payment authorizer %q", o.payment)
	}
	return app.New(o.root, registry, authorizer), nil
}

func (o *options) versionCommand() *cobra.Command {
	return &cobra.Command{Use: "version", Short: "Print version", RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "ward %s (%s/%s)\n", Version, runtime.GOOS, runtime.GOARCH)
		return err
	}}
}

func (o *options) inspectCommand() *cobra.Command {
	return &cobra.Command{Use: "inspect", Short: "Detect project runtime and requirements", RunE: func(cmd *cobra.Command, _ []string) error {
		svc, err := o.service()
		if err != nil {
			return err
		}
		spec, manifest, path, err := svc.Inspect()
		if err != nil {
			return err
		}
		return o.print(cmd, map[string]any{"project": spec, "manifest": manifest, "manifest_path": path})
	}}
}

func (o *options) initCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{Use: "init", Short: "Generate a reviewed deployment manifest", RunE: func(cmd *cobra.Command, _ []string) error {
		svc, err := o.service()
		if err != nil {
			return err
		}
		spec, _, existing, err := svc.Inspect()
		if err != nil {
			return err
		}
		if existing != "" && !force {
			return fmt.Errorf("manifest already exists at %s; use --force to replace it", existing)
		}
		b, err := config.Render(spec)
		if err != nil {
			return err
		}
		path := filepath.Join(o.root, "warden.yaml")
		if err := writeExclusive(path, b, force); err != nil {
			return err
		}
		return o.print(cmd, map[string]any{"created": path, "project": spec.Name})
	}}
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing manifest")
	return cmd
}

func (o *options) providersCommand() *cobra.Command {
	return &cobra.Command{Use: "providers", Short: "Show compatible deployment providers", RunE: func(cmd *cobra.Command, _ []string) error {
		svc, err := o.service()
		if err != nil {
			return err
		}
		candidates, err := svc.Providers(cmd.Context())
		if err != nil {
			return err
		}
		return o.print(cmd, candidates)
	}}
}

func (o *options) planCommand() *cobra.Command {
	var providerID, budget, currency string
	cmd := &cobra.Command{Use: "plan", Short: "Create a deterministic deployment plan", RunE: func(cmd *cobra.Command, _ []string) error {
		svc, err := o.service()
		if err != nil {
			return err
		}
		plan, err := svc.CreatePlan(cmd.Context(), providerID, domain.Money{Amount: budget, Currency: currency})
		if err != nil {
			return err
		}
		return o.print(cmd, plan)
	}}
	cmd.Flags().StringVar(&providerID, "provider", "", "provider ID or auto")
	cmd.Flags().StringVar(&budget, "budget", "", "maximum monthly budget")
	cmd.Flags().StringVar(&currency, "currency", "", "ISO 4217 currency")
	return cmd
}

func (o *options) deployCommand() *cobra.Command {
	var planID, providerID, budget, currency string
	var dryRun, yes bool
	cmd := &cobra.Command{Use: "deploy", Short: "Deploy an authorized plan", RunE: func(cmd *cobra.Command, _ []string) error {
		svc, err := o.service()
		if err != nil {
			return err
		}
		if planID == "" {
			plan, err := svc.CreatePlan(cmd.Context(), providerID, domain.Money{Amount: budget, Currency: currency})
			if err != nil {
				return err
			}
			planID = plan.ID
		}
		if !dryRun {
			plan, err := svc.GetPlan(planID)
			if err != nil {
				return err
			}
			if err := (&interactiveGate{execute: true, yes: yes, in: cmd.InOrStdin(), out: cmd.ErrOrStderr()}).Approve(cmd.Context(), agent.Action{Kind: "deploy", Summary: fmt.Sprintf("Deploy %s to %s", plan.Project.Name, plan.Provider.Name), External: plan.Provider.Billing != domain.BillingNone, Cost: &plan.Budget}); err != nil {
				return err
			}
		}
		dep, err := svc.Deploy(cmd.Context(), planID, dryRun)
		if printErr := o.print(cmd, dep); err == nil {
			err = printErr
		}
		return err
	}}
	cmd.Flags().StringVar(&planID, "plan", "", "plan ID; defaults to a newly created plan")
	cmd.Flags().StringVar(&providerID, "provider", "", "provider for a newly created plan")
	cmd.Flags().StringVar(&budget, "budget", "", "budget for a newly created plan")
	cmd.Flags().StringVar(&currency, "currency", "", "currency for a newly created plan")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate without provisioning")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "approve deployment non-interactively")
	return cmd
}

func (o *options) statusCommand() *cobra.Command {
	return &cobra.Command{Use: "status [deployment-id]", Args: cobra.MaximumNArgs(1), Short: "Refresh deployment status", RunE: func(cmd *cobra.Command, args []string) error {
		svc, err := o.service()
		if err != nil {
			return err
		}
		id := ""
		if len(args) == 1 {
			id = args[0]
		}
		dep, err := svc.Status(cmd.Context(), id)
		if printErr := o.print(cmd, dep); err == nil {
			err = printErr
		}
		return err
	}}
}

func (o *options) destroyCommand() *cobra.Command {
	var dryRun, yes bool
	cmd := &cobra.Command{Use: "destroy [deployment-id]", Args: cobra.MaximumNArgs(1), Short: "Destroy a deployment", RunE: func(cmd *cobra.Command, args []string) error {
		svc, err := o.service()
		if err != nil {
			return err
		}
		id := ""
		if len(args) == 1 {
			id = args[0]
		}
		dep, err := svc.GetDeployment(id)
		if err != nil {
			return err
		}
		if !dryRun {
			if err := (&interactiveGate{execute: true, yes: yes, in: cmd.InOrStdin(), out: cmd.ErrOrStderr()}).Approve(cmd.Context(), agent.Action{Kind: "destroy", Summary: "Destroy deployment " + dep.ID, Destructive: true, External: true}); err != nil {
				return err
			}
		}
		dep, err = svc.Destroy(cmd.Context(), dep.ID, dryRun)
		if printErr := o.print(cmd, dep); err == nil {
			err = printErr
		}
		return err
	}}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be destroyed")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "approve destruction non-interactively")
	return cmd
}

func (o *options) paymentCommand() *cobra.Command {
	group := &cobra.Command{Use: "payment", Short: "Manage Prava linking and budget authorization"}
	var name, platform, description string
	setup := &cobra.Command{Use: "setup", Short: "Link this deployment agent to Prava", RunE: func(cmd *cobra.Command, _ []string) error {
		if o.payment != "prava" {
			return fmt.Errorf("payment setup requires --payment prava")
		}
		client := payment.PravaCLI{Runner: execx.OSRunner{}}
		url, err := client.Setup(cmd.Context(), name, platform, description)
		if err != nil {
			return err
		}
		return o.print(cmd, map[string]any{"approval_url": url, "next": "ward payment setup-poll"})
	}}
	setup.Flags().StringVar(&name, "name", "Warden", "agent name shown to the owner")
	setup.Flags().StringVar(&platform, "platform", "custom", "Prava agent platform identifier")
	setup.Flags().StringVar(&description, "description", "Agentic provider-agnostic deployment budget controller", "agent description")
	setupPoll := &cobra.Command{Use: "setup-poll", Short: "Wait for Prava agent-link approval", RunE: func(cmd *cobra.Command, _ []string) error {
		if o.payment != "prava" {
			return fmt.Errorf("payment setup requires --payment prava")
		}
		client := payment.PravaCLI{Runner: execx.OSRunner{}}
		if err := client.SetupPoll(cmd.Context()); err != nil {
			return err
		}
		return o.print(cmd, map[string]any{"linked": true})
	}}
	var planID, cadence string
	var yes bool
	authorize := &cobra.Command{Use: "authorize", Short: "Authorize a plan budget through Prava", RunE: func(cmd *cobra.Command, _ []string) error {
		svc, err := o.service()
		if err != nil {
			return err
		}
		plan, err := svc.GetPlan(planID)
		if err != nil {
			return err
		}
		if err := (&interactiveGate{execute: true, yes: yes, in: cmd.InOrStdin(), out: cmd.ErrOrStderr()}).Approve(cmd.Context(), agent.Action{Kind: "authorize_budget", Summary: fmt.Sprintf("Authorize %s %s per %s for %s", plan.Budget.Amount, plan.Budget.Currency, cadence, plan.Provider.Name), External: true, Cost: &plan.Budget}); err != nil {
			return err
		}
		auth, err := svc.Authorize(cmd.Context(), plan.ID, cadence)
		if err != nil {
			return err
		}
		return o.print(cmd, auth)
	}}
	authorize.Flags().StringVar(&planID, "plan", "", "plan ID; defaults to latest")
	authorize.Flags().StringVar(&cadence, "cadence", "monthly", "monthly or one_time")
	authorize.Flags().BoolVarP(&yes, "yes", "y", false, "approve mandate creation non-interactively")
	var authID string
	poll := &cobra.Command{Use: "poll", Short: "Wait for Prava mandate approval", RunE: func(cmd *cobra.Command, _ []string) error {
		svc, err := o.service()
		if err != nil {
			return err
		}
		auth, err := svc.PollAuthorization(cmd.Context(), authID)
		if printErr := o.print(cmd, auth); err == nil {
			err = printErr
		}
		return err
	}}
	poll.Flags().StringVar(&authID, "authorization", "", "authorization ID")
	_ = poll.MarkFlagRequired("authorization")
	status := &cobra.Command{Use: "status", Short: "Check Prava CLI linking", RunE: func(cmd *cobra.Command, _ []string) error {
		if o.payment != "prava" {
			return fmt.Errorf("payment status requires --payment prava")
		}
		client := payment.PravaCLI{Runner: execx.OSRunner{}}
		if err := client.Doctor(cmd.Context()); err != nil {
			return err
		}
		return o.print(cmd, map[string]any{"ready": true})
	}}
	group.AddCommand(setup, setupPoll, authorize, poll, status)
	return group
}

func (o *options) agentCommand() *cobra.Command {
	var execute, yes bool
	var model, effort, baseURL string
	cmd := &cobra.Command{Use: "agent <goal>", Args: cobra.MinimumNArgs(1), Short: "Ask the deployment agent to inspect, plan, and optionally execute", RunE: func(cmd *cobra.Command, args []string) error {
		svc, err := o.service()
		if err != nil {
			return err
		}
		client := agent.Client{APIKey: os.Getenv("OPENAI_API_KEY"), BaseURL: baseURL, Model: model, ReasoningEffort: effort}
		gate := &interactiveGate{execute: execute, yes: yes, in: cmd.InOrStdin(), out: cmd.ErrOrStderr()}
		answer, err := client.Run(cmd.Context(), agent.Instructions, strings.Join(args, " "), agent.Tools{Service: svc, Gate: gate})
		if err != nil {
			return err
		}
		if o.json {
			return o.print(cmd, map[string]string{"answer": answer})
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), answer)
		return err
	}}
	cmd.Flags().BoolVar(&execute, "execute", false, "allow the agent to request external actions after approval")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "approve agent-requested actions non-interactively; requires --execute")
	cmd.Flags().StringVar(&model, "model", envOr("OPENAI_MODEL", agent.DefaultModel), "OpenAI model")
	cmd.Flags().StringVar(&effort, "reasoning", envOr("OPENAI_REASONING_EFFORT", "medium"), "reasoning effort")
	cmd.Flags().StringVar(&baseURL, "openai-base-url", envOr("OPENAI_BASE_URL", "https://api.openai.com/v1"), "Responses API base URL")
	return cmd
}

func (o *options) doctorCommand() *cobra.Command {
	return &cobra.Command{Use: "doctor", Short: "Check local provider and payment prerequisites", RunE: func(cmd *cobra.Command, _ []string) error {
		svc, err := o.service()
		if err != nil {
			return err
		}
		candidates, inspectErr := svc.Providers(cmd.Context())
		pravaErr := svc.Payments.Doctor(cmd.Context())
		result := map[string]any{"providers": candidates, "openai_api_key": os.Getenv("OPENAI_API_KEY") != "", "project_error": errorString(inspectErr), "payment_error": errorString(pravaErr)}
		if err := o.print(cmd, result); err != nil {
			return err
		}
		if inspectErr != nil {
			return inspectErr
		}
		if pravaErr != nil {
			return pravaErr
		}
		return nil
	}}
}

func (o *options) print(cmd *cobra.Command, value any) error {
	var b []byte
	var err error
	if o.json {
		b, err = json.MarshalIndent(value, "", "  ")
	} else {
		b, err = yaml.Marshal(value)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), string(b))
	return err
}

type interactiveGate struct {
	execute, yes bool
	in           io.Reader
	out          io.Writer
}

func (g *interactiveGate) Approve(_ context.Context, action agent.Action) error {
	if !g.execute {
		return fmt.Errorf("%s is blocked; rerun agent mode with --execute or use the dedicated command", action.Kind)
	}
	if g.yes {
		return nil
	}
	if g.in == nil || g.out == nil {
		return fmt.Errorf("%s requires interactive approval or --yes", action.Kind)
	}
	label := action.Summary
	if action.Destructive {
		label = "DESTRUCTIVE: " + label
	}
	if _, err := fmt.Fprintf(g.out, "%s\nApprove? [y/N]: ", label); err != nil {
		return err
	}
	line, err := bufio.NewReader(g.in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	line = strings.ToLower(strings.TrimSpace(line))
	if line != "y" && line != "yes" {
		return fmt.Errorf("%s was not approved", action.Kind)
	}
	return nil
}

func writeExclusive(path string, data []byte, force bool) error {
	flags := os.O_WRONLY | os.O_CREATE
	if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
