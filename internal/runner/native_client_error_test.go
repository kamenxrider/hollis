// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import "testing"

func TestNativeClientFailureMessage(t *testing.T) {
	for _, domain := range []string{"ModelManagerServices.ModelManagerError", "ModelManagerError", "Other.Error"} {
		code := 1042
		failure := &Error{Kind: KindNativeFailed, NativeFailure: &NativeFailureInfo{Domain: domain, Code: &code}}
		if got := failure.NativeClientFailureMessage(); (got != "") != (domain != "Other.Error") {
			t.Fatalf("domain=%s message=%q", domain, got)
		}
		code = 1012
		if got := failure.NativeClientFailureMessage(); got != "" {
			t.Fatal("misclassified resource failure")
		}
	}
}
