package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPCCQuotaPreflightUsesNonGenerativeStatus(t *testing.T) {
	for _, status := range []string{"available", "approaching", "limit_reached", "unknown", "future_status"} {
		helper := fakeNative(t, "cat >/dev/null\n"+nativePrint(nativeLine("status", map[string]any{
			"available": false,
			"capabilities": map[string]any{"pcc_quota": map[string]any{
				"status": status, "reset_date": "2026-01-02T03:04:05Z",
			}},
		})))
		reading := ReadPCCQuota(t.Context(), helper)
		for _, model := range []Model{ModelCloud, ModelCloudPro, ModelAuto, ModelOnDevice, ModelChatGPT} {
			shortcut, directory := runnerWithFake(t, "echo")
			shortcut.quotaReader = func(ctx context.Context) PCCQuota { return ReadPCCQuota(ctx, helper) }
			_, _, err := shortcut.Run(t.Context(), model, "synthetic quota test")
			wantRefusal := status == "limit_reached" && (model == ModelCloud || model == ModelCloudPro || model == ModelAuto)
			if wantRefusal {
				if _, statErr := os.Stat(filepath.Join(directory, "count.txt")); !os.IsNotExist(statErr) {
					t.Fatal("quota refusal spawned a Shortcut")
				}
				var classified *Error
				if !errors.As(err, &classified) || classified.Kind != KindRateLimited || classified.ExitCode != -1 ||
					!strings.Contains(classified.PublicShortcutMessage(), "2026-01-02T03:04:05Z") {
					t.Fatalf("quota=%+v model=%s err=%v", reading, model, err)
				}
			} else if err != nil {
				t.Fatalf("uncertain/non-Cloud reading blocked model=%s status=%s: %v", model, status, err)
			}
		}
	}
}

func TestPCCQuotaFailureAndTimeoutAreUnknown(t *testing.T) {
	for _, script := range []string{"cat >/dev/null\nexit 1\n", "cat >/dev/null\nsleep 10\n", "cat >/dev/null\n" + nativeReady()} {
		helper := fakeNative(t, script)
		start := time.Now()
		quota := ReadPCCQuota(context.Background(), helper)
		if quota.Status != "unknown" || time.Since(start) > 3*time.Second {
			t.Fatalf("unbounded or blocking uncertainty: %+v duration=%s", quota, time.Since(start))
		}
	}
}

func TestPCCQuotaResetDateDoesNotEchoArbitraryText(t *testing.T) {
	quota := (PCCQuota{Status: "limit_reached", ResetDate: "PRIVATE-CANARY"}).normalized()
	if strings.Contains(quota.LimitMessage(), "PRIVATE") || quota.ResetDate != "" {
		t.Fatal("invalid date reached public text")
	}
}

func TestNativeBoundedStreamAcceptsOnlyNegotiatedUsage(t *testing.T) {
	limit := 12
	for _, advertised := range []bool{false, true} {
		status := nativePrint(nativeLine("status", map[string]any{
			"available": true, "capabilities": map[string]any{"stream_token_usage": advertised},
		}))
		helper := fakeNative(t, "cat >/dev/null\n"+status+
			nativePrint(nativeLine("snapshot", map[string]any{"text": "Answer"}))+nativeDone("Answer", true))
		result, err := helper.RunGeneration(t.Context(), ModelLocal, GenerationRequest{
			Prompt: "synthetic bounded stream", Options: &GenerationOptions{MaxTokens: &limit},
		}, func(string) error { return nil })
		if advertised {
			if err != nil || result.Usage == nil {
				t.Fatalf("negotiated usage missing: %+v %v", result, err)
			}
		} else {
			requireNativeKind(t, err, KindNativeProtocol)
		}
	}
}

func TestAppleCode600RetryAdviceDoesNotClaimQuota(t *testing.T) {
	r, _ := runnerWithFake(t, "empty")
	r.failureLog = func(context.Context, time.Time, time.Time) ShortcutFailureInfo {
		return ShortcutFailureInfo{Domain: "WFAskLLMActionErrorDomain", Code: 600, Keys: []string{"AppleIntelligenceRetryAfterDate"}}
	}
	e := &Error{Kind: KindShortcutFailed, ExitCode: 1}
	r.classifyFailure(t.Context(), e, time.Now(), time.Now(), "synthetic", "", "")
	if e.Kind != KindShortcutFailed || !strings.Contains(e.PublicShortcutMessage(), "Apple asked to retry") || strings.Contains(e.PublicShortcutMessage(), "rate-limit") {
		t.Fatalf("code 600: %v", e)
	}
}
