# Architecture

## Design invariants

1. The language model cannot directly execute a provider command.
2. Every provider action passes through typed Go interfaces and policy validation.
3. External provisioning requires an approved plan and, where configured, a matching Prava authorization.
4. Payment credentials and provider secrets are never written to the journal or supplied to the model.
5. Every local plan, authorization, and deployment has an independent ID and state transition.
6. Provider plugin responses cannot replace local journal IDs or change a deployment's provider ownership.

## Components

```text
CLI / agent goal
      |
      v
OpenAI Responses tool loop ---- host approval gate
      |                                |
      v                                v
Application service ------------ policy engine
      |          |                     |
      |          +---- state journal --+
      |
      +---- project detector
      +---- capability planner
      +---- Prava authorizer
      +---- provider registry
                   |
                   +-- Docker
                   +-- Fly.io CLI
                   +-- Vercel CLI
                   +-- Render hook
                   +-- provider/v1 plugins
```

The OpenAI integration is intentionally an orchestration shell. Domain decisions—capability matching, budget equality, allowed providers, state ownership, and command execution—remain deterministic.

## Agent loop

The Responses API client sends strict function definitions with `parallel_tool_calls: false`. A response can contain reasoning, messages, and function calls. The client preserves every output item and appends tool results using the matching `call_id`. With `store: false`, encrypted reasoning content is requested and replayed rather than reducing history to assistant text.

The loop stops when the model returns a message, an approval gate refuses an action, or twelve tool rounds are exhausted.

## Normalized project model

Detection emits a `ProjectSpec` rather than provider configuration. A plan derives one required portability capability:

- Static project → `static`
- Dockerfile/container → `container`
- Worker → `worker`
- Other service → `buildpack`

Providers declare their supported capabilities. Auto-selection scores only compatible and locally available providers.

## Payment state

```text
not_required
awaiting_approval -> authorized
                  -> expired
                  -> declined
                  -> failed
```

A Prava mandate is merchant-, amount-, currency-, and cadence-scoped. Before deployment, policy checks that the latest authorization:

- is `authorized`;
- belongs to the exact plan;
- names the exact provider;
- matches the plan's budget exactly.

This is an authorization control. Provider invoice settlement remains separate.

## Deployment state

```text
planned -> provisioning -> live -> degraded
                         -> failed
live -> destroying -> destroyed
                  -> failed
```

Payment and deployment states do not collapse into one status. This allows recovery when, for example, authorization succeeds but provider provisioning fails.

## Persistence

`.warden/state.json` uses schema `v1`. Updates are written to a permission-restricted temporary file, flushed, and renamed into place. On Windows, replacement falls back to remove-and-rename after the temporary file is durable. Legacy `.prava-deploy/state.json` journals are read and migrated on the next write.

State contains operational metadata only. Provider-native CLIs retain their own authentication; OpenAI and Render credentials are sourced from environment variables.

## Versioning

- CLI releases follow semantic versioning. The initial product line is `0.x` while command behavior is still evolving.
- Manifest schema: `v1`.
- Journal schema: `v1`.
- Provider RPC protocol: `warden.provider/v1` (legacy v0.1 manifests remain readable).
- A breaking schema or RPC change requires a new version identifier and an explicit migration path. It must not silently reinterpret old state.
