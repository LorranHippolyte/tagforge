# Self-Hosting `tagforge` (Optional Remote Streamable HTTP MCP)

`tagforge` is **local-first** by default: running the CLI or `tagforge mcp` (`stdio`) on your workstation requires **no VPS, no open ports, and no remote server**.

If you want a shared team endpoint or want to connect web-based MCP clients over HTTPS, use the optional `tagforge serve --http` command behind an HTTPS reverse proxy (Traefik, Caddy, Nginx, or Dokploy).

---

## Security Model for Remote HTTP

1. **Loopback default:** `tagforge serve --http` binds to `127.0.0.1:8080` at `/mcp`.
2. **Non-loopback refuses unauthenticated startup:** binding to `0.0.0.0:8080` (or any non-loopback IP) **fails immediately** unless `--auth-token` / `TAGFORGE_HTTP_AUTH_TOKEN` is configured (or `--allow-insecure-no-auth` is passed explicitly for reverse proxies enforcing their own auth).
3. **Constant-time Bearer check:** requests to `/mcp` must present `Authorization: Bearer <token>`; invalid/missing tokens receive `401 Unauthorized` with `WWW-Authenticate: Bearer realm="tagforge"`.
4. **GTM safety policy still applies:** `TAGFORGE_MODE=workspace-only` remains active by default over HTTP, blocking live `publish` and container `delete` remotely.
5. **Unauthenticated health probe:** `GET /healthz` returns `{"status":"ok","service":"tagforge","version":...,"mode":...}` so Traefik/Docker/Kubernetes health checks work without exposing credentials.
6. **Strict CORS allowlist:** CORS headers are disabled unless `--allow-origin https://app.example.com` is passed (wildcards `*` are never emitted).

---

## Quick Local HTTP Test

```bash
# Loopback only (no Bearer token needed locally)
tagforge serve --http --addr 127.0.0.1:8080

# Check health endpoint
curl http://127.0.0.1:8080/healthz
```

Or with Bearer auth enabled:

```bash
export TAGFORGE_HTTP_AUTH_TOKEN="$(openssl rand -hex 32)"
tagforge serve --http --addr 0.0.0.0:8080
```

---

## Docker / Compose Deployment (Traefik / Dokploy)

See [Dockerfile](../Dockerfile) and [deploy/docker-compose.yml](../deploy/docker-compose.yml):

```bash
export TAGFORGE_HTTP_AUTH_TOKEN="your-long-random-token"
# Place your Google Service Account JSON in deploy/gtm-service-account.json
docker compose -f deploy/docker-compose.yml up -d --build
```

Point your HTTPS reverse proxy to `http://127.0.0.1:8080` and configure MCP clients against:

- **Endpoint:** `https://gtm-mcp.yourdomain.com/mcp`
- **Authorization Header:** `Bearer <TAGFORGE_HTTP_AUTH_TOKEN>`
- **Health Check Path:** `/healthz`

*(Never run HTTP directly over plain internet HTTP without TLS termination in front of `tagforge`.)*
