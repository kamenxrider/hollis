// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"encoding/json"
	"strings"
	"testing"
)

// object wraps one property schema in an otherwise minimal root object.
func schemaWithProperty(property string) string {
	return `{"type":"object","additionalProperties":false,"properties":{"p":` + property + `},"required":["p"]}`
}

// objectChain returns a root object whose single property chain nests the
// given number of object levels, ending in a string leaf.
func objectChain(levels int) string {
	schema := `{"type":"string"}`
	for index := 0; index < levels; index++ {
		schema = `{"type":"object","additionalProperties":false,"properties":{"p":` + schema + `}}`
	}
	return schema
}

func TestParseSchemaAcceptsSupportedSubset(t *testing.T) {
	cases := map[string]string{
		"empty object":     `{"type":"object","additionalProperties":false}`,
		"string":           schemaWithProperty(`{"type":"string"}`),
		"integer":          schemaWithProperty(`{"type":"integer","minimum":0,"maximum":10}`),
		"number":           schemaWithProperty(`{"type":"number","minimum":-1.5,"maximum":1.5}`),
		"boolean":          schemaWithProperty(`{"type":"boolean"}`),
		"string enum":      schemaWithProperty(`{"type":"string","enum":["a","b"]}`),
		"array of string":  schemaWithProperty(`{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":2}`),
		"array bounds 0":   schemaWithProperty(`{"type":"array","items":{"type":"string"},"minItems":0,"maxItems":0}`),
		"array bounds 100": schemaWithProperty(`{"type":"array","items":{"type":"string"},"maxItems":100}`),
		"array of objects": schemaWithProperty(`{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"a":{"type":"string"}}}}`),
		"nested depth 3":   objectChain(3),
		"object inside array at depth 3": `{"type":"object","additionalProperties":false,"properties":{"a":{"type":"object",` +
			`"additionalProperties":false,"properties":{"b":{"type":"array","items":{"type":"object","additionalProperties":false,` +
			`"properties":{"c":{"type":"string"}}}}}}}}`,
		"optional property":      `{"type":"object","additionalProperties":false,"properties":{"p":{"type":"string"}}}`,
		"integer without bounds": schemaWithProperty(`{"type":"integer"}`),
	}
	for name, schema := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSchema([]byte(schema)); err != nil {
				t.Fatalf("ParseSchema(%s) = %v; want accepted", schema, err)
			}
		})
	}
}

