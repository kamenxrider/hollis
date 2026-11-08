// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import "github.com/kamenxrider/hollis/internal/jsonoutput"

// ErrorContract holds the stable public classification of a runner failure.
// CLI and HTTP codes differ intentionally; HTTP messages never expose helper
// diagnostics. Empty HTTPMessage means the caller supplies a bounded message.
type ErrorContract struct {
	Kind        Kind
	ExitCode    int
	MachineCode string
	HTTPStatus  int
	HTTPType    string
	HTTPCode    string
	HTTPMessage string
	Documented  bool
}

// Entries without a Kind document command-layer semantic codes, not runner
// failures. Keeping those here lets help, selection and agent-context derive
// their existing code lists without changing their public surface.
var errorContracts = []ErrorContract{
	{KindInvalidJSONOutput, 5, "invalid_json_output", 502, "server_error", "invalid_json_output", jsonoutput.InvalidMessage, true},
	{KindRateLimited, 5, "rate_limited", 429, "rate_limit_error", "rate_limited", "the native local model is rate limited", true},
	{KindContentRefused, 5, "content_refused", 502, "server_error", "native_failed", "the native local model request failed", true},
	{KindAccessRequired, 5, "access_required", 502, "server_error", "native_failed", "the native local model request failed", true},
	{KindRequestDeclined, 5, "request_declined", 502, "server_error", "request_declined", "the native local model declined the request", true},
	{KindShortcutFailed, 5, "shortcut_failed", 502, "server_error", "native_failed", "the native local model request failed", true},
	{KindNativeProtocol, 5, "native_protocol", 502, "server_error", "native_protocol", "the native local helper returned an invalid or incomplete response", true},
	{KindUnsupportedCapability, 5, "unsupported_capability", 502, "server_error", "unsupported_capability", "", true},
	{KindLocalUnavailable, 5, "local_unavailable", 503, "server_error", "model_unavailable", "the native local model or its matching helper is unavailable", true},
	{KindModelBusy, 5, "model_busy", 503, "server_error", "model_busy", "the native local model is temporarily busy", true},
	{KindNativeFailed, 5, "native_failed", 502, "server_error", "native_failed", "the native local model request failed", true},
	{KindToolCallInvalid, 5, "tool_call_invalid", 502, "server_error", "tool_call_invalid", "native tool call arguments or identity are invalid; nothing was dispatched", true},
	{KindToolRoundCap, 5, "tool_round_cap", 502, "server_error", "tool_round_cap", "tool loop reached its model decision limit", true},
	{KindContextCapacity, 5, "context_capacity", 413, "invalid_request_error", "context_too_large", "the full conversation exceeds the local model's capacity; no messages were shortened or dropped", true},
	{KindInvalidDocument, 5, "invalid_document", 502, "server_error", "native_failed", "the native local model request failed", true},
	{KindExtractNoText, 5, "extract_no_text", 502, "server_error", "native_failed", "the native local model request failed", true},
	{KindUnsupportedParameter, 2, "unsupported_parameter", 502, "server_error", "native_failed", "the native local model request failed", true},
	{KindUnsupportedMedia, 2, "unsupported_media", 400, "invalid_request_error", "unsupported_media", "native image input supports PNG and JPEG only", true},
	{KindExtractMediaMismatch, 2, "extract_media_mismatch", 502, "server_error", "native_failed", "the native local model request failed", true},
	{KindPageOutOfRange, 2, "page_out_of_range", 502, "server_error", "native_failed", "the native local model request failed", true},
	{KindInvalidImage, 5, "invalid_image", 502, "server_error", "invalid_image", "the native helper could not decode the image", true},
	{KindUnsupportedOS, 5, "unsupported_os", 502, "server_error", "unsupported_os", "native image input requires macOS 27 or later", true},
	{KindEmptyPrompt, 2, "usage", 502, "server_error", "native_failed", "the native local model request failed", false},
	{KindNoOutput, 5, "transport", 502, "server_error", "native_failed", "the native local model request failed", false},
	{KindShortcutMissing, 3, "missing", 502, "server_error", "native_failed", "the native local model request failed", false},
	{KindUsage, 2, "usage", 400, "invalid_request_error", "invalid_input", "invalid Local request", false},
	{KindSIGABRT, 5, "transport", 502, "server_error", "native_failed", "the native local model request failed", false},
	{KindSignal, 5, "transport", 502, "server_error", "native_failed", "the native local model request failed", false},
	{KindTimeout, 7, "timeout", 504, "server_error", "native_timeout", "the native local model request was canceled or exceeded its deadline", false},
	{KindContextCanceled, 5, "transport", 504, "server_error", "native_timeout", "the native local model request was canceled or exceeded its deadline", false},
	{KindListFailure, 5, "transport", 502, "server_error", "native_failed", "the native local model request failed", false},
	{KindTransport, 5, "transport", 502, "server_error", "transport", "the native local transport could not complete the request", false},
	{MachineCode: "mcp_config", ExitCode: 10, Documented: true},
	{MachineCode: "mcp_unavailable", ExitCode: 5, Documented: true},
	{MachineCode: "mcp_protocol", ExitCode: 5, Documented: true},
	{MachineCode: "mcp_auth", ExitCode: 5, Documented: true},
	{MachineCode: "mcp_schema_unvalidated", ExitCode: 5, Documented: true},
	{MachineCode: "approval_required", ExitCode: 5, Documented: true},
	{MachineCode: "approval_args_too_large", ExitCode: 5, Documented: true},
	{MachineCode: "tool_denied", ExitCode: 5, Documented: true},
	{MachineCode: "tool_not_allowlisted", ExitCode: 5, Documented: true},
	{MachineCode: "tool_definition_changed", ExitCode: 5, Documented: true},
	{MachineCode: "tool_result_too_large", ExitCode: 5, Documented: true},
	{MachineCode: "tool_result_too_large_for_local", ExitCode: 5, Documented: true},
	{MachineCode: "tool_name_conflict", ExitCode: 2, Documented: true},
	{MachineCode: "tool_count_limit", ExitCode: 2, Documented: true},
}

// ErrorContractFor preserves the generic transport/native-failure fallback for
// unrecognized kinds. The returned value is a copy, never mutable table state.
func ErrorContractFor(kind Kind) ErrorContract {
	for _, entry := range errorContracts {
		if entry.Kind != "" && entry.Kind == kind {
			return entry
		}
	}
	return ErrorContract{ExitCode: 5, MachineCode: "transport", HTTPStatus: 502,
		HTTPType: "server_error", HTTPCode: "native_failed", HTTPMessage: "the native local model request failed"}
}

// SemanticCode describes one existing documented command-layer machine code.
type SemanticCode struct {
	Name string
	Code int
}

func DocumentedSemanticCodes() []SemanticCode {
	var codes []SemanticCode
	for _, entry := range errorContracts {
		if entry.Documented {
			codes = append(codes, SemanticCode{Name: entry.MachineCode, Code: entry.ExitCode})
		}
	}
	return codes
}
