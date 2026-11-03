// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

// Caller-provided tools on the native local route.
//
// The model writes tool calls as ordinary text; this file detects, normalizes
// and validates them. Nothing here executes a tool, makes a model call, or
// sends a tool definition to the helper; the tool loop transports validated
// definitions as native metadata separately from the conversation.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const (
	// MaxTools bounds the tools offered in one request.
	MaxTools = 32
	// MaxToolCallsPerRound is the most calls one round may carry. More is a
	// framing failure, not an argument problem, so it is never repaired.
	MaxToolCallsPerRound = 16
	// ToolRoundCap bounds visible prior tool turns plus actual new model calls,
	// including corrections. A call at the cap fails instead of continuing.
	ToolRoundCap = 3
)

// ToolChoice is the caller's tool policy. Only auto and none exist: a named
// function is refused, and the SDK's stricter required mode is measured broken.
type ToolChoice string

const (
	ToolChoiceAuto ToolChoice = "auto"
	ToolChoiceNone ToolChoice = "none"
)

// Tool is one validated caller tool. Its parameters fit the widened subset
// exactly as a --schema document does, so every argument can be validated
// with the same rules guided output uses.
type Tool struct {
	Name        string
	Description string
	Parameters  *Schema
}

// ToolCall is one normalized, validated call. Arguments is the JSON object
// encoded as a string, the form both the caller and the transcript carry.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

var toolNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// ToolDefinitionError rejects a tools array before anything is staged or
// spawned. It is always a caller fault. The message names the tool by index
// and quotes only schema pointers, never a description or other caller text.
type ToolDefinitionError struct {
	Message string
}

func (e *ToolDefinitionError) Error() string { return e.Message }

func toolDefinitionError(format string, args ...any) error {
	return &ToolDefinitionError{Message: fmt.Sprintf(format, args...)}
}

// toolDefinition is the OpenAI tools element. strict is accepted because
// every Hollis tool is validated strictly; any other member is refused.
type toolDefinition struct {
	Type     string          `json:"type"`
	Function json.RawMessage `json:"function"`
}

