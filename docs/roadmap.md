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

## Phase 2 — MCP Server (`stdio`) & Official Blueprints v1 (Completed)
- `tagforge mcp` subcommand built on `modelcontextprotocol/go-sdk` sharing the exact `internal/gtm`, `internal/safety`, `internal/auth`, and `internal/blueprints` core
- Consolidated resource tools (17 tools) to preserve LLM context window budget
- Declarative YAML blueprints (`//go:embed`) with `plan` (dry-run diff + `dataLayer` contract) and atomic `apply` (`workspace.bulkUpdate`):
  - **Google:** `google-tag-ga4-base` (`googtag` + `gtes`), `google-ga4-leadgen` (`gaawe`), `google-ga4-ecommerce` (`gaawe` + `sendEcommerceData`), `google-ads-conversions` (`gclidw` + `awct` + `awec`), `consent-mode-v2` (`consentInit` baseline)
  - **Meta:** `meta-pixel-web` (`stape-io/fb-tag` + `stape-io/unique-event-id-variable`) and `meta-capi-sgtm` (`stape-io/facebook-tag` with `event_id` deduplication)

## Phase 3 — Deterministic Container Auditor & Tracking Plan Generator (Completed)
- `tagforge audit` (CLI + MCP tool) checking containers in code:
  - PII/LGPD exposure in parameters/URLs
  - Missing or misconfigured Consent Mode v2 settings
  - Deprecated tags (`ua`, `gaawc`), orphaned triggers/variables, paused/untriggered tags
  - Duplicate Google Tags and Meta Pixel/CAPI deduplication mismatches
  - Unsafe Custom HTML tags replaceable by sandboxed templates
  - Naming, folder, and workspace hygiene
- `tagforge tracking-plan` exporting Markdown tracking documentation and typed `dataLayer.push` snippets (JS/TS, Next.js, Shopify)
- MCP Resources (`gtm://...`) and MCP Prompts with tool fallbacks

## Phase 4 — Multi-Platform Blueprints & E-commerce Packs (Completed)
- Official declarative blueprints (`16` total embedded in binary):
  - **TikTok:** `tiktok-pixel-web` (`stape-io/tiktok-web-tag` + `stape-io/unique-event-id-variable`) and `tiktok-events-api-sgtm` (`tiktok/gtm-template-eapi` with `event_id` deduplication)
  - **LinkedIn:** `linkedin-insight-web` (`linkedin/linkedin-gtm-community-template`) and `linkedin-capi-sgtm` (`linkedin-developers/linkedin-capi-tag-template` with SHA-256 user matching and `eventId`)
  - **Microsoft:** `microsoft-clarity-web` (`microsoft/clarity-gtm-template`) and `microsoft-uet-web` (`baut` Page Load + Variable Revenue)
  - **Pinterest:** `pinterest-tag-web` (`pinterest/ws-gtm-template` with `useGA4Ecommerce: true`)
- Shopify + GTM starter packs: `shopify-gtm-web` (Shopify Customer Events sandbox bridge with `customerPrivacy` -> Consent Mode v2, `gtes` `page_location` override, full GA4 Ecommerce, optional Google Ads Enhanced Conversions) and `shopify-sgtm-pack` (`sgtmgaaw` forwarder ready for CAPI composition)

## Phase 5 — Multi-Platform Distribution & Optional Remote HTTP Mode (Completed)
- Automated GoReleaser v2 publishing (`.goreleaser.yaml` + `.github/workflows/release.yml`): static macOS/Linux/Windows binaries (`amd64`/`arm64`), SHA-256 `checksums.txt`, Homebrew Cask tap, Scoop bucket, and WinGet PR automation
- Thin zero-dependency npm package (`packaging/npm`, `npx -y tagforge` / `pnpm dlx tagforge`) downloading and SHA-256 verifying official GitHub Release binaries
- Optional `tagforge serve --http` (MCP Streamable HTTP with `/healthz`, constant-time Bearer token auth, non-loopback refusal without auth, and strict CORS allowlist) + `Dockerfile` & `deploy/docker-compose.yml`
