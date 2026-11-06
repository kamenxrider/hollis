// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package runner

// NativeClientFailureMessage exposes a fixed diagnostic, never SDK prose.
// Code 1042 is invalidClientIdentifier in the inspected ModelManager enum;
// it is not evidence of a transient resource failure.
func (e *Error) NativeClientFailureMessage() string {
	if e.Kind != KindNativeFailed || e.NativeFailure == nil || e.NativeFailure.Code == nil {
		return ""
	}
	info := e.NativeFailure
	if *info.Code == 1042 && (info.Domain == "ModelManagerServices.ModelManagerError" || info.Domain == "ModelManagerError") {
		return "the native local model rejected its client identifier (ModelManager error 1042); no automatic retry was made"
	}
	return ""
}
