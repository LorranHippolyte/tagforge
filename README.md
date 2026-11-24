# tagforge

**Safe, standards-compliant Google Tag Manager automation, official blueprints, and container auditing — built in Go, local-first, zero VPS required.**

`tagforge` is an open-source CLI and local MCP server designed for engineers, analysts, and agencies who want to configure, inspect, and audit Google Tag Manager containers safely and reproducibly according to official vendor documentation (Google Tag / GA4, Google Ads, Consent Mode v2, Meta Pixel & Conversions API, TikTok, LinkedIn, Microsoft Clarity/UET, Pinterest, Shopify Customer Events, and Server-Side GTM).

---

## Why tagforge?

Most existing GTM automation tools and MCP servers either expose raw API CRUD operations without guardrails—leaving LLMs or scripts to guess complex tag schemas—or ship unverified templates with outdated parameters.

`tagforge` is built around three core principles:

1. **Safety by Code, Not by Prompt**
   - **`workspace-only` mode is the default**: inspect accounts/containers and manage workspace entities freely, while blocking container deletion and live publication unless `--mode full --confirm` is explicitly provided.
   - **Destructive actions require `--confirm`**: deleting tags, triggers, variables, workspaces, disabling built-in variables, or publishing versions always requires explicit confirmation.
   - **User permissions disabled by default**: `accounts.user_permissions` is gated off unless explicitly unlocked via `--allow-user-permissions`.
   - **Optimistic concurrency & pre-checks**: updates and reverts require a valid `fingerprint`; version creation inspects workspace status first and aborts on merge conflicts or empty workspaces.
   - **Duplicate name guard**: prevents accidental duplicate entities on retries.

2. **Core-First Architecture (CLI + Local MCP)**
   - Everything runs through a single, rate-limited, cache-aware Go core (`internal/gtm`, `internal/safety`, `internal/auth`, `internal/blueprints`, `internal/auditor`, `internal/trackingplan`).
   - Use it directly from your terminal or CI pipelines, or launch `tagforge mcp` (`stdio`) to expose the exact same core across 19 context-efficient tools, 6 resources/templates, and 7 workflow prompts to Claude Code, Cursor, Claude Desktop, VS Code, Antigravity, and Codex without needing a remote server or VPS.

3. **Official, Declarative Blueprints & Deterministic Auditing**
   - **16 embedded declarative YAML blueprints** (`//go:embed`) with `plan` (dry-run diff + `dataLayer` snippet generation) and atomic `apply` (`workspace.bulkUpdate`) covering Google Tag/GA4, Lead Gen, GA4 Ecommerce, Google Ads Enhanced Conversions, Consent Mode v2, Meta Pixel + CAPI (sGTM), TikTok Pixel + Events API (sGTM), LinkedIn Insight Tag + CAPI (sGTM), Microsoft Clarity + Advertising UET, Pinterest Tag, and Shopify + GTM Web/sGTM starter packs.
   - Deterministic container auditing (`tagforge audit`, **16 rules**) for Consent Mode v2, PII/LGPD hygiene, orphaned triggers/variables, duplicate tags, deprecated Universal Analytics tags, and cross-platform `event_id` deduplication (Meta, TikTok, LinkedIn CAPI, Pinterest), plus living tracking plans and typed `dataLayer.push` snippet generation (`tagforge tracking-plan`).

---

## Current Status

| Phase | Scope | Status |
|---|---|---|
| **Phase 0** | Independent Go repo, MIT license, CI, docs, safety contract | **Ready** |
| **Phase 1** | Core GTM API v2 client, rate limiter, defensive cache, safety engine, OAuth PKCE + Keychain/Service Account auth, local CLI | **Ready** |
| **Phase 2** | `tagforge mcp` (`stdio`, 17 tools) + Official Google, Meta & Consent Mode v2 Blueprints (`plan`/`apply`) | **Ready** |
| **Phase 3** | Deterministic `audit`, `tracking-plan` generator (`ts`/`js`/`nextjs`/`shopify`), 19 MCP tools, 6 resources/templates & 5 prompts | **Ready** |
| **Phase 4** | 16 total Blueprints (TikTok, LinkedIn, Microsoft Clarity/UET, Pinterest + Shopify Web/sGTM Customer Events packs), 16 audit rules, 7 MCP prompts | **Ready** |
| **Phase 5** | GoReleaser v2 binaries (Homebrew Cask, Scoop, WinGet), thin `npx` wrapper (`packaging/npm`), optional Streamable HTTP MCP (`tagforge serve --http`) & Docker | **Ready** |

