# Safety Model

`tagforge` enforces all guardrails **in deterministic Go code** before any API call leaves your machine. It does not rely on LLM prompts or user discipline alone.

## 1. Execution Modes (`--mode`)

| Mode | Scope | Allowed Actions | Blocked Actions |
|---|---|---|---|
| `read-only` | Read & inspection | `read`, `preview` | All create, update, delete, revert, sync, version creation, and publish operations |
| `workspace-only` *(default)* | Safe authoring inside a workspace | Reads, previews, workspace-scoped CRUD (`tag`, `trigger`, `variable`, `builtin_variable`, `folder`, `template`, `client`, `transformation`, `zone`, `gtag_config`), `workspace create/sync`, `create_version` | `publish`, deleting a `container` or `environment`, and `user_permission` |
| `full` | Full lifecycle | Everything allowed in `workspace-only`, plus `publish` and container/environment mutations | `user_permission` (unless `--allow-user-permissions` is also set) |

## 2. Explicit Confirmation Gates (`--confirm`)

Regardless of mode (`workspace-only` or `full`), the following operations ALWAYS require `Confirm == true` (`--confirm` on CLI):
- `delete` (tags, triggers, variables, folders, workspaces, containers)
- `disable` (disabling built-in variables)
- `publish` (publishing a container version to live traffic)

## 3. User Permissions Lock (`--allow-user-permissions`)

Managing GTM user permissions (`accounts.user_permissions`) is security-sensitive and blocked for all actions unless `--allow-user-permissions` (or `TAGFORGE_ALLOW_USER_PERMISSIONS=true`) is explicitly passed.

## 4. Least-Privilege OAuth Scopes

`internal/auth.ScopesForMode` requests only the Google Tag Manager scopes needed for the selected mode:
- `read-only`: `tagmanager.readonly`
- `workspace-only`: `tagmanager.readonly`, `tagmanager.edit.containers`, `tagmanager.edit.containerversions`
- `full`: adds `tagmanager.publish` and `tagmanager.delete.containers` (plus `tagmanager.manage.users` only when `--allow-user-permissions` is enabled)

## 5. Optimistic Concurrency & Pre-Flight Checks

- **Mandatory fingerprints:** Updates, reverts, and version publishing require the resource `fingerprint` returned by `get`/`create-version`. Stale updates fail loudly rather than overwriting teammate changes.
- **Version pre-check:** `CreateVersion` calls `workspaces.getStatus` first and aborts if:
  - Merge conflicts exist (`ErrWorkspaceHasConflicts`)
  - No pending workspace changes exist (`ErrWorkspaceNoChanges`)
- **Duplicate name guard:** Creating workspaces, tags, triggers, variables, or folders checks existing entity names (case-insensitive) and prevents accidental duplicate creation on retries.

## 6. Secret & PII Protection

- Local OAuth tokens are persisted in the OS Keychain (`tagforge`), falling back to `0600` files in `$XDG_CONFIG_HOME/tagforge/tokens/`.
- `auth status` inspects tokens via Google's `tokeninfo` endpoint and never outputs raw access tokens, refresh tokens, client secrets, or private keys.
- `stdout` is strictly reserved for structured command results; interactive prompts and warnings go to `stderr`.
