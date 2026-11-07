// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package runner

// Retryable identifies transient failures, not permission to replay a request.
// Callers must additionally enforce route, output and tool-dispatch barriers.
// Rate limits, refusals, ambiguous timeouts and invalid output are never transient.
func (c ErrorContract) Retryable() bool {
	return c.Kind == KindModelBusy
}
