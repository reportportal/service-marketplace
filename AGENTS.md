# service-marketplace — agent instructions

Go 1.25 service: the ReportPortal plugin registry. It serves the public read API, the operator console, publish, lifecycle (block, remove, tier), and licensing. The API contract is `docs/openapi/service-marketplace-v1.yaml`.

## Checks

Run `bash scripts/check.sh` before you finish. It runs `gofmt`, `go vet`, `go test -race ./...`, `go build`, and `govulncheck`. Keep `-race`.

## Layout

- `cmd/marketplace`: entrypoint
- `internal/config`: environment config
- `internal/httpapi`: router, handlers, error envelope, wire types
- `internal/publish`, `internal/lifecycle`: publish and lifecycle operations
- `internal/catalogue`: public read API
- `internal/storage`: `ObjectStore`, local and GCS stores, signed URLs
- `internal/auth`: session JWT, admin login, GitHub OAuth, publish OIDC
- `internal/license`: entitlements
- `internal/cdn`: Cloud CDN invalidation
- `internal/analytics`: GA4 events
- `web/operator`: operator console, embedded in the binary
- `docs/openapi`: OpenAPI v1 spec

## Rules

- Do not edit `.github/workflows`.
- Do not write secrets, Secret Manager values, or Terraform.
- Stop and ask before changing `internal/auth`, publish OIDC, `internal/license`, signed URL code, or `docs/openapi`.
- An API change updates `docs/openapi/service-marketplace-v1.yaml`, `internal/httpapi/wire_contract_test.go`, and `internal/openapispec/` in the same change.

## Where to read more

- Story implementation: `.cursor/skills/marketplace-story/SKILL.md`
- Code layout and test helpers: `.cursor/skills/marketplace-developer/SKILL.md`
