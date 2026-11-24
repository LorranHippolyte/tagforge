# Distribution & Release Guide

`tagforge` ships standalone static binaries (zero runtime dependencies) via **GoReleaser v2**, plus a thin **`npx` / `pnpm dlx` npm wrapper** (`packaging/npm/`) that downloads and SHA-256 verifies the official GitHub Release binary for the user's OS and CPU architecture.

---

## Supported Installation Channels

- **Homebrew (macOS & Linux):**
  ```bash
  brew install LorranHippolyte/tap/tagforge
  ```
- **Scoop (Windows):**
  ```powershell
  scoop bucket add lorran https://github.com/LorranHippolyte/scoop-bucket
  scoop install tagforge
  ```
- **WinGet (Windows):**
  ```powershell
  winget install LorranHippolyte.tagforge
  ```
- **npm / pnpm (`npx` / `pnpm dlx` wrapper):**
  ```bash
  npx -y tagforge version
  pnpm dlx tagforge blueprints list
  ```
- **Go Toolchain:**
  ```bash
  go install github.com/LorranHippolyte/tagforge/cmd/tagforge@latest
  ```
- **GitHub Releases Archives:** `.tar.gz` (Linux/macOS `amd64`/`arm64`) and `.zip` (Windows `amd64`/`arm64`) with `checksums.txt`.

---

## Cutting a Release (`vX.Y.Z`)

1. Ensure `main` passes CI (`gofmt`, `go vet`, `go test -race ./...`, `node --test packaging/npm/test/wrapper.test.js`, `goreleaser check`).
2. Tag and push a SemVer tag:
   ```bash
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```
3. [`.github/workflows/release.yml`](../.github/workflows/release.yml) automatically:
   - Runs `go test -race ./...`.
   - Builds archives + `checksums.txt` and publishes the GitHub Release via GoReleaser v2.
   - Updates Homebrew tap (`HOMEBREW_TAP_TOKEN`), Scoop bucket (`SCOOP_BUCKET_TOKEN`), and WinGet PR (`WINGET_GITHUB_TOKEN`) when those optional repository secrets are configured (and gracefully skips any publisher whose token is unset).
   - Syncs `packaging/npm/package.json` to the release version and publishes to npm when `NPM_TOKEN` is configured.
