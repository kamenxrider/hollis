// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import "testing"

func TestRetryContractOnlyTransientBusy(t *testing.T) {
	for _, contract := range errorContracts {
		if got, want := contract.Retryable(), contract.Kind == KindModelBusy; got != want {
			t.Errorf("%s retryable=%v, want %v", contract.Kind, got, want)
		}
	}
	if ErrorContractFor(Kind("unknown")).Retryable() {
		t.Fatal("unknown failures must not retry")
	}
}
