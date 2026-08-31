# Threat model

## Protected assets

- Prava account linkage and mandate authority.
- Provider credentials held by native CLIs or environment variables.
- OpenAI API credentials.
- Deployment state, app identifiers, and URLs.
- User intent around spending, provisioning, and destruction.

## Trust boundaries

### Language model

The model is untrusted for authorization. It can select and call exposed tools but cannot execute commands directly. Go handlers validate arguments and host-side gates authorize consequential calls.

### Repository content

Repository filenames and metadata may be adversarial. Detection reads only bounded files, ignores dependency/build directories, and never interprets repository text as shell syntax. Provider commands receive arguments through `exec.Command`, not a shell.

### Provider plugins

Plugins are trusted executable code. Their environment is reduced to operating-system essentials and a manifest allowlist, but they can read files available to the current operating-system user. Install only trusted plugins and use OS sandboxing for stronger isolation.

### Native provider CLIs

Provider CLIs own authentication and may mutate provider state. They execute only after plan validation and approval. Their output is stored only as bounded operational metadata.

### Prava CLI

Prava commands are marked sensitive. Prava payment credentials are never requested by Warden. The integration uses mandate setup and approval status only; it does not call mandate charge or payment-session token output.

## Key mitigations

- No shell command concatenation.
- Exact budget equality between plan and authorization.
- Idempotent local plan and authorization references.
- Secrets rejected from manifest provider config.
- `.env.example` parsing records names only, never values.
- JSON schemas use `additionalProperties: false` for agent tools.
- Destruction requires an independent approval.
- Agent tool rounds are bounded.
- OpenAI response history uses `store: false`.

## Residual risks

Interactive conversation history and readline history are retained only in process memory and are not written to disk. Closing Warden or using `/clear` discards them. Deployment plans, authorization metadata, and receipts remain separate in the project journal.

- Provider CLIs and plugins are supply-chain dependencies.
- Provider pricing and billing can differ from estimates.
- A remote Docker context may represent billed infrastructure even though Docker is marked unbilled.
- A failed AWS deployment can leave a billable Lightsail container service; Warden journals the service name and region as soon as creation succeeds so the normal destroy path can remove it.
- Deleting an AWS Lightsail deployment deletes the complete recorded container service, but does not delete the local Docker image or unrelated AWS resources.
- A process crash after provider success but before journal persistence can require manual reconciliation.
- The Windows state-file replacement fallback has a brief non-atomic replacement window.
- Provider-side resources related to an app may survive app deletion.
