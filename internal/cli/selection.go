// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/kamenxrider/hollis/internal/batch"
	"github.com/kamenxrider/hollis/internal/imagegen"
	"github.com/kamenxrider/hollis/internal/runner"
)

// Result schemas describe possible fields, including absent optional fields.
// Add new command outputs here before exposing them to --select.
type fieldSchema struct {
	fields  map[string]*fieldSchema
	element *fieldSchema
}

func resultFields(names ...string) *fieldSchema {
	schema := &fieldSchema{fields: make(map[string]*fieldSchema, len(names))}
	for _, name := range names {
		schema.fields[name] = &fieldSchema{}
	}
	return schema
}

func resultArray(element *fieldSchema) *fieldSchema {
	return &fieldSchema{element: element}
}

// Type-based construction includes omitempty fields and handles recursive
// agent-context subcommands without inspecting a result or unrolling its depth.
func resultType(t reflect.Type) *fieldSchema {
	return resultTypeRec(t, make(map[reflect.Type]*fieldSchema))
}

func resultTypeRec(t reflect.Type, seen map[reflect.Type]*fieldSchema) *fieldSchema {
	if schema, ok := seen[t]; ok {
		return schema
	}
	schema := &fieldSchema{}
	seen[t] = schema
	switch t.Kind() {
	case reflect.Pointer:
		return resultTypeRec(t.Elem(), seen)
	case reflect.Slice, reflect.Array:
		schema.element = resultTypeRec(t.Elem(), seen)
	case reflect.Struct:
		schema.fields = make(map[string]*fieldSchema)
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			schema.fields[strings.ToLower(name)] = resultTypeRec(field.Type, seen)
		}
	}
	return schema
}

var (
	helpResultSchema     = resultFields("command", "use", "short", "long")
	versionResultSchema  = resultFields("name", "version")
	commandResultSchemas = buildResultSchemas()
)

