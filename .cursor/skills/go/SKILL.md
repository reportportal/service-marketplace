---
name: go
description: Go style for the service-marketplace module (Go 1.27.4). Use when writing, reviewing, debugging, or refactoring .go files, go.mod, handlers, storage, context, goroutines, errors, or tests in this repository.
---

# Go in service-marketplace

Module `github.com/reportportal/service-marketplace`, Go 1.27.4 from `go.mod`. Package map and handler test helpers are in the marketplace-developer skill. Story scope and sensitive packages are in the marketplace-story skill.

## Checks

Finish with `bash scripts/check.sh` from the repository root. It runs `gofmt -l .`, `go vet ./...`, `go test -race ./...`, `go build -o bin/marketplace ./cmd/marketplace`, and `govulncheck ./...`. Keep `-race`: a torn read in `LocalStore` is invisible without it.

While iterating, run one package: `go test -race ./internal/httpapi -run TestName`.

`gofmt -l .` must print nothing. Write Go files as gofmt emits them (LF).

## Style

- Match the surrounding file. `gofmt` is the formatter. There is no golangci-lint config and no testify.
- `context.Context` is the first parameter of any method that does I/O or can be cancelled (`storage.ObjectStore`). Pass `r.Context()` from handlers. Keep contexts off structs; services hold a `Store`, not a `context`.
- Service structs use pointer receivers (`func (s *Service)`). Small error values use value receivers (`domain.ValidationError`).
- An interface marks a real boundary. `ObjectStore` and the CDN invalidator already have one. A new interface needs a second implementation or that same kind of boundary.
- Reach for the standard library and the modules already in `go.mod` (`chi`, `uuid`, `jwx`, `yaml.v3`, `golang.org/x/crypto`, Cloud Storage). `errgroup` is not a dependency. Run `go get` and `go mod tidy` only when the change needs a new module.
- Group imports as neighboring files do: standard library, third party, then `github.com/reportportal/service-marketplace/...`.
- Comment an exported function when the reason for its behavior is not the name (`CompareVersions` keeps core numbers as strings so they cannot overflow `int`).

## Errors

- Expected failures return `error`. Package sentinels are `var ErrX = errors.New("...")`. Match them with `errors.Is`.
- A handler-facing failure is `*httpapi.APIError` passed to `writeError`. `writeError` uses `errors.As`. Codes live in `internal/httpapi/errors.go` and match the OpenAPI `ErrorResponse.code` enum. The message is safe for a client: no object paths, secrets, or upstream body text.
- Wrap with `%w` when the caller must still match the cause (`config.Load`, GCS, OpenAPI reads). Replacing the cause returns the new sentinel (`storage.ErrNotFound` becomes `catalogue.ErrNotFound`).
- Startup failure in `cmd/marketplace` is `log.Fatalf`. Library and handler code returns an error.
- The blank identifier on an error is a decision with a comment, as in `invalidateCeiling`. Logging uses the standard `log` package.

## HTTP

A handler validates the path (`requirePluginID`, `domain.ValidatePluginID`, `domain.ValidateVersion`), calls the service, then `writeJSON` or `writeError`.

A new JSON body is a named struct in `internal/httpapi/responses.go` with explicit `json` tags. `wire_contract_test.go` binds those structs to the OpenAPI schemas. `GET /health` and `GET /ready` are the existing `map[string]any` responses.

## Concurrency

Write the straight-line version first. Shared maps use `sync.Mutex` or `sync.RWMutex` (`LocalStore.generations`, session denylist, admin lockout, CDN self-check). A new goroutine is started from `main` or from a method that already takes a cancellable context (`StartOrphanCleanup`) so shutdown stops it.

## Tests

- Put `foo_test.go` next to `foo.go` in the same package.
- Table-driven tests for pure functions (`domain` validation, version compare, config parsing).
- HTTP behavior goes through `newTestEnv` and `httptest`, asserting status, error `code`, and headers.
- `t.Fatalf` includes the value that was wrong. `t.Helper()` on shared assertions. `storagetest.FaultStore` injects one storage failure without breaking other operations.
- Cover the failure the acceptance criterion names, including cancellation and the race the change can introduce.
