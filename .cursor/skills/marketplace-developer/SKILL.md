---
name: marketplace-developer
description: Explains where behaviour lives in service-marketplace and how its Go tests are written. Use when changing Go code under internal/, cmd/, or web/operator, adding or fixing tests, or deciding which package a change belongs in.
---

# service-marketplace developer reference

## Where behaviour lives

- Routes and handlers: `internal/httpapi/router.go` and `handlers_*.go`. Errors go through `errors.go` and the `ErrorResponse` envelope. Its `code` enum must match the OpenAPI spec.
- Wire types: `internal/httpapi/responses.go`. `wire_contract_test.go` checks them against the OpenAPI schemas through `internal/openapispec`.
- Domain types and validation: `internal/domain` (`types.go`, `validate.go`, `version.go`, `range.go`).
- Storage: `internal/storage/store.go` defines `ObjectStore`. `local.go` and `gcs.go` implement it. Object paths are in `paths.go`. Signed URLs are in `signing.go`.
- Config: `internal/config/config.go`. Insecure defaults are gated by `ALLOW_INSECURE_DEFAULTS`.
- Operator console: `web/operator/`, embedded by `web/embed.go` and served from `internal/httpapi/operator_fs.go`.

## Tests

- Handler tests use `newTestEnv` in `internal/httpapi/testutil_test.go`. It runs the real router against `httptest` recorders.
- Helpers in that file: `newRequest` and `do` for requests, `operatorSessionToken` for operator auth, `seedPlugin` and `publishViaHTTP` for data.
- Check error responses with `decodeErrorEnvelope` and `assertOpenAPIErrorSchema`.
- Put a test in the `_test.go` file next to the code it covers.
- Run one package while you work: `go test -race ./internal/httpapi -run TestName`. Run `bash scripts/check.sh` before you finish.