func buildResultSchemas() map[string]*fieldSchema {
	usage := resultType(reflect.TypeFor[runner.Usage]())
	processing := resultType(reflect.TypeFor[imagegen.OutputOptions]())
	text := resultFields("model_requested", "model_used", "response", "fallback_reason")
	text.fields["usage"] = usage
	image := resultFields("path", "format", "bytes", "width", "height", "checksum",
		"native_width", "native_height", "reference_image_sent", "reference_sha256")
	image.fields["output_processing"] = processing
	chat := resultFields("conversation_id", "operation", "style", "visual_content_observed")
	for name, field := range text.fields {
		chat.fields[name] = field
	}
	for name, field := range image.fields {
		chat.fields[name] = field
	}
	local := resultFields("model", "backend", "apple_model", "available", "status",
		"inference_verified", "helper_protocol", "helper_version")
	local.fields["capabilities"] = resultFields("streaming", "complete_usage", "stream_usage", "images", "tools")
	models := resultFields("wfllm_model", "resolved_ref", "source", "verified")
	for name, field := range local.fields {
		models.fields[name] = field
	}
	doctor := resultFields("shortcuts_cli", "macos", "macos_build", "support", "resolution", "timeout_default", "version")
	doctor.fields["local"] = local
	doctor.fields["bridges"] = resultType(reflect.TypeFor[[]bridgeCheck]())
	doctor.fields["exit_codes"] = resultFields("success", "unexpected", "usage", "missing", "transport", "timeout", "config")
	// Selected-route readiness (doctor --model and setup) shares one plan shape.
	readiness := resultFields("schema_version", "source", "route", "status", "reason_code",
		"message", "artifact_verification", "inference_tested")
	readiness.fields["actions"] = resultArray(resultFields("code", "message"))
	readiness.fields["bridge"] = resultFields("discovery", "configured", "reference", "discovered")
	for name, field := range readiness.fields {
		doctor.fields[name] = field
	}
	config := resultFields("path", "default_model", "image_bridge")
	config.fields["bridges"] = resultFields("cloud", "cloud-pro", "on-device", "chatgpt")
	config.fields["image_bridges"] = resultFields(imageStyles...)
	search := resultFields("id", "title", "model", "updated_at")
	search.fields["hits"] = resultArray(resultFields("seq", "role", "snippet"))
	show := resultFields("id", "title", "model", "created_at", "updated_at")
	show.fields["messages"] = resultArray(resultFields("seq", "role", "content"))
	plan := resultFields("job_path", "schema_version", "model", "input_dir", "output_dir",
		"instruction_source", "instruction_sha256", "item_count", "skipped_count")
	plan.fields["items"] = resultType(reflect.TypeFor[[]batch.Item]())
	plan.fields["skipped"] = resultType(reflect.TypeFor[[]batch.Skipped]())
	invocation := resultFields("job_path", "attempted", "total_attempts", "pending", "succeeded",
		"failed", "uncertain", "complete", "budget_exhausted")
	context := resultType(reflect.TypeFor[agentContext]())
	context.fields["contracts"].fields["exit_codes"] = resultFields("success", "unexpected", "usage",
		"missing", "transport", "timeout", "config", "request_declined", "shortcut_failed")
	context.fields["commands"].element.fields["annotations"] = resultFields("mcp:read-only")
	return map[string]*fieldSchema{
		"hollis version":                 versionResultSchema,
		"hollis respond":                 text,
		"hollis chat":                    chat,
		"hollis chats list":              resultArray(resultFields("id", "title", "model", "messages", "created_at", "updated_at", "archived")),
		"hollis chats search":            resultArray(search),
		"hollis chats show":              show,
		"hollis chats rename":            resultFields("ok", "conversation_id", "title"),
		"hollis chats delete":            resultFields("ok", "conversation_id", "deleted"),
		"hollis config show":             config,
		"hollis config set":              resultFields("ok", "key", "value", "tier", "configured", "ref"),
		"hollis config set image-bridge": resultFields("ok", "style", "bridge", "protocol", "configured"),
		"hollis image styles":            resultArray(resultFields("style", "native_style", "bridge", "protocol", "configured", "runtime_verified")),
		"hollis image generate":          image,
		"hollis models":                  resultArray(models),
		"hollis doctor":                  doctor,
		"hollis setup":                   readiness,
		"hollis batch plan":              plan,
		"hollis batch run":               invocation,
		"hollis batch resume":            invocation,
		"hollis agent-context":           context,
	}
}

func selectionPaths(fields string, schema *fieldSchema) ([][]string, error) {
	if strings.TrimSpace(fields) == "" {
		return nil, nil
	}
	if schema == nil {
		return nil, usageErr(fmt.Errorf("--select is not supported for this command's output"))
	}
	var paths [][]string
	for _, field := range strings.Split(fields, ",") {
		field = strings.TrimSpace(field)
		path := strings.Split(strings.ToLower(field), ".")
		node := schema
		for _, part := range path {
			for node != nil && node.element != nil {
				node = node.element
			}
			if part == "" || node == nil || node.fields[part] == nil {
				return nil, usageErr(fmt.Errorf("invalid --select field %q: unknown or malformed field path", field))
			}
			node = node.fields[part]
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func projectOutput(data any, flags *rootFlags) (any, error) {
	paths, err := selectionPaths(flags.selectFields, flags.resultSchema)
	if err != nil || len(paths) == 0 {
		return data, err
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return projectValue(value, paths), nil
}

func projectValue(value any, paths [][]string) any {
	switch value := value.(type) {
	case []any:
		for i, item := range value {
			value[i] = projectValue(item, paths)
		}
		return value
	case map[string]any:
		out := make(map[string]any)
		for key, item := range value {
			var children [][]string
			whole := false
			for _, path := range paths {
				if strings.EqualFold(key, path[0]) {
					if len(path) == 1 {
						whole = true
					} else {
						children = append(children, path[1:])
					}
				}
			}
			if whole {
				out[key] = item
			} else if len(children) > 0 {
				out[key] = projectValue(item, children)
			}
		}
		return out
	default:
		return value
	}
}
