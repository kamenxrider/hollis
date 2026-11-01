// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

// Guided output: the supported JSON Schema subset and its validation.
//
// native/SchemaSubset.swift is the helper's half of this contract. The rules,
// limits, JSON pointers and rejection reasons here mirror it node for node so a
// schema accepted by the CLI is accepted by the helper, and a returned object is
// judged by the same rules on both sides. Anything outside the subset is
// rejected before a model is touched: this file is a pure function of the
// decoded schema and never performs inference.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

const (
	// SchemaMaxBytes bounds the normalized schema encoding.
	SchemaMaxBytes = 64 << 10
	// SchemaMaxDepth counts object levels: the root is depth 1 and an array
	// adds no object level.
	SchemaMaxDepth = 3
	// SchemaMaxProperties bounds declared properties across the whole tree.
	SchemaMaxProperties = 32
	// SchemaMaxItems is the largest accepted minItems/maxItems value.
	SchemaMaxItems = 100
	// schemaMaxInteger is the largest integer bound exactly representable as a
	// JSON double, which is the bound the helper applies.
	schemaMaxInteger = 1 << 53
)

// SchemaError rejects a schema or a returned payload. Pointer is a JSON
// pointer into the offending document (empty for the document root); Reason is
// a fixed sentence that never contains caller content.
type SchemaError struct {
	Pointer string
	Reason  string
}

func (e *SchemaError) Error() string {
	if e.Pointer == "" {
		return e.Reason
	}
	return "schema at " + e.Pointer + ": " + e.Reason
}

// Schema is a parsed, validated request schema. It is immutable and safe to
// reuse: Bytes returns the normalized encoding that is sent to the helper.
type Schema struct {
	root *schemaNode
	raw  []byte
}

// Bytes is the normalized schema encoding for the native request.
func (s *Schema) Bytes() []byte { return s.raw }

type schemaKind uint8

const (
	schemaNull schemaKind = iota
	schemaBool
	schemaNumber
	schemaString
	schemaArray
	schemaObject
)

// schemaValue is a decoded JSON tree. Numbers stay json.Number so an integer
// literal can be told from a fractional one without a lossy round trip.
type schemaValue struct {
	kind    schemaKind
	object  map[string]schemaValue
	array   []schemaValue
	text    string
	number  json.Number
	boolean bool
}

type schemaProperty struct {
	name     string
	node     *schemaNode
	required bool
}

// schemaNode is one validated subset node. Bounds stay float64 because that is
// the comparison the helper performs.
type schemaNode struct {
	kind       string
	properties []schemaProperty
	required   map[string]bool
	items      *schemaNode
	minItems   *int
	maxItems   *int
	choices    []string
	hasChoices bool
	minimum    *float64
	maximum    *float64
}

// ParseSchema validates data against the supported subset and returns the
// schema plus its normalized encoding. Rejections carry a JSON pointer.
func ParseSchema(data []byte) (*Schema, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return nil, &SchemaError{Reason: "schema is not valid JSON"}
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, &SchemaError{Reason: "schema must be one JSON value"}
	}
	value, ok := schemaValueOf(raw)
	if !ok {
		return nil, &SchemaError{Reason: "schema is not valid JSON"}
	}
	normalized, ok := value.encode()
	if !ok || len(normalized) > SchemaMaxBytes {
		return nil, &SchemaError{Reason: "schema is larger than 64 KiB"}
	}
	if kind, present := value.object["type"]; value.kind != schemaObject || !present ||
		kind.kind != schemaString || kind.text != "object" {
		return nil, &SchemaError{Reason: "root schema must be an object"}
	}
	budget := 0
	root, err := parseSchemaNode(value, "", 1, &budget)
	if err != nil {
		return nil, err
	}
	if root.kind != "object" {
		return nil, &SchemaError{Reason: "root schema must be an object"}
	}
	return &Schema{root: root, raw: normalized}, nil
}

// Validate reports whether a returned payload conforms to the requested
// schema. Structural validity is all it establishes: it never claims the
// values are semantically correct.
func (s *Schema) Validate(payload json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return &SchemaError{Reason: "returned payload is not valid JSON"}
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return &SchemaError{Reason: "returned payload is not one JSON value"}
	}
	value, ok := schemaValueOf(raw)
	if !ok {
		return &SchemaError{Reason: "returned payload is not valid JSON"}
	}
	if bad := validateSchemaValue(value, s.root, ""); bad != "" {
		return &SchemaError{Pointer: bad, Reason: "returned payload does not match the requested schema"}
	}
	return nil
}

