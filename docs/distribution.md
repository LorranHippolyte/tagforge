# Distribution & Release Guide

`tagforge` distributes standalone static binaries (zero runtime dependencies) through **GoReleaser v2** directly from **`LorranHippolyte/tagforge`** — **100% GitHub-native with zero external repositories or package-registry accounts required**.

Both the Homebrew Cask ([`Casks/tagforge.rb`](../Casks/tagforge.rb)) and the Scoop manifest ([`bucket/tagforge.json`](../bucket/tagforge.json)) live inside this repository and are updated automatically on every tagged release using the workflow's built-in `GITHUB_TOKEN`.

---

## Supported Installation Channels (100% GitHub-Native)

### Homebrew (macOS & Linux)

```bash
brew tap LorranHippolyte/tagforge https://github.com/LorranHippolyte/tagforge
brew trust lorranhippolyte/tagforge
brew install --cask LorranHippolyte/tagforge/tagforge
```

> Homebrew 5+ requires `brew trust` once for third-party taps before loading cask definitions.

### Scoop (Windows)

```powershell
scoop bucket add tagforge https://github.com/LorranHippolyte/tagforge
scoop install tagforge
```

### Prebuilt GitHub Release Archives (`tar.gz` / `zip` + `checksums.txt`)

Download the binary archive for your platform (`darwin`, `linux`, `windows` × `amd64`, `arm64`) and verify its SHA-256 digest against `checksums.txt` on the [Releases page](https://github.com/LorranHippolyte/tagforge/releases).

### Go Toolchain & Source Build

```bash
go install github.com/LorranHippolyte/tagforge/cmd/tagforge@latest
```

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
   - Updates `Casks/tagforge.rb` (Homebrew Cask) and `bucket/tagforge.json` (Scoop) directly on `main` using `secrets.GITHUB_TOKEN` (zero extra PATs required).
   - Skips optional external registries cleanly when their secrets are not configured.

---

## Distribution Backlog (Optional Future Channels)

The scaffolding for external package registries is already present in the repository and remains parked in the backlog while `v0.1.x` ships 100% from GitHub:

- **npm / `npx` (`tagforge` wrapper in [`packaging/npm/`](../packaging/npm/)):** thin zero-dependency Node.js wrapper (`>= 18`) that downloads and SHA-256 verifies official GitHub Release archives; activates automatically on future tags once `NPM_TOKEN` is configured.
- **WinGet (`LorranHippolyte.tagforge`):** GoReleaser PR publisher targeting `microsoft/winget-pkgs`; activates automatically once `WINGET_GITHUB_TOKEN` and a personal fork are configured.
