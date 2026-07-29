# Helm chart: service-marketplace

Deploys the ReportPortal Plugin Marketplace registry to Kubernetes with GCS-backed storage and Cloud CDN configuration.

## Install

```bash
helm upgrade --install marketplace ./charts/service-marketplace \
  -f my-values.yaml \
  --set admin.passwordHash='...' \
  --set jwt.secret='...'
```

Generate a bcrypt admin password hash locally:

```bash
./gradlew genAdminHash --args='your-password'
```

## Values reference

| Value | Env / config | Description |
|-------|--------------|-------------|
| `replicaCount` | — | Deployment replicas |
| `image.repository` / `image.tag` | — | Container image |
| `service.port` | — | Service port (8080) |
| `resources` | — | CPU/memory limits |
| `storage.type` | `STORAGE_TYPE` | `gcs` (default in chart) |
| `gcs.bucket` | `GCS_BUCKET` | GCS bucket |
| `gcs.location` | `GCS_LOCATION` | Bucket location |
| `cdn.baseUrl` | `CDN_BASE_URL` | Public CDN hostname |
| `cdn.urlMap` | `CDN_URL_MAP` | URL map for invalidation |
| `admin.username` | `ADMIN_USERNAME` | Operator admin (secret) |
| `admin.passwordHash` | `ADMIN_PASSWORD_HASH` | bcrypt hash (secret) |
| `jwt.secret` | `JWT_SECRET` | Session JWT secret (secret) |
| `jwt.issuer` | `JWT_ISSUER` | JWT issuer |
| `jwt.ttlSeconds` | `JWT_TTL_SECONDS` | Session TTL |
| `githubOAuth.clientId` | `GITHUB_OAUTH_CLIENT_ID` | OAuth App id (secret) |
| `githubOAuth.clientSecret` | `GITHUB_OAUTH_CLIENT_SECRET` | OAuth secret (secret) |
| `githubOAuth.allowedOrg` | `GITHUB_OAUTH_ALLOWED_ORG` | Required org |
| `githubOAuth.allowedTeam` | `GITHUB_OAUTH_ALLOWED_TEAM` | Optional team gate |
| `githubOAuth.redirectUri` | `GITHUB_OAUTH_REDIRECT_URI` | Callback URL |
| `publishOidcTrust.audience` | `PUBLISH_OIDC_AUDIENCE` | CI OIDC audience |
| `publishOidcTrust.allowedSources` | `application-helm.yml` map | `owner/repo` → plugin id |

Example `publishOidcTrust.allowedSources`:

```yaml
publishOidcTrust:
  audience: marketplace.reportportal.io
  allowedSources:
    reportportal/plugin-jira-cloud: plugin-jira-cloud
    reportportal/plugin-slack: plugin-slack
```

## GCP sandbox (dev/staging)

For bucket, Cloud CDN, IAM, and URL map setup patterns see the requirements repo guide:

[marketplace-dev-env.md](https://github.com/reportportal/reportportal-requirements/blob/develop/docs/marketplace-dev-env.md)

Local laptop development usually skips this chart and runs `./gradlew bootRun` with `STORAGE_TYPE=local` and the embedded `/cdn` proxy.

## Operator access

- UI: `http://<ingress-host>/operator/`
- Health: `http://<ingress-host>/actuator/health`
