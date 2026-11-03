// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pdfFixture = "%PDF-1.7\nsynthetic bytes for transport tests\n"

// extractCapabilities is the advertisement the shipped helper sends.
func extractCapabilities() map[string]any {
	return map[string]any{"extract": map[string]any{"pdf": true, "ocr": true, "pages": true, "max_pages": 2000, "max_text_bytes": 131072}}
}

func extractReady() string {
	return nativePrint(nativeLine("status", map[string]any{"available": true, "capabilities": extractCapabilities()}))
}

func extractDone(pageCount int, pages ...map[string]any) string {
	total := 0
	for _, page := range pages {
		total += len(page["text"].(string))
	}
	return nativePrint(nativeLine("complete", map[string]any{"media": "pdf", "page_count": pageCount, "pages": pages, "text_bytes": total}))
}

func page(number int, text, method string) map[string]any {
	return map[string]any{"page": number, "text": text, "method": method}
}

// captureExtract writes the request and a copy of the staged file the helper
// was pointed at, then runs script.
func captureExtract(t *testing.T, script string) (*NativeRunner, string, string) {
	t.Helper()
	dir := t.TempDir()
	request, staged := filepath.Join(dir, "request"), filepath.Join(dir, "staged")
	capture := "cat > '" + request + "'\npath=$(sed -n 's/.*\"path\":\"\\([^\"]*\\)\".*/\\1/p' '" + request + "')\n" +
		"cp \"$path\" '" + staged + "'\nstat -f '%Lp' \"$path\" >> '" + staged + ".mode'\nstat -f '%Lp' \"$(dirname \"$path\")\" >> '" + staged + ".mode'\necho \"$path\" > '" + staged + ".path'\n"
	return fakeNative(t, capture+script), request, staged
}

func TestNativeExtractRequestStagesAPrivateCopy(t *testing.T) {
	r, requestFile, staged := captureExtract(t, extractReady()+extractDone(4, page(2, "two", "text"), page(4, "four\n", "ocr")))
	result, err := r.ExtractPDF(t.Context(), []byte(pdfFixture), []int{2, 4})
	if err != nil {
		t.Fatal(err)
	}
	if result.PageCount != 4 || result.TextBytes != 8 || len(result.Pages) != 2 ||
		result.Pages[1] != (ExtractedPage{Page: 4, Text: "four\n", Method: "ocr"}) {
		t.Fatalf("result %#v", result)
	}
	raw, err := os.ReadFile(requestFile)
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	if request["operation"] != "extract" || request["media"] != "pdf" || request["prompt"] != nil ||
		request["schema"] != nil || request["tool_calling"] != nil {
		t.Fatalf("request %s", raw)
	}
	if pages, _ := json.Marshal(request["pages"]); string(pages) != "[2,4]" {
		t.Fatalf("pages %s", pages)
	}
	if copy, err := os.ReadFile(staged); err != nil || string(copy) != pdfFixture {
		t.Fatalf("staged copy %q err=%v", copy, err)
	}
	if modes, _ := os.ReadFile(staged + ".mode"); string(modes) != "600\n700\n" {
		t.Fatalf("staged file and directory modes %q; want 600 and 700", modes)
	}
	path, _ := os.ReadFile(staged + ".path")
	if _, err := os.Stat(strings.TrimSpace(string(path))); !os.IsNotExist(err) {
		t.Fatalf("staged document survived the request: %v", err)
	}
}

func TestNativeExtractAllPagesSendsNoSelection(t *testing.T) {
	r, requestFile, _ := captureExtract(t, extractReady()+extractDone(2, page(1, "a", "text"), page(2, "", "ocr")))
	result, err := r.ExtractPDF(t.Context(), []byte(pdfFixture), nil)
	if err != nil || len(result.Pages) != 2 {
		t.Fatalf("result %#v err=%v", result, err)
	}
	raw, _ := os.ReadFile(requestFile)
	if strings.Contains(string(raw), `"pages"`) {
		t.Fatalf("request %s carries a selection", raw)
	}
}

// Extraction makes no model call, so a model that is unavailable must not
// stop it; the generation that follows reports availability itself.
func TestNativeExtractDoesNotNeedTheModel(t *testing.T) {
	status := nativePrint(nativeLine("status", map[string]any{"available": false, "reason": "not ready", "capabilities": extractCapabilities()}))
	r := fakeNative(t, "cat >/dev/null\n"+status+extractDone(1, page(1, "text", "text")))
	if _, err := r.ExtractPDF(t.Context(), []byte(pdfFixture), nil); err != nil {
		t.Fatal(err)
	}
}

