# Changelog

This project follows Semantic Versioning. Manifest, journal, and provider protocol versions are tracked independently because their compatibility lifetimes differ from the CLI release.

## 0.2.0 - 2026-08-02

- Renamed the product from Prava Deploy to Warden and the executable to `ward`.
- Renamed the generated manifest to `warden.yaml`, operational state to `.warden/`, provider environment prefix to `WARD_`, and provider protocol to `warden.provider/v1`.
- Preserved read compatibility for v0.1 manifests, journals, plugin manifests, and `PRAVA_DEPLOY_PROVIDER_PLUGINS` during migration.
- Added `.env.example` covering Warden, OpenAI, Prava, Render, Fly.io, and Vercel configuration.

## 0.1.0 - 2026-08-01

- Initial agentic deployment workflow using the OpenAI Responses API.
- Prava merchant-scoped budget mandate integration.
- Docker, Fly.io, Vercel, and Render adapters.
- Versioned provider plugin protocol with environment allowlisting.
- Project detection, capability matching, policy gates, and persistent deployment journal.
- Cross-platform CI and GoReleaser configuration.
