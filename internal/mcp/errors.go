// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package mcp

import (
	"errors"
	"fmt"
)

// Code is a stable machine code. The CLI maps each code to an exit status;
// the strings are part of the public error contract.
type Code string

const (
	CodeConfig            Code = "mcp_config"
	CodeUnavailable       Code = "mcp_unavailable"
	CodeProtocol          Code = "mcp_protocol"
	CodeAuth              Code = "mcp_auth"
	CodeSchemaUnvalidated Code = "mcp_schema_unvalidated"
	CodeApprovalRequired  Code = "approval_required"
	CodeApprovalArgsLarge Code = "approval_args_too_large"
	CodeDenied            Code = "tool_denied"
	CodeNotAllowlisted    Code = "tool_not_allowlisted"
	CodeDefinitionChanged Code = "tool_definition_changed"
	CodeResultTooLarge    Code = "tool_result_too_large"
	CodeUnknownTool       Code = "unknown_tool"
	CodeInvalidArguments  Code = "tool_call_invalid"
	CodeNameConflict      Code = "tool_name_conflict"
	CodeTimeout           Code = "timeout"
)

// Error is every failure this package reports. Message is written for a
// person and never contains tool arguments, results, server stderr or a
// credential value.
type Error struct {
	Code    Code
	Server  string
	Tool    string
	Message string
	// Err is the underlying cause, kept for errors.Is and never printed: SDK
	// and transport messages can quote server-controlled text.
	Err error
}

func (e *Error) Error() string {
	prefix := string(e.Code)
	switch {
	case e.Server != "" && e.Tool != "":
		prefix += fmt.Sprintf(": server %q tool %q", e.Server, e.Tool)
	case e.Server != "":
		prefix += fmt.Sprintf(": server %q", e.Server)
	}
	if e.Message == "" {
		return prefix
	}
	return prefix + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// CodeOf reports the machine code of err, or "" when err did not come from
// this package.
func CodeOf(err error) Code {
	var mcpErr *Error
	if errors.As(err, &mcpErr) {
		return mcpErr.Code
	}
	return ""
}

func newError(code Code, server, tool, format string, args ...any) *Error {
	return &Error{Code: code, Server: server, Tool: tool, Message: fmt.Sprintf(format, args...)}
}
