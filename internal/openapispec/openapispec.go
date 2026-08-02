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

// PropertyEnum resolves the declared enum values of a single PROPERTY within the named
// schema (e.g. ErrorResponse's "code" field), following $ref and allOf composition the
// same way Properties does. Unlike Enum, which reads a schema's own top-level enum
// (a schema that IS an enum, like PluginCategory), this is for a schema that HAS a
// property whose value is constrained to an enum.
func PropertyEnum(schemas map[string]Schema, schemaName, propertyName string) ([]string, error) {
	s, ok := schemas[schemaName]
	if !ok {
		return nil, fmt.Errorf("openapispec: schema %q not found", schemaName)
	}
	enum, err := propertyEnum(schemas, s, propertyName)
	if err != nil {
		return nil, err
	}
	if enum == nil {
		return nil, fmt.Errorf("openapispec: property %q not found on schema %q", propertyName, schemaName)
	}
	return enum, nil
}

func propertyEnum(schemas map[string]Schema, s Schema, propertyName string) ([]string, error) {
	if s.Ref != "" {
		refName := strings.TrimPrefix(s.Ref, "#/components/schemas/")
		sub, ok := schemas[refName]
		if !ok {
			return nil, fmt.Errorf("openapispec: $ref %q not found", s.Ref)
		}
		return propertyEnum(schemas, sub, propertyName)
	}
	if p, ok := s.Properties[propertyName]; ok {
		return p.Enum, nil
	}
	for _, sub := range s.AllOf {
		enum, err := propertyEnum(schemas, sub, propertyName)
		if err != nil {
			return nil, err
		}
		if enum != nil {
			return enum, nil
		}
	}
	return nil, nil
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
// understands — enough to read a controlled-vocabulary enum off it.
type jsonSchemaProperty struct {
	Enum []string `json:"enum"`
}

type jsonSchemaDocument struct {
	Properties map[string]jsonSchemaProperty `json:"properties"`
}

// JSONSchemaEnum reads the enum declared on a top-level property of a JSON Schema
// document (e.g. the manifest schema's "category" property).
func JSONSchemaEnum(path, property string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("openapispec: read %s: %w", path, err)
	}
	data = bytes.TrimPrefix(data, utf8BOM)
	var doc jsonSchemaDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("openapispec: parse %s: %w", path, err)
	}
	prop, ok := doc.Properties[property]
	if !ok {
		return nil, fmt.Errorf("openapispec: property %q not found in %s", property, path)
	}
	return prop.Enum, nil
}
