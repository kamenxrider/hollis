// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kamenxrider/hollis/internal/runner"
)

// Tier says how much Hollis validates a tool's arguments itself.
type Tier int

const (
	// TierUnsupported tools are listed but never offered to the model.
	TierUnsupported Tier = iota
	// TierValidated tools fit the shared schema subset; Hollis validates
	// every argument locally before approval and execution.
	TierValidated
	// TierServerValidated tools are offered with their schema as text; the
	// server validates the arguments. They are ask-only.
	TierServerValidated
)

func (t Tier) String() string {
	switch t {
	case TierValidated:
		return "validated locally"
	case TierServerValidated:
		return "server-validated"
	default:
		return "unsupported"
	}
}

const (
	// MaxRenderedName is the loop's offered-name rule: ^[A-Za-z0-9_-]{1,64}$.
	MaxRenderedName = 64
	// MaxToolsPerServer and MaxToolsTotal bound what one invocation offers.
	MaxToolsPerServer = 64
	MaxToolsTotal     = 128
	// maxSchemaBytes matches the shared subset's normalized bound.
	maxSchemaBytes = 64 << 10
	// maxSchemaTextBytes bounds one tier-2 tool's schema rendered as text,
	// and maxSchemaTextTotal bounds all of them together in one invocation.
	maxSchemaTextBytes = 2 << 10
	maxSchemaTextTotal = 16 << 10
	// maxDescriptionBytes bounds a tool description so an oversized one is
	// refused at attach instead of failing later as a round-budget error.
	maxDescriptionBytes = 4 << 10
)

// Tool is one tool a server published, as Hollis classified it.
type Tool struct {
	Server string
	Name   string
	// Rendered is the name the model sees: server__tool.
	Rendered    string
	Description string
	// InputSchema is the schema exactly as published.
	InputSchema json.RawMessage
	Tier        Tier
	// SchemaText is the compact schema offered to the model for a tier-2
	// tool.
	SchemaText string
	// Reason and Pointer explain why a tool is unsupported or why it fell
	// to tier 2. Both are fixed text and never quote server content beyond
	// the JSON pointer.
	Reason  string
	Pointer string
	// Pin is the SHA-256 identity of the definition: name, description and
	// schema. An allowlisted tool runs automatically only while its live
	// definition still matches the pin recorded when it was allowlisted.
	Pin string
	// Late marks a tool that was absent from the attach-time snapshot.
	Late bool

	schema *runner.Schema
}

// Offered reports whether the model may see the tool.
func (t Tool) Offered() bool { return t.Tier != TierUnsupported }

// ValidateArguments checks args against the tool's schema. A tier-1 tool is
// validated fully; a tier-2 tool only has to be a JSON object.
func (t Tool) ValidateArguments(args json.RawMessage) error {
	if !isJSONObject(args) {
		return newError(CodeInvalidArguments, t.Server, t.Name, "arguments must be a JSON object")
	}
	if t.Tier == TierValidated && t.schema != nil {
		if err := t.schema.Validate(args); err != nil {
			return &Error{Code: CodeInvalidArguments, Server: t.Server, Tool: t.Name, Message: "arguments do not match the tool's schema: " + err.Error()}
		}
	}
	return nil
}

// RenderName maps (server, tool) to the model-facing name. Characters outside
// [A-Za-z0-9_-] become '_'; the mapping is kept so collisions can be refused.
func RenderName(server, tool string) string {
	return sanitizeNamePart(server) + "__" + sanitizeNamePart(tool)
}

func sanitizeNamePart(part string) string {
	var b strings.Builder
	for _, r := range part {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// DefinitionPin hashes the parts of a tool definition the model and the
// approval decision depend on. JSON objects are re-encoded with sorted keys so
// the pin does not depend on a server's key order.
func DefinitionPin(name, description string, schema json.RawMessage) string {
	canonical, err := canonicalJSON(schema)
	if err != nil {
		canonical = schema
	}
	h := sha256.New()
	fmt.Fprintf(h, "hollis-mcp-tool-v1\x00%d:%s\x00%d:%s\x00", len(name), name, len(description), description)
	h.Write(canonical)
	return hex.EncodeToString(h.Sum(nil))
}

func canonicalJSON(raw json.RawMessage) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// classify assigns a tier and computes the rendered name and pin.
func classify(server, name, description string, schema json.RawMessage) Tool {
	tool := Tool{
		Server:      server,
		Name:        name,
		Rendered:    RenderName(server, name),
		Description: description,
		InputSchema: schema,
		Pin:         DefinitionPin(name, description, schema),
	}
	unsupported := func(reason, pointer string) Tool {
		tool.Tier, tool.Reason, tool.Pointer = TierUnsupported, reason, pointer
		return tool
	}
	if name == "" {
		return unsupported("tool has no name", "")
	}
	if len(tool.Rendered) > MaxRenderedName {
		return unsupported(fmt.Sprintf("rendered name is %d characters; the model-facing limit is %d", len(tool.Rendered), MaxRenderedName), "")
	}
	if len(description) > maxDescriptionBytes {
		return unsupported(fmt.Sprintf("description is %d bytes; the limit is %d", len(description), maxDescriptionBytes), "/description")
	}
	if len(schema) > maxSchemaBytes {
		return unsupported(fmt.Sprintf("input schema is %d bytes; the limit is %d", len(schema), maxSchemaBytes), "")
	}
	if !isJSONObject(schema) || !schemaRootIsObject(schema) {
		return unsupported("root schema must be an object", "")
	}
	parsed, err := runner.ParseSchema(schema)
	if err == nil {
		tool.Tier, tool.schema = TierValidated, parsed
		return tool
	}
	if schemaErr, ok := err.(*runner.SchemaError); ok {
		tool.Reason, tool.Pointer = schemaErr.Reason, schemaErr.Pointer
	} else {
		tool.Reason = "schema is outside the locally validated subset"
	}
	compact, cerr := canonicalJSON(schema)
	if cerr != nil {
		return unsupported("input schema is not valid JSON", "")
	}
	if len(compact) > maxSchemaTextBytes {
		return unsupported(fmt.Sprintf("schema needs server validation and its text is %d bytes; the limit is %d", len(compact), maxSchemaTextBytes), tool.Pointer)
	}
	tool.Tier, tool.SchemaText = TierServerValidated, string(compact)
	return tool
}

func schemaRootIsObject(schema json.RawMessage) bool {
	var root struct {
		Type any `json:"type"`
	}
	if json.Unmarshal(schema, &root) != nil {
		return false
	}
	kind, ok := root.Type.(string)
	return ok && kind == "object"
}

func isJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	return json.Valid(trimmed)
}
