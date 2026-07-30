# service-marketplace

ReportPortal Plugin Marketplace registry — public catalogue for ReportPortal plugins.

Plugins and plugins metadata stored in GCS (or local filesystem for development).

## Quick start (local profile)

`bootRun` activates the `local` profile (`application-local.yml`). From the repo root:

```bash
./gradlew bootRun
```

On Windows:

```bat
.\gradlew.bat bootRun
```

| Endpoint         | URL                                     |
| ---------------- | --------------------------------------- |
| Public catalogue | <http://localhost:8080/api/v1/plugins>  |
| Operator UI      | <http://localhost:8080/operator/>       |
| Local CDN proxy  | <http://localhost:8080/cdn/…>           |
| Health           | <http://localhost:8080/actuator/health> |

**Local admin login:** username `admin`, password is the same.

The packaged jar carries **no default profile**, run it with
`SPRING_PROFILES_ACTIVE=local` for a throwaway local instance, or provide real secrets (see below).

Optional GitHub OAuth: set `GITHUB_OAUTH_CLIENT_ID` and `GITHUB_OAUTH_CLIENT_SECRET` to enable **Login with GitHub**. Until both are set, both GitHub auth endpoints return `503`.

### Tests

```bash
./gradlew test
```

## OpenAPI

Contract: [`docs/openapi/service-marketplace-v1.yaml`](docs/openapi/service-marketplace-v1.yaml)

Operator publish, block, remove, advisory, license, and auth endpoints are documented there.

## Environment variables

Spring relaxed binding maps env vars to `marketplace.*` properties (see `application.yml`).

| Variable                         | Property                                          | Purpose                                                              |
| -------------------------------- | ------------------------------------------------- | -------------------------------------------------------------------- |
| `STORAGE_TYPE`                   | `marketplace.storage.type`                        | `local` (default) or `gcs`                                           |
| `STORAGE_LOCAL_ROOT`             | `marketplace.storage.local.root`                  | Local object root (default `./data/marketplace`)                     |
| `GCS_BUCKET`                     | `marketplace.gcs.bucket`                          | Public GCS bucket (Cloud CDN origin)                                 |
| `GCS_PRIVATE_BUCKET`             | `marketplace.gcs.private-bucket`                  | Private GCS bucket for premium artifacts                             |
| `GCS_LOCATION`                   | `marketplace.gcs.location`                        | GCS bucket location                                                  |
| `CDN_BASE_URL`                   | `marketplace.cdn.base-url`                        | Public CDN base URL for catalogue/asset links                        |
| `CDN_URL_MAP`                    | `marketplace.cdn.url-map`                         | GCP URL map name for cache invalidation                              |
| `ADMIN_USERNAME`                 | `marketplace.auth.admin.username`                 | Operator admin username                                              |
| `ADMIN_PASSWORD_HASH`            | `marketplace.auth.admin.password-hash`            | **Required** bcrypt hash (plaintext admin password is not supported) |
| `JWT_SECRET`                     | `marketplace.auth.jwt.secret`                     | **Required.** Session/OAuth-state JWT signing secret, ≥32 chars      |
| `JWT_ISSUER`                     | `marketplace.auth.jwt.issuer`                     | JWT `iss` claim                                                      |
| `JWT_TTL_SECONDS`                | `marketplace.auth.jwt.ttl-seconds`                | Session lifetime                                                     |
| `GITHUB_OAUTH_CLIENT_ID`         | `marketplace.auth.github.client-id`               | GitHub OAuth App client id                                           |
| `GITHUB_OAUTH_CLIENT_SECRET`     | `marketplace.auth.github.client-secret`           | GitHub OAuth App secret                                              |
| `GITHUB_OAUTH_ALLOWED_ORG`       | `marketplace.auth.github.allowed-org`             | Required org membership                                              |
| `GITHUB_OAUTH_ALLOWED_TEAM`      | `marketplace.auth.github.allowed-team`            | Optional team **slug** (e.g. `core-team`); enforced when set         |
| `GITHUB_OAUTH_REDIRECT_URI`      | `marketplace.auth.github.redirect-uri`            | OAuth callback URL                                                   |
| `GITHUB_OAUTH_STATE_TTL_SECONDS` | `marketplace.auth.github.oauth-state-ttl-seconds` | Signed OAuth `state` TTL (default `600`)                             |
| `LOGIN_RATE_LIMIT_*`             | `marketplace.auth.login-rate-limit.*`             | Per-username admin login lockout (see below)                         |
| `PUBLISH_OIDC_AUDIENCE`          | `marketplace.publish-oidc-trust.audience`         | Expected OIDC `aud` for CI publish                                   |
| `MAX_UPLOAD_FILE_SIZE`           | `spring.servlet.multipart.max-file-size`          | Max size of a single bundle part (default `128MB`)                   |
| `MAX_UPLOAD_REQUEST_SIZE`        | `spring.servlet.multipart.max-request-size`       | Max size of the whole bundle (default `160MB`)                       |

