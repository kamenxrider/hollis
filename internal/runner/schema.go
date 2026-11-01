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
	// SchemaMaxNesting counts schema nodes on one path, root included, so an
	// array chain cannot nest without bound. Objects are bounded more tightly
	// by SchemaMaxDepth; this bound applies to every node kind.
	SchemaMaxNesting = 16
	// SchemaMaxItems is the largest accepted minItems/maxItems value.
	SchemaMaxItems = 100
	// schemaMaxInteger is the largest integer bound exactly representable as a
	// JSON double, which is the bound the helper applies.
	schemaMaxInteger = 1 << 53
)

// SchemaError rejects a schema or a returned payload. Pointer is a JSON
// pointer into the offending document (empty for the document root); Reason is
// a fixed sentence that never contains caller content.
//
// Pointer keeps the document's property names verbatim so it can be compared
// with the helper's own pointer for the same fault. Those names are caller
// input, so Error escapes control characters: a property name containing ESC or
// BEL must not reach a terminal as a live escape sequence.
type SchemaError struct {
	Pointer string
	Reason  string
}

func (e *SchemaError) Error() string {
	if e.Pointer == "" {
		return e.Reason
	}
	return "schema at " + sanitizeSchemaPointer(e.Pointer) + ": " + e.Reason
}

// sanitizeSchemaPointer renders control characters as escapes. It mirrors the
// terminal policy used for model output, but applies unconditionally: an error
// message may be written to a terminal the caller has not identified, and no
// control byte in a JSON pointer carries meaning.
func sanitizeSchemaPointer(pointer string) string {
	if !strings.ContainsFunc(pointer, func(r rune) bool {
		return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
	}) {
		return pointer
	}
	var safe strings.Builder
	for _, character := range pointer {
		switch {
		case character < 0x20 || character == 0x7f || (character >= 0x80 && character <= 0x9f):
			fmt.Fprintf(&safe, "\\u%04x", character)
		default:
			safe.WriteRune(character)
		}
	}
	return safe.String()
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
	value, err := decodeSchemaDocument(data, "schema")
	if err != nil {
		return nil, err
	}
	normalized, ok := value.encode()
	if !ok {
		// Decoding already rejects numbers that cannot be re-encoded, so this
		// is defensive: report it as its own fault rather than as a size
		// failure, which would be false.
		return nil, &SchemaError{Reason: "schema contains a value that cannot be encoded"}
	}
	if len(normalized) > SchemaMaxBytes {
		return nil, &SchemaError{Reason: "schema is larger than 64 KiB"}
	}
	if kind, present := value.object["type"]; value.kind != schemaObject || !present ||
		kind.kind != schemaString || kind.text != "object" {
		return nil, &SchemaError{Reason: "root schema must be an object"}
	}
	budget := 0
	root, err := parseSchemaNode(value, "", 1, 1, &budget)
	if err != nil {
		return nil, err
	}
	if root.kind != "object" {
		return nil, &SchemaError{Reason: "root schema must be an object"}
	}
	return &Schema{root: root, raw: normalized}, nil
}

// decodeSchemaDocument decodes one JSON document into the generic tree,
// rejecting duplicate object keys and numbers that cannot be re-encoded.
//
// Duplicate keys are refused rather than resolved. Foundation's decoder keeps
// the first value where encoding/json keeps the last, so a schema with a
// repeated key would be measured and enforced differently on each side; failing
// closed here means the helper never sees an ambiguous schema. Numbers outside
// the finite double range are refused with their own reason: they cannot be
// measured or re-encoded, and reporting them as an oversized schema would be
// false.
func decodeSchemaDocument(data []byte, subject string) (schemaValue, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeSchemaValue(decoder, subject, "")
	if err != nil {
		return schemaValue{}, documentError(err, subject, " is not valid JSON")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return schemaValue{}, documentError(err, subject, " must be one JSON value")
	}
	return value, nil
}

// documentError keeps a specific rejection (duplicate key, number range) and
// otherwise reports the document-level failure for this subject.
func documentError(err error, subject, fallback string) error {
	var failure *SchemaError
	if errors.As(err, &failure) {
		return failure
	}
	return &SchemaError{Reason: subject + fallback}
}