See [docs/roadmap.md](docs/roadmap.md) for details.

---

## Installation

### Package Managers & `npx` (Recommended for Releases)

- **Homebrew (macOS & Linux):**
  ```bash
  brew install LorranHippolyte/tap/tagforge
  ```
- **npx / pnpm (`Node >= 18`, downloads & SHA-256 verifies native binary):**
  ```bash
  npx -y tagforge version
  pnpm dlx tagforge blueprints list
  ```
- **Scoop / WinGet (Windows):**
  ```powershell
  scoop bucket add lorran https://github.com/LorranHippolyte/scoop-bucket
  scoop install tagforge
  # or
  winget install LorranHippolyte.tagforge
  ```

### From Source (Go 1.26+)

```bash
git clone https://github.com/LorranHippolyte/tagforge.git
cd tagforge
make build
./bin/tagforge version
```

Or install into `$GOPATH/bin`:

```bash
go install github.com/LorranHippolyte/tagforge/cmd/tagforge@latest
```

---

## Authentication

`tagforge` supports four credential sources (in order of precedence):

1. **Google Service Account** (`GOOGLE_SERVICE_ACCOUNT_KEY` as raw JSON or path, or `GOOGLE_APPLICATION_CREDENTIALS`)
2. **OAuth Refresh Token** (`GOOGLE_CLIENT_ID` + `GOOGLE_CLIENT_SECRET` + `GOOGLE_REFRESH_TOKEN`)
3. **Static Access Token** (`GOOGLE_ACCESS_TOKEN` — debugging only)
4. **Local OAuth 2.0 + PKCE Login** (`tagforge login`), stored in the OS Keychain (`macOS Keychain` / `Windows Credential Manager` / `Linux Secret Service`) with automatic `0600` file-store fallback when a system keyring is unavailable.

### Quickstart with OAuth Login

