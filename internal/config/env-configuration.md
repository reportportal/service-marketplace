# Environment configuration

Variables `cmd/marketplace` reads at startup through `config.Load`. Use this document to prepare a dev, staging, or production deployment. The laptop and GCS sandbox workflow is the [dev profile](dev-profile.md).

An empty value is the same as an unset variable: the default applies. A boolean, integer, or duration that is set and cannot be parsed fails startup and names the variable. Booleans accept `true` and `false` (the forms `strconv.ParseBool` accepts). Durations use Go syntax (`30s`, `5m`).

The container image sets `HTTP_ADDR=:8080`, `STORAGE_TYPE=local`, and `STORAGE_LOCAL_ROOT=/data`. A `go run` from the repository uses the code defaults below unless the environment overrides them.

Google Cloud client libraries use Application Default Credentials. `GOOGLE_APPLICATION_CREDENTIALS` may point at a service-account JSON file. This process does not parse that variable. `GCP_PROJECT` below is the project id used for Cloud CDN invalidation.

## Dev, staging, and production

Secrets below are placeholders. Generate them outside the repository. Leave `ALLOW_INSECURE_DEFAULTS` unset on staging and production.

Dev, local filesystem (also see [dev profile](dev-profile.md)):

```bash
export ALLOW_INSECURE_DEFAULTS=true
export STORAGE_TYPE=local
export STORAGE_LOCAL_ROOT=./data
export CDN_BASE_URL=http://localhost:8080/cdn
export HTTP_ADDR=:8080
export ADMIN_LOGIN_ENABLED=true
export ADMIN_USERNAME=admin
export ADMIN_PASSWORD_HASH='<bcrypt-hash>'
```

Dev, GCS sandbox:

```bash
export STORAGE_TYPE=gcs
export GCS_BUCKET=reportportal-marketplace-dev
export GCS_PRIVATE_BUCKET=reportportal-marketplace-dev-private
export CDN_BASE_URL=https://cdn-dev.example.com/cdn
export CDN_URL_MAP=marketplace-dev-url-map
export GCP_PROJECT=<dev-project-id>
export JWT_SECRET='<random-string-at-least-32-characters>'
export STORAGE_SIGNING_SECRET='<random-string-at-least-32-characters>'
export ADMIN_LOGIN_ENABLED=true
export ADMIN_USERNAME=admin
export ADMIN_PASSWORD_HASH='<bcrypt-hash>'
```

Staging uses the same shape as the GCS sandbox with staging names: bucket `reportportal-marketplace-staging`, private bucket `reportportal-marketplace-staging-private`, `CDN_BASE_URL=https://cdn-staging.example.com/cdn`, `CDN_URL_MAP=marketplace-staging-url-map`, and `GCP_PROJECT` set to the staging project. Production uses bucket `reportportal-marketplace`, private bucket `reportportal-marketplace-private`, `CDN_BASE_URL=https://cdn.example.com/cdn`, `CDN_URL_MAP=marketplace-url-map`, and the production project id. Each environment has its own secrets, OAuth app, and publish allow-list.

## Currently used env vars

**GCS and local storage**

### `STORAGE_TYPE`

Object store. `local` keeps objects on disk under `STORAGE_LOCAL_ROOT`. `gcs` uses Cloud Storage and requires `GCS_BUCKET`. Any other value fails startup. Default `local` (`local` in the container image as well).

Example: `gcs`

### `STORAGE_LOCAL_ROOT`

Directory for objects when `STORAGE_TYPE=local`. Created as needed. Default `./data`. The container image sets `/data` and mounts a volume there.

Example: `./data`

### `STORAGE_SIGNING_SECRET`

HMAC secret for signed artifact URLs. Required at 32 characters or more when `ALLOW_INSECURE_DEFAULTS` is unset. The value `dev-signing-secret` is the local-only stand-in and is rejected otherwise. With `ALLOW_INSECURE_DEFAULTS=true`, an empty value or that stand-in is kept for local development.

Example: `<random-string-at-least-32-characters>`

### `GCS_BUCKET`

Cloud Storage bucket for catalogue objects when `STORAGE_TYPE=gcs`. Required in that mode. Premium and `auth/` objects use this bucket when `GCS_PRIVATE_BUCKET` is empty. Bucket location is chosen when the bucket is created.