type toolFunction struct {
	Name        string          `json:"name"`
	Description *string         `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      *bool           `json:"strict"`
}

// emptyToolParameters is what an omitted parameters member means: a tool
// that takes no arguments.
var emptyToolParameters = []byte(`{"type":"object","properties":{}}`)

// ParseTools validates a tools array in the OpenAI shape. subject names the
// member in messages ("--tools" or "tools").
//
// Caller tools must fit the widened schema subset (tier 1). The ask-only tier
// for schemas the subset cannot express belongs to server-supplied tools,
// where a person approves each call; a caller's own tools are returned to
// the caller, so a schema that cannot be validated is refused here with its
// pointer rather than offered with a weaker promise.
func ParseTools(data []byte, subject string) ([]Tool, error) {
	var elements []json.RawMessage
	if err := strictDecode(data, &elements); err != nil || elements == nil {
		return nil, toolDefinitionError("%s must be a JSON array of tools", subject)
	}
	if len(elements) == 0 {
		return nil, toolDefinitionError("%s must contain at least one tool", subject)
	}
	if len(elements) > MaxTools {
		return nil, toolDefinitionError("%s has %d tools; the maximum is %d", subject, len(elements), MaxTools)
	}
	tools := make([]Tool, 0, len(elements))
	seen := map[string]bool{}
	for index, element := range elements {
		at := fmt.Sprintf("%s[%d]", subject, index)
		var definition toolDefinition
		if err := strictDecode(element, &definition); err != nil {
			return nil, decodeDefinitionError(err, at)
		}
		if definition.Type != "function" {
			return nil, toolDefinitionError("%s.type must be \"function\"", at)
		}
		var function toolFunction
		if len(definition.Function) == 0 || isJSONNull(definition.Function) {
			return nil, toolDefinitionError("%s.function is required", at)
		}
		if err := strictDecode(definition.Function, &function); err != nil {
			return nil, decodeDefinitionError(err, at+".function")
		}
		if !toolNamePattern.MatchString(function.Name) {
			return nil, toolDefinitionError("%s.function.name must match ^[a-zA-Z0-9_-]{1,64}$", at)
		}
		if seen[function.Name] {
			return nil, toolDefinitionError("%s.function.name repeats an earlier tool name", at)
		}
		seen[function.Name] = true
		if function.Description == nil || strings.TrimSpace(*function.Description) == "" {
			return nil, toolDefinitionError("%s.function.description must be a non-empty string", at)
		}
		parameters := []byte(function.Parameters)
		if len(parameters) == 0 {
			parameters = emptyToolParameters
		}
		schema, err := ParseSchema(parameters)
		if err != nil {
			var failure *SchemaError
			if errors.As(err, &failure) {
				return nil, toolDefinitionError("%s.function.parameters: %s", at, failure.Error())
			}
			return nil, toolDefinitionError("%s.function.parameters is not a supported schema", at)
		}
		tools = append(tools, Tool{Name: function.Name, Description: *function.Description, Parameters: schema})
	}
	return tools, nil
}

func decodeDefinitionError(err error, at string) error {
	if strings.Contains(err.Error(), "unknown field") {
		return toolDefinitionError("%s contains an unsupported member", at)
	}
	return toolDefinitionError("%s has an invalid shape", at)
}

func strictDecode(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if !errors.Is(decoder.Decode(new(any)), io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func isJSONNull(raw []byte) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

// Fixed instructions accompany native definitions, never caller-authored text.
const (
	toolFramingOpen = "You can call tools. When a tool is needed, respond with exactly one JSON object and nothing else, in this form:\n" +
		`{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"<tool name>","arguments":"<arguments>"}}]}` + "\n" +
		"When no tool is needed, answer in plain text. Call only the tools declared in the native tool definitions."
	toolFramingClose = "The arguments value is a JSON object encoded as a string. Arguments are checked against the parameters schema for types, required properties, allowed values and numeric bounds; string length, pattern and format are not enforced. A property whose schema allows null may be left out."
	toolInstructions = toolFramingOpen + "\n\n" + toolFramingClose
)

func encodeCompact(value any) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		panic("runner: value cannot be encoded: " + err.Error())
	}
	return strings.TrimSuffix(buffer.String(), "\n")
}

// detectToolCalls finds the first candidate whose JSON has a top-level
// tool_calls array. Candidates, in order: the whole text, each fenced code
// block, then every balanced {...} object left to right (string-aware), and
// finally closure repairs at the end of the text.
func detectToolCalls(text string) ([]schemaValue, bool) {
	if !strings.Contains(text, `"tool_calls"`) {
		return nil, false
	}
	try := func(candidate string) ([]schemaValue, bool) {
		if !strings.Contains(candidate, `"tool_calls"`) {
			return nil, false
		}
		value, err := decodeSchemaDocument([]byte(strings.TrimSpace(candidate)), "tool call")
		if err != nil || value.kind != schemaObject {
			return nil, false
		}
		calls, present := value.object["tool_calls"]
		if !present || calls.kind != schemaArray {
			return nil, false
		}
		return calls.array, true
	}
	if calls, ok := try(text); ok {
		return calls, true
	}
	for _, block := range fencedBlocks(text) {
		if calls, ok := try(block); ok {
			return calls, true
		}
	}
	objects, open := balancedObjects(text)
	for _, candidates := range [][]string{objects, open} {
		for _, candidate := range candidates {
			if calls, ok := try(candidate); ok {
				return calls, true
			}
		}
	}
	if candidate, ok := closeTerminalToolArray(text); ok {
		return try(candidate)
	}
	return nil, false
}

// closeTerminalToolArray only inserts the outer tool_calls array's missing
// closer before the final object closer. It never closes an argument container,
// edits a value, or selects one wrapper from multiple malformed candidates.
func closeTerminalToolArray(text string) (string, bool) {
	if len(text) > maxDetectionSteps {
		return "", false
	}
	text = strings.TrimSpace(text)
	markers := toolAttemptMarker.FindAllStringIndex(text, 2)
	if len(markers) != 1 || markers[0][0] != 0 {
		return "", false
	}
	var stack []byte
	inString, escaped := false, false
	for index := 0; index < len(text); index++ {
		character := text[index]
		if inString {
			switch {
			case escaped:
				escaped = false
			case character == '\\':
				escaped = true
			case character == '"':
				inString = false
			}
			continue
		}
		switch character {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, character)
		case '}', ']':
			if len(stack) == 0 {
				return "", false
			}
			if (character == '}') != (stack[len(stack)-1] == '{') {
				if character == '}' && index == len(text)-1 &&
					len(stack) == 2 && stack[0] == '{' && stack[1] == '[' {
					return text[:index] + "]}", true
				}
				return "", false
			}
			stack = stack[:len(stack)-1]
		}
	}
	return "", false
}

// fencedBlocks returns the body of each ``` fenced block, without the info
// string on the opening line.
func fencedBlocks(text string) []string {
	var blocks []string
	rest := text
	for {
		start := strings.Index(rest, "```")
		if start < 0 {
			return blocks
		}
		rest = rest[start+3:]
		newline := strings.IndexByte(rest, '\n')
		end := strings.Index(rest, "```")
		if end < 0 {
			return blocks
		}
		body := rest[:end]
		if newline >= 0 && newline < end {
			body = rest[newline+1 : end]
		}
		blocks = append(blocks, body)
		rest = rest[end+3:]
	}
}

