// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// minimalPDF writes a PDF with one text-layer line per page. Offsets in the
// cross-reference table are computed, so the file is well formed rather than
// relying on a reader's repair path.
func minimalPDF(lines []string) []byte {
	var body strings.Builder
	offsets := []int{}
	object := func(content string) {
		offsets = append(offsets, body.Len())
		fmt.Fprintf(&body, "%d 0 obj\n%s\nendobj\n", len(offsets), content)
	}
	body.WriteString("%PDF-1.4\n")
	pages := len(lines)
	kids := make([]string, pages)
	for i := range lines {
		kids[i] = fmt.Sprintf("%d 0 R", 4+2*i)
	}
	object("<< /Type /Catalog /Pages 2 0 R >>")
	object(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), pages))
	object("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	for i, line := range lines {
		object(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*i))
		stream := fmt.Sprintf("BT /F1 24 Tf 72 700 Td (%s) Tj ET", line)
		object(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}
	xref := body.Len()
	fmt.Fprintf(&body, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&body, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&body, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return []byte(body.String())
}

// The native golden suite supplies its freshly built helper. Extraction calls
// PDFKit only; the helper emits status and never opens a model session.
func TestBuiltHelperExtractsThroughTheRunner(t *testing.T) {
	path := os.Getenv("HOLLIS_TEST_NATIVE_HELPER")
	if path == "" {
		t.Skip("run by the built-helper golden suite")
	}
	r := &NativeRunner{helperPath: path, platformCheck: func(context.Context) error { return nil }}
	document := minimalPDF([]string{"First page line", "Second page line", "Third page line"})
	result, err := r.ExtractPDF(t.Context(), document, []int{1, 3})
	if err != nil {
		t.Fatal(err)
	}
	if result.PageCount != 3 || len(result.Pages) != 2 || result.Pages[0].Page != 1 || result.Pages[1].Page != 3 {
		t.Fatalf("result %#v", result)
	}
	for i, want := range []string{"First page line", "Third page line"} {
		if result.Pages[i].Method != "text" || !strings.Contains(result.Pages[i].Text, want) {
			t.Fatalf("page %d = %#v; want text-layer %q", result.Pages[i].Page, result.Pages[i], want)
		}
	}
	_, err = r.ExtractPDF(t.Context(), document, []int{2, 4})
	requireNativeKind(t, err, KindPageOutOfRange)
	if failure := err.(*Error); failure.Page != 4 || failure.PageCount != 3 {
		t.Fatalf("failure %#v", failure)
	}
	_, err = r.ExtractPDF(t.Context(), []byte("%PDF-1.4\nnot a document\n"), nil)
	requireNativeKind(t, err, KindInvalidDocument)
}
