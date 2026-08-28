// Package openapispec reads just enough of the project's published contracts —
// docs/openapi/service-marketplace-v1.yaml and the marketplace-manifest JSON Schema —
// to let tests bind Go wire types to them. It is deliberately narrow: property names
// and enum values only, no request/response/parameter modeling.
package openapispec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// utf8BOM is the byte-order mark some editors prepend to UTF-8 JSON files (as
// docs/schemas/marketplace-manifest.schema.json has); encoding/json rejects it outright.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// Schema is the subset of an OpenAPI 3 Schema Object this package understands.
type Schema struct {
	Enum       []string          `yaml:"enum"`
	Properties map[string]Schema `yaml:"properties"`
	AllOf      []Schema          `yaml:"allOf"`
	Ref        string            `yaml:"$ref"`
	Pattern    string            `yaml:"pattern"`
	Not        *Schema           `yaml:"not"`
}

// StringConstraint is the published string validation of a single property: the regex a
// value must match and, optionally, one it must not. Two keywords rather than one regex
// because both must stay compilable by Go's RE2 — lookaround would put the published
// rules out of reach of the tests that bind them to the registry's own validator.
type StringConstraint struct {
	Pattern    string
	NotPattern string
}

type document struct {
	Components struct {
		Schemas map[string]Schema `yaml:"schemas"`
	} `yaml:"components"`
}

// Load parses an OpenAPI document and returns its named component schemas.
func Load(path string) (map[string]Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("openapispec: read %s: %w", path, err)
	}
	var doc document
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("openapispec: parse %s: %w", path, err)
	}
	return doc.Components.Schemas, nil
}

// Properties resolves the flattened set of property names the named schema carries on
// the wire, following $ref and allOf composition (both required and optional properties;
// this package does not distinguish them).
func Properties(schemas map[string]Schema, name string) (map[string]bool, error) {
	s, ok := schemas[name]
	if !ok {
		return nil, fmt.Errorf("openapispec: schema %q not found", name)
	}
	return properties(schemas, s)
}

func properties(schemas map[string]Schema, s Schema) (map[string]bool, error) {
	if s.Ref != "" {
		refName := strings.TrimPrefix(s.Ref, "#/components/schemas/")
		sub, ok := schemas[refName]
		if !ok {
			return nil, fmt.Errorf("openapispec: $ref %q not found", s.Ref)
		}
		return properties(schemas, sub)
	}
	out := map[string]bool{}
	for _, sub := range s.AllOf {
		p, err := properties(schemas, sub)
		if err != nil {
			return nil, err
		}
		for k := range p {
			out[k] = true
		}
	}
	for k := range s.Properties {
		out[k] = true
	}
	return out, nil
}

// PropertyConstraint resolves the string constraint published for one property of the
// named schema, following $ref and allOf the same way Properties does.
func PropertyConstraint(schemas map[string]Schema, name, property string) (StringConstraint, error) {
	s, ok := schemas[name]
	if !ok {
		return StringConstraint{}, fmt.Errorf("openapispec: schema %q not found", name)
	}
	prop, found, err := propertySchema(schemas, s, property)
	if err != nil {
		return StringConstraint{}, err
	}
	if !found {
		return StringConstraint{}, fmt.Errorf("openapispec: property %q not found in schema %q", property, name)
	}
	c := StringConstraint{Pattern: prop.Pattern}
	if prop.Not != nil {
		c.NotPattern = prop.Not.Pattern
	}
	return c, nil
}

func propertySchema(schemas map[string]Schema, s Schema, property string) (Schema, bool, error) {
	if s.Ref != "" {
		refName := strings.TrimPrefix(s.Ref, "#/components/schemas/")
		sub, ok := schemas[refName]
		if !ok {
			return Schema{}, false, fmt.Errorf("openapispec: $ref %q not found", s.Ref)
		}
		return propertySchema(schemas, sub, property)
	}
	if p, ok := s.Properties[property]; ok {
		return p, true, nil
	}
	for _, sub := range s.AllOf {
		p, found, err := propertySchema(schemas, sub, property)
		if err != nil || found {
			return p, found, err
		}
	}
	return Schema{}, false, nil
}

// Enum resolves the declared enum values of the named schema, following $ref.
func Enum(schemas map[string]Schema, name string) ([]string, error) {
	s, ok := schemas[name]
	if !ok {
		return nil, fmt.Errorf("openapispec: schema %q not found", name)
	}
	if s.Ref != "" {
		return Enum(schemas, strings.TrimPrefix(s.Ref, "#/components/schemas/"))
	}
	return s.Enum, nil
}

// jsonSchemaProperty is the subset of a JSON Schema (draft-07) property this package
// understands — a controlled-vocabulary enum and a string pattern, including one nested
// under "not".
type jsonSchemaProperty struct {
	Enum    []string            `json:"enum"`
	Pattern string              `json:"pattern"`
	Not     *jsonSchemaProperty `json:"not"`
}

type jsonSchemaDocument struct {
	Properties map[string]jsonSchemaProperty `json:"properties"`
}

func loadJSONSchema(path string) (*jsonSchemaDocument, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("openapispec: read %s: %w", path, err)
	}
	data = bytes.TrimPrefix(data, utf8BOM)
	var doc jsonSchemaDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("openapispec: parse %s: %w", path, err)
	}
	return &doc, nil
}

// JSONSchemaProperties returns the top-level property names a JSON Schema document
// declares (e.g. the manifest schema's fields).
func JSONSchemaProperties(path string) (map[string]bool, error) {
	doc, err := loadJSONSchema(path)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(doc.Properties))
	for name := range doc.Properties {
		out[name] = true
	}
	return out, nil
}

// JSONSchemaConstraint reads the string constraint declared on a top-level property of a
// JSON Schema document — the pattern publishers validate against locally.
func JSONSchemaConstraint(path, property string) (StringConstraint, error) {
	doc, err := loadJSONSchema(path)
	if err != nil {
		return StringConstraint{}, err
	}
	prop, ok := doc.Properties[property]
	if !ok {
		return StringConstraint{}, fmt.Errorf("openapispec: property %q not found in %s", property, path)
	}
	c := StringConstraint{Pattern: prop.Pattern}
	if prop.Not != nil {
		c.NotPattern = prop.Not.Pattern
	}
	return c, nil
}

// JSONSchemaEnum reads the enum declared on a top-level property of a JSON Schema
// document (e.g. the manifest schema's "category" property).
func JSONSchemaEnum(path, property string) ([]string, error) {
	doc, err := loadJSONSchema(path)
	if err != nil {
		return nil, err
	}
	prop, ok := doc.Properties[property]
	if !ok {
		return nil, fmt.Errorf("openapispec: property %q not found in %s", property, path)
	}
	return prop.Enum, nil
}
