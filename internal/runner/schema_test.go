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
		// Authoring names and enum choices are printable ASCII. Foundation
		// compares strings by canonical equivalence, so a precomposed name
		// would be one name there and two here, and a precomposed enum choice
		// would accept a decomposed value. Refusing non-ASCII keeps the two
		// sides agreeing instead of documenting a divergence.
		{"precomposed property name", `{"type":"object","additionalProperties":false,"properties":{"\u00e9":{"type":"string"}}}`, "/properties", "property names must be printable ASCII"},
		{"decomposed property name", `{"type":"object","additionalProperties":false,"properties":{"e\u0301":{"type":"string"}}}`, "/properties", "property names must be printable ASCII"},
		{"non-ascii property name", `{"type":"object","additionalProperties":false,"properties":{"名前":{"type":"string"}}}`, "/properties", "property names must be printable ASCII"},
		{"control character in a property name", `{"type":"object","additionalProperties":false,"properties":{"a\u001bb":{"type":"string"}}}`, "/properties", "property names must be printable ASCII"},
		{"precomposed required name", `{"type":"object","additionalProperties":false,"properties":{"p":{"type":"string"}},"required":["\u00e9"]}`, "/required/0", "required names must be printable ASCII"},
		{"decomposed required name", `{"type":"object","additionalProperties":false,"properties":{"p":{"type":"string"}},"required":["e\u0301"]}`, "/required/0", "required names must be printable ASCII"},
		{"precomposed enum choice", schemaWithProperty(`{"type":"string","enum":["caf\u00e9"]}`), "/properties/p/enum", "enum choices must be printable ASCII"},
		{"decomposed enum choice", schemaWithProperty(`{"type":"string","enum":["cafe\u0301"]}`), "/properties/p/enum", "enum choices must be printable ASCII"},
		{"non-ascii enum choice", schemaWithProperty(`{"type":"string","enum":["\u9078\u629e"]}`), "/properties/p/enum", "enum choices must be printable ASCII"},
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

func TestParseSchemaRejectsDuplicateKeys(t *testing.T) {
	// Foundation's decoder keeps the first value of a repeated key where
	// encoding/json keeps the last, so a duplicate would be measured and
	// enforced differently on each side. The schema is refused instead.
	cases := []struct {
		name    string
		schema  string
		pointer string
	}{
		{"root type", `{"type":"object","additionalProperties":false,"type":"array"}`, "/type"},
		{"nested property", `{"type":"object","additionalProperties":false,"properties":{"a":{"type":"string","type":"integer"},"b":{"type":"string"}}}`, "/properties/a/type"},
		{"inside array items", `{"type":"object","additionalProperties":false,"properties":{"p":{"type":"array","items":{"type":"string","type":"boolean"}}}}`, "/properties/p/items/type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSchema([]byte(tc.schema))
			var failure *SchemaError
			if !asSchemaError(err, &failure) {
				t.Fatalf("error = %v; want *SchemaError", err)
			}
			if failure.Pointer != tc.pointer || failure.Reason != "schema must not repeat an object key" {
				t.Fatalf("rejection = (%q, %q); want (%q, %q)",
					failure.Pointer, failure.Reason, tc.pointer, "schema must not repeat an object key")
			}
		})
	}
	// A duplicate in the returned payload is refused for the same reason: two
	// decoders would read different values out of it.
	schema := mustParseSchema(t, guidedSchema)
	err := schema.Validate(json.RawMessage(`{"name":"a","name":"b","age":3}`))
	var failure *SchemaError
	if !asSchemaError(err, &failure) || failure.Reason != "returned payload must not repeat an object key" {
		t.Fatalf("Validate error = %v; want the duplicate-key rejection", err)
	}
	if failure.Pointer != "/name" {
		t.Fatalf("pointer = %q; want /name", failure.Pointer)
	}
}

