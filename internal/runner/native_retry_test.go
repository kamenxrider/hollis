package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNativeBusyDecisionRetry(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     Kind
		domain   string
		code     int
		captured bool
		twice    bool
		want     Kind
		attempts int
	}{
		{"transient then success", KindModelBusy, "ModelManagerError", 1012, false, false, "", 2},
		{"transient twice", KindModelBusy, "ModelManagerError", 1012, false, true, KindModelBusy, 2},
		{"language model then success", KindModelBusy, "FoundationModels.LanguageModelError", -1, false, false, "", 2},
		{"language model twice", KindModelBusy, "FoundationModels.LanguageModelError", -1, false, true, KindModelBusy, 2},
		{"language model other code", KindNativeFailed, "FoundationModels.LanguageModelError", -2, false, false, KindNativeFailed, 1},
		{"invalid client identifier", KindNativeFailed, "ModelManagerServices.ModelManagerError", 1042, false, false, KindNativeFailed, 1},
		{"invalid client mislabeled busy", KindModelBusy, "ModelManagerServices.ModelManagerError", 1042, false, false, KindModelBusy, 1},
		{"unallowlisted busy code", KindModelBusy, "FoundationModels.LanguageModelError", -2, false, false, KindModelBusy, 1},
		{"language model other domain", KindNativeFailed, "Other.LanguageModelError", -1, false, false, KindNativeFailed, 1},
		{"guardrail", KindRequestDeclined, "FoundationModels.LanguageModelError", -1, false, false, KindRequestDeclined, 1},
		{"context", KindContextCapacity, "FoundationModels.LanguageModelError", -1, false, false, KindContextCapacity, 1},
		{"decoding", KindNativeFailed, "FoundationModels.LanguageModelError", -1, false, false, KindNativeFailed, 1},
		{"captured callback", KindModelBusy, "ModelManagerError", 1012, true, false, KindModelBusy, 1},
		{"language model captured callback", KindModelBusy, "FoundationModels.LanguageModelError", -1, true, false, KindModelBusy, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			counter := filepath.Join(dir, "attempts")
			requests := filepath.Join(dir, "request")
			code, captured := tc.code, tc.captured
			info := NativeFailureInfo{SwiftType: "Foundation.NSError", Domain: tc.domain,
				Code: &code, CallbackCaptured: &captured}
			failure := nativePrint(nativeLine("error", map[string]any{
				"kind": tc.kind, "request_id": "test-request", "error_info": info,
			}))
			script := "if [ \"$#\" -eq 0 ]; then cat >/dev/null\n" +
				nativePrint(metadataStatus(true)) + "exit 0\nfi\n" +
				"n=0\nif [ -f '" + counter + "' ]; then read -r n < '" + counter + "'; fi\n" +
				"n=$((n+1))\nprintf '%s\\n' \"$n\" > '" + counter + "'\n" +
				"cat > '" + requests + "'.\"$n\"\n" + nativePrint(nativeRequestStatus(true))
			if tc.twice {
				script += failure
			} else {
				script += "if [ \"$n\" -eq 1 ]; then\n" + failure +
					"else\n" + nativeRequestDone("Done.") + "fi\n"
			}
			r := fakeNative(t, script)
			start := time.Now()
			result, err := r.RunToolRound(t.Context(), ModelLocal, metadataRound(t))
			if tc.want == "" {
				if err != nil || result.Text != "Done." || result.Usage != nil {
					t.Fatalf("completion=%+v err=%v", result, err)
				}
			} else {
				requireNativeKind(t, err, tc.want)
				if tc.want == KindModelBusy {
					failure := err.(*Error)
					if failure.NativeFailure == nil || *failure.NativeFailure.Code != tc.code ||
						failure.NativeFailure.Domain != tc.domain {
						t.Fatalf("classification lost: %+v", failure)
					}
				}
			}
			raw, readErr := os.ReadFile(counter)
			if readErr != nil || strings.TrimSpace(string(raw)) != strconv.Itoa(tc.attempts) {
				t.Fatalf("attempts=%q err=%v", raw, readErr)
			}
			if tc.attempts == 2 {
				if time.Since(start) < 500*time.Millisecond {
					t.Fatal("retry did not back off")
				}
				first, _ := os.ReadFile(requests + ".1")
				second, _ := os.ReadFile(requests + ".2")
				if string(first) != string(second) {
					t.Fatal("retry changed the decision input or request ID")
				}
			}
		})
	}
}

