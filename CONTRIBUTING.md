# Contributing to tagforge

Thank you for your interest in contributing to `tagforge`!

## Development Setup

### Requirements
- **Go 1.26+**
- `make` (optional, commands can be run directly with `go`)

### Local Workflow
1. Fork and clone the repository.
2. Run checks before making changes:
   ```bash
   make check
   ```
3. Write unit tests for all new code. Never call live Google APIs or touch the real OS keychain in tests:
   - Use `httptest.Server` + `google.golang.org/api/option` (`WithHTTPClient`, `WithEndpoint`, `WithoutAuthentication`) for GTM API tests.
   - Use `keyring.MockInit()` for keyring tests and `t.TempDir()` for file store tests.
4. Ensure formatting, vet, race detector tests, and build pass:
   ```bash
   gofmt -s -w .
   go vet ./...
   go test -race -cover ./...
   make build
   ```

## Standards
- **Stdout vs. Stderr:** Reserve `stdout` strictly for command output (`table`/`json` and future MCP `stdio` JSON-RPC). Send logs and interactive prompts only to `stderr`.
- **Safety First:** Every GTM operation must pass through `internal/safety.Policy.Check` before making any network request.
- **Zero Hardcoded Secrets:** Never commit credentials, OAuth client secrets, service account keys, or tokens.

## Pull Requests
- Keep PRs focused and small.
- Describe what changed, why, and how it was verified.
- Ensure CI passes across Linux and macOS.
