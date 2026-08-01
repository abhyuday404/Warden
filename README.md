# Prava Deploy

Prava Deploy is a local-first, agentic deployment CLI. It inspects an unfamiliar repository, chooses a compatible hosting provider, creates an explainable plan, obtains a bounded infrastructure-budget approval through Prava, and then executes the deployment through deterministic provider adapters.

The agent proposes. Go code validates. The user approves consequential actions.

## Why Prava is integrated as a budget gate

Cloud providers usually meter usage and charge an account later; they do not expose a card checkout for every deployment. Prava Deploy therefore does not claim that a Prava mandate pays a cloud invoice automatically.

For externally billed providers, the flow is:

1. Create a deployment plan with a maximum monthly budget.
2. Create a merchant-scoped Prava mandate for that provider and amount.
3. The account owner approves the mandate in Prava with a passkey.
4. Prava Deploy records only the authorization and refuses to provision without it.
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
go install github.com/abhyuday404/prava-hack/cmd/prava-deploy@latest
npm install -g @prava-sdk/cli
```

From this repository:

```bash
go build -o bin/prava-deploy ./cmd/prava-deploy
```

## Quick start

Inspect a repository and generate a reviewed manifest:

```bash
prava-deploy inspect
prava-deploy init
prava-deploy providers
```

Create a bounded deployment plan:

```bash
prava-deploy plan --provider fly --budget 25.00 --currency USD
```

Link the deployment agent to Prava once:

```bash
prava-deploy payment setup
prava-deploy payment setup-poll
```

Authorize the latest plan and wait for owner approval:

```bash
prava-deploy payment authorize --cadence monthly
prava-deploy payment poll --authorization auth_xxx
```

Then deploy:

```bash
prava-deploy deploy --plan plan_xxx
prava-deploy status dep_xxx
```

Use `--dry-run` to validate without provisioning. Use `--json` for automation.

## Agent mode

Agent mode uses the OpenAI Responses API with strict function tools. The default model is `gpt-5.6-sol`; override it with `--model` or `OPENAI_MODEL`.

Planning is read-only apart from the local journal:

```bash
prava-deploy agent "Inspect this app, compare compatible hosts, and propose a $20 monthly plan"
```

External actions remain disabled unless the host process grants permission:

```bash
prava-deploy agent --execute "Deploy this app with a maximum monthly budget of $20"
```

The CLI prompts separately before mandate creation, provisioning, and destruction. `--yes` suppresses terminal prompts only when combined with `--execute`; Prava still requires the owner's own approval.

The model never receives card data, provider credentials, `.env` values, or raw plugin environment variables.

## Configuration

`prava-deploy init` creates `prava-deploy.yaml`. See [prava-deploy.example.yaml](prava-deploy.example.yaml).

Secrets are prohibited in `deploy.config`. Authenticate through provider-native login or environment variables instead. The manifest records only non-secret identifiers such as app names, regions, and Docker contexts.

Vercel deployments are previews by default. Set `deploy.config.production: "true"` and `policy.allow_production: true` together to request production explicitly.

Operational state lives at `.prava-deploy/state.json`. It contains plans, authorization metadata, and deployment receipts but no API keys or payment credentials.

## Provider plugins

Set `PRAVA_DEPLOY_PROVIDER_PLUGINS` to a platform path-list containing manifest files or directories:

```bash
export PRAVA_DEPLOY_PROVIDER_PLUGINS=/opt/prava/providers
```

Plugin manifests use `prava-deploy.provider/v1`. Each invocation receives one JSON request on stdin and must return one JSON response on stdout. Plugins get a minimal process environment plus only the variable names declared in their manifest.

See [provider plugin documentation](docs/provider-plugins.md) and the [manifest schema](api/provider-plugin.schema.json).

## Development-only authorization

Tests and demos can bypass Prava explicitly:

```bash
prava-deploy --payment manual --development plan --provider docker --budget 0.00 --currency USD
```

This state is labeled `development-only` and must not be described as a Prava approval.

## Safety boundaries

- Provider pricing estimates are never represented as final invoices.
- A deployment rollback is not a refund.
- Destroying an application may not delete separately billed databases, volumes, snapshots, IPs, or DNS resources.
- Render deploy hooks cannot query status or destroy a service.
- Fly destruction removes the app and is treated as a destructive action.
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