func TestNativeExtractRejectsCompletionsThatDisagreeWithTheRequest(t *testing.T) {
	const canary = "EXTRACTED_TEXT_CANARY"
	line := func(fields map[string]any) string { return nativePrint(nativeLine("complete", fields)) }
	cases := []struct {
		name   string
		pages  []int
		script string
	}{
		{"page outside the selection", []int{2}, extractDone(3, page(1, canary, "text"))},
		{"page count disagrees with an all-pages result", nil, extractDone(3, page(1, canary, "text"))},
		{"pages out of order", nil, extractDone(2, page(2, canary, "text"), page(1, "b", "text"))},
		{"text bytes disagree", nil, line(map[string]any{"media": "pdf", "page_count": 1, "pages": []any{page(1, canary, "text")}, "text_bytes": 3})},
		{"empty text layer claimed", nil, extractDone(2, page(1, canary, "text"), page(2, " ", "text"))},
		{"no text at all", nil, extractDone(1, page(1, "  ", "ocr"))},
		{"unknown method", nil, extractDone(1, page(1, canary, "guess"))},
		{"generation text on an extraction", nil, line(map[string]any{"text": canary, "media": "pdf", "page_count": 1, "pages": []any{page(1, canary, "text")}, "text_bytes": len(canary)})},
		{"usage on an extraction", nil, line(map[string]any{"usage": nativeUsageEvent(), "media": "pdf", "page_count": 1, "pages": []any{page(1, canary, "text")}, "text_bytes": len(canary)})},
		{"media other than pdf", nil, line(map[string]any{"media": "rtf", "page_count": 1, "pages": []any{page(1, canary, "text")}, "text_bytes": len(canary)})},
		{"over the text ceiling", nil, extractDone(1, page(1, strings.Repeat("x", ExtractMaxTextBytes+1), "text"))},
		{"page count above the ceiling", nil, extractDone(2001, page(1, canary, "text"))},
		{"no status first", nil, extractDone(1, page(1, canary, "text"))},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ready := extractReady()
			if tt.name == "no status first" {
				ready = ""
			}
			r := fakeNative(t, "cat >/dev/null\n"+ready+tt.script)
			result, err := r.ExtractPDF(t.Context(), []byte(pdfFixture), tt.pages)
			requireNativeKind(t, err, KindNativeProtocol)
			if strings.Contains(err.Error(), canary) || len(result.Pages) != 0 {
				t.Fatalf("result %#v err=%v; want no text and no text in the message", result, err)
			}
		})
	}
}

func TestNativeExtractFailuresCarryMeasuredNumbers(t *testing.T) {
	failure := func(fields map[string]any) string { return nativePrint(nativeLine("error", fields)) }
	cases := []struct {
		name  string
		event map[string]any
		kind  Kind
		want  string
		check func(*Error) bool
	}{
		{"unreadable", map[string]any{"kind": "invalid_document"}, KindInvalidDocument, "could not be read", func(e *Error) bool { return e.Limit == 0 }},
		{"page ceiling", map[string]any{"kind": "invalid_document", "limit": 2000, "actual": 3000}, KindInvalidDocument, "document has 3000 pages; the limit is 2000",
			func(e *Error) bool { return e.Limit == 2000 && e.Actual == 3000 }},
		{"file ceiling", map[string]any{"kind": "invalid_document", "limit": 64 << 20, "actual": 64<<20 + 1}, KindInvalidDocument, "document is 67108865 bytes",
			func(e *Error) bool { return e.Actual == 64<<20+1 }},
		{"page out of range", map[string]any{"kind": "page_out_of_range", "page": 9, "page_count": 4}, KindPageOutOfRange, "page 9 is beyond the document's 4 pages",
			func(e *Error) bool { return e.Page == 9 && e.PageCount == 4 }},
		{"no text", map[string]any{"kind": "extract_no_text"}, KindExtractNoText, "selected pages", nil},
		{"text ceiling", map[string]any{"kind": "context_capacity", "limit": 131072, "actual": 131200}, KindContextCapacity, "reached 131200 bytes",
			func(e *Error) bool { return e.Limit == 131072 && e.Actual == 131200 }},
		{"unsupported media", map[string]any{"kind": "unsupported_media"}, KindUnsupportedMedia, "PDF is the only format", nil},
		{"media mismatch", map[string]any{"kind": "extract_media_mismatch", "media": "png"}, KindExtractMediaMismatch, "detected png content where Hollis detected pdf", nil},
		// Numbers that do not describe a crossed ceiling are not reported as one.
		{"limit without actual", map[string]any{"kind": "invalid_document", "limit": 2000}, KindNativeProtocol, "", nil},
		{"actual within limit", map[string]any{"kind": "context_capacity", "limit": 131072, "actual": 10}, KindNativeProtocol, "", nil},
		{"text ceiling without numbers", map[string]any{"kind": "context_capacity"}, KindNativeProtocol, "", nil},
		{"page inside the document", map[string]any{"kind": "page_out_of_range", "page": 2, "page_count": 4}, KindNativeProtocol, "", nil},
		{"page count on another kind", map[string]any{"kind": "extract_no_text", "page_count": 4}, KindNativeProtocol, "", nil},
		{"mismatch naming pdf", map[string]any{"kind": "extract_media_mismatch", "media": "pdf"}, KindNativeProtocol, "", nil},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := fakeNative(t, "cat >/dev/null\n"+extractReady()+failure(tt.event))
			_, err := r.ExtractPDF(t.Context(), []byte(pdfFixture), nil)
			requireNativeKind(t, err, tt.kind)
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v; want %q", err, tt.want)
			}
			var failure *Error
			_ = errors.As(err, &failure)
			if tt.check != nil && !tt.check(failure) {
				t.Fatalf("error fields %#v", failure)
			}
		})
	}
}

