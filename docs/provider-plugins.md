# Provider plugin protocol v1

Provider plugins let Prava Deploy support a host without linking that host's SDK into the Go binary. A plugin is a trusted executable plus a `.provider.json` manifest.

## Manifest

```json
{
  "protocol": "prava-deploy.provider/v1",
  "id": "example-cloud",
  "name": "Example Cloud",
  "description": "Deploy OCI containers to Example Cloud.",
  "command": "example-cloud-provider",
  "args": [],
  "environment": ["EXAMPLE_CLOUD_TOKEN"],
  "capabilities": ["container", "regions", "custom_domain"],
  "billing": "metered",
  "merchant_url": "https://example.com",
  "country": "US"
}
```

Relative commands containing a slash are resolved relative to the manifest. Bare command names are resolved through `PATH`.

The plugin process inherits a minimal operating-system environment and only the explicitly allowlisted variables in `environment`. The request never contains their values.

## Transport

Each invocation is a new process:

- One request JSON document arrives on stdin.
- One response JSON document must be written to stdout.
- Diagnostics belong on stderr.
- A non-zero exit code is an execution failure.
- The parent command's context controls cancellation and timeout.

All requests contain:

```json
{
  "protocol": "prava-deploy.provider/v1",
  "method": "estimate"
}
```

## Methods

### `estimate`

Request fields: `project`, `config`.

Response:

```json
{
  "estimate": {
    "kind": "provider_metered",
    "monthly_min": {"amount": "5.00", "currency": "USD"},
    "monthly_max": {"amount": "25.00", "currency": "USD"},
    "confidence": "low",
    "notes": ["Network egress is not included."]
  },
  "warnings": ["Actual usage may exceed the estimate."]
}
```

### `deploy`

Request fields: `plan`, `dry_run`.

Response field: `deployment`. The core replaces `id`, `plan_id`, `provider_id`, and `created_at` with locally owned values. Provider-native identifiers belong in `provider_ref` or `metadata`.

### `status`

Request field: `deployment`. Return the updated `deployment`.

### `destroy`

Request fields: `deployment`, `dry_run`. Return the updated `deployment`.

## Errors

A handled provider error may return:

```json
{"error": "organization has no billing method"}
```

Do not include credentials, response headers containing tokens, card data, or complete environment values in errors.

## Billing declarations

- `none`: no provider billing gate.
- `existing_account`: provider charges an existing account.
- `prepaid_credits`: provider supports funding credits.
- `fixed_checkout`: provider exposes a fixed purchase.
- `metered`: provider settles variable usage later.

Every billing mode except `none` requires a Prava budget authorization when the project policy enables budget approval.

In protocol v1 these values describe policy behavior only. There is no `fund` or `settle` RPC method, and plugins must not request Prava card credentials through another method.