func TestParseSchemaRejectsNumbersItCannotEncode(t *testing.T) {
	// A number outside the finite double range cannot be measured or encoded.
	// It must not be reported as an oversized schema: that reason is false.
	for _, schema := range []string{
		schemaWithProperty(`{"type":"number","minimum":1e400}`),
		`{"type":"object","additionalProperties":false,"properties":{"p":{"type":"string","enum":["a"]}},"x":1e400}`,
	} {
		_, err := ParseSchema([]byte(schema))
		var failure *SchemaError
		if !asSchemaError(err, &failure) {
			t.Fatalf("ParseSchema(%s) error = %v; want *SchemaError", schema, err)
		}
		if failure.Reason != "schema contains a number outside the supported range" {
			t.Fatalf("ParseSchema(%s) reason = %q; want the number-range reason", schema, failure.Reason)
		}
	}
}

func TestParseSchemaEncodesLikeFoundation(t *testing.T) {
	// Foundation's JSONSerialization writes a solidus as \/ and a number with
	// 17 significant digits. Both decisions move the 64 KiB measurement, so
	// the Go encoding has to match, character for character.
	cases := []struct {
		name   string
		schema string
		want   string
	}{
		{
			"solidus in a property name",
			`{"type":"object","additionalProperties":false,"properties":{"a/b":{"type":"string"}}}`,
			`{"additionalProperties":false,"properties":{"a\/b":{"type":"string"}},"type":"object"}`,
		},
		{
			"solidus in an enum choice",
			schemaWithProperty(`{"type":"string","enum":["/etc/hosts","a b"]}`),
			`{"additionalProperties":false,"properties":{"p":{"enum":["\/etc\/hosts","a b"],"type":"string"}},"required":["p"],"type":"object"}`,
		},
		{
			// Non-ASCII is not reachable here: names and enum choices are held
			// to printable ASCII, so the encoder's UTF-8 handling is exercised
			// only by values the subset cannot carry. The solidus and number
			// cases above are the decisions that remain observable.
			"solidus next to punctuation",
			schemaWithProperty(`{"type":"string","enum":["a/b~c d"]}`),
			`{"additionalProperties":false,"properties":{"p":{"enum":["a\/b~c d"],"type":"string"}},"required":["p"],"type":"object"}`,
		},
		{
			"seventeen significant digits",
			schemaWithProperty(`{"type":"number","minimum":0.1,"maximum":1e300}`),
			`{"additionalProperties":false,"properties":{"p":{"maximum":1.0000000000000001e+300,"minimum":0.10000000000000001,"type":"number"}},"required":["p"],"type":"object"}`,
		},
		{
			"integral bound without a fraction",
			schemaWithProperty(`{"type":"integer","minimum":1.0,"maximum":9007199254740992}`),
			`{"additionalProperties":false,"properties":{"p":{"maximum":9007199254740992,"minimum":1,"type":"integer"}},"required":["p"],"type":"object"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema, err := ParseSchema([]byte(tc.schema))
			if err != nil {
				t.Fatalf("ParseSchema: %v", err)
			}
			if string(schema.Bytes()) != tc.want {
				t.Fatalf("encoding =\n%s\nwant\n%s", schema.Bytes(), tc.want)
			}
			if _, err := ParseSchema(schema.Bytes()); err != nil {
				t.Fatalf("normalized encoding does not re-parse: %v", err)
			}
		})
	}
}

func TestParseSchemaIntegerBoundAtTwoToThe53(t *testing.T) {
	// 2^53 is the largest integer a double represents exactly: it is the
	// documented bound, so it must be accepted, and the next representable
	// value above it must not.
	if _, err := ParseSchema([]byte(schemaWithProperty(`{"type":"integer","maximum":9007199254740992}`))); err != nil {
		t.Fatalf("2^53 bound rejected: %v", err)
	}
	_, err := ParseSchema([]byte(schemaWithProperty(`{"type":"integer","maximum":9007199254740994}`)))
	var failure *SchemaError
	if !asSchemaError(err, &failure) || failure.Pointer != "/properties/p/maximum" {
		t.Fatalf("error = %v; want the bound rejection at the maximum", err)
	}
	negative := schemaWithProperty(`{"type":"integer","minimum":-9007199254740994}`)
	if _, err := ParseSchema([]byte(negative)); err == nil {
		t.Fatal("a magnitude above 2^53 was accepted as an integer bound")
	}
}

func TestSchemaErrorEscapesControlCharactersInPointers(t *testing.T) {
	// A returned payload's keys are caller content, not schema-authoring names,
	// so a rejected payload can still carry a control character in its pointer.
	// The pointer keeps it verbatim for comparison with the helper's pointer;
	// the message must not print a live ESC, BEL or 8-bit CSI.
	parsed, err := ParseSchema([]byte(`{"type":"object","additionalProperties":false,` +
		`"properties":{"p":{"type":"string"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		raw     string
		escaped string
		payload string
	}{
		{"escape", "a\u001bb", `\u001b`, `{"p":"x","a\u001bb":1}`},
		{"bell", "c\u0007d", `\u0007`, `{"p":"x","c\u0007d":1}`},
		// 0x9b is an 8-bit CSI introducer on some terminals; JSON leaves C1
		// unescaped, so this one really would be a live byte in the document.
		{"csi", "e\u009bf", `\u009b`, "{\"p\":\"x\",\"e\u009bf\":1}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := parsed.Validate([]byte(tc.payload))
			var failure *SchemaError
			if !asSchemaError(err, &failure) {
				t.Fatalf("Validate error = %v; want *SchemaError", err)
			}
			// The structured pointer keeps the raw key so it can be compared
			// with the helper's; only the message is escaped.
			if failure.Pointer != "/"+tc.raw {
				t.Fatalf("pointer = %q; want the raw key", failure.Pointer)
			}
			message := err.Error()
			if strings.Contains(message, tc.raw) {
				t.Fatalf("message = %q; the control character reached the text", message)
			}
			if !strings.Contains(message, tc.escaped) {
				t.Fatalf("message = %q; want %s", message, tc.escaped)
			}
		})
	}
}

// arrayChain nests one array schema inside the root object's single property.
// The root object is nesting 1, so chain(k) puts the leaf string at node depth
// k+2.
func arrayChain(arrays int) string {
	schema := `{"type":"string"}`
	for index := 0; index < arrays; index++ {
		schema = `{"type":"array","items":` + schema + `}`
	}
	return schemaWithProperty(schema)
}

func TestParseSchemaBoundsTotalNesting(t *testing.T) {
	// Object depth stops objects at three levels. Arrays were unbounded: items
	// can be another array, and neither parser counted them. A document deep
	// enough then failed in the helper's decoder after the request had already
	// spawned, which contradicts the pre-spawn rejection.
	//
	// Root object (1) plus k arrays puts the leaf at k+2, so k=6 reaches the
	// bound of 8 and k=7 exceeds it.
	if _, err := ParseSchema([]byte(arrayChain(6))); err != nil {
		t.Fatalf("nesting 8 rejected: %v", err)
	}
	_, err := ParseSchema([]byte(arrayChain(7)))
	var failure *SchemaError
	if !asSchemaError(err, &failure) {
		t.Fatalf("error = %v; want *SchemaError", err)
	}
	if failure.Reason != "schema nesting exceeds depth 8" {
		t.Fatalf("reason = %q; want the nesting sentence", failure.Reason)
	}
	if want := "/properties/p" + strings.Repeat("/items", 7); failure.Pointer != want {
		t.Fatalf("pointer = %q; want %q", failure.Pointer, want)
	}
	// The reviewer's probe: hundreds of levels were accepted by both parsers
	// and only failed later, in the helper's decoder. The bound rejects them
	// here, before any process exists. 600 is well below Go's own recursion
	// limit, so this is a rejection rather than a stack test.
	if _, err := ParseSchema([]byte(arrayChain(600))); err == nil {
		t.Fatal("a 600-level array chain was accepted")
	}
	if _, err := ParseSchema([]byte(arrayChain(600))); !asSchemaError(err, &failure) {
		t.Fatalf("deep chain error = %v; want *SchemaError", err)
	}
	// The bound counts nodes on one path, not the tree: a wide schema with
	// many shallow properties stays valid.
	wide := make([]string, 0, 8)
	for index := 0; index < 8; index++ {
		wide = append(wide, `"p`+itoa(index)+`":{"type":"array","items":{"type":"string"}}`)
	}
	if _, err := ParseSchema([]byte(`{"type":"object","additionalProperties":false,"properties":{` +
		strings.Join(wide, ",") + `}}`)); err != nil {
		t.Fatalf("wide schema rejected: %v", err)
	}
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