Outside the `local` and `test` profiles, `StartupSecurityValidator` aborts startup when `JWT_SECRET`
is missing, shorter than 32 characters, or equal to a value committed to this repository, and when
`ADMIN_PASSWORD_HASH` is missing or equal to the development bcrypt hash. Plaintext admin password
configuration is not supported.

GitHub OAuth uses a signed, browser-bound `state` (HttpOnly `mp_oauth_state` cookie + authorize
query param) so login works across replicas without a server-side map. When
`GITHUB_OAUTH_ALLOWED_TEAM` is set, the callback requires an **active** membership in that team
slug under the allowed org.

Admin password login applies a per-username lockout (default: 5 failures / 5 minutes → 60s lockout
with exponential backoff up to 15 minutes) and returns `429 TOO_MANY_REQUESTS`. IP throttling
belongs at the edge — on GKE, attach a Cloud Armor policy via Helm `cloudArmor.securityPolicy`
(see the chart README). The Operator UI is served under a strict Content-Security-Policy
(`script-src`/`style-src 'self'`, no `unsafe-inline`).

**`publishOidcTrust.allowedSources`** (Helm `publishOidcTrust.allowedSources`, Spring map): maps GitHub repository `owner/repo` → plugin id for OIDC publish trust (ADR-014). Example:

```yaml
publishOidcTrust:
  audience: marketplace.reportportal.io
  allowedSources:
    reportportal/plugin-jira-cloud: plugin-jira-cloud
```

OIDC publishing is disabled when `allowedSources` is empty. Every accepted OIDC token must come
from a configured repository, and that repository may publish only its mapped plugin id.

## Local CDN

With `STORAGE_TYPE=local`, `LocalCdnController` serves public objects under `/cdn/**`. Premium artifacts use HMAC-signed, short-lived `/cdn-private/**` URLs. Set `CDN_BASE_URL` to match (local default: `http://localhost:8080/cdn`). `CatalogueService` builds public asset URLs from this base.

Only `index.json` and `plugins/**` are publicly servable.

For GCP deployments, point `CDN_BASE_URL` at the public bucket's Cloud CDN hostname and set `CDN_URL_MAP` for invalidation on publish/block. The private bucket must not be attached to Cloud CDN or granted public IAM access.

The service intentionally does not fall back to the public bucket.

## GCS / storage layout

```text
index.json
plugins/{id}/plugin.json
plugins/{id}/versions/{ver}/manifest.json
plugins/{id}/versions/{ver}/{id}-{ver}.jar         # public artifacts
plugins/{id}/versions/{ver}/CHANGELOG.md          # optional
plugins/{id}/versions/{ver}/screenshots/*         # optional
plugins/{id}/versions/{ver}/assets.json
plugins/{id}/versions/{ver}/advisory.json         # optional

# Private bucket only:
private/plugins/{id}/versions/{ver}/{id}-{ver}.jar # premium artifacts
auth/authorized_keys.json                          # licence entitlements
```

Publish atomicity: version artifacts → `plugin.json` → `index.json` (commit) → CDN invalidation.

## Container image

The Dockerfile runs the service as a non-root `app` user (UID/GID 1000). When deploying with Helm,
`podSecurityContext` and `containerSecurityContext` match that identity and drop Linux
capabilities. Set CPU/memory `resources` explicitly for production workloads.

OIDC publish tokens must carry issuer `https://token.actions.githubusercontent.com` exactly
(configured via `marketplace.publish-oidc-trust.issuer`); substring matches are rejected.

## Documentation

| Topic                                | Location                                                                                                                       |
| ------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------ |
| Manifest author RFC (PUB-001)        | [`docs/marketplace-manifest-rfc.md`](docs/marketplace-manifest-rfc.md)                                                         |
| JSON Schema                          | [`docs/schemas/marketplace-manifest.schema.json`](docs/schemas/marketplace-manifest.schema.json)                               |
| Official plugins migration (PUB-008) | [`docs/official-plugins-migration.md`](docs/official-plugins-migration.md)                                                     |
| Helm chart                           | [`charts/service-marketplace/`](charts/service-marketplace/)                                                                   |
| Publish GitHub Action                | [`actions/marketplace-publish-action/`](actions/marketplace-publish-action/)                                                   |
| Example workflow                     | [`docs/examples/github-workflows/publish.yml`](docs/examples/github-workflows/publish.yml)                                     |
| GCP dev sandbox (requirements repo)  | [`marketplace-dev-env.md`](https://github.com/reportportal/reportportal-requirements/blob/develop/docs/marketplace-dev-env.md) |

## Deploy

Production: install the Helm chart (see [`charts/service-marketplace/README.md`](charts/service-marketplace/README.md)). Local dev typically uses `./gradlew bootRun` with `STORAGE_TYPE=local` instead.
