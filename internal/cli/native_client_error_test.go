// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package cli

import (
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestNativeClientErrorCLI(t *testing.T) {
	code := 1042
	err := toCLIError(&runner.Error{Kind: runner.KindNativeFailed,
		NativeFailure: &runner.NativeFailureInfo{Domain: "ModelManagerServices.ModelManagerError", Code: &code}})
	if ExitCode(err) != 5 || errorCode(err) != "native_failed" || !strings.Contains(err.Error(), "client identifier") {
		t.Fatal(err)
	}
}