1. Enable the **Tag Manager API** in your [Google Cloud Console](https://console.cloud.google.com/).
2. Create an **OAuth 2.0 Client ID** of type **Desktop application** and download the JSON file.
3. Run:

```bash
tagforge login --credentials-file /path/to/credentials.json
tagforge auth status
```

*(Never commit `credentials.json` or token files. They are git-ignored by default.)*

---

## Quickstart CLI Examples

```bash
# List accessible GTM accounts
tagforge accounts list

# List containers in an account
tagforge containers list --account 123456

# Lookup a container by its public GTM ID
tagforge containers lookup --public-id GTM-XXXXXXX

# Create a dedicated workspace (safe default workflow)
tagforge workspaces create --account 123456 --container 789012 --name "feat-ga4-checkout"

# Inspect tags as JSON
tagforge tags list --account 123456 --container 789012 --workspace 5 --format json

# Preview workspace changes and compiler status
tagforge workspaces preview --account 123456 --container 789012 --workspace 5

# Create a tag from JSON payload
tagforge tags create --account 123456 --container 789012 --workspace 5 --file tag.json

# Delete a tag (explicit confirmation required)
tagforge tags delete --account 123456 --container 789012 --workspace 5 --tag 42 --confirm

# Create a container version (checks for conflicts and empty changes first)
tagforge workspaces create-version --account 123456 --container 789012 --workspace 5 --name "Add GA4 checkout"

# Publish a version (requires full mode + explicit --confirm + fingerprint)
tagforge versions publish --account 123456 --container 789012 --version 12 --fingerprint 1732412345 --mode full --confirm

# List, plan (dry-run), and apply official tracking blueprints
tagforge blueprints list
tagforge blueprints plan --name google-ga4-ecommerce --account 123456 --container 789012 --workspace 5 --param measurement_id=G-XXXXXXXXXX
tagforge blueprints apply --name meta-pixel-web --account 123456 --container 789012 --create-workspace "bp-meta-web" --import-templates --param pixel_id=123456789
tagforge blueprints apply --name shopify-gtm-web --account 123456 --container 789012 --create-workspace "bp-shopify" --import-templates --param measurement_id=G-XXXXXXXXXX --param server_container_url=https://metrics.store.com
tagforge blueprints apply --name tiktok-pixel-web --account 123456 --container 789012 --workspace 5 --import-templates --param pixel_id=CD9079RC77U0N3GBV16Y

# Audit a workspace (or --live version) and generate a tracking plan with typed snippets
tagforge audit --account 123456 --container 789012 --workspace 5 --fail-on high --format markdown
tagforge tracking-plan --account 123456 --container 789012 --workspace 5 --snippets nextjs --format markdown
```

---

## MCP Server Setup (`stdio`, Local-First)

`tagforge mcp` runs locally over `stdio` using your saved `tagforge login` Keychain credentials (or `GOOGLE_SERVICE_ACCOUNT_KEY` / `GOOGLE_REFRESH_TOKEN` environment variables), keeping `stdout` strictly for JSON-RPC.

### Claude Code

```bash
claude mcp add tagforge -- tagforge mcp
```

### Claude Desktop / Cursor / Antigravity / Codex

```json
{
  "mcpServers": {
    "tagforge": {
      "command": "tagforge",
      "args": ["mcp"]
    }
  }
}
```

*(Pass `"--mode", "read-only"` or `"--mode", "full"` in `args` if you want to override the default `workspace-only` safety policy.)*

### Optional Remote Streamable HTTP Mode (`tagforge serve --http`)

For shared team deployments or web-based MCP connectors, run `tagforge serve --http` behind an HTTPS reverse proxy (non-loopback addresses require `--auth-token` / `TAGFORGE_HTTP_AUTH_TOKEN`):

```bash
export TAGFORGE_HTTP_AUTH_TOKEN="$(openssl rand -hex 32)"
tagforge serve --http --addr 0.0.0.0:8080
```

See [docs/self-hosting.md](docs/self-hosting.md) for `/healthz`, Bearer auth, CORS allowlisting, and Docker Compose instructions.

---

## Global Flags & Environment Variables

| Flag | Env Variable | Default | Description |
|---|---|---|---|
| `--mode` | `TAGFORGE_MODE` | `workspace-only` | Safety mode: `read-only`, `workspace-only`, `full` |
| `--format` | — | `table` | Output format: `table`, `json`, or `markdown` |
| `--profile` | — | `default` | Auth token profile name |
| `--confirm` | — | `false` | Required for destructive actions & publishing |
| `--allow-user-permissions` | `TAGFORGE_ALLOW_USER_PERMISSIONS` | `false` | Unlock `user_permission` operations |
| `--no-cache` | — | `false` | Disable in-memory list snapshot cache |
| `--fresh` | — | `false` | Bypass snapshot cache on list calls |
| — | `TAGFORGE_RATE_LIMIT_QPS` | `0.5` | GTM API rate limiter requests/sec |
| — | `TAGFORGE_GOOGLE_CLIENT_ID` | — | OAuth Client ID (alternative to `--credentials-file`) |
| — | `TAGFORGE_GOOGLE_CLIENT_SECRET` | — | OAuth Client Secret |

---

## Documentation

- [Architecture](docs/architecture.md)
- [Safety Model](docs/safety-model.md)
- [Roadmap](docs/roadmap.md)
- [Blueprints Contract](blueprints/README.md)
- [Self-Hosting (Streamable HTTP MCP)](docs/self-hosting.md)
- [Distribution & Releases](docs/distribution.md)
- [Contributing](CONTRIBUTING.md)
- [Security Policy](SECURITY.md)

---

## License

[MIT](LICENSE) © 2026 Lorran Hippolyte
