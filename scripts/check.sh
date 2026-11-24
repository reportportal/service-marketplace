#!/usr/bin/env bash
# Command checks for service-marketplace. The marketplace pipeline and GitHub
# Actions call this script. -race stays on: a torn read in LocalStore was a
# real bug a plain go test cannot see.
set -euo pipefail
cd "$(dirname "$0")/.."

unformatted="$(gofmt -l .)"
if [ -n "$unformatted" ]; then
  echo "gofmt needed on:"
  echo "$unformatted"
  exit 1
fi

go vet ./...
go test -race ./...
go build -o bin/marketplace ./cmd/marketplace
govulncheck ./...