func schemaValueOf(raw any) (schemaValue, bool) {
	switch value := raw.(type) {
	case nil:
		return schemaValue{kind: schemaNull}, true
	case bool:
		return schemaValue{kind: schemaBool, boolean: value}, true
	case json.Number:
		return schemaValue{kind: schemaNumber, number: value}, true
	case string:
		return schemaValue{kind: schemaString, text: value}, true
	case []any:
		items := make([]schemaValue, 0, len(value))
		for _, item := range value {
			converted, ok := schemaValueOf(item)
			if !ok {
				return schemaValue{}, false
			}
			items = append(items, converted)
		}
		return schemaValue{kind: schemaArray, array: items}, true
	case map[string]any:
		fields := make(map[string]schemaValue, len(value))
		for name, item := range value {
			converted, ok := schemaValueOf(item)
			if !ok {
				return schemaValue{}, false
			}
			fields[name] = converted
		}
		return schemaValue{kind: schemaObject, object: fields}, true
	default:
		return schemaValue{}, false
	}
}

func (v schemaValue) asFloat() (float64, bool) {
	if v.kind != schemaNumber {
		return 0, false
	}
	number, err := v.number.Float64()
	if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
		return 0, false
	}
	return number, true
}

// parseSchemaNode mirrors SchemaSubset.parseNode. The declaration order is the
// helper's: type, unknown keywords, then the per-kind structure.
func parseSchemaNode(value schemaValue, path string, objectDepth int, budget *int) (*schemaNode, error) {
	if value.kind != schemaObject {
		return nil, &SchemaError{Pointer: path, Reason: "schema node must be an object"}
	}
	typeValue, present := value.object["type"]
	if !present || typeValue.kind != schemaString {
		return nil, &SchemaError{Pointer: pointer(path, "type"), Reason: "unsupported schema type"}
	}
	kind := typeValue.text
	var allowed []string
	switch kind {
	case "object":
		allowed = []string{"type", "properties", "required", "additionalProperties"}
	case "array":
		allowed = []string{"type", "items", "minItems", "maxItems"}
	case "string":
		allowed = []string{"type", "enum"}
	case "integer", "number":
		allowed = []string{"type", "minimum", "maximum"}
	case "boolean":
		allowed = []string{"type"}
	default:
		return nil, &SchemaError{Pointer: pointer(path, "type"), Reason: "unsupported schema type"}
	}
	// The helper asks a dictionary for an arbitrary unknown key; sorting here
	// keeps a multi-fault rejection reproducible without changing the rule.
	for _, key := range sortedKeys(value.object) {
		if !contains(allowed, key) {
			reason := "unsupported schema keyword"
			if key == "enum" {
				reason = "enums must be strings"
			}
			return nil, &SchemaError{Pointer: pointer(path, key), Reason: reason}
		}
	}
	switch kind {
	case "object":
		if objectDepth > SchemaMaxDepth {
			return nil, &SchemaError{Pointer: path, Reason: "object nesting exceeds depth 3"}
		}
		additional, present := value.object["additionalProperties"]
		if !present || additional.kind != schemaBool || additional.boolean {
			return nil, &SchemaError{Pointer: pointer(path, "additionalProperties"),
				Reason: "additionalProperties:false is required"}
		}
		node := &schemaNode{kind: "object", required: map[string]bool{}}
		declared := map[string]bool{}
		if rawProperties, present := value.object["properties"]; present {
			if rawProperties.kind != schemaObject {
				return nil, &SchemaError{Pointer: pointer(path, "properties"), Reason: "properties must be an object"}
			}
			for _, name := range sortedKeys(rawProperties.object) {
				if name == "" {
					return nil, &SchemaError{Pointer: pointer(path, "properties"),
						Reason: "property names must not be empty"}
				}
				*budget++
				if *budget > SchemaMaxProperties {
					return nil, &SchemaError{Pointer: pointer(path, "properties"),
						Reason: "schema declares more than 32 properties"}
				}
				child, err := parseSchemaNode(rawProperties.object[name],
					pointer(pointer(path, "properties"), name), objectDepth+1, budget)
				if err != nil {
					return nil, err
				}
				declared[name] = true
				node.properties = append(node.properties, schemaProperty{name: name, node: child})
			}
		}
		if rawRequired, present := value.object["required"]; present {
			if rawRequired.kind != schemaArray {
				return nil, &SchemaError{Pointer: pointer(path, "required"),
					Reason: "required must be an array of names"}
			}
			for index, entry := range rawRequired.array {
				at := path + "/required/" + strconv.Itoa(index)
				if entry.kind != schemaString {
					return nil, &SchemaError{Pointer: at, Reason: "required entries must be strings"}
				}
				if node.required[entry.text] {
					return nil, &SchemaError{Pointer: at, Reason: "required names must be unique"}
				}
				if !declared[entry.text] {
					return nil, &SchemaError{Pointer: pointer(path, "required"),
						Reason: "required names a property that is not declared"}
				}
				node.required[entry.text] = true
			}
		}
		for index := range node.properties {
			node.properties[index].required = node.required[node.properties[index].name]
		}
		return node, nil
	case "array":
		rawItems, present := value.object["items"]
		if !present {
			return nil, &SchemaError{Pointer: pointer(path, "items"), Reason: "array items are required"}
		}
		// Arrays add no object level: an object inside an array sits one level
		// below the object that declares the array property.
		items, err := parseSchemaNode(rawItems, pointer(path, "items"), objectDepth, budget)
		if err != nil {
			return nil, err
		}
		minimum, err := itemBound(value.object, "minItems", pointer(path, "minItems"))
		if err != nil {
			return nil, err
		}
		maximum, err := itemBound(value.object, "maxItems", pointer(path, "maxItems"))
		if err != nil {
			return nil, err
		}
		if minimum != nil && maximum != nil && *minimum > *maximum {
			return nil, &SchemaError{Pointer: path, Reason: "maxItems is smaller than minItems"}
		}
		return &schemaNode{kind: "array", items: items, minItems: minimum, maxItems: maximum}, nil
	case "string":
		node := &schemaNode{kind: "string"}
		if rawEnum, present := value.object["enum"]; present {
			if rawEnum.kind != schemaArray || len(rawEnum.array) == 0 {
				return nil, &SchemaError{Pointer: pointer(path, "enum"), Reason: "enum must not be empty"}
			}
			seen := map[string]bool{}
			for _, choice := range rawEnum.array {
				if choice.kind != schemaString {
					return nil, &SchemaError{Pointer: pointer(path, "enum"), Reason: "enum choices must be strings"}
				}
				if seen[choice.text] {
					return nil, &SchemaError{Pointer: pointer(path, "enum"), Reason: "enum choices must be unique"}
				}
				seen[choice.text] = true
				node.choices = append(node.choices, choice.text)
			}
			node.hasChoices = true
		}
		return node, nil
	case "integer":
		minimum, err := integerBound(value.object, "minimum", pointer(path, "minimum"))
		if err != nil {
			return nil, err
		}
		maximum, err := integerBound(value.object, "maximum", pointer(path, "maximum"))
		if err != nil {
			return nil, err
		}
		if minimum != nil && maximum != nil && *minimum > *maximum {
			return nil, &SchemaError{Pointer: path, Reason: "maximum is smaller than minimum"}
		}
		return &schemaNode{kind: "integer", minimum: minimum, maximum: maximum}, nil
	case "number":
		minimum, err := numberBound(value.object, "minimum", pointer(path, "minimum"))
		if err != nil {
			return nil, err
		}
		maximum, err := numberBound(value.object, "maximum", pointer(path, "maximum"))
		if err != nil {
			return nil, err
		}
		if minimum != nil && maximum != nil && *minimum > *maximum {
			return nil, &SchemaError{Pointer: path, Reason: "maximum is smaller than minimum"}
		}
		return &schemaNode{kind: "number", minimum: minimum, maximum: maximum}, nil
	default:
		return &schemaNode{kind: "boolean"}, nil
	}
}

