package publish

// A publisher validates marketplace-manifest.json against the published JSON Schema (and
// reads the OpenAPI) BEFORE uploading. If those documents accept a pf4jId the registry
// then rejects, the publisher was misled: local validation passed, the upload 422s, and
// the rule that actually decided it was never written down anywhere machine-readable.
//
// So the published constraints are not documentation here — they are bound to
// domain.ValidatePF4JID and must agree with it on every value, verdict for verdict. Any
// rule the registry enforces has to be expressible in the published schema, which is why
// the schema states it as `pattern` + `not.pattern` rather than prose: two RE2-compilable
// keywords instead of one regex needing lookaround.

import (
	"regexp"
	"testing"

	"github.com/reportportal/service-marketplace/internal/domain"
	"github.com/reportportal/service-marketplace/internal/openapispec"
)

const openAPIPath = "../../docs/openapi/service-marketplace-v1.yaml"

// pf4jIDVerdictCorpus is every value whose verdict the registry pins, accepted and
// rejected alike — the real Plugin-Ids plus each rule ValidatePF4JID enforces.
var pf4jIDVerdictCorpus = []string{
	// Real Plugin-Ids: all must be accepted by both.
	"Azure DevOps", "JIRA Cloud", "GitLab", "Monday", "RobotFramework",
	"GitHub", "github", "jira", "rally", "junit", "mobitru", "saucelabs",
	"slack", "telegram", "quality gate", "test-execution",
	// Length bounds.
	"a", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", // 64
	"", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", // 0, 65
	// Surrounding whitespace: invisible, and never matches IntegrationType.name.
	" GitHub", "GitHub ", "  ", " ",
	// Separators and traversal.
	"bts/github", `Azure\DevOps`, "..", "../../etc/passwd", "a..b", "a.b", ".",
	// Outside printable ASCII.
	"GitHub\nBUILD SUCCESSFUL", "Git\tHub", "GitHub\x00", "GitHub\r", "Сloud", "Git​Hub",
}

// assertConstraintMatchesValidator checks a published constraint decides every corpus
// value exactly as ValidatePF4JID does.
func assertConstraintMatchesValidator(t *testing.T, source string, c openapispec.StringConstraint) {
	t.Helper()
	if c.Pattern == "" {
		t.Fatalf("%s: declares no pattern for pf4jId", source)
	}
	pattern, err := regexp.Compile(c.Pattern)
	if err != nil {
		t.Fatalf("%s: pattern %q does not compile: %v", source, c.Pattern, err)
	}
	var notPattern *regexp.Regexp
	if c.NotPattern != "" {
		if notPattern, err = regexp.Compile(c.NotPattern); err != nil {
			t.Fatalf("%s: not.pattern %q does not compile: %v", source, c.NotPattern, err)
		}
	}

	for _, id := range pf4jIDVerdictCorpus {
		published := pattern.MatchString(id) && (notPattern == nil || !notPattern.MatchString(id))
		registry := domain.ValidatePF4JID(id) == nil
		switch {
		case published && !registry:
			t.Errorf("%s accepts pf4jId %q but the registry rejects it: a publisher's local validation passes and the upload then 422s", source, id)
		case !published && registry:
			t.Errorf("%s rejects pf4jId %q but the registry accepts it: a valid manifest fails the publisher's local validation", source, id)
		}
	}
}

func TestPublishedPF4JIDPatternMatchesRegistryValidator(t *testing.T) {
	t.Run("manifest JSON Schema", func(t *testing.T) {
		c, err := openapispec.JSONSchemaConstraint(manifestSchemaPath, "pf4jId")
		if err != nil {
			t.Fatalf("reading manifest schema: %v", err)
		}
		assertConstraintMatchesValidator(t, manifestSchemaPath, c)
	})

	schemas, err := openapispec.Load(openAPIPath)
	if err != nil {
		t.Fatalf("loading OpenAPI spec: %v", err)
	}
	// Both wire schemas carry pf4jId; a reader of either must get the same rules.
	for _, schema := range []string{"PluginManifestFields", "PluginListItem"} {
		t.Run("OpenAPI "+schema, func(t *testing.T) {
			c, err := openapispec.PropertyConstraint(schemas, schema, "pf4jId")
			if err != nil {
				t.Fatalf("reading %s.pf4jId: %v", schema, err)
			}
			assertConstraintMatchesValidator(t, "OpenAPI "+schema, c)
		})
	}
}