// balancedObjects returns, in start order, every balanced {...} object in the
// text and every object still open at the end of it. Each '{' is its own
// scan start and quotes are tracked from that start, so a stray brace or
// quote in prose cannot hide a later object. A mismatched closer ends that
// start's scan. An open object comes back closed with exactly the brackets it
// is missing, unless the text ends inside a string; that is the one closure
// repair, and it only ever appends.
//
// The total scan is bounded by maxDetectionSteps so a reply made of braces
// costs a fixed amount of work; starts past the bound are not tried.
func balancedObjects(text string) (objects, open []string) {
	steps := 0
	for start := 0; start < len(text); start++ {
		if text[start] != '{' {
			continue
		}
		var stack []byte
		inString, escaped, closed := false, false, false
		index := start
		for ; index < len(text) && !closed; index++ {
			steps++
			if steps > maxDetectionSteps {
				return objects, open
			}
			character := text[index]
			if inString {
				switch {
				case escaped:
					escaped = false
				case character == '\\':
					escaped = true
				case character == '"':
					inString = false
				}
				continue
			}
			switch character {
			case '"':
				inString = true
			case '{', '[':
				stack = append(stack, character)
			case '}', ']':
				if (character == '}') != (stack[len(stack)-1] == '{') {
					stack = nil
					closed = true
					continue
				}
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					objects = append(objects, text[start:index+1])
					closed = true
				}
			}
		}
		if !closed && len(stack) > 0 && !inString {
			var closers strings.Builder
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i] == '{' {
					closers.WriteByte('}')
				} else {
					closers.WriteByte(']')
				}
			}
			open = append(open, strings.TrimRight(text[start:], " \t\r\n")+closers.String())
		}
	}
	return objects, open
}

// maxDetectionSteps bounds the balanced-object scan. A native reply is far
// smaller than this in practice; the bound exists for adversarial text.
const maxDetectionSteps = 1 << 22

var toolAttemptMarker = regexp.MustCompile(`\{\s*"tool_calls"\s*:\s*\[`)
var toolAttemptFunction = regexp.MustCompile(`"function"\s*:`)

