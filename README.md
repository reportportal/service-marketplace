# ReportPortal Plugin Marketplace (`service-marketplace`)

Go greenfield implementation on the **`dev`** branch. This is the active registry service.

> **Note:** The Java implementation on `main` / `review-fixes` is **historical only** and is not maintained. All new development happens here on `dev`.

## Overview

HTTP registry for ReportPortal plugin catalogue, publish, lifecycle, licensing, and operator console. Implements [OpenAPI v1](docs/openapi/service-marketplace-v1.yaml) stages 0–10.

## Quick start (local)

```bash
# Generate admin password hash (example password: admin)
go run golang.org/x/crypto/bcrypt@latest
# or: htpasswd -bnBC 10 "" admin | tr -d ':\n'

export ADMIN_PASSWORD_HASH='$2a$10$...'
export JWT_SECRET='dev-secret'
export STORAGE_TYPE=local
export STORAGE_LOCAL_ROOT=./data
export CDN_BASE_URL=http://localhost:8080/cdn

go run ./cmd/marketplace
```

- Health: `GET /health`, `GET /ready`
- Operator UI: http://localhost:8080/operator/
- API: `/api/v1/...`

## Configuration

| Variable | Description |
|----------|-------------|
| `STORAGE_TYPE` | `local` or `gcs` |
| `STORAGE_LOCAL_ROOT` | Local filesystem root (default `./data`) |
| `STORAGE_SIGNING_SECRET` | HMAC secret for local signed URLs |
| `GCS_BUCKET` / `GCS_PRIVATE_BUCKET` | GCS buckets |
| `CDN_BASE_URL` | Public CDN base for 302 redirects |
| `CDN_URL_MAP` | GCP URL map name for invalidation |
| `ADMIN_LOGIN_ENABLED` | Enable admin form login |
| `ADMIN_USERNAME` / `ADMIN_PASSWORD_HASH` | Bcrypt admin credentials |
| `JWT_SECRET` / `JWT_ISSUER` / `JWT_TTL_SECONDS` | Operator session JWT |
| `GITHUB_OAUTH_*` | GitHub OAuth for operator login |
| `PUBLISH_OIDC_AUDIENCE` | GitHub Actions OIDC audience |
| `PUBLISH_OIDC_ALLOWED_SOURCES` | JSON map `repo→pluginId` |
| `GA4_MEASUREMENT_ID` / `GA4_API_SECRET` | Analytics (optional) |
| `GCP_PROJECT` | GCP project for Cloud CDN invalidation |
| `HTTP_ADDR` | Listen address (default `:8080`) |
| `ORPHAN_CLEANUP_INTERVAL` | Orphan artifact cleanup interval |

## Layout

```
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

## API

See [docs/openapi/service-marketplace-v1.yaml](docs/openapi/service-marketplace-v1.yaml) and [docs/schemas/marketplace-manifest.schema.json](docs/schemas/marketplace-manifest.schema.json).

## License

Apache 2.0