Example: `reportportal-marketplace`

### `GCS_PRIVATE_BUCKET`

Optional second bucket for objects under `private/` and `auth/` (premium jars, `auth/authorized_keys.json`, session denylist). When empty, those objects are stored in `GCS_BUCKET`.

Example: `reportportal-marketplace-private`

**Cloud CDN**

### `CDN_BASE_URL`

Base of every artifact, changelog, screenshot, and signed premium URL the registry returns. A trailing slash is removed. The base must reach this registry's `/cdn` handler, where signatures are checked. Default `http://localhost:8080/cdn`.

Example: `https://cdn.example.com/cdn`

### `CDN_BASE_URL_SUFFIX_CHECK`

When `true` (the default), startup logs a warning if `CDN_BASE_URL` does not end in `/cdn`. Set `false` when a CDN host forwards to `/cdn` and the suffix is not part of the public base. A non-boolean value fails startup. The process still starts when the warning fires.

Example: `true`

### `CDN_SELF_CHECK`

When `true` (the default), a background probe requests a signed URL under `CDN_BASE_URL` and records the result on `GET /ready` as `checks.cdn`. Set `false` when the process cannot reach its own public host. A non-boolean value fails startup.

Example: `true`

### `CDN_URL_MAP`

Cloud CDN URL map name passed to `urlMaps.invalidateCache` after publish, block, and remove. Empty skips invalidation. Set this together with `GCP_PROJECT` on dev, staging, and production whenever a URL map fronts the bucket.

Example: `marketplace-url-map`

### `GCP_PROJECT`

GCP project id that owns `CDN_URL_MAP`. Empty logs the invalidation instead of calling the Compute API. The Cloud Storage client still needs Application Default Credentials in `gcs` mode.

Example: `marketplace-prod`

**Auth**

### `ADMIN_LOGIN_ENABLED`

Turns the username and password form on or off. Default `true`. A non-boolean value fails startup. Login still requires `ADMIN_PASSWORD_HASH` before the form accepts a password.

Example: `true`

### `ADMIN_USERNAME`

Operator username checked against the form. Default `admin`.

Example: `admin`

### `ADMIN_PASSWORD_HASH`

Bcrypt hash of the operator password. Empty leaves password login unconfigured. The plaintext password is not an environment variable.

Example: `<bcrypt-hash>`

### `JWT_SECRET`

HMAC secret for operator session tokens. Required at 32 characters or more when `ALLOW_INSECURE_DEFAULTS` is unset. The value `dev-jwt-secret-change-me` is the local-only stand-in and is rejected otherwise. With `ALLOW_INSECURE_DEFAULTS=true`, an empty value or that stand-in is kept for local development.

Example: `<random-string-at-least-32-characters>`

### `JWT_ISSUER`

`iss` claim on operator session tokens. Default `service-marketplace`.

Example: `service-marketplace`

### `JWT_TTL_SECONDS`

Session lifetime in seconds. Default `3600`. Values below 60 are raised to 60 and values above 3600 are lowered to 3600. A non-integer fails startup.

Example: `3600`

### `ALLOW_INSECURE_DEFAULTS`

`true` only on a local workstation: empty `JWT_SECRET` and `STORAGE_SIGNING_SECRET` fall back to the stand-in values named above. Default `false`. A non-boolean value fails startup. Staging and production leave this unset.

Example: `false`

### `TRUSTED_PROXY_HOPS`

Number of trusted reverse-proxy hops. `0` (the default) ignores `X-Forwarded-For` and `X-Forwarded-Proto`. Above zero, the login rate-limit address is taken from `X-Forwarded-For` and `X-Forwarded-Proto: https` marks session cookies `Secure`. A negative value or a non-integer fails startup. Set `1` behind a single TLS-terminating load balancer.

Example: `1`

### `GITHUB_OAUTH_CLIENT_ID`

GitHub OAuth App client id. GitHub login is enabled only when this, `GITHUB_OAUTH_CLIENT_SECRET`, and `GITHUB_OAUTH_ORG` are all set.

Example: `<oauth-app-client-id>`

### `GITHUB_OAUTH_CLIENT_SECRET`