// malformedToolAttemptName recognizes only a single call's valid header, never
// its arguments. Ambiguous/multiple calls are left alone rather than salvaging
// a subset or guessing which names belong to malformed argument text.
func malformedToolAttemptName(text string) string {
	if len(text) > maxDetectionSteps {
		return ""
	}
	markers := toolAttemptMarker.FindAllStringIndex(text, 2)
	if len(markers) != 1 {
		return ""
	}
	candidate := text[markers[0][0]:]
	if len(toolAttemptFunction.FindAllStringIndex(candidate, 2)) != 1 {
		return ""
	}
	decoder := json.NewDecoder(strings.NewReader(candidate))
	tokenIs := func(want any) bool {
		token, err := decoder.Token()
		return err == nil && token == want
	}
	if !tokenIs(json.Delim('{')) || !tokenIs("tool_calls") ||
		!tokenIs(json.Delim('[')) || !tokenIs(json.Delim('{')) {
		return ""
	}
	seen := map[string]bool{}
	for {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return ""
		}
		seen[key] = true
		switch key {
		case "id", "type":
			var value string
			if decoder.Decode(&value) != nil {
				return ""
			}
		case "function":
			token, err := decoder.Token()
			if err != nil {
				return ""
			}
			name, bare := token.(string)
			if !bare {
				if token != json.Delim('{') || !tokenIs("name") || decoder.Decode(&name) != nil {
					return ""
				}
			}
			if !tokenIs("arguments") {
				return ""
			}
			return name
		default:
			return ""
		}
	}
}

// roundOutcome is what one round's text means once detected and normalized.
type roundOutcome struct {
	// plain is true when the text is the final answer: no candidate, or a
	// candidate naming a tool that was not offered.
	plain bool
	calls []ToolCall
	// fault is a fixed, repairable validation reason; framing marks the
	// unrepairable too-many-calls failure.
	fault   string
	framing bool
}

// interpretRound detects, normalizes and validates one round's text. round
// numbers synthesized ids; used holds every id already in the conversation.
func interpretRound(text string, tools []Tool, round int, used map[string]bool) roundOutcome {
	elements, found := detectToolCalls(text)
	if !found {
		if name := malformedToolAttemptName(text); name != "" {
			for _, tool := range tools {
				if tool.Name == name {
					return roundOutcome{fault: "the tool-call reply is not valid JSON"}
				}
			}
		}
		return roundOutcome{plain: true}
	}
	if len(elements) > MaxToolCallsPerRound {
		return roundOutcome{framing: true, fault: fmt.Sprintf("the reply contained %d tool calls; at most %d are allowed in one round", len(elements), MaxToolCallsPerRound)}
	}
	if len(elements) == 0 {
		return roundOutcome{fault: "tool_calls must contain at least one call"}
	}
	offered := make(map[string]*Tool, len(tools))
	for index := range tools {
		offered[tools[index].Name] = &tools[index]
	}
	type pending struct {
		id, name  string
		arguments schemaValue
		present   bool
	}
	parsed := make([]pending, 0, len(elements))
	for _, element := range elements {
		name, arguments, present, id, ok := callShape(element)
		// A call that names no offered tool is not normalized into one: the
		// whole reply is the answer, delivered verbatim and never executed.
		if !ok || offered[name] == nil {
			return roundOutcome{plain: true}
		}
		parsed = append(parsed, pending{id: id, name: name, arguments: arguments, present: present})
	}
	calls := make([]ToolCall, 0, len(parsed))
	ids := map[string]bool{}
	for index, call := range parsed {
		tool := offered[call.name]
		label := fmt.Sprintf("call %d to tool %q", index+1, call.name)
		if !call.present {
			return roundOutcome{fault: label + " has no arguments"}
		}
		arguments := call.arguments
		if arguments.kind == schemaString {
			decoded, err := decodeSchemaDocument([]byte(arguments.text), "arguments")
			if err != nil {
				return roundOutcome{fault: label + " has arguments that are not a JSON object"}
			}
			arguments = decoded
		}
		if arguments.kind != schemaObject {
			return roundOutcome{fault: label + " has arguments that are not a JSON object"}
		}
		arguments = coerceNumericStrings(arguments, tool.Parameters.root)
		if fault := checkSchemaValue(arguments, tool.Parameters.root, ""); fault.pointer != "" {
			return roundOutcome{fault: fmt.Sprintf("%s: the argument at %s %s", label, sanitizeSchemaPointer(fault.at), fault.reason)}
		}
		id := call.id
		if !toolNamePattern.MatchString(id) || used[id] || ids[id] {
			id = synthesizeCallID(round, index, used, ids)
		}
		ids[id] = true
		calls = append(calls, ToolCall{ID: id, Name: call.name, Arguments: encodeArguments(arguments)})
	}
	return roundOutcome{calls: calls}
}