func TestNativeBusyAfterEmittedCallbackDoesNotRetry(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "attempts")
	call := NativeToolCall{ID: "captured", Name: "get_weather", Arguments: `{"city":"Tokyo"}`}
	request := metadataRound(t)
	// Use the offered name rather than assuming fixture naming.
	call.Name = request.Native.Tools[0].Name
	code, captured := -1, false
	info := NativeFailureInfo{SwiftType: "NSError", Domain: "FoundationModels.LanguageModelError",
		Code: &code, CallbackCaptured: &captured}
	script := "if [ \"$#\" -eq 0 ]; then cat >/dev/null\n" +
		nativePrint(metadataStatus(true)) + "exit 0\nfi\n" +
		"printf 'attempt\\n' >> '" + counter + "'\ncat >/dev/null\n" +
		nativePrint(nativeRequestStatus(true)) +
		nativePrint(nativeLine("tool_calls", map[string]any{
			"request_id": "test-request", "step": 1, "calls": []NativeToolCall{call},
		})) +
		nativePrint(nativeLine("error", map[string]any{
			"kind": KindModelBusy, "request_id": "test-request", "error_info": info,
		}))
	r := fakeNative(t, script)
	_, err := r.RunToolRound(t.Context(), ModelLocal, request)
	requireNativeKind(t, err, KindNativeProtocol)
	raw, _ := os.ReadFile(counter)
	if strings.Count(string(raw), "attempt") != 1 {
		t.Fatal("a captured batch caused a retry")
	}
}

func TestNativeTransientRetryDecisionBoundary(t *testing.T) {
	for _, mode := range []string{"prior result", "original deadline", "mixed transient errors"} {
		t.Run(mode, func(t *testing.T) {
			counter := filepath.Join(t.TempDir(), "attempts")
			code, captured := -1, false
			info := NativeFailureInfo{SwiftType: "NSError", Domain: "FoundationModels.LanguageModelError",
				Code: &code, CallbackCaptured: &captured}
			first := nativePrint(nativeLine("error", map[string]any{
				"kind": KindModelBusy, "request_id": "test-request", "error_info": info,
			}))
			second := nativeRequestDone("Done.")
			if mode == "mixed transient errors" {
				code = 1012
				info.Domain = "ModelManagerError"
				second = nativePrint(nativeLine("error", map[string]any{
					"kind": KindModelBusy, "request_id": "test-request", "error_info": info,
				}))
			}
			script := "if [ \"$#\" -eq 0 ]; then cat >/dev/null\n" +
				nativePrint(metadataStatus(true)) + "exit 0\nfi\n" +
				"printf 'attempt\\n' >> '" + counter + "'\ncat >/dev/null\n" +
				nativePrint(nativeRequestStatus(true)) +
				"if [ \"$(wc -l < '" + counter + "')\" -eq 1 ]; then\n" + first +
				"else\n" + second + "fi\n"
			r := fakeNative(t, script)
			if _, err := r.NativeStatus(t.Context()); err != nil {
				t.Fatal(err)
			}
			request := metadataRound(t)
			if mode == "prior result" {
				name, output := request.Native.Tools[0].Name, `{"temperature":21}`
				request.Native.Step, request.Native.RemainingSteps = 2, NativeToolRoundCap-1
				request.Native.History = append(request.Native.History,
					NativeToolHistory{Type: "calls", Calls: []NativeToolCall{
						{ID: "completed", Name: name, Arguments: `{"city":"Tokyo"}`},
					}},
					NativeToolHistory{Type: "output", ID: "completed", Name: name, Text: &output})
			}
			ctx, want, attempts := t.Context(), Kind(""), 2
			if mode == "original deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 250*time.Millisecond)
				defer cancel()
				want, attempts = KindTimeout, 1
			} else if mode == "mixed transient errors" {
				want = KindModelBusy
			}
			out, err := r.RunToolRound(ctx, ModelLocal, request)
			if want != "" {
				requireNativeKind(t, err, want)
			} else if err != nil || out.Text != "Done." {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			raw, _ := os.ReadFile(counter)
			if strings.Count(string(raw), "attempt") != attempts {
				t.Fatalf("attempts=%q want=%d", raw, attempts)
			}
		})
	}
}

func TestNativeErrorClassificationRejectsContent(t *testing.T) {
	info := map[string]any{"swift_type": "NSError", "domain": "PRIVATE prompt",
		"code": 1012, "callback_captured": false}
	encoded, _ := json.Marshal(info)
	r := fakeNative(t, "cat >/dev/null\n"+nativeReady()+
		nativePrint(nativeLine("error", map[string]any{"kind": KindModelBusy, "error_info": json.RawMessage(encoded)})))
	_, err := r.RunComplete(t.Context(), ModelLocal, "hello")
	requireNativeKind(t, err, KindNativeProtocol)
	if strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("classification leaked content")
	}
}
