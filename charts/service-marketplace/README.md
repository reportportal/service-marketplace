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
| ------- | -------------- | ------------- |
| `replicaCount` | — | Deployment replicas |
| `podSecurityContext` | — | Non-root pod security (UID/GID 1000, matches Dockerfile) |
| `containerSecurityContext` | — | Drop Linux capabilities, deny privilege escalation |
| `resources` | — | CPU/memory requests and limits (set explicitly for production) |
| `image.repository` / `image.tag` | — | Container image |
| `service.port` | — | Service port (8080) |
| `storage.type` | `STORAGE_TYPE` | `gcs` (default in chart) |
| `gcs.bucket` | `GCS_BUCKET` | Public GCS bucket used by Cloud CDN |
| `gcs.privateBucket` | `GCS_PRIVATE_BUCKET` | Private GCS bucket for premium artifacts |
| `gcs.location` | `GCS_LOCATION` | Bucket location |
| `cdn.baseUrl` | `CDN_BASE_URL` | Public CDN hostname |
| `cdn.urlMap` | `CDN_URL_MAP` | URL map for invalidation |
| `admin.username` | `ADMIN_USERNAME` | Operator admin (secret) |
| `admin.passwordHash` | `ADMIN_PASSWORD_HASH` | bcrypt hash (secret) |
| `jwt.secret` | `JWT_SECRET` | **Required.** Session JWT secret, ≥32 chars (secret) |
| `jwt.issuer` | `JWT_ISSUER` | JWT issuer |
| `jwt.ttlSeconds` | `JWT_TTL_SECONDS` | Session TTL |
| `githubOAuth.clientId` | `GITHUB_OAUTH_CLIENT_ID` | OAuth App id (secret) |
| `githubOAuth.clientSecret` | `GITHUB_OAUTH_CLIENT_SECRET` | OAuth secret (secret) |
| `githubOAuth.allowedOrg` | `GITHUB_OAUTH_ALLOWED_ORG` | Required org |
| `githubOAuth.allowedTeam` | `GITHUB_OAUTH_ALLOWED_TEAM` | Optional team **slug** gate (enforced when set) |
| `githubOAuth.redirectUri` | `GITHUB_OAUTH_REDIRECT_URI` | Callback URL |
| `githubOAuth.oauthStateTtlSeconds` | `GITHUB_OAUTH_STATE_TTL_SECONDS` | Signed OAuth state TTL (default 600) |
| `cloudArmor.securityPolicy` | — | Optional existing Cloud Armor policy name; creates BackendConfig + Service annotation |
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

An empty `allowedSources` map disables OIDC publishing. Configure every repository explicitly;
each repository can publish only the mapped plugin id.

`jwt.secret` and `admin.passwordHash` ship empty and must be supplied at install time. The chart
runs the `helm` profile, so the pod refuses to start on a missing or repository-default secret
rather than coming up with credentials that are public knowledge.

## Cloud Armor (admin login IP throttle)

The application enforces **per-username** lockout on `POST /api/v1/auth/login`. **Per-IP**
throttling should be applied at Google Cloud Armor. Create a backend security policy in the GCP
project (the chart never creates the policy itself), then set:

```yaml
cloudArmor:
  securityPolicy: marketplace-login-armor
```

Example rate-based ban for the login path (adjust project/policy names):

```bash
gcloud compute security-policies create marketplace-login-armor \
  --description="Marketplace operator login throttle"

gcloud compute security-policies rules create 1000 \
  --security-policy=marketplace-login-armor \
  --expression="request.method == 'POST' && request.path.lower().urlDecode().startsWith('/api/v1/auth/login')" \
  --action=rate-based-ban \
  --rate-limit-threshold-count=20 \
  --rate-limit-threshold-interval-sec=60 \
  --ban-duration-sec=300 \
  --conform-action=allow \
  --exceed-action=deny-429 \
  --enforce-on-key=IP
```

Attach the policy through GKE Ingress by setting `cloudArmor.securityPolicy`. The chart renders a
`BackendConfig` and annotates the Service with `cloud.google.com/backend-config`.

## Container hardening

The Docker image runs as UID/GID **1000** (`app` user). The Helm chart sets matching
`podSecurityContext` / `containerSecurityContext` (non-root, drop all capabilities, no privilege
escalation). Tune `resources.requests` and `resources.limits` for your cluster — defaults are a
starting point, not a production capacity plan.

## GCP sandbox (dev/staging)

For bucket, Cloud CDN, IAM, and URL map setup patterns see the requirements repo guide:

[marketplace-dev-env.md](https://github.com/reportportal/reportportal-requirements/blob/develop/docs/marketplace-dev-env.md)

Local development usually skips this chart and runs `./gradlew bootRun` with `STORAGE_TYPE=local` and the embedded `/cdn` proxy.

## Operator access

- UI: `http://<ingress-host>/operator/`
- Health: `http://<ingress-host>/actuator/health`
