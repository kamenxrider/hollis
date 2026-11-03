// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNativeStatusDistinguishesIdentityAndMalformedProtocol(t *testing.T) {
	for _, tc := range []struct {
		name, script, reason string
		protocol             int
	}{
		{"old protocol", "exit 99\n", "helper_mismatch", NativeProtocol - 1},
		{"wrong version", strings.Replace(nativeReady(), NativeVersion, "0.0.0", 1), "helper_mismatch", NativeProtocol},
		{"invalid JSON", "echo broken\n", "", NativeProtocol},
		{"null event", "echo 'null'\n", "", NativeProtocol},
		{"empty event", "echo '{}'\n", "", NativeProtocol},
		{"missing protocol", `echo '{"version":"` + NativeVersion + `","model":"local","event":"status","available":true}'` + "\n", "", NativeProtocol},
		{"null protocol", `echo '{"protocol":null,"version":"` + NativeVersion + `","model":"local","event":"status","available":true}'` + "\n", "", NativeProtocol},
		{"missing version", `echo '{"protocol":3,"model":"local","event":"status","available":true}'` + "\n", "", NativeProtocol},
		{"null version", `echo '{"protocol":3,"version":null,"model":"local","event":"status","available":true}'` + "\n", "", NativeProtocol},
		{"missing model", `echo '{"protocol":3,"version":"` + NativeVersion + `","event":"status","available":true}'` + "\n", "", NativeProtocol},
		{"null model", `echo '{"protocol":3,"version":"` + NativeVersion + `","model":null,"event":"status","available":true}'` + "\n", "", NativeProtocol},
		{"early exit", "exit 0\n", "", NativeProtocol},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fakeNativeAt(t, tc.protocol, "cat >/dev/null\n"+tc.script)
			_, err := r.NativeStatus(t.Context())
			var failure *Error
			if !errors.As(err, &failure) || failure.Kind != KindNativeProtocol || failure.NativeReason != tc.reason {
				t.Fatalf("diagnostic=%#v err=%v", failure, err)
			}
		})
	}
}

func TestNativeStatusPreservesPlatformFailureBeforeHelperLookup(t *testing.T) {
	for _, reason := range []string{"platform_unsupported", "platform_unknown"} {
		r := &NativeRunner{helperPath: "/nonexistent/hollis-native",
			platformCheck: func(context.Context) error {
				return nativeDiagnosticError(KindLocalUnavailable, reason, "platform check refused")
			}}
		_, err := r.NativeStatus(t.Context())
		var failure *Error
		if !errors.As(err, &failure) || failure.NativeReason != reason {
			t.Fatalf("platform failure lost: %v", err)
		}
	}
}
