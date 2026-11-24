# Dev profile

Local development profile for `service-marketplace`. This document is separate from the variable reference in [env-configuration.md](env-configuration.md). There is no Spring profile: `SPRING_PROFILES_ACTIVE` is a retired name listed in that reference.

Two ways to run the dev profile:

- **Laptop, API only.** `STORAGE_TYPE=local`. No GCP project.
- **GCS sandbox.** `STORAGE_TYPE=gcs` against a private dev bucket. Add a dev Cloud CDN URL map when you need invalidation and public artifact URLs that match staging and production.

`ALLOW_INSECURE_DEFAULTS=true` belongs on a workstation only. A shared dev cluster uses the same secret rules as staging: real `JWT_SECRET` and `STORAGE_SIGNING_SECRET` of at least 32 characters, and `ALLOW_INSECURE_DEFAULTS` left unset.

## Laptop, API only

From the repository root, with Go 1.27.4 or newer:

```bash
export ALLOW_INSECURE_DEFAULTS=true
export STORAGE_TYPE=local
export STORAGE_LOCAL_ROOT=./data
export CDN_BASE_URL=http://localhost:8080/cdn
export HTTP_ADDR=:8080
export ADMIN_LOGIN_ENABLED=true
export ADMIN_USERNAME=admin
export ADMIN_PASSWORD_HASH='<bcrypt-hash>'

go run ./cmd/marketplace
```

`ADMIN_PASSWORD_HASH` is a bcrypt hash. With `ALLOW_INSECURE_DEFAULTS=true`, `JWT_SECRET` and `STORAGE_SIGNING_SECRET` may be omitted; the process uses its workstation stand-ins.

After start:

- Health: `GET http://localhost:8080/health` and `GET http://localhost:8080/ready`
- Operator UI: `http://localhost:8080/operator/`
- Catalogue: `GET http://localhost:8080/api/v1/plugins`

Objects are written under `./data`. Stop the process with Ctrl+C.

## GCS sandbox

### Resources

One dev bucket, not the staging or production bucket. Example name: `reportportal-marketplace-dev`.

- Uniform bucket-level access, with no `allUsers` or `allAuthenticatedUsers` on objects.
- Location chosen on the bucket. The process has no location variable.
- Empty at first. Publish creates `index.json`, `plugins/{id}/...`, and `auth/authorized_keys.json`. Premium jars land under `private/`.

Optional private bucket `reportportal-marketplace-dev-private` for `private/` and `auth/` objects (`GCS_PRIVATE_BUCKET`).

Cloud CDN, when the dev profile should match production redirects:

- Global external Application Load Balancer, backend bucket pointed at the dev bucket, Cloud CDN enabled, HTTPS.
- Record the URL map name. That name is `CDN_URL_MAP`.

### Credentials and IAM

The Cloud Storage and Compute clients use Application Default Credentials. On a laptop, `gcloud auth application-default login` is enough. A service-account JSON file is `GOOGLE_APPLICATION_CREDENTIALS`; the process does not parse that variable itself. On GKE or Cloud Run, use the attached service account.

Grant the identity, scoped to the dev bucket and the dev URL map:

- `storage.objects.get`, `storage.objects.create`, `storage.objects.delete`, and `storage.objects.list` on the dev bucket (`roles/storage.objectAdmin` on that bucket).
- `compute.urlMaps.invalidateCache` on the dev URL map when Cloud CDN is in use.

`GCP_PROJECT` is the project id the registry sends with cache invalidation. `GOOGLE_CLOUD_PROJECT` is not read by this process.

### Environment

```bash
export STORAGE_TYPE=gcs
export GCS_BUCKET=reportportal-marketplace-dev
export GCS_PRIVATE_BUCKET=reportportal-marketplace-dev-private
export CDN_BASE_URL=https://cdn-dev.example.com/cdn
export CDN_URL_MAP=marketplace-dev-url-map
export GCP_PROJECT=<dev-project-id>
export CDN_SELF_CHECK=true
export JWT_SECRET='<random-string-at-least-32-characters>'
export STORAGE_SIGNING_SECRET='<random-string-at-least-32-characters>'
export ADMIN_LOGIN_ENABLED=true
export ADMIN_USERNAME=admin
export ADMIN_PASSWORD_HASH='<bcrypt-hash>'
export HTTP_ADDR=:8080

go run ./cmd/marketplace
```

`CDN_BASE_URL` ends with `/cdn` unless the CDN host forwards to that path, in which case set `CDN_BASE_URL_SUFFIX_CHECK=false`. Leave `GA4_MEASUREMENT_ID` and `GA4_API_SECRET` empty to skip analytics. Leave `PUBLISH_OIDC_ALLOWED_SOURCES` unset unless a CI job publishes into this sandbox.

Operator login is the admin form when `ADMIN_PASSWORD_HASH` is set. GitHub login instead needs `GITHUB_OAUTH_CLIENT_ID`, `GITHUB_OAUTH_CLIENT_SECRET`, and `GITHUB_OAUTH_ORG`, plus a callback of `http://localhost:8080/api/v1/auth/github/callback` (or `GITHUB_OAUTH_REDIRECT_URL`).

## Checklist

1. Pick laptop (`STORAGE_TYPE=local`) or a dev GCS bucket (`STORAGE_TYPE=gcs`).
2. For GCS, create the private dev bucket and grant object admin on it. Add a URL map and `GCP_PROJECT` when Cloud CDN is part of the profile.
3. Authenticate with Application Default Credentials for the GCS profile.
4. Set `CDN_BASE_URL` so it reaches this process's `/cdn` handler.
5. Set `ADMIN_USERNAME` and a bcrypt `ADMIN_PASSWORD_HASH`, or the three GitHub OAuth variables.
6. On a workstation, set `ALLOW_INSECURE_DEFAULTS=true`. On a shared dev deployment, set `JWT_SECRET` and `STORAGE_SIGNING_SECRET` and leave insecure defaults unset.
7. Run `go run ./cmd/marketplace` and open `http://localhost:8080/operator/`.
