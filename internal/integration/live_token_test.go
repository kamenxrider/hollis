// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package integration_test

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func newTestBearerToken(t testing.TB) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate live bearer token: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func TestTestBearerTokensAreFreshAndStrong(t *testing.T) {
	first := newTestBearerToken(t)
	second := newTestBearerToken(t)
	if first == second {
		t.Fatal("two generated bearer tokens matched")
	}
	for _, token := range []string{first, second} {
		raw, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil || len(raw) != 32 {
			t.Fatalf("token has %d decoded bytes, err=%v; want 32", len(raw), err)
		}
	}
}