// callShape reads one tool_calls element. The canonical shape has a function
// object carrying name and arguments; the observed alternative has function
// as the bare name with arguments beside it. ok is false when no name can be
// read, which makes the reply plain text rather than a guessed call.
func callShape(element schemaValue) (name string, arguments schemaValue, present bool, id string, ok bool) {
	if element.kind != schemaObject {
		return "", schemaValue{}, false, "", false
	}
	if value, has := element.object["id"]; has && value.kind == schemaString {
		id = value.text
	}
	function, has := element.object["function"]
	switch {
	case has && function.kind == schemaObject:
		nameValue, hasName := function.object["name"]
		if !hasName || nameValue.kind != schemaString {
			return "", schemaValue{}, false, "", false
		}
		arguments, present = function.object["arguments"]
		return nameValue.text, arguments, present, id, true
	case has && function.kind == schemaString:
		arguments, present = element.object["arguments"]
		return function.text, arguments, present, id, true
	}
	return "", schemaValue{}, false, "", false
}

// coerceNumericStrings converts a string to a number where the schema
// declares integer or number and the string is exactly a finite JSON number.
// Nothing else is converted, so a non-numeric string still fails validation.
func coerceNumericStrings(value schemaValue, node *schemaNode) schemaValue {
	switch node.kind {
	case "integer", "number":
		if value.kind != schemaString {
			return value
		}
		text := strings.TrimSpace(value.text)
		if !json.Valid([]byte(text)) {
			return value
		}
		number, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) || !isJSONNumber(text) {
			return value
		}
		return schemaValue{kind: schemaNumber, number: json.Number(text)}
	case "object":
		if value.kind != schemaObject {
			return value
		}
		fields := make(map[string]schemaValue, len(value.object))
		for key, child := range value.object {
			fields[key] = child
		}
		for _, property := range node.properties {
			if child, present := fields[property.name]; present {
				fields[property.name] = coerceNumericStrings(child, property.node)
			}
		}
		return schemaValue{kind: schemaObject, object: fields}
	case "array":
		if value.kind != schemaArray {
			return value
		}
		items := make([]schemaValue, len(value.array))
		for index, child := range value.array {
			items[index] = coerceNumericStrings(child, node.items)
		}
		return schemaValue{kind: schemaArray, array: items}
	}
	return value
}

func isJSONNumber(text string) bool {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return false
	}
	number, ok := value.(json.Number)
	return ok && number.String() == text
}

func synthesizeCallID(round, index int, used, ids map[string]bool) string {
	id := fmt.Sprintf("call_%d_%d", round, index)
	for suffix := 1; used[id] || ids[id]; suffix++ {
		id = fmt.Sprintf("call_%d_%d_%d", round, index, suffix)
	}
	return id
}

// encodeArguments is the normalized arguments object as standard JSON text.
func encodeArguments(value schemaValue) string {
	return encodeCompact(value.native())
}

func (v schemaValue) native() any {
	switch v.kind {
	case schemaObject:
		fields := make(map[string]any, len(v.object))
		for key, child := range v.object {
			fields[key] = child.native()
		}
		return fields
	case schemaArray:
		items := make([]any, len(v.array))
		for index, child := range v.array {
			items[index] = child.native()
		}
		return items
	case schemaString:
		return v.text
	case schemaNumber:
		return v.number
	case schemaBool:
		return v.boolean
	}
	return nil
}