func TestParseSchemaRejectsUnsupportedSubset(t *testing.T) {
	cases := []struct {
		name    string
		schema  string
		pointer string
		reason  string
	}{
		{"empty document", `{}`, "", "root schema must be an object"},
		{"array root", `{"type":"array","items":{"type":"string"}}`, "", "root schema must be an object"},
		{"scalar root", `"hello"`, "", "root schema must be an object"},
		{"null root", `null`, "", "root schema must be an object"},
		{"missing additionalProperties", `{"type":"object"}`, "/additionalProperties", "additionalProperties:false is required"},
		{"additionalProperties true", `{"type":"object","additionalProperties":true}`, "/additionalProperties", "additionalProperties:false is required"},
		{"additionalProperties zero", `{"type":"object","additionalProperties":0}`, "/additionalProperties", "additionalProperties:false is required"},
		{"reference", schemaWithProperty(`{"type":"object","additionalProperties":false,"$ref":"#/x"}`), "/properties/p/$ref", "unsupported schema keyword"},
		{"definitions", `{"type":"object","additionalProperties":false,"$defs":{}}`, "/$defs", "unsupported schema keyword"},
		{"anyOf", schemaWithProperty(`{"type":"object","additionalProperties":false,"anyOf":[]}`), "/properties/p/anyOf", "unsupported schema keyword"},
		{"oneOf", schemaWithProperty(`{"type":"object","additionalProperties":false,"oneOf":[]}`), "/properties/p/oneOf", "unsupported schema keyword"},
		{"allOf", schemaWithProperty(`{"type":"object","additionalProperties":false,"allOf":[]}`), "/properties/p/allOf", "unsupported schema keyword"},
		{"not", schemaWithProperty(`{"type":"string","not":{}}`), "/properties/p/not", "unsupported schema keyword"},
		{"pattern", schemaWithProperty(`{"type":"string","pattern":"a"}`), "/properties/p/pattern", "unsupported schema keyword"},
		{"format", schemaWithProperty(`{"type":"string","format":"date"}`), "/properties/p/format", "unsupported schema keyword"},
		{"default", schemaWithProperty(`{"type":"string","default":"a"}`), "/properties/p/default", "unsupported schema keyword"},
		{"string bounds", schemaWithProperty(`{"type":"string","minLength":2}`), "/properties/p/minLength", "unsupported schema keyword"},
		{"descriptions", schemaWithProperty(`{"type":"string","description":"x"}`), "/properties/p/description", "unsupported schema keyword"},
		{"missing type", schemaWithProperty(`{"properties":{}}`), "/properties/p/type", "unsupported schema type"},
		{"unknown type", schemaWithProperty(`{"type":"null"}`), "/properties/p/type", "unsupported schema type"},
		{"depth 4", objectChain(4), "/properties/p/properties/p/properties/p", "object nesting exceeds depth 3"},
		{"integer enum", schemaWithProperty(`{"type":"integer","enum":[1,2]}`), "/properties/p/enum", "enums must be strings"},
		{"boolean enum", schemaWithProperty(`{"type":"boolean","enum":[true]}`), "/properties/p/enum", "enums must be strings"},
		{"empty enum", schemaWithProperty(`{"type":"string","enum":[]}`), "/properties/p/enum", "enum must not be empty"},
		{"non-array enum", schemaWithProperty(`{"type":"string","enum":"a"}`), "/properties/p/enum", "enum must not be empty"},
		{"non-string enum choice", schemaWithProperty(`{"type":"string","enum":[1]}`), "/properties/p/enum", "enum choices must be strings"},
		{"duplicate enum choice", schemaWithProperty(`{"type":"string","enum":["a","a"]}`), "/properties/p/enum", "enum choices must be unique"},
		{"items missing", schemaWithProperty(`{"type":"array"}`), "/properties/p/items", "array items are required"},
		{"minItems above 100", schemaWithProperty(`{"type":"array","items":{"type":"string"},"minItems":101}`), "/properties/p/minItems", "array bounds must be whole numbers from 0 to 100"},
		{"negative minItems", schemaWithProperty(`{"type":"array","items":{"type":"string"},"minItems":-1}`), "/properties/p/minItems", "array bounds must be whole numbers from 0 to 100"},
		{"fractional maxItems", schemaWithProperty(`{"type":"array","items":{"type":"string"},"maxItems":1.5}`), "/properties/p/maxItems", "array bounds must be whole numbers from 0 to 100"},
		{"string minItems", schemaWithProperty(`{"type":"array","items":{"type":"string"},"minItems":"1"}`), "/properties/p/minItems", "array bounds must be whole numbers from 0 to 100"},
		{"contradictory items", schemaWithProperty(`{"type":"array","items":{"type":"string"},"minItems":3,"maxItems":2}`), "/properties/p", "maxItems is smaller than minItems"},
		{"fractional integer bound", schemaWithProperty(`{"type":"integer","minimum":1.5}`), "/properties/p/minimum", "integer bounds must be whole numbers"},
		{"integer bound beyond 2^53", schemaWithProperty(`{"type":"integer","maximum":1e300}`), "/properties/p/maximum", "integer bounds must be whole numbers"},
		{"contradictory integer bounds", schemaWithProperty(`{"type":"integer","minimum":5,"maximum":1}`), "/properties/p", "maximum is smaller than minimum"},
		{"contradictory number bounds", schemaWithProperty(`{"type":"number","minimum":5,"maximum":1}`), "/properties/p", "maximum is smaller than minimum"},
		{"non-numeric number bound", schemaWithProperty(`{"type":"number","minimum":"1"}`), "/properties/p/minimum", "number bounds must be finite numbers"},
		{"required not array", `{"type":"object","additionalProperties":false,"required":"p"}`, "/required", "required must be an array of names"},
		{"required entry not string", `{"type":"object","additionalProperties":false,"properties":{"p":{"type":"string"}},"required":[1]}`, "/required/0", "required entries must be strings"},
		{"duplicate required", `{"type":"object","additionalProperties":false,"properties":{"p":{"type":"string"}},"required":["p","p"]}`, "/required/1", "required names must be unique"},
		{"undeclared required", `{"type":"object","additionalProperties":false,"properties":{"p":{"type":"string"}},"required":["q"]}`, "/required", "required names a property that is not declared"},
		{"properties not object", `{"type":"object","additionalProperties":false,"properties":[]}`, "/properties", "properties must be an object"},
		{"empty property name", `{"type":"object","additionalProperties":false,"properties":{"":{"type":"string"}}}`, "/properties", "property names must not be empty"},
		{"node not object", schemaWithProperty(`"string"`), "/properties/p", "schema node must be an object"},
		{"trailing data", `{"type":"object","additionalProperties":false} {}`, "", "schema must be one JSON value"},
		{"not json", `not json`, "", "schema is not valid JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSchema([]byte(tc.schema))
			var failure *SchemaError
			if !asSchemaError(err, &failure) {
				t.Fatalf("ParseSchema(%s) error = %v; want *SchemaError", tc.schema, err)
			}
			if failure.Pointer != tc.pointer || failure.Reason != tc.reason {
				t.Fatalf("ParseSchema(%s) = (%q, %q); want (%q, %q)",
					tc.schema, failure.Pointer, failure.Reason, tc.pointer, tc.reason)
			}
		})
	}
}

