package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Acceptance: the env configuration document lives in this repo as its own
// file. README may link to it; it is not that file.
func TestEnvConfigurationDocumentIsSeparateFromReadme(t *testing.T) {
	const docPath = "env-configuration.md"
	doc := readRepoFile(t, docPath)
	if filepath.Base(docPath) == "README.md" {
		t.Fatalf("env configuration document path = %s, want a file other than README.md", docPath)
	}
	readme := readRepoFile(t, filepath.Join("..", "..", "README.md"))
	if strings.TrimSpace(doc) == strings.TrimSpace(readme) {
		t.Fatal("env-configuration.md has the same contents as README.md")
	}
	if strings.Contains(readme, doc) {
		t.Fatal("README.md embeds env-configuration.md; the document must be a separate file")
	}
}

// Acceptance: every variable config.Load reads has a description and an example
// in the env configuration document.
func TestEnvConfigurationDocumentDescribesCurrentEnvVars(t *testing.T) {
	doc := readRepoFile(t, "env-configuration.md")
	keys := currentEnvKeys(t)
	exampleLine := regexp.MustCompile("^Example: `[^`]+`$")
	for _, name := range keys {
		section, ok := varSection(doc, name)
		if !ok {
			t.Fatalf("env-configuration.md has no section for currently used env var %s", name)
		}
		var example string
		var descLines []string
		for _, line := range strings.Split(section, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, "Example:") {
				example = trimmed
				continue
			}
			descLines = append(descLines, trimmed)
		}
		desc := strings.Join(descLines, " ")
		if len(desc) < 40 {
			t.Fatalf("%s description = %q, want a description of the variable", name, desc)
		}
		if !exampleLine.MatchString(example) {
			t.Fatalf("%s example = %q, want a line of the form Example: `value`", name, example)
		}
	}
}

// Acceptance: retired env vars live in their own section, after the variables
// the process still reads.
func TestEnvConfigurationDocumentHasRetiredSection(t *testing.T) {
	doc := readRepoFile(t, "env-configuration.md")
	const heading = "## Retired env vars"
	idx := strings.Index(doc, heading)
	if idx < 0 {
		t.Fatalf("env-configuration.md missing %q section", heading)
	}
	if strings.Count(doc, heading) != 1 {
		t.Fatalf("retired heading count = %d, want one separate section", strings.Count(doc, heading))
	}
	if strings.Contains(doc[idx+len(heading):], "\n## ") {
		t.Fatal("retired env vars section contains another top-level section")
	}
	section := doc[idx:]
	retired := []string{
		"GCS_LOCATION",
		"GITHUB_OAUTH_ALLOWED_ORG",
		"GITHUB_OAUTH_REDIRECT_URI",
		"GITHUB_OAUTH_STATE_TTL_SECONDS",
		"LOGIN_RATE_LIMIT_",
		"MAX_UPLOAD_FILE_SIZE",
		"MAX_UPLOAD_REQUEST_SIZE",
		"SPRING_PROFILES_ACTIVE",
	}
	src := readRepoFile(t, "config.go")
	for _, name := range retired {
		if !strings.Contains(section, name) {
			t.Fatalf("retired env vars section does not mention %s", name)
		}
		if name == "LOGIN_RATE_LIMIT_" {
			continue
		}
		if strings.Contains(src, `"`+name+`"`) {
			t.Fatalf("config.Load still reads %s", name)
		}
	}
}

// Acceptance: README.md links to the env configuration document.
func TestReadmeLinksToEnvConfigurationDocument(t *testing.T) {
	readme := readRepoFile(t, filepath.Join("..", "..", "README.md"))
	link := regexp.MustCompile(`\[[^\]]+\]\(internal/config/env-configuration\.md\)`)
	if !link.MatchString(readme) {
		t.Fatal("README.md has no markdown link to internal/config/env-configuration.md")
	}
}

// Acceptance: the dev profile is its own document in this repo.
func TestDevProfileDocumentExists(t *testing.T) {
	const path = "dev-profile.md"
	profile := readRepoFile(t, path)
	if filepath.Base(path) == "README.md" || path == "env-configuration.md" {
		t.Fatalf("dev profile path = %s, want a separate document", path)
	}
	envDoc := readRepoFile(t, "env-configuration.md")
	readme := readRepoFile(t, filepath.Join("..", "..", "README.md"))
	if strings.TrimSpace(profile) == strings.TrimSpace(envDoc) || strings.TrimSpace(profile) == strings.TrimSpace(readme) {
		t.Fatal("dev-profile.md is not a separate document")
	}
	if !strings.Contains(strings.ToLower(profile), "dev") {
		t.Fatal("dev-profile.md does not describe a dev profile")
	}
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		t.Fatalf("%s is empty", path)
	}
	return string(b)
}

func currentEnvKeys(t *testing.T) []string {
	t.Helper()
	src := readRepoFile(t, "config.go")
	re := regexp.MustCompile(`getEnv(?:BoolE|IntE|DurationE)?\("([A-Z][A-Z0-9_]*)"`)
	matches := re.FindAllStringSubmatch(src, -1)
	if len(matches) == 0 {
		t.Fatal("config.go contains no getEnv calls")
	}
	seen := map[string]struct{}{}
	var keys []string
	for _, m := range matches {
		if _, ok := seen[m[1]]; ok {
			continue
		}
		seen[m[1]] = struct{}{}
		keys = append(keys, m[1])
	}
	return keys
}

func varSection(doc, name string) (string, bool) {
	heading := "### `" + name + "`"
	idx := strings.Index(doc, heading+"\n")
	if idx < 0 {
		return "", false
	}
	rest := doc[idx+len(heading):]
	if loc := regexp.MustCompile(`(?m)^#{2,3} `).FindStringIndex(rest); loc != nil {
		rest = rest[:loc[0]]
	}
	return rest, true
}
