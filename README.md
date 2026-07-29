# service-marketplace

ReportPortal Plugin Marketplace registry — public catalogue for ReportPortal plugins.

Plugins and plugins metadata stored in GCS (or local filesystem for development).

## Quick start (local profile)

The default Spring profile is `local` (`application-local.yml`). From the repo root:

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

Optional GitHub OAuth: configure `GITHUB_OAUTH_*` env vars; otherwise use the admin form or **Login with GitHub** when OAuth is enabled.

### Tests

```bash
./gradlew test
```

## OpenAPI

Contract: [`docs/openapi/service-marketplace-v1.yaml`](docs/openapi/service-marketplace-v1.yaml)

Operator publish, block, remove, advisory, license, and auth endpoints are documented there.

## Environment variables

Spring relaxed binding maps env vars to `marketplace.*` properties (see `application.yml`).

| Variable                     | Property                                  | Purpose                                          |
| ---------------------------- | ----------------------------------------- | ------------------------------------------------ |
| `STORAGE_TYPE`               | `marketplace.storage.type`                | `local` (default) or `gcs`                       |
| `STORAGE_LOCAL_ROOT`         | `marketplace.storage.local.root`          | Local object root (default `./data/marketplace`) |
| `GCS_BUCKET`                 | `marketplace.gcs.bucket`                  | GCS bucket name                                  |
| `GCS_LOCATION`               | `marketplace.gcs.location`                | GCS bucket location                              |
| `CDN_BASE_URL`               | `marketplace.cdn.base-url`                | Public CDN base URL for catalogue/asset links    |
| `CDN_URL_MAP`                | `marketplace.cdn.url-map`                 | GCP URL map name for cache invalidation          |
| `ADMIN_USERNAME`             | `marketplace.auth.admin.username`         | Operator admin username                          |
| `ADMIN_PASSWORD_HASH`        | `marketplace.auth.admin.password-hash`    | bcrypt hash (not plaintext)                      |
| `JWT_SECRET`                 | `marketplace.auth.jwt.secret`             | Session JWT signing secret                       |
| `JWT_ISSUER`                 | `marketplace.auth.jwt.issuer`             | JWT `iss` claim                                  |
| `JWT_TTL_SECONDS`            | `marketplace.auth.jwt.ttl-seconds`        | Session lifetime                                 |
| `GITHUB_OAUTH_CLIENT_ID`     | `marketplace.auth.github.client-id`       | GitHub OAuth App client id                       |
| `GITHUB_OAUTH_CLIENT_SECRET` | `marketplace.auth.github.client-secret`   | GitHub OAuth App secret                          |
| `GITHUB_OAUTH_ALLOWED_ORG`   | `marketplace.auth.github.allowed-org`     | Required org membership                          |
| `GITHUB_OAUTH_ALLOWED_TEAM`  | `marketplace.auth.github.allowed-team`    | Optional team slug gate                          |
| `GITHUB_OAUTH_REDIRECT_URI`  | `marketplace.auth.github.redirect-uri`    | OAuth callback URL                               |
| `PUBLISH_OIDC_AUDIENCE`      | `marketplace.publish-oidc-trust.audience` | Expected OIDC `aud` for CI publish               |
| `MAX_UPLOAD_FILE_SIZE`       | `spring.servlet.multipart.max-file-size`  | Max size of a single bundle part (default `128MB`)|
| `MAX_UPLOAD_REQUEST_SIZE`    | `spring.servlet.multipart.max-request-size` | Max size of the whole bundle (default `160MB`)  |

**`publishOidcTrust.allowedSources`** (Helm `publishOidcTrust.allowedSources`, Spring map): maps GitHub repository `owner/repo` → plugin id for OIDC publish trust (ADR-014). Example:

```yaml
publishOidcTrust:
  audience: marketplace.reportportal.io
  allowedSources:
    reportportal/plugin-jira-cloud: plugin-jira-cloud
```

## Local CDN

With `STORAGE_TYPE=local`, `LocalCdnController` serves stored objects under `/cdn/**`. Set `CDN_BASE_URL` to match (local default: `http://localhost:8080/cdn`). `CatalogueService` builds asset URLs from this base.

For GCP deployments, point `CDN_BASE_URL` at the Cloud CDN hostname and set `CDN_URL_MAP` for invalidation on publish/block.

## GCS / storage layout

```text
index.json
plugins/{id}/plugin.json
plugins/{id}/versions/{ver}/manifest.json
plugins/{id}/versions/{ver}/{id}-{ver}.jar
plugins/{id}/versions/{ver}/CHANGELOG.md          # optional
plugins/{id}/versions/{ver}/screenshots/*         # optional
plugins/{id}/versions/{ver}/assets.json
plugins/{id}/versions/{ver}/advisory.json         # optional
auth/authorized_keys.json
```

Publish atomicity: version artifacts → `plugin.json` → `index.json` (commit) → CDN invalidation.

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

Production: install the Helm chart (see [`charts/service-marketplace/README.md`](charts/service-marketplace/README.md)). Local laptop dev typically uses `./gradlew bootRun` with `STORAGE_TYPE=local` instead.
