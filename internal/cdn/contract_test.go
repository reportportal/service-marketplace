package cdn

import (
	"context"
	"testing"
)

// implementations returns every Invalidator implementation this contract
// test runs against. GCPInvalidator is exercised in its "stub" configuration
// (URLMap set, Project unset) so this file needs no network access — it logs
// and returns nil rather than calling the real Compute API, exactly like the
// Java original's stub GcpCdnInvalidationService did before it grew a real
// implementation. gcp_test.go separately exercises the real
// UrlMaps.InvalidateCache request path against a fake HTTP server.
func implementations() map[string]Invalidator {
	return map[string]Invalidator{
		"noop": NoopInvalidator{},
		"gcp-stub": &GCPInvalidator{
			URLMap:  "marketplace-url-map",
			Project: "", // stub mode: logs and returns nil, no network call
		},
		"log": &LogInvalidator{URLMap: "marketplace-url-map"},
	}
}

// The path shapes below mirror exactly what each lifecycle/publish call site
// builds (internal/publish/service.go's PublishVersion, internal/lifecycle/
// service.go's BlockVersion/RemovePlugin/AttachAdvisory), per
// CdnInvalidationServiceContractTest.java's acceptsThePathsXProduces cases.
var contractCases = map[string][]string{
	"publish version": {
		"/index.json",
		"/plugins/plugin-alpha/plugin.json",
		"/plugins/plugin-alpha/versions/1.0.0/*",
	},
	"block version": {
		"/index.json",
		"/plugins/plugin-alpha/plugin.json",
		"/plugins/plugin-alpha/versions/1.0.0/*",
	},
	"remove plugin": {
		"/index.json",
		"/plugins/plugin-alpha/*",
	},
	"attach advisory": {
		"/plugins/plugin-alpha/versions/1.0.0/advisory.json",
	},
	"nil paths":   nil,
	"empty paths": {},
	"paths containing an empty element": {
		"/index.json", "",
	},
}

// TestContractAcceptsEveryCallerShape pins the path shapes every real
// lifecycle/publish caller produces, including the trailing "/*" wildcard
// form, as always accepted (no error) by every Invalidator implementation.
//
// This is the regression test for the Java original's bug: mutate GCPInvalidator
// to reject a path containing "/*" (see this file's doc comment history / the
// GCPInvalidator doc comment) and TestContractAcceptsEveryCallerShape/gcp-stub
// will not catch it, because the stub short-circuits before reaching the
// wildcard-sensitive code — that path is covered end-to-end by gcp_test.go's
// TestGCPInvalidatorAcceptsWildcardPathsOverTheWire instead. This test's job
// is the null/empty/never-panics contract every implementation must share.
func TestContractAcceptsEveryCallerShape(t *testing.T) {
	for implName, inv := range implementations() {
		for caseName, paths := range contractCases {
			t.Run(implName+"/"+caseName, func(t *testing.T) {
				if err := inv.Invalidate(context.Background(), paths); err != nil {
					t.Fatalf("Invalidate(%v) returned an error, want nil: %v", paths, err)
				}
			})
		}
	}
}

// TestContractNilPathsNeverPanics is the null-safety half of the contract,
// isolated from the "no error" assertion above so a future implementation
// that legitimately wants to error on nil (unlikely, but not what this test
// checks) still can't regress into a panic.
func TestContractNilPathsNeverPanics(t *testing.T) {
	for implName, inv := range implementations() {
		t.Run(implName, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Invalidate(nil) panicked: %v", r)
				}
			}()
			_ = inv.Invalidate(context.Background(), nil)
		})
	}
}
