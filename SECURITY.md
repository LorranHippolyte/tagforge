# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in `tagforge` (such as credential leakage, OAuth callback vulnerabilities, safety policy bypasses, or PII exposure), please **do not open a public GitHub issue**.

Instead, report it privately via GitHub Security Advisories on `LorranHippolyte/tagforge` or contact the maintainer directly. Please include:
- A description of the issue and affected component.
- Steps to reproduce.
- Potential impact.

## Security Design Commitments

- **Least-privilege OAuth scopes:** Scopes match the active safety mode (`read-only`, `workspace-only`, `full`).
- **Local-only OAuth callback:** Loopback redirect servers bind strictly to `127.0.0.1` with PKCE (`S256`) and cryptographic `state` validation.
- **No secret logging:** Tokens, refresh tokens, client secrets, and service account private keys are never printed to `stdout`, `stderr`, or JSON status outputs.
- **Strict workspace isolation by default:** `workspace-only` mode blocks container deletion and live publishing; `user_permissions` is disabled by default.