// Extraction members belong to extract alone: a generation that receives them
// is talking to a helper that does not match this client.
func TestNativeGenerationRefusesExtractionMembers(t *testing.T) {
	for name, line := range map[string]string{
		"pages on complete": nativePrint(nativeLine("complete", map[string]any{"text": "ok", "usage": nativeUsageEvent(), "page_count": 1})),
		"limit on error":    nativePrint(nativeLine("error", map[string]any{"kind": "context_capacity", "limit": 1, "actual": 2})),
		"extract kind":      nativePrint(nativeLine("error", map[string]any{"kind": "extract_no_text"})),
	} {
		t.Run(name, func(t *testing.T) {
			r := fakeNative(t, "cat >/dev/null\n"+nativeReady()+line)
			_, err := r.RunComplete(t.Context(), ModelLocal, "prompt")
			requireNativeKind(t, err, KindNativeProtocol)
		})
	}
}

func TestNativeExtractRefusesBadInputBeforeSpawn(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "spawned")
	r := fakeNative(t, ": > '"+marker+"'\n")
	tooMany := make([]int, ExtractMaxPages+1)
	for i := range tooMany {
		tooMany[i] = i + 1
	}
	for name, pages := range map[string][]int{
		"empty selection": {}, "zero": {0, 1}, "descending": {3, 2}, "duplicate": {2, 2}, "too many": tooMany,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := r.ExtractPDF(t.Context(), []byte(pdfFixture), pages)
			requireNativeKind(t, err, KindUsage)
		})
	}
	_, err := r.ExtractPDF(t.Context(), make([]byte, ExtractMaxFileBytes+1), nil)
	requireNativeKind(t, err, KindInvalidDocument)
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatal("helper spawned for a request refused before spawn")
	}
}

func TestNativeExtractRequiresTheAdvertisedCapability(t *testing.T) {
	plain := nativeReady()
	noPages := nativePrint(nativeLine("status", map[string]any{"available": true, "capabilities": map[string]any{
		"extract": map[string]any{"pdf": true, "ocr": true, "pages": false, "max_pages": 2000, "max_text_bytes": 131072}}}))
	for name, tt := range map[string]struct {
		status string
		pages  []int
	}{
		"no capabilities":         {plain, nil},
		"selection not supported": {noPages, []int{1}},
	} {
		t.Run(name, func(t *testing.T) {
			r := fakeNative(t, "cat >/dev/null\n"+tt.status+extractDone(1, page(1, "never", "text")))
			_, err := r.ExtractPDF(t.Context(), []byte(pdfFixture), tt.pages)
			requireNativeKind(t, err, KindUnsupportedCapability)
			if err.Error() != "native helper does not support extract; install the matching bundle" {
				t.Fatalf("error = %v", err)
			}
		})
	}
	r := fakeNative(t, "cat >/dev/null\n"+noPages+extractDone(1, page(1, "all pages", "text")))
	if _, err := r.ExtractPDF(t.Context(), []byte(pdfFixture), nil); err != nil {
		t.Fatalf("an all-pages request needs no selection support: %v", err)
	}
}
