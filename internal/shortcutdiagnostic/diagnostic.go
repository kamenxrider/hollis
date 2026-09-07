// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

// Package shortcutdiagnostic recognizes measured failed-process diagnostics.
package shortcutdiagnostic

import "strings"

const DeclinedMessage = "Apple asked for a different description; the underlying reason was not specified."
const FailedMessage = "The Shortcut execution failed for an unrecognized reason. No automatic retry was made."

// RequestDeclined matches the complete diagnostic recorded from image generation.
// Call only for an unsuccessful, normally exited process, never for model output.
// Additional text, embedded quotations and unobserved refusal wording stay unknown.
func RequestDeclined(stderr string) bool {
	return strings.TrimSpace(stderr) == "Error: Try describing something different to create an image."
}
