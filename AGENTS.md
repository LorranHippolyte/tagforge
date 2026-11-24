# AGENTS.md — Guidelines for AI Coding Agents

## Overview

`tagforge` is an independent, open-source Go CLI & upcoming MCP server for safe, standards-compliant Google Tag Manager automation, official blueprints, and container auditing.

## Workflow (PREVC)

Follow **PREVC** (Plan -> Review -> Execute -> Verify -> Complete):
1. Do not make wide architectural changes without an approved plan.
2. Keep functions focused, cleanly typed, and covered by unit tests.
3. Verify every change before concluding:
   ```bash
   test -z "$(gofmt -l .)" && go vet ./... && go test -race -cover ./... && make build
   ```

## Engineering Rules

- **Go Version:** Target `go 1.26` as declared in `go.mod`.
- **Clean `stdout`:** NEVER print logs, warnings, banners, or debug info to `os.Stdout`. Only command output (`table` / `json`)—and in Phase 2, MCP JSON-RPC frames—belongs on `stdout`. Use `os.Stderr` (or injected `io.Writer`) for diagnostics and interactive OAuth prompts.
- **Safety First:** Every GTM operation MUST call `safety.Policy.Check(...)` BEFORE making any HTTP/API request.
  - Default mode is `workspace-only`.
  - `delete`, `publish`, and `disable` (built-in variables) ALWAYS require `Confirm: true`.
  - `user_permission` is blocked unless `AllowUserPermissions: true`.
- **Concurrency & Caching:**
  - All Google Tag Manager API v2 calls MUST go through `gtm.Scheduler` (token-bucket rate limiter + bounded retry on `429`/`5xx`).
  - Cached list snapshots MUST return defensive copies and MUST be invalidated on any mutating operation.
- **Testing Discipline:**
  - Never depend on live Google credentials or Application Default Credentials in tests.
  - Mock Google APIs using `net/http/httptest.Server` with `option.WithHTTPClient`, `option.WithEndpoint`, and `option.WithoutAuthentication()`.
  - Never touch the real macOS/Linux/Windows keyring in tests: call `keyring.MockInit()` and use `t.TempDir()` for file-backed storage tests.
- **No Secrets:** Never commit `credentials.json`, `token.json`, `*.key.json`, `.env`, or literal tokens.
