# tagforge (npm / `npx` / `pnpm dlx` Wrapper)

Thin zero-dependency Node.js wrapper for **[tagforge](https://github.com/LorranHippolyte/tagforge)** — the safe, standards-compliant Google Tag Manager CLI and local MCP server written in Go.

On installation, this package downloads the static Go release binary matching your OS and CPU architecture from GitHub Releases and verifies its **SHA-256 checksum** before execution.

## Usage

Run directly without installing globally:

```bash
npx -y tagforge version
npx -y tagforge blueprints list
pnpm dlx tagforge audit --account 123456 --container 789012 --workspace 5 --format markdown
```

Register the local `stdio` MCP server in Claude Code:

```bash
claude mcp add tagforge -- npx -y tagforge mcp
```

## Environment Variables

- `TAGFORGE_BINARY_PATH`: override the binary location (useful for local Go builds or air-gapped environments).
- `TAGFORGE_SKIP_DOWNLOAD=1`: skip postinstall binary download when using `TAGFORGE_BINARY_PATH`.
- `TAGFORGE_RELEASE_BASE_URL`: override the release download base URL.

## Safety by Code

`tagforge` runs in `workspace-only` mode by default: destructive deletions and live container publication are blocked unless explicitly authorized with `--mode full --confirm`.
