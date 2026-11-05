package runner

import (
	"encoding/json"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// GenerationOptions is per model decision. Nil fields retain SDK defaults.
type GenerationOptions struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	Seed        *uint64  `json:"seed,omitempty"`
}

func (o *GenerationOptions) Validate() error {
	if o == nil {
		return nil
	}
	if o.Temperature != nil && (math.IsNaN(*o.Temperature) || math.IsInf(*o.Temperature, 0) || *o.Temperature < 0 || *o.Temperature > 2) {
		return nativeError(KindUsage, "temperature must be finite and in 0..2")
	}
	if o.TopP != nil && (math.IsNaN(*o.TopP) || math.IsInf(*o.TopP, 0) || *o.TopP <= 0 || *o.TopP > 1) {
		return nativeError(KindUsage, "top_p must be finite and in (0,1]")
	}
	if o.MaxTokens != nil && *o.MaxTokens <= 0 {
		return nativeError(KindUsage, "max_tokens must be a positive integer")
	}
	if o.Temperature != nil && *o.Temperature == 0 && (o.TopP != nil || o.Seed != nil) {
		return nativeError(KindUsage, "temperature zero cannot be combined with top_p or seed")
	}
	return nil
}

type NativeToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type NativeToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Source      string          `json:"source"`
	Validation  string          `json:"validation"`
}

type NativeToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// NativeToolHistory is built only from explicit API fields or stored envelopes.
type NativeToolHistory struct {
	Type    string           `json:"type"`
	Text    *string          `json:"text,omitempty"`
	ID      string           `json:"id,omitempty"`
	Name    string           `json:"name,omitempty"`
	Calls   []NativeToolCall `json:"calls,omitempty"`
	IsError *bool            `json:"is_error,omitempty"`
}

type NativeToolStep struct {
	Protocol       int                    `json:"protocol"`
	Operation      string                 `json:"operation"`
	RequestID      string                 `json:"request_id"`
	Step           int                    `json:"step"`
	RemainingSteps int                    `json:"remaining_steps"`
	Choice         NativeToolChoice       `json:"tool_choice"`
	Instructions   string                 `json:"instructions"`
	Tools          []NativeToolDefinition `json:"tools"`
	History        []NativeToolHistory    `json:"history"`
	Options        *GenerationOptions     `json:"generation_options,omitempty"`
}

func validNativeCallID(id string) bool {
	return id != "" && len(id) <= 128 && utf8.ValidString(id) &&
		!strings.ContainsFunc(id, unicode.IsControl)
}

func (s NativeToolStep) validate() error {
	bad := func() error { return nativeError(KindUsage, "invalid native tool step") }
	if s.Protocol != 4 || s.Operation != "tool_step" || !validNativeCallID(s.RequestID) ||
		s.Step < 1 || s.Step > 128 || s.RemainingSteps < 1 || s.RemainingSteps > 128 ||
		len(s.Tools) > MaxToolOffers || len(s.History) == 0 {
		return bad()
	}
	if err := s.Options.Validate(); err != nil {
		return err
	}
	names := map[string]bool{}
	callers, size := 0, len(s.Instructions)
	for _, tool := range s.Tools {
		if !toolNamePattern.MatchString(tool.Name) || names[tool.Name] ||
			!utf8.ValidString(tool.Description) ||
			(tool.Source != "caller" && tool.Source != "mcp") ||
			(tool.Validation != "strict" && tool.Validation != "server") ||
			(tool.Validation == "server" && tool.Source != "mcp") {
			return bad()
		}
		if tool.Source == "caller" {
			callers++
			if strings.TrimSpace(tool.Description) == "" {
				return bad()
			}
		}
		if _, err := ParseSchema(tool.Parameters); err != nil {
			return bad()
		}
		names[tool.Name] = true
		size += len(tool.Name) + len(tool.Description) + len(tool.Parameters)
	}
	if callers > MaxTools {
		return bad()
	}
	switch s.Choice.Type {
	case "auto", "none":
		if s.Choice.Name != "" {
			return bad()
		}
	case "required":
		if len(names) == 0 || s.Choice.Name != "" {
			return bad()
		}
	case "function":
		if !names[s.Choice.Name] {
			return bad()
		}
	default:
		return bad()
	}
	used, outstanding := map[string]bool{}, map[string]string{}
	for _, h := range s.History {
		switch h.Type {
		case "user", "assistant":
			if len(outstanding) > 0 || h.Text == nil || h.ID != "" || h.Name != "" || len(h.Calls) > 0 || h.IsError != nil || !utf8.ValidString(*h.Text) {
				return bad()
			}
			size += len(*h.Text)
		case "calls":
			if len(outstanding) > 0 || len(h.Calls) == 0 || len(h.Calls) > MaxToolCallsPerRound || h.Text != nil || h.ID != "" || h.Name != "" || h.IsError != nil {
				return bad()
			}
			for _, call := range h.Calls {
				value, err := decodeToolDocument([]byte(call.Arguments))
				if !validNativeCallID(call.ID) || used[call.ID] || !names[call.Name] || len(call.Arguments) > MaxToolArgumentsBytes || err != nil || value.kind != schemaObject {
					return bad()
				}
				used[call.ID], outstanding[call.ID] = true, call.Name
				size += len(call.ID) + len(call.Name) + len(call.Arguments)
			}
		case "output":
			if name, ok := outstanding[h.ID]; !ok || name != h.Name || h.Text == nil || len(h.Calls) > 0 || !utf8.ValidString(*h.Text) {
				return bad()
			}
			delete(outstanding, h.ID)
			size += len(h.ID) + len(h.Name) + len(*h.Text)
		default:
			return bad()
		}
	}
	if len(outstanding) > 0 {
		return bad()
	}
	if size > NativeMaxPromptBytes {
		return nativeError(KindContextCapacity, "native history and tools exceed 128 KiB; nothing was truncated")
	}
	return nil
}
