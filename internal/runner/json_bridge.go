// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"context"
	"fmt"
)

// BridgeOutputKind is an explicit transport capability, independent of names.
type BridgeOutputKind string

const (
	BridgeOutputText BridgeOutputKind = "public.plain-text"
	BridgeOutputJSON BridgeOutputKind = "public.json"
)

// BridgeSpec describes a separately installed output variant of a route.
type BridgeSpec struct {
	Names  []string
	Output BridgeOutputKind
}

type jsonOutputKey struct{}

// WithJSONObject selects the Dictionary bridge when positively discovered.
// Missing JSON bridges retain the text transport; validation belongs to callers.
// Context selection keeps concurrent HTTP text and JSON requests independent.
func WithJSONObject(ctx context.Context) context.Context {
	return context.WithValue(ctx, jsonOutputKey{}, true)
}

// JSONBridgeHint explains the non-inference fallback to an existing text bridge.
func JSONBridgeHint(r Runner, model Model) string {
	transport := jsonBridgeTransport(r)
	if transport == nil || !transport.JSONDiscoveryKnown || transport.JSONBridgeRefs[model] != "" {
		return ""
	}
	return MissingJSONBridgeHint(model)
}

// HasJSONBridge reports only a positively discovered Dictionary bridge.
func HasJSONBridge(r Runner, model Model) bool {
	transport := jsonBridgeTransport(r)
	return transport != nil && transport.JSONBridgeRefs[model] != ""
}

func jsonBridgeTransport(r Runner) *ShortcutRunner {
	var transport *ShortcutRunner
	if direct, ok := r.(*ShortcutRunner); ok {
		transport = direct
	} else if routed, ok := r.(interface{ ShortcutTransport() *ShortcutRunner }); ok {
		transport = routed.ShortcutTransport()
	}
	return transport
}

// MissingJSONBridgeHint is shared by request output and non-fatal doctor warnings.
func MissingJSONBridgeHint(model Model) string {
	return fmt.Sprintf("JSON bridge missing; using the text bridge. Run hollis setup --import %s for reliable JSON.", model)
}
