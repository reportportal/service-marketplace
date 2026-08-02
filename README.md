# ReportPortal Plugin Marketplace

## Overview

HTTP registry for ReportPortal plugin catalogue, publish, lifecycle, licensing, and operator console. Implements [OpenAPI v1](docs/openapi/service-marketplace-v1.yaml).

## Quick start (local)

Requires [Go 1.22+](https://go.dev/dl/) on your `PATH`.

Production requires strong `JWT_SECRET` and `STORAGE_SIGNING_SECRET` (≥32 chars). Set `TRUSTED_PROXY_HOPS` only when behind a trusted reverse proxy.

After start:

- Health: `GET http://localhost:8080/health`, `GET http://localhost:8080/ready`
- Operator UI: <http://localhost:8080/operator/>
- API: `/api/v1/...`

### Linux / macOS

```bash
export ALLOW_INSECURE_DEFAULTS=true   # local/dev only — never in production
export ADMIN_PASSWORD_HASH='$2a$10$...'
export JWT_SECRET='local-dev-jwt-secret-at-least-32-chars!!'
export STORAGE_SIGNING_SECRET='local-dev-signing-secret-32chars!'
# Or omit the two secrets above when ALLOW_INSECURE_DEFAULTS=true

export STORAGE_TYPE=local
export STORAGE_LOCAL_ROOT=./data
export CDN_BASE_URL=http://localhost:8080/cdn

go run ./cmd/marketplace
```

### Windows (PowerShell)

From the repository root (`service-marketplace`):

```powershell
$env:ALLOW_INSECURE_DEFAULTS = "true"   # local/dev only — never in production
$env:STORAGE_TYPE = "local"
$env:STORAGE_LOCAL_ROOT = ".\data"
$env:CDN_BASE_URL = "http://localhost:8080/cdn"
$env:HTTP_ADDR = ":8080"

# Optional when ALLOW_INSECURE_DEFAULTS=true — otherwise set strong secrets (≥32 chars):
# $env:JWT_SECRET = "local-dev-jwt-secret-at-least-32-chars!!"
# $env:STORAGE_SIGNING_SECRET = "local-dev-signing-secret-32chars!"

# Optional admin login (bcrypt hash). Generate one, then set:
#   go run golang.org/x/crypto/bcrypt@latest
# $env:ADMIN_LOGIN_ENABLED = "true"
# $env:ADMIN_USERNAME = "admin"
# $env:ADMIN_PASSWORD_HASH = '$2a$10$...'

go run .\cmd\marketplace
```

Stop with `Ctrl+C`. Data is written under `.\data`.

Verify in another PowerShell window:

```powershell
curl.exe -s http://localhost:8080/health
curl.exe -s http://localhost:8080/api/v1/plugins
curl.exe -s http://localhost:8080/api/v1/auth/config
```

## Configuration

| Variable | Description |
| ---------- | ------------- |
| `STORAGE_TYPE` | `local` or `gcs` |
| `STORAGE_LOCAL_ROOT` | Local filesystem root (default `./data`) |
| `STORAGE_SIGNING_SECRET` | HMAC secret for local signed URLs |
| `GCS_BUCKET` / `GCS_PRIVATE_BUCKET` | GCS buckets |
| `CDN_BASE_URL` | Public CDN base for 302 redirects |
| `CDN_URL_MAP` | GCP URL map name for invalidation |
| `ADMIN_LOGIN_ENABLED` | Enable admin form login |
| `ADMIN_USERNAME` / `ADMIN_PASSWORD_HASH` | Bcrypt admin credentials |
| `JWT_SECRET` / `JWT_ISSUER` / `JWT_TTL_SECONDS` | Operator session JWT (≥32 chars; required unless `ALLOW_INSECURE_DEFAULTS`) |
| `STORAGE_SIGNING_SECRET` | HMAC for signed CDN URLs (≥32 chars; required unless insecure defaults) |
| `ALLOW_INSECURE_DEFAULTS` | `true` only for local/dev — enables weak default secrets |
| `TRUSTED_PROXY_HOPS` | Honor `X-Forwarded-For` only when >0 (login rate-limit keying) |
| `GITHUB_OAUTH_*` | GitHub OAuth for operator login |
| `PUBLISH_OIDC_AUDIENCE` | Required when OIDC allow-list is non-empty |
| `PUBLISH_OIDC_ALLOWED_SOURCES` | JSON map `repo→pluginId` |
| `GA4_MEASUREMENT_ID` / `GA4_API_SECRET` | Analytics (optional) |
| `GCP_PROJECT` | GCP project for Cloud CDN invalidation |
| `HTTP_ADDR` | Listen address (default `:8080`) |
| `ORPHAN_CLEANUP_INTERVAL` | How often the orphan-cleanup goroutine wakes up to check whether a sweep is due (default `5m`) — the sweep itself only runs once per `ORPHAN_CLEANUP_RUN_INTERVAL`, and only if enabled; see below |

### Orphan cleanup sweeper — unsupported, ships disabled

**Do not set `ORPHAN_CLEANUP_ENABLED=true` in production.** The orphan-cleanup sweep deletes plugin version directories it decides are unreferenced by `index.json`. Three independent review rounds each found a distinct way to make it delete a version that was never actually orphaned — it **may delete committed, in-use plugin versions**. Two of those three routes are closed in code; the third is documented in review but not closed, and no fourth guard attempt has been made. Enabling it is unsupported pending a proven guard — see the doc comment on `internal/lifecycle.OrphanCleanup` for the full defeat history.

The disabled state is enforced as a contract, not a default that happens to be `false` today: there is no environment value, missing or malformed, that turns the sweep on by accident (`internal/config.TestLoad_OrphanCleanupDisabledByDefault` fails the build if that ever regresses).

What remains safe to use today is the **dry-run path**: with `ORPHAN_CLEANUP_ENABLED=true` and `ORPHAN_CLEANUP_DRY_RUN=true` (the default once enabled), the sweep runs its full decision process and reports exactly what it would delete, without issuing a single delete call, so an operator can compare its output against what should be deleted before anyone trusts it further.

| Variable | Description |
| ---------- | ------------- |
| `ORPHAN_CLEANUP_ENABLED` | Gates the sweep entirely. Default `false`. **Unsupported in production** — see above. |
| `ORPHAN_CLEANUP_DRY_RUN` | Default `true` even once enabled, so enabling and trusting it to delete are two separate, deliberate actions. |
| `ORPHAN_CLEANUP_MIN_AGE` | Age guard: a version directory is never a deletion candidate until its newest object is at least this old (default `24h`). |
| `ORPHAN_CLEANUP_RUN_INTERVAL` | Minimum time between sweep attempts (default `24h`). |
| `ORPHAN_CLEANUP_LEASE_TTL` | How long one replica holds the cross-replica sweep lease (default `15m`). |

## Layout

```txt
cmd/marketplace/          Entrypoint
internal/config/          Environment config
internal/domain/          Index, plugin, manifest, license types
internal/storage/         ObjectStore (local + GCS)
internal/cdn/             CDN invalidation
internal/auth/            Session JWT, admin, GitHub OAuth, publish OIDC
internal/catalogue/       Public read API
internal/publish/         Jar ingest & index commit
internal/lifecycle/       Block, remove, tier, advisory, orphan cleanup
internal/license/         authorized_keys.json entitlements
internal/analytics/       GA4 events
internal/httpapi/         Chi router & handlers
web/operator/             Operator console (static)
actions/marketplace-publish-action/  GitHub Action for CI publish
charts/service-marketplace/          Helm chart
```

## Docker

```bash
docker build -t service-marketplace .
docker run -p 8080:8080 -e ADMIN_PASSWORD_HASH='...' -e JWT_SECRET='...' -v mp-data:/data service-marketplace
```

## Tests

```bash
go test ./...
```

## GitHub Oauth App

GitHub login for the Operator UI is enabled when these three env vars are set: `GITHUB_OAUTH_CLIENT_ID`, `GITHUB_OAUTH_CLIENT_SECRET`, and `GITHUB_OAUTH_ORG`.

### 1. Create a GitHub OAuth App

1. GitHub → **Settings** → **Developer settings** → **OAuth Apps** → **New OAuth App**
2. Set:
   - **Homepage URL:** `http://localhost:8080/operator/`
   - **Authorization callback URL:** `http://localhost:8080/api/v1/auth/github/callback`
3. Create the app and generate a **Client secret**.

For non-local hosts, use your real base URL in both fields (and in `GITHUB_OAUTH_REDIRECT_URL`).

### 2. Set env vars and start

PowerShell example:

```powershell
$env:ALLOW_INSECURE_DEFAULTS = "true"
$env:STORAGE_TYPE = "local"
$env:STORAGE_LOCAL_ROOT = ".\data"
$env:CDN_BASE_URL = "http://localhost:8080/cdn"

$env:GITHUB_OAUTH_CLIENT_ID = "<from OAuth App>"
$env:GITHUB_OAUTH_CLIENT_SECRET = "<from OAuth App>"
$env:GITHUB_OAUTH_ORG = "reportportal"   # org the user must belong to
# restrict to a team (slug)
$env:GITHUB_OAUTH_ALLOWED_TEAM = "marketplace-operators"
# Optional override (defaults to localhost callback above)
# $env:GITHUB_OAUTH_REDIRECT_URL = "http://localhost:8080/api/v1/auth/github/callback"

go run .\cmd\marketplace
```

### 3. Verify config:

```powershell
curl.exe -s http://localhost:8080/api/v1/auth/config
```

You should see `"githubEnabled": true`.

## API

See [docs/openapi/service-marketplace-v1.yaml](docs/openapi/service-marketplace-v1.yaml) and [docs/schemas/marketplace-manifest.schema.json](docs/schemas/marketplace-manifest.schema.json).

## License

Apache 2.0