func asSchemaError(err error, target **SchemaError) bool {
	if err == nil {
		return false
	}
	failure, ok := err.(*SchemaError)
	if ok {
		*target = failure
	}
	return ok
}

func TestParseSchemaBoundsPropertyCountAndSize(t *testing.T) {
	// 32 declared properties is the limit, counted across the whole tree.
	properties := make([]string, 0, SchemaMaxProperties)
	for index := 0; index < SchemaMaxProperties; index++ {
		properties = append(properties, `"p`+itoa(index)+`":{"type":"string"}`)
	}
	atLimit := `{"type":"object","additionalProperties":false,"properties":{` + strings.Join(properties, ",") + `}}`
	if _, err := ParseSchema([]byte(atLimit)); err != nil {
		t.Fatalf("32 properties rejected: %v", err)
	}
	overLimit := `{"type":"object","additionalProperties":false,"properties":{` + strings.Join(properties, ",") + `,"extra":{"type":"string"}}}`
	if _, err := ParseSchema([]byte(overLimit)); err == nil {
		t.Fatal("33 properties accepted; want rejection")
	} else if failure, ok := err.(*SchemaError); !ok || failure.Reason != "schema declares more than 32 properties" {
		t.Fatalf("33 properties = %v; want the property-count reason", err)
	}

	// The bound applies to the normalized encoding: compact JSON at exactly
	// 64 KiB is accepted, one byte more is not.
	prefix := `{"type":"object","additionalProperties":false,"properties":{"p":{"type":"string","enum":["`
	suffix := `"]}}}`
	padding := SchemaMaxBytes - len(prefix) - len(suffix)
	if padding <= 0 {
		t.Fatal("test skeleton already exceeds the bound")
	}
	atBound := prefix + strings.Repeat("a", padding) + suffix
	if len(atBound) != SchemaMaxBytes {
		t.Fatalf("fixture is %d bytes; want %d", len(atBound), SchemaMaxBytes)
	}
	if _, err := ParseSchema([]byte(atBound)); err != nil {
		t.Fatalf("schema at the 64 KiB bound rejected: %v", err)
	}
	overBound := prefix + strings.Repeat("a", padding+1) + suffix
	if _, err := ParseSchema([]byte(overBound)); err == nil {
		t.Fatal("schema over the 64 KiB bound accepted; want rejection")
	} else if failure, ok := err.(*SchemaError); !ok || failure.Reason != "schema is larger than 64 KiB" {
		t.Fatalf("oversized schema = %v; want the size reason", err)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

func TestParseSchemaNormalizesEncoding(t *testing.T) {
	// Sorted keys, no whitespace, and canonical numbers: this is the exact
	// encoding the helper measures and the request carries.
	input := `{
		"properties": {
			"b": {"type": "string"},
			"a": {"type": "integer", "minimum": 1.0}
		},
		"additionalProperties": false,
		"type": "object"
	}`
	schema, err := ParseSchema([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"additionalProperties":false,"properties":{"a":{"minimum":1,"type":"integer"},` +
		`"b":{"type":"string"}},"type":"object"}`
	if string(schema.Bytes()) != want {
		t.Fatalf("normalized encoding =\n%s\nwant\n%s", schema.Bytes(), want)
	}
	// The normalized encoding must itself parse: the helper receives exactly it.
	if _, err := ParseSchema(schema.Bytes()); err != nil {
		t.Fatalf("normalized encoding does not re-parse: %v", err)
	}
}

func TestParseSchemaEscapesPointers(t *testing.T) {
	schema := `{"type":"object","additionalProperties":false,"properties":{"a/b~c":{"type":"string","pattern":"x"}}}`
	_, err := ParseSchema([]byte(schema))
	var failure *SchemaError
	if !asSchemaError(err, &failure) {
		t.Fatalf("error = %v; want *SchemaError", err)
	}
	if failure.Pointer != "/properties/a~1b~0c/pattern" {
		t.Fatalf("pointer = %q; want the escaped property path", failure.Pointer)
	}
}

func TestSchemaValidateChecksStructureNotTruth(t *testing.T) {
	schemaText := `{"type":"object","additionalProperties":false,"required":["name","age"],"properties":{` +
		`"name":{"type":"string","enum":["a","b"]},` +
		`"age":{"type":"integer","minimum":0,"maximum":10},` +
		`"score":{"type":"number","minimum":0,"maximum":1},` +
		`"tags":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":2},` +
		`"flag":{"type":"boolean"}}}`
	schema, err := ParseSchema([]byte(schemaText))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		payload string
		pointer string
	}{
		{"conforming", `{"name":"a","age":3,"score":0.5,"tags":["x"],"flag":true}`, ""},
		{"optional omitted", `{"name":"b","age":0}`, ""},
		{"integer written as float", `{"name":"a","age":3.0}`, ""},
		{"missing required", `{"name":"a"}`, "/age"},
		{"extra property", `{"name":"a","age":3,"other":1}`, "/other"},
		{"wrong enum", `{"name":"c","age":3}`, "/name"},
		{"wrong type", `{"name":1,"age":3}`, "/name"},
		{"fractional integer", `{"name":"a","age":1.5}`, "/age"},
		{"integer below minimum", `{"name":"a","age":-1}`, "/age"},
		{"integer above maximum", `{"name":"a","age":11}`, "/age"},
		{"number out of range", `{"name":"a","age":3,"score":2}`, "/score"},
		{"boolean as string", `{"name":"a","age":3,"flag":"true"}`, "/flag"},
		{"array too short", `{"name":"a","age":3,"tags":[]}`, "/tags"},
		{"array too long", `{"name":"a","age":3,"tags":["x","y","z"]}`, "/tags"},
		{"array element type", `{"name":"a","age":3,"tags":[1]}`, "/tags/0"},
		{"root not object", `["a"]`, "/"},
		{"null payload", `null`, "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := schema.Validate(json.RawMessage(tc.payload))
			if tc.pointer == "" {
				if err != nil {
					t.Fatalf("Validate(%s) = %v; want conforming", tc.payload, err)
				}
				return
			}
			var failure *SchemaError
			if !asSchemaError(err, &failure) {
				t.Fatalf("Validate(%s) = %v; want *SchemaError", tc.payload, err)
			}
			if failure.Pointer != tc.pointer {
				t.Fatalf("Validate(%s) pointer = %q; want %q", tc.payload, failure.Pointer, tc.pointer)
			}
			if failure.Reason != "returned payload does not match the requested schema" {
				t.Fatalf("Validate(%s) reason = %q", tc.payload, failure.Reason)
			}
		})
	}
}
