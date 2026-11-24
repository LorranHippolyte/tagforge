# Architecture

`tagforge` follows a **Core-First, Local-First** architecture so the CLI, future `stdio` MCP server, and optional remote HTTP adapter share identical safety guarantees, API rate-limiting, caching, and business logic.

## Layered Design

```text
+-------------------------------------------------------------------+
|                         Interfaces                                |
|   CLI (internal/cli)   | MCP stdio (19 tools) |    HTTP (Ph 5)    |
+-------------------------------------------------------------------+
                                 |
                                 v
+-------------------------------------------------------------------+
|                     Domain Engines                                |
| Blueprints Engine | Deterministic Auditor | Tracking Plan Engine  |
+-------------------------------------------------------------------+
                                 |
                                 v
+-------------------------------------------------------------------+
|                       Core Foundation                             |
|  internal/safety  |  internal/auth  |  internal/gtm               |
|  Policy & Guards  |  PKCE/Keyring   |  API v2 + Limiter + Cache   |
+-------------------------------------------------------------------+
```

## Package Breakdown

- `cmd/tagforge` — Binary entrypoint wiring Cobra CLI and exit codes.
- `internal/version` — Build metadata (`Version`, `Commit`, `Date`) injected via `-ldflags`.
- `internal/safety` — Policy engine enforcing operation modes (`read-only`, `workspace-only`, `full`), confirmation gates (`--confirm`), `user_permission` isolation, and duplicate-name checks.
- `internal/auth` — Least-privilege Google Tag Manager OAuth scopes, OS Keychain (`zalando/go-keyring`) + `0600` file fallback store, OAuth 2.0 loopback PKCE login flow, multi-source credential resolver (Service Account, Refresh Token, Static Token, Stored OAuth), and token introspection.
- `internal/gtm` — Official `tagmanager/v2` wrapper featuring:
  - **Rate-limit scheduler** (`x/time/rate` + bounded exponential backoff with jitter on `429` / `5xx`).
  - **Defensive snapshot cache** (JSON-cloned reads with TTL and automatic workspace/container invalidation on writes).
  - **Concurrency & status guards** (mandatory `fingerprint` on updates/reverts/publish; `CreateVersion` pre-checks `GetStatus` for conflicts and empty changes).
  - **Error translation** mapping raw Google API HTTP errors into clear, actionable error types.
- `internal/blueprints` — Embedded YAML blueprint loader, schema validation, dry-run diff planner (`plan`), and atomic `bulkUpdate` applier (`apply`).
- `internal/auditor` — Deterministic container auditor (15 rules across `privacy`, `security`, `reliability`, and `hygiene`) producing scored reports (0–100, Grade A–F) for workspaces or live versions.
- `internal/trackingplan` — Living tracking plan builder generating structured Markdown documentation and typed `dataLayer.push` snippets (`ts`, `js`, `nextjs`, `shopify`) with secret constant redaction.
- `internal/mcpserver` — Local `stdio` MCP server exposing 19 consolidated tools, 6 resources/templates (`gtm://...`), and 5 workflow prompts backed by the shared core.
- `internal/cli` — Cobra commands with `--format table|json|markdown`, clean `stdout`/`stderr` separation, and injectable dependencies for deterministic tests.

## Why Go + Declarative Blueprints?

- **Zero-dependency distribution**: single static binary per OS/architecture, ~10ms startup, native OS keyring support, and optional thin `npx` wrapper via GoReleaser (Phase 5).
- **Community-friendly blueprints**: tracking blueprints live as declarative YAML/JSON specs embedded via `//go:embed` (`blueprints/`), allowing GTM practitioners to contribute validated Google, Meta, or e-commerce setups without writing Go.