GitHub OAuth App client secret. Paired with `GITHUB_OAUTH_CLIENT_ID`.

Example: `<oauth-app-client-secret>`

### `GITHUB_OAUTH_ORG`

GitHub organisation the operator must belong to. One of the three values that turn GitHub login on.

Example: `reportportal`

### `GITHUB_OAUTH_ALLOWED_TEAM`

Optional team slug inside `GITHUB_OAUTH_ORG`. When set, the callback requires an active membership in that team. Empty allows any member of the organisation.

Example: `core-team`

### `GITHUB_OAUTH_REDIRECT_URL`

OAuth callback URL registered on the GitHub OAuth App. When empty, startup uses `http://localhost:8080/api/v1/auth/github/callback`. Staging and production set the public `https` callback.

Example: `https://marketplace.example.com/api/v1/auth/github/callback`

**Publish OIDC**

### `PUBLISH_OIDC_AUDIENCE`

Expected `aud` on CI publish tokens. Required when `PUBLISH_OIDC_ALLOWED_SOURCES` is not empty, and when `PUBLISH_OIDC_GITLAB_ISSUER` is set.

Example: `marketplace.reportportal.io`

### `PUBLISH_OIDC_ALLOWED_SOURCES`

JSON object mapping a CI source to the only plugin id that source may publish. GitHub sources are `owner/repo`. GitLab sources are the project path. Default `{}`, which leaves OIDC publish disabled until a map is provided. Invalid JSON fails startup.

Example: `{"EPM-RPP/plugin-billing-epam":"billing"}`

### `PUBLISH_OIDC_GITLAB_ISSUER`

GitLab origin for CI publish tokens, for example `https://git.epam.com`. Empty leaves GitHub Actions as the only issuer. The value is an `https` origin with no path, query, or userinfo. A trailing slash is removed. The GitHub Actions issuer is rejected. Setting this requires `PUBLISH_OIDC_AUDIENCE`.

Example: `https://git.epam.com`

### `PUBLISH_OIDC_GITLAB_JWKS_URL`

Optional `https` URL of the GitLab signing keys. Empty uses `<issuer>/oauth/discovery/keys`. Setting this without `PUBLISH_OIDC_GITLAB_ISSUER` fails startup.

Example: `https://git.epam.com/oauth/discovery/keys`

**Analytics and process**

### `GA4_MEASUREMENT_ID`

Google Analytics 4 measurement id for server-side download events. Empty disables emission. A failed send is logged and does not fail the download.

Example: `G-XXXXXXXXXX`

### `GA4_API_SECRET`

Measurement Protocol API secret paired with `GA4_MEASUREMENT_ID`. Empty disables emission.

Example: `<ga4-api-secret>`

### `HTTP_ADDR`

TCP address the process listens on. Default `:8080`. The container image sets the same value.

Example: `:8080`

### `ORPHAN_CLEANUP_INTERVAL`

How often orphan artifact cleanup runs. Default `5m`. A value that is not a Go duration fails startup.

Example: `5m`

## Retired env vars

These names were part of the previous registry configuration. This process ignores them. Setting one has no effect and does not fail startup.

| Retired variable | What to set instead |
| --- | --- |
| `GCS_LOCATION` | Nothing. Location is a property of the bucket, chosen when the bucket is created. |
| `GITHUB_OAUTH_ALLOWED_ORG` | `GITHUB_OAUTH_ORG` |
| `GITHUB_OAUTH_REDIRECT_URI` | `GITHUB_OAUTH_REDIRECT_URL` |
| `GITHUB_OAUTH_STATE_TTL_SECONDS` | Nothing. OAuth `state` lasts 10 minutes. |
| `LOGIN_RATE_LIMIT_*` | Nothing. Password login locks that client and username after 5 failures, for 15 minutes. |
| `MAX_UPLOAD_FILE_SIZE` | Nothing. A publish bundle is limited to 160 MiB. |
| `MAX_UPLOAD_REQUEST_SIZE` | Nothing. A publish bundle is limited to 160 MiB. |
| `SPRING_PROFILES_ACTIVE` | The variables in this document. Local development is the [dev profile](dev-profile.md). `ALLOW_INSECURE_DEFAULTS=true` is the workstation-only switch. |
