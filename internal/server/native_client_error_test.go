// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package server

import (
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestNativeClientErrorHTTP(t *testing.T) {
	code := 1042
	status, detail := nativeError(&runner.Error{Kind: runner.KindNativeFailed,
		NativeFailure: &runner.NativeFailureInfo{Domain: "ModelManagerServices.ModelManagerError", Code: &code}})
	if status != 502 || detail["code"] != "native_failed" || !strings.Contains(detail["message"].(string), "client identifier") {
		t.Fatal(status, detail)
	}
}