func itemBound(fields map[string]schemaValue, key, path string) (*int, error) {
	value, present := fields[key]
	if !present {
		return nil, nil
	}
	number, ok := value.asFloat()
	if !ok || number != math.Trunc(number) || number < 0 || number > SchemaMaxItems {
		return nil, &SchemaError{Pointer: path, Reason: "array bounds must be whole numbers from 0 to 100"}
	}
	bound := int(number)
	return &bound, nil
}

func integerBound(fields map[string]schemaValue, key, path string) (*float64, error) {
	value, present := fields[key]
	if !present {
		return nil, nil
	}
	number, ok := value.asFloat()
	if !ok || number != math.Trunc(number) || math.Abs(number) > schemaMaxInteger {
		return nil, &SchemaError{Pointer: path, Reason: "integer bounds must be whole numbers"}
	}
	return &number, nil
}

func numberBound(fields map[string]schemaValue, key, path string) (*float64, error) {
	value, present := fields[key]
	if !present {
		return nil, nil
	}
	number, ok := value.asFloat()
	if !ok {
		return nil, &SchemaError{Pointer: path, Reason: "number bounds must be finite numbers"}
	}
	return &number, nil
}

// validateSchemaValue mirrors SchemaSubset.validate: it returns the first
// offending pointer, or "" when the value conforms.
func validateSchemaValue(value schemaValue, node *schemaNode, path string) string {
	self := func() string {
		if path == "" {
			return "/"
		}
		return path
	}
	switch node.kind {
	case "object":
		if value.kind != schemaObject {
			return self()
		}
		declared := make(map[string]schemaProperty, len(node.properties))
		for _, property := range node.properties {
			declared[property.name] = property
		}
		for _, key := range sortedKeys(value.object) {
			if _, ok := declared[key]; !ok {
				return pointer(path, key)
			}
		}
		for _, property := range node.properties {
			if _, present := value.object[property.name]; !present && property.required {
				return pointer(path, property.name)
			}
		}
		for _, property := range node.properties {
			if child, present := value.object[property.name]; present {
				if bad := validateSchemaValue(child, property.node, pointer(path, property.name)); bad != "" {
					return bad
				}
			}
		}
		return ""
	case "array":
		if value.kind != schemaArray {
			return self()
		}
		if node.minItems != nil && len(value.array) < *node.minItems {
			return self()
		}
		if node.maxItems != nil && len(value.array) > *node.maxItems {
			return self()
		}
		for index, child := range value.array {
			if bad := validateSchemaValue(child, node.items, path+"/"+strconv.Itoa(index)); bad != "" {
				return bad
			}
		}
		return ""
	case "string":
		if value.kind != schemaString {
			return self()
		}
		if node.hasChoices && !contains(node.choices, value.text) {
			return self()
		}
		return ""
	case "integer":
		number, ok := value.asFloat()
		if !ok || number != math.Trunc(number) {
			return self()
		}
		if node.minimum != nil && number < *node.minimum {
			return self()
		}
		if node.maximum != nil && number > *node.maximum {
			return self()
		}
		return ""
	case "number":
		number, ok := value.asFloat()
		if !ok {
			return self()
		}
		if node.minimum != nil && number < *node.minimum {
			return self()
		}
		if node.maximum != nil && number > *node.maximum {
			return self()
		}
		return ""
	default:
		if value.kind != schemaBool {
			return self()
		}
		return ""
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func sortedKeys(fields map[string]schemaValue) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func pointer(path, component string) string {
	return path + "/" + strings.ReplaceAll(strings.ReplaceAll(component, "~", "~0"), "/", "~1")
}

// encode is the whitespace-free, sorted-key encoding the helper measures. It
// follows Foundation's JSON encoder for the value shapes this subset admits:
// non-ASCII stays UTF-8, and only JSON's own escapes are emitted.
func (v schemaValue) encode() ([]byte, bool) {
	var builder strings.Builder
	if !v.encodeTo(&builder) {
		return nil, false
	}
	return []byte(builder.String()), true
}

func (v schemaValue) encodeTo(builder *strings.Builder) bool {
	switch v.kind {
	case schemaObject:
		builder.WriteByte('{')
		for index, key := range sortedKeys(v.object) {
			if index > 0 {
				builder.WriteByte(',')
			}
			writeJSONString(builder, key)
			builder.WriteByte(':')
			if !v.object[key].encodeTo(builder) {
				return false
			}
		}
		builder.WriteByte('}')
	case schemaArray:
		builder.WriteByte('[')
		for index, item := range v.array {
			if index > 0 {
				builder.WriteByte(',')
			}
			if !item.encodeTo(builder) {
				return false
			}
		}
		builder.WriteByte(']')
	case schemaString:
		writeJSONString(builder, v.text)
	case schemaNumber:
		number, ok := v.asFloat()
		if !ok {
			return false
		}
		if number == math.Trunc(number) && math.Abs(number) < 1e21 {
			builder.WriteString(strconv.FormatFloat(number, 'f', -1, 64))
			return true
		}
		builder.WriteString(strconv.FormatFloat(number, 'g', -1, 64))
	case schemaBool:
		builder.WriteString(strconv.FormatBool(v.boolean))
	case schemaNull:
		builder.WriteString("null")
	}
	return true
}

func writeJSONString(builder *strings.Builder, value string) {
	builder.WriteByte('"')
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch character {
		case '"':
			builder.WriteString(`\"`)
		case '\\':
			builder.WriteString(`\\`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		case '\b':
			builder.WriteString(`\b`)
		case '\f':
			builder.WriteString(`\f`)
		default:
			if character < 0x20 {
				fmt.Fprintf(builder, `\u%04x`, character)
				continue
			}
			builder.WriteByte(character)
		}
	}
	builder.WriteByte('"')
}
