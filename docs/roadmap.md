# Roadmap

## Phase 0 — Repository Foundation & Governance (Completed)
- Independent Go 1.26 repository under MIT license (`LorranHippolyte/tagforge`)
- Architecture, safety model, contribution guidelines, security policy, and CI workflow
- GoReleaser configuration skeleton

## Phase 1 — Core GTM API v2 Engine, Safety, Auth & CLI (Completed)
- `internal/safety`: `read-only` / `workspace-only` / `full` modes, `--confirm` gate, `user_permission` isolation, duplicate guard
- `internal/auth`: least-privilege scopes, OAuth 2.0 PKCE loopback login, OS Keychain + `0600` file fallback, Service Account / Refresh Token / Access Token precedence, `auth status`
- `internal/gtm`: GTM API v2 client covering Web & Server-Side resources, token-bucket rate limiter + backoff, defensive snapshot cache with mutation invalidation, concurrency & pre-flight checks
- `internal/cli` & `cmd/tagforge`: full CLI (`table` and `json` output) with unit and mock HTTP integration tests

## Phase 2 — MCP Server (`stdio`) & Official Blueprints v1
- `tagforge mcp` subcommand built on `modelcontextprotocol/go-sdk` sharing the exact `internal/gtm` and `internal/safety` core
- Consolidated resource tools (~20 tools) to preserve LLM context window budget
- Declarative YAML/JSON blueprints (`//go:embed`) with `blueprint plan` (dry-run diff + `dataLayer` contract) and atomic `blueprint apply` (`workspace.bulkUpdate`):
  - **Google:** Google Tag (`googtag`), GA4 Event Settings (`gtes`), GA4 Events (`gaawe` for Lead Gen & GA4 E-commerce `sendEcommerceData`), Google Ads Conversion Linker (`gclidw`), Conversion Tracking (`awct`), Enhanced Conversions (`awec`), Consent Mode v2 (`ad_storage`, `analytics_storage`, `ad_user_data`, `ad_personalization`)
  - **Meta:** Meta Pixel Web (Community Gallery template + unique `event_id`) and Meta Conversions API (sGTM with `event_id` deduplication, `_fbp`/`_fbc`, SHA-256 User Data, `test_event_code`)

## Phase 3 — Deterministic Container Auditor & Tracking Plan Generator
- `tagforge audit` (CLI + MCP tool) checking containers in code:
  - PII/LGPD exposure in parameters/URLs
  - Missing or misconfigured Consent Mode v2 settings
  - Deprecated tags (`ua`, `gaawc`), orphaned triggers/variables, paused/untriggered tags
  - Duplicate Google Tags and Meta Pixel/CAPI deduplication mismatches
  - Unsafe Custom HTML tags replaceable by sandboxed templates
  - Naming, folder, and workspace hygiene
- `tagforge tracking-plan` exporting Markdown tracking documentation and typed `dataLayer.push` snippets (JS/TS, Next.js, Shopify)
- MCP Resources (`gtm://...`) and MCP Prompts with tool fallbacks

## Phase 4 — Multi-Platform Blueprints & E-commerce Packs
- Official blueprints for TikTok Pixel + Events API, LinkedIn Insight Tag + CAPI, Microsoft Clarity & Bing UET, Pinterest Tag
- Shopify + GTM Web/sGTM starter pack (Custom Pixel / Web Pixel `dataLayer` contract + container blueprint)

## Phase 5 — Multi-Platform Distribution & Optional Remote HTTP Mode
- Automated GoReleaser publishing: macOS/Linux/Windows binaries, Homebrew Tap, Scoop/Winget manifests
- Thin npm package (`npx -y tagforge`) distributing native platform binaries
- Optional `tagforge serve --http` (MCP Streamable HTTP + Google OAuth) for self-hosted team deployments or web-only MCP connectors