// decodeSchemaValue reads one value from the token stream, tracking object keys
// so a duplicate is reported at its own pointer.
func decodeSchemaValue(decoder *json.Decoder, subject, path string) (schemaValue, error) {
	token, err := decoder.Token()
	if err != nil {
		return schemaValue{}, err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		switch value := token.(type) {
		case nil:
			return schemaValue{kind: schemaNull}, nil
		case bool:
			return schemaValue{kind: schemaBool, boolean: value}, nil
		case json.Number:
			converted := schemaValue{kind: schemaNumber, number: value}
			if _, ok := converted.asFloat(); !ok {
				return schemaValue{}, &SchemaError{Pointer: path,
					Reason: subject + " contains a number outside the supported range"}
			}
			return converted, nil
		case string:
			return schemaValue{kind: schemaString, text: value}, nil
		default:
			return schemaValue{}, errors.New("unsupported JSON token")
		}
	}
	switch delim {
	case '{':
		fields := map[string]schemaValue{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return schemaValue{}, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return schemaValue{}, errors.New("unsupported JSON object key")
			}
			if _, duplicate := fields[key]; duplicate {
				return schemaValue{}, &SchemaError{Pointer: pointer(path, key),
					Reason: subject + " must not repeat an object key"}
			}
			child, err := decodeSchemaValue(decoder, subject, pointer(path, key))
			if err != nil {
				return schemaValue{}, err
			}
			fields[key] = child
		}
		if _, err := decoder.Token(); err != nil {
			return schemaValue{}, err
		}
		return schemaValue{kind: schemaObject, object: fields}, nil
	case '[':
		var items []schemaValue
		for index := 0; decoder.More(); index++ {
			child, err := decodeSchemaValue(decoder, subject, path+"/"+strconv.Itoa(index))
			if err != nil {
				return schemaValue{}, err
			}
			items = append(items, child)
		}
		if _, err := decoder.Token(); err != nil {
			return schemaValue{}, err
		}
		return schemaValue{kind: schemaArray, array: items}, nil
	default:
		return schemaValue{}, errors.New("unsupported JSON delimiter")
	}
}

// Validate reports whether a returned payload conforms to the requested
// schema. Structural validity is all it establishes: it never claims the
// values are semantically correct.
func (s *Schema) Validate(payload json.RawMessage) error {
	value, err := decodeSchemaDocument(payload, "returned payload")
	if err != nil {
		return err
	}
	if bad := validateSchemaValue(value, s.root, ""); bad != "" {
		return &SchemaError{Pointer: bad, Reason: "returned payload does not match the requested schema"}
	}
	return nil
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
func parseSchemaNode(value schemaValue, path string, objectDepth, nesting int, budget *int) (*schemaNode, error) {
	// Nesting is checked before anything else, for every node kind. Object
	// depth alone would leave array chains unbounded: an array's items can be
	// another array, and a document nested deeply enough fails in the helper's
	// decoder after the request has already spawned. Rejecting here keeps the
	// pre-spawn promise and gives Go and Swift the same pointer.
	if nesting > SchemaMaxNesting {
		return nil, &SchemaError{Pointer: path, Reason: "schema nesting exceeds depth 16"}
	}
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
				// Two byte-distinct names that are canonically equivalent in
				// Unicode are one name to Foundation and two to Go, so a schema
				// could be measured and enforced differently on each side.
				// Restricting authoring names to printable ASCII removes that:
				// no two ASCII strings are canonically equivalent unless they
				// are identical.
				if !printableASCII(name) {
					return nil, &SchemaError{Pointer: pointer(path, "properties"),
						Reason: "property names must be printable ASCII"}
				}
				*budget++
				if *budget > SchemaMaxProperties {
					return nil, &SchemaError{Pointer: pointer(path, "properties"),
						Reason: "schema declares more than 32 properties"}
				}
				child, err := parseSchemaNode(rawProperties.object[name],
					pointer(pointer(path, "properties"), name), objectDepth+1, nesting+1, budget)
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
				if !printableASCII(entry.text) {
					return nil, &SchemaError{Pointer: at, Reason: "required names must be printable ASCII"}
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
		items, err := parseSchemaNode(rawItems, pointer(path, "items"), objectDepth, nesting+1, budget)
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
				// An enum choice is matched against model output. Foundation
				// compares strings by canonical equivalence, so a precomposed
				// choice would accept a decomposed value that Go rejects. With
				// printable ASCII choices the two comparisons agree.
				if !printableASCII(choice.text) {
					return nil, &SchemaError{Pointer: pointer(path, "enum"),
						Reason: "enum choices must be printable ASCII"}
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

// printableASCII reports whether every byte of name is a printable ASCII
// character. Schema-authoring names and enum choices are held to this so that
// byte identity and canonical identity are the same relation: Foundation
// compares strings by Unicode canonical equivalence, and no non-ASCII string is
// canonically equivalent to an ASCII one. Control characters are excluded too,
// which also keeps a name out of a terminal escape sequence.
func printableASCII(name string) bool {
	for index := 0; index < len(name); index++ {
		if name[index] < 0x20 || name[index] > 0x7e {
			return false
		}
	}
	return true
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
// keys sorted, non-ASCII kept as UTF-8, "/" escaped as "\/", and numbers
// printed with 17 significant digits. The 64 KiB bound is applied to this
// encoding on both sides, so matching Foundation's number and slash decisions
// is what keeps the two measurements comparable.
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
		builder.WriteString(formatJSONNumber(number))
	case schemaBool:
		builder.WriteString(strconv.FormatBool(v.boolean))
	case schemaNull:
		builder.WriteString("null")
	}
	return true
}

// formatJSONNumber reproduces Foundation's JSON number text: 17 significant
// digits in shortest-exponent form. Verified against JSONSerialization for
// 1, 1.5, 0.1, 1e18, 1e21, 2^53, 1e300, 5e-324 and -0.
func formatJSONNumber(value float64) string {
	return strconv.FormatFloat(value, 'g', 17, 64)
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
		// Foundation escapes every solidus, not only the JSON-mandated ones.
		// Matching it keeps the measured size identical for schema values that
		// contain a path, and a divergence here would shift the 64 KiB gate.
		case '/':
			builder.WriteString(`\/`)
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
