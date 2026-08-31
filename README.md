# Warden

Warden is a local-first, agentic deployment CLI. It inspects an unfamiliar repository, chooses a compatible hosting provider, creates an explainable plan, obtains a bounded infrastructure-budget approval through Prava, and then executes the deployment through deterministic provider adapters.

The agent proposes. Go code validates. The user approves consequential actions.

## Why Prava is integrated as a budget gate

Cloud providers usually meter usage and charge an account later; they do not expose a card checkout for every deployment. Warden therefore does not claim that a Prava mandate pays a cloud invoice automatically.

For externally billed providers, the flow is:

1. Create a deployment plan with a maximum monthly budget.
2. Create a merchant-scoped Prava mandate for that provider and amount.
3. The account owner approves the mandate in Prava with a passkey.
4. Warden records only the authorization and refuses to provision without it.
5. The provider continues to bill the user's own provider account.

The core deliberately keeps `authorization`, `funding`, and `provider billing` as separate concepts. Protocol v1 records a provider's billing model but does not automate settlement or prepaid-credit purchases; those require an audited provider-specific extension in a later protocol revision.

Prava documentation: [CLI mandates](https://docs.prava.space/prava-pay/mandates), [payment sessions](https://docs.prava.space/prava-pay/sessions), and [authentication environments](https://docs.prava.space/authentication).

## Capabilities

- Detects Docker, Go, Node.js, Python, Rust, Java, and plain static projects.
- Recognizes frameworks including Vite, React, Next.js, Astro, Remix, Express, Fastify, and Koa.
- Normalizes projects into a provider-independent deployment specification.
- Matches required capabilities against provider capabilities.
- Maintains independent payment and deployment state machines.
- Uses atomic, permission-restricted local state with no provider or payment secrets.
- Provides strict approval gates outside the model.
- Supports human-readable YAML and stable `--json` output.
- Supports versioned provider plugins implemented in any language.
- Replays complete Responses API items, including encrypted reasoning items, while using `store: false`.

## Built-in providers

| Provider | Workloads | Execution path | Billing treatment |
| --- | --- | --- | --- |
| Docker | OCI containers | Local or named remote Docker context | Docker itself is unbilled; host costs are external |
| AWS Lightsail | Dockerfile-based containers | AWS CLI v2 + Docker + `lightsailctl` | Metered existing account + Prava budget gate |
| Fly.io | Containers and buildpacks | `flyctl` / `fly` | Metered existing account + Prava budget gate |
| Vercel | Static and supported serverless apps | Vercel CLI | Metered existing account + Prava budget gate |
| Render | Existing Git-connected services | Deploy hook | Metered existing account + Prava budget gate |
| Plugins | Declared by plugin | Versioned JSON-over-stdio RPC | Declared by plugin |

## Installation

Requirements:

- Go 1.24+ to build from source.
- Node.js 20+ for the official Prava CLI fallback.
- The native CLI or credential environment required by the selected provider.
- `OPENAI_API_KEY` for natural-language agent mode. Deterministic commands do not require it.

```bash
go install github.com/abhyuday404/Warden/cmd/ward@latest
npm install -g @prava-sdk/cli
```

From this repository:

```bash
go build -o bin/ward ./cmd/ward
```

## Quick start

Launch Warden from the project you want to deploy:

```bash
cd /path/to/project
ward
```

The no-argument command opens an interactive workspace. Write a request in plain English:

On an interactive terminal, Warden opens with a responsive wordmark, miniature guardian mascot, and workspace status rail. Narrow terminals receive a compact layout, redirected output stays plain, and the standard `NO_COLOR` environment variable disables ANSI color.

```text
ward [plan] › inspect this application and recommend the best compatible host under $20 per month
```

Or use slash commands for deterministic control:

```text
/inspect
/providers
/plan fly 20 USD
/execute on
/deploy plan_xxx --dry-run
```

The interactive conversation retains model messages, reasoning items, tool calls, and tool outputs locally for the current process while continuing to send `store: false`. Use `/clear` to discard conversational context without changing deployment state, `/help` for all commands, and `/exit` to leave.

The original one-shot interface remains available for scripts and CI:

Inspect a repository and generate a reviewed manifest:

```bash
ward inspect
ward init
ward providers
```

Create a bounded deployment plan:

```bash
ward plan --provider fly --budget 25.00 --currency USD
```

Link the deployment agent to Prava once:

```bash
ward payment setup
ward payment setup-poll
```

Authorize the latest plan and wait for owner approval:

```bash
ward payment authorize --cadence monthly
ward payment poll --authorization auth_xxx
```

Then deploy:

```bash
ward deploy --plan plan_xxx
ward status dep_xxx
```

Use `--dry-run` to validate without provisioning. Use `--json` for automation.

## Agent mode

Agent mode uses the OpenAI Responses API with strict function tools. The default model is `gpt-5.6-sol`; override it with `--model` or `OPENAI_MODEL`.

Planning is read-only apart from the local journal:

```bash
ward agent "Inspect this app, compare compatible hosts, and propose a $20 monthly plan"
```

External actions remain disabled unless the host process grants permission:

```bash
ward agent --execute "Deploy this app with a maximum monthly budget of $20"
```

The CLI prompts separately before mandate creation, provisioning, and destruction. `--yes` suppresses terminal prompts only when combined with `--execute`; Prava still requires the owner's own approval.

The model never receives card data, provider credentials, `.env` values, or raw plugin environment variables.

## Configuration

`ward init` creates `warden.yaml`. See [warden.example.yaml](warden.example.yaml).

Secrets are prohibited in `deploy.config`. Authenticate through provider-native login or environment variables instead. The manifest records only non-secret identifiers such as app names, regions, and Docker contexts.

Copy [`.env.example`](.env.example) as a reference for supported variables. Warden intentionally does not auto-load `.env`; inject only the values you need through your shell, CI secret store, or process manager so repository inspection never becomes a secret-loading operation.

Vercel deployments are previews by default. Set `deploy.config.production: "true"` and `policy.allow_production: true` together to request production explicitly.

### AWS Lightsail containers

The built-in `aws` provider deploys Dockerfile-based projects to an Amazon Lightsail Container Service. Install AWS CLI v2, Docker, and the AWS `lightsailctl` plugin, then authenticate with your normal AWS CLI profile. Warden never reads or stores AWS access keys.

Configure `warden.yaml` with an AWS region and optional non-secret resource settings:

```yaml
deploy:
  provider: aws
  region: ap-south-1
  config:
    profile: staging       # optional; otherwise use the active AWS CLI profile
    service: example-api   # optional; defaults to a plan-specific name
    power: nano            # nano, micro, small, medium, large, or xlarge
    scale: "1"             # 1-20 compute nodes
    container: app
    image_label: example-api
    health_check_path: /healthz
```

The project port becomes the public HTTPS endpoint port; it defaults to `8080`. Deployment builds the Docker image locally, creates or reuses the named Lightsail service, pushes the image, and starts a deployment. `ward status` maps Lightsail service/deployment states into Warden states. `ward destroy` deletes the entire recorded Lightsail container service, which is the action required to stop its service charge; the local Docker image is retained.

AWS setup and service details: [install the required tools](https://docs.aws.amazon.com/lightsail/latest/userguide/amazon-lightsail-install-software.html) and [manage container services](https://docs.aws.amazon.com/lightsail/latest/userguide/amazon-lightsail-container-services.html).

Operational state lives at `.warden/state.json`. It contains plans, authorization metadata, and deployment receipts but no API keys or payment credentials. Warden reads legacy `.prava-deploy/state.json` journals and migrates them on the next write.

## Provider plugins

Set `WARD_PROVIDER_PLUGINS` to a platform path-list containing manifest files or directories:

```bash
export WARD_PROVIDER_PLUGINS=/opt/warden/providers
```

Plugin manifests use `warden.provider/v1`. Each invocation receives one JSON request on stdin and must return one JSON response on stdout. Plugins get a minimal process environment plus only the variable names declared in their manifest. Warden accepts the legacy `prava-deploy.provider/v1` manifest identifier during migration.

See [provider plugin documentation](docs/provider-plugins.md) and the [manifest schema](api/provider-plugin.schema.json).

## Development-only authorization

Tests and demos can bypass Prava explicitly:

```bash
ward --payment manual --development plan --provider docker --budget 0.00 --currency USD
```

This state is labeled `development-only` and must not be described as a Prava approval.

## Safety boundaries

- Provider pricing estimates are never represented as final invoices.
- A deployment rollback is not a refund.
- Destroying an application may not delete separately billed databases, volumes, snapshots, IPs, or DNS resources.
- Render deploy hooks cannot query status or destroy a service.
- Fly destruction removes the app and is treated as a destructive action.
- AWS destruction removes the entire recorded Lightsail container service; locally built images remain on the developer machine.
- Third-party provider plugins are trusted executable code and should be installed only from trusted publishers.
- The CLI does not deploy or pay for categories prohibited by Prava or a provider's terms.

More detail: [architecture](docs/architecture.md) and [threat model](docs/threat-model.md).

## Validation

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```
