// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/runner"
)

const pdfBytes = "%PDF-1.7\nsynthetic transport fixture\n"

// pdfRecordingRunner extracts scripted pages and records what reaches the
// model, so a test can see the exact framed prompt and every extract call.
type pdfRecordingRunner struct {
	extraction runner.Extraction
	extractErr error
	runErr     error
	extracts   [][]int
	data       [][]byte
	calls      int
	prompt     string
	model      runner.Model
}

func (r *pdfRecordingRunner) ExtractPDF(_ context.Context, data []byte, pages []int) (runner.Extraction, error) {
	r.extracts = append(r.extracts, pages)
	r.data = append(r.data, data)
	return r.extraction, r.extractErr
}

func (r *pdfRecordingRunner) Run(_ context.Context, model runner.Model, prompt string) (string, runner.Model, error) {
	r.calls++
	r.prompt, r.model = prompt, model
	return "pdf fixture response", model, r.runErr
}

func (r *pdfRecordingRunner) RunComplete(ctx context.Context, model runner.Model, prompt string) (runner.Completion, error) {
	text, used, err := r.Run(ctx, model, prompt)
	return runner.Completion{Text: text, Model: used}, err
}

func twoPages() runner.Extraction {
	return runner.Extraction{Media: "pdf", PageCount: 9, TextBytes: 16, Pages: []runner.ExtractedPage{
		{Page: 2, Text: "Page two text.", Method: "text"},
		{Page: 7, Text: "", Method: "ocr"},
	}}
}

func runRespond(t *testing.T, r runner.Runner, args ...string) (string, string, error) {
	t.Helper()
	withNonInteractiveStdin(t)
	stubConfigPath(t)
	cmd := NewRootCmd(func() runner.Runner { return r })
	cmd.SetArgs(append([]string{"respond"}, args...))
	cmd.SetIn(&bytes.Buffer{})
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func machineCode(err error) string { return errorCode(err) }

func TestRespondPDFIsExtractedAndFramedByPage(t *testing.T) {
	path := writeRespondFile(t, "report.pdf", pdfBytes)
	r := &pdfRecordingRunner{extraction: twoPages()}
	if _, _, err := runRespond(t, r, "--model", "local", "--file", path, "--pages", "7", "--pages", "2", "Summarize"); err != nil {
		t.Fatal(err)
	}
	want := "Summarize\n\n--- Document \"report.pdf\" ---\n--- Page 2 ---\nPage two text.\n--- Page 7 ---\n\n\n--- End document ---\n"
	if r.calls != 1 || r.prompt != want {
		t.Fatalf("calls=%d prompt=%q\nwant %q", r.calls, r.prompt, want)
	}
	if !reflect.DeepEqual(r.extracts, [][]int{{2, 7}}) || string(r.data[0]) != pdfBytes {
		t.Fatalf("extract calls %v", r.extracts)
	}
}

// Content decides the route: a PDF with a text extension is still a PDF, and a
// text file named .pdf is still text.
func TestRespondFileRouteIsDecidedByContent(t *testing.T) {
	disguised := writeRespondFile(t, "notes.txt", pdfBytes)
	r := &pdfRecordingRunner{extraction: twoPages()}
	if _, _, err := runRespond(t, r, "--model", "local", "--file", disguised, "Summarize"); err != nil {
		t.Fatal(err)
	}
	if len(r.extracts) != 1 || r.extracts[0] != nil {
		t.Fatalf("extract calls %v; want one all-pages extraction", r.extracts)
	}

	for _, name := range []string{"notes.pdf", "source.rtf", "README", "notes.TXT"} {
		t.Run(name, func(t *testing.T) {
			path := writeRespondFile(t, name, "plain text\n")
			r := &pdfRecordingRunner{}
			_, _, err := runRespond(t, r, "--model", "local", "--file", path, "Summarize")
			if err != nil || len(r.extracts) != 0 || r.calls != 1 || !strings.Contains(r.prompt, "plain text\n") {
				t.Fatalf("err=%v extracts=%d calls=%d", err, len(r.extracts), r.calls)
			}
		})
	}
}

func TestRespondMixesTextAndPDFDocumentsInFlagOrder(t *testing.T) {
	notes := writeRespondFile(t, "notes.md", "alpha\n")
	pdf := writeRespondFile(t, "scan.pdf", pdfBytes)
	r := &pdfRecordingRunner{extraction: runner.Extraction{Media: "pdf", PageCount: 1, TextBytes: 4, Pages: []runner.ExtractedPage{{Page: 1, Text: "beta", Method: "ocr"}}}}
	if _, _, err := runRespond(t, r, "--model", "local", "--file", notes, "--file", pdf, "Compare"); err != nil {
		t.Fatal(err)
	}
	want := "Compare\n\n--- Document \"notes.md\" ---\nalpha\n\n--- End document ---\n--- Document \"scan.pdf\" ---\n--- Page 1 ---\nbeta\n\n--- End document ---\n"
	if r.prompt != want {
		t.Fatalf("prompt %q\nwant %q", r.prompt, want)
	}
}

func TestRespondRefusesNonDocumentContentBeforeAnyRun(t *testing.T) {
	cases := map[string]struct {
		content, want string
	}{
		"png":    {"\x89PNG\r\n\x1a\nrest", "pass images with --image"},
		"jpeg":   {"\xff\xd8\xff\xe0rest", "pass images with --image"},
		"binary": {"\x00\x01\x02 not text", "neither UTF-8 text nor a PDF"},
		"latin1": {"caf\xe9\n", "neither UTF-8 text nor a PDF"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeRespondFile(t, "input.md", tc.content)
			r := &pdfRecordingRunner{}
			_, _, err := runRespond(t, r, "--model", "local", "--file", path, "Explain")
			if ExitCode(err) != 2 || machineCode(err) != "unsupported_media" || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v exit=%d code=%s", err, ExitCode(err), machineCode(err))
			}
			if len(r.extracts) != 0 || r.calls != 0 {
				t.Fatal("refused content reached the runner")
			}
			if strings.Contains(err.Error(), filepath.Dir(path)) {
				t.Fatalf("error carries the caller's directory: %v", err)
			}
		})
	}
}

func TestRespondPagesParsing(t *testing.T) {
	for _, tc := range []struct {
		values []string
		want   []int
	}{
		{[]string{"3"}, []int{3}},
		{[]string{"3-7"}, []int{3, 4, 5, 6, 7}},
		{[]string{"1,4-6"}, []int{1, 4, 5, 6}},
		{[]string{"6", "1-2", "2,5-6"}, []int{1, 2, 5, 6}},
		{[]string{"1-2000"}, nil},
		{[]string{"5-5"}, []int{5}},
	} {
		got, err := parsePages(tc.values)
		if err != nil {
			t.Fatalf("%v: %v", tc.values, err)
		}
		if tc.want == nil {
			if len(got) != 2000 || got[0] != 1 || got[1999] != 2000 {
				t.Fatalf("%v: %d pages", tc.values, len(got))
			}
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%v = %v; want %v", tc.values, got, tc.want)
		}
	}
}

func TestRespondPagesRejectionsNameTheExactToken(t *testing.T) {
	path := writeRespondFile(t, "report.pdf", pdfBytes)
	for _, tc := range []struct {
		value, want string
	}{
		{"0", `"0": pages are numbered from 1`},
		{"-2", `"-2" is not a page number`},
		{"two", `"two" is not a page number`},
		{"7-3", `"7-3" is a reversed range`},
		{"1,,2", `"" is not a page number`},
		{"", `"" is not a page number`},
		{"3-", `"3-" is not a page number`},
		{" 3", `" 3" is not a page number`},
		{"0-4", `"0-4": pages are numbered from 1`},
		{"1-2001", "selects 2001 pages; the limit is 2000"},
		{"99999999999999999999", `"99999999999999999999": pages are numbered from 1`},
		{strings.TrimSuffix(strings.Repeat("1,", 2001), ","), "lists 2001 tokens; the limit is 2000"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			r := &pdfRecordingRunner{extraction: twoPages()}
			_, _, err := runRespond(t, r, "--model", "local", "--file", path, "--pages", tc.value, "Summarize")
			if ExitCode(err) != 2 || machineCode(err) != "unsupported_parameter" || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v exit=%d code=%s; want %q", err, ExitCode(err), machineCode(err), tc.want)
			}
			if len(r.extracts) != 0 || r.calls != 0 {
				t.Fatal("a rejected selection reached the runner")
			}
		})
	}
}

func TestRespondPagesNeedExactlyOnePDF(t *testing.T) {
	pdf := writeRespondFile(t, "report.pdf", pdfBytes)
	text := writeRespondFile(t, "notes.md", "alpha\n")
	image := writeCLIImage(t, "photo.png")
	for name, args := range map[string][]string{
		"no file":       {"--pages", "1", "Summarize"},
		"text document": {"--file", text, "--pages", "1", "Summarize"},
		"two files":     {"--file", pdf, "--file", pdf, "--pages", "1", "Summarize"},
		"image":         {"--image", image, "--pages", "1", "Describe"},
	} {
		t.Run(name, func(t *testing.T) {
			r := &pdfRecordingRunner{extraction: twoPages()}
			_, _, err := runRespond(t, r, append([]string{"--model", "local"}, args...)...)
			if ExitCode(err) != 2 || !strings.Contains(err.Error(), "--pages") {
				t.Fatalf("err=%v exit=%d", err, ExitCode(err))
			}
			if len(r.extracts) != 0 || r.calls != 0 {
				t.Fatal("a refused --pages request reached the runner")
			}
		})
	}
}

func TestRespondPageOutOfRangeIsACallerFaultWithTheRealPageCount(t *testing.T) {
	path := writeRespondFile(t, "report.pdf", pdfBytes)
	failure := &runner.Error{Kind: runner.KindPageOutOfRange, Page: 12, PageCount: 9, ExitCode: -1, Err: errors.New("page 12 is beyond the document's 9 pages")}
	r := &pdfRecordingRunner{extractErr: failure}
	_, _, err := runRespond(t, r, "--model", "local", "--file", path, "--pages", "2,12", "Summarize")
	if ExitCode(err) != 2 || machineCode(err) != "page_out_of_range" {
		t.Fatalf("err=%v exit=%d code=%s", err, ExitCode(err), machineCode(err))
	}
	const want = `--pages 2,12 asks for page 12, but document 1 ("report.pdf") has 9 pages (page_count 9)`
	if err.Error() != want {
		t.Fatalf("error %q\nwant  %q", err, want)
	}
	if r.calls != 0 {
		t.Fatal("generation ran after an out-of-range page")
	}
}

func TestRespondExtractionFailuresMapToTheTaxonomy(t *testing.T) {
	path := writeRespondFile(t, "report.pdf", pdfBytes)
	for _, tc := range []struct {
		kind  runner.Kind
		limit int
		exit  int
	}{
		{runner.KindInvalidDocument, 0, 5},
		{runner.KindInvalidDocument, 2000, 5},
		{runner.KindExtractNoText, 0, 5},
		{runner.KindContextCapacity, 131072, 5},
		{runner.KindUnsupportedMedia, 0, 2},
		{runner.KindExtractMediaMismatch, 0, 2},
		{runner.KindUnsupportedCapability, 0, 5},
		{runner.KindNativeProtocol, 0, 5},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			message := "measured message " + string(tc.kind)
			failure := &runner.Error{Kind: tc.kind, Limit: tc.limit, Actual: tc.limit + 1, ExitCode: -1, Err: errors.New(message)}
			if tc.kind == runner.KindUnsupportedCapability {
				failure.Capability = "extract"
			}
			r := &pdfRecordingRunner{extractErr: failure}
			_, _, err := runRespond(t, r, "--model", "local", "--file", path, "Summarize")
			if ExitCode(err) != tc.exit || machineCode(err) != string(tc.kind) {
				t.Fatalf("err=%v exit=%d code=%s", err, ExitCode(err), machineCode(err))
			}
			if tc.kind != runner.KindUnsupportedCapability && tc.kind != runner.KindNativeProtocol {
				if !strings.Contains(err.Error(), `document 1 ("report.pdf"): `+message) {
					t.Fatalf("error %q does not locate the document and keep the measured message", err)
				}
			}
			if r.calls != 0 {
				t.Fatal("generation ran after a failed extraction")
			}
		})
	}
}

func TestRespondFramedPDFOverThePromptLimitFailsWithBothNumbers(t *testing.T) {
	path := writeRespondFile(t, "report.pdf", pdfBytes)
	big := strings.Repeat("x", runner.ExtractMaxTextBytes-10)
	r := &pdfRecordingRunner{extraction: runner.Extraction{Media: "pdf", PageCount: 1, TextBytes: len(big), Pages: []runner.ExtractedPage{{Page: 1, Text: big, Method: "text"}}}}
	_, _, err := runRespond(t, r, "--model", "local", "--file", path, "Summarize")
	if ExitCode(err) != 2 || r.calls != 0 {
		t.Fatalf("err=%v exit=%d calls=%d", err, ExitCode(err), r.calls)
	}
	size := len("Summarize") + 2 + len(`--- Document "report.pdf" ---`) + 1 + len("--- Page 1 ---\n") + len(big) + 1 + 1 + len("--- End document ---") + 1
	if want := "prepared prompt is " + itoa(size) + " bytes; maximum is " + itoa(chat.MaxRenderedPromptBytes); !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q; want %q", err, want)
	}
}

func TestRespondDeclinedPDFRequestExplainsGuardrails(t *testing.T) {
	path := writeRespondFile(t, "report.pdf", pdfBytes)
	r := &pdfRecordingRunner{extraction: twoPages(), runErr: &runner.Error{Kind: runner.KindRequestDeclined, ExitCode: -1, Err: errors.New("declined")}}
	_, _, err := runRespond(t, r, "--model", "local", "--file", path, "Summarize")
	if ExitCode(err) != 5 || machineCode(err) != "request_declined" ||
		!strings.Contains(err.Error(), "Apple's guardrails can refuse such text") ||
		!strings.Contains(err.Error(), "does not mean the file was unreadable") {
		t.Fatalf("err=%v", err)
	}
	text := writeRespondFile(t, "notes.md", "alpha\n")
	r = &pdfRecordingRunner{runErr: &runner.Error{Kind: runner.KindRequestDeclined, ExitCode: -1, Err: errors.New("declined")}}
	_, _, err = runRespond(t, r, "--model", "local", "--file", text, "Summarize")
	if strings.Contains(err.Error(), "guardrails") {
		t.Fatalf("a text-only request carried the extraction note: %v", err)
	}
}

// Extracted text is prompt content. It reaches the model and nothing else:
// not stderr, not an error message, not the JSON error envelope.
func TestRespondExtractedTextNeverReachesDiagnostics(t *testing.T) {
	const canary = "EXTRACTED_PAGE_CANARY"
	path := writeRespondFile(t, "report.pdf", pdfBytes)
	extraction := runner.Extraction{Media: "pdf", PageCount: 1, TextBytes: len(canary), Pages: []runner.ExtractedPage{{Page: 1, Text: canary, Method: "ocr"}}}
	for _, failure := range []error{
		&runner.Error{Kind: runner.KindRequestDeclined, ExitCode: -1, Err: errors.New("declined")},
		&runner.Error{Kind: runner.KindContextCapacity, ExitCode: -1, Err: errors.New("capacity")},
		&runner.Error{Kind: runner.KindTimeout, ExitCode: -1, Err: errors.New("timed out")},
		&runner.Error{Kind: runner.KindNativeFailed, ExitCode: -1, Err: errors.New("failed")},
		errors.New("unclassified"),
	} {
		for _, mode := range [][]string{nil, {"--json"}, {"--agent"}} {
			r := &pdfRecordingRunner{extraction: extraction, runErr: failure}
			args := append(append([]string{}, mode...), "--model", "local", "--file", path, "Summarize")
			out, errOut, err := runRespond(t, r, args...)
			if err == nil {
				t.Fatalf("%v: generation failure was reported as success", failure)
			}
			if !strings.Contains(r.prompt, canary) {
				t.Fatal("the extracted text did not reach the model")
			}
			for _, stream := range []string{out, errOut, err.Error()} {
				if strings.Contains(stream, canary) {
					t.Fatalf("%v %v: extracted text leaked: %q", failure, mode, stream)
				}
			}
		}
	}
}

func TestRespondPDFSourceLargerThanTheTextLimitIsReadWhole(t *testing.T) {
	large := pdfBytes + strings.Repeat("%", chat.MaxRenderedPromptBytes)
	path := writeRespondFile(t, "large.pdf", large)
	r := &pdfRecordingRunner{extraction: twoPages()}
	if _, _, err := runRespond(t, r, "--model", "local", "--file", path, "Summarize"); err != nil {
		t.Fatal(err)
	}
	if len(r.data) != 1 || len(r.data[0]) != len(large) {
		t.Fatal("PDF bytes were not passed through whole")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestPagesCountsAreExactWithoutExpandingHugeRanges(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []struct {
		values []string
		want   string
	}{
		{[]string{"1-2001", "3000-3010"}, "selects 2012 pages; the limit is 2000"},
		{[]string{"1-2500", "1000-3000"}, "selects 3000 pages; the limit is 2000"},
		{[]string{"1-" + strconv.Itoa(maxInt)}, "selects " + strconv.Itoa(maxInt) + " pages; the limit is 2000"},
		{[]string{strings.Repeat("1,", 3000) + "1", "2,3"}, "lists 3003 tokens; the limit is 2000"},
	} {
		_, err := parsePages(tc.values)
		if ExitCode(err) != 2 || machineCode(err) != "unsupported_parameter" || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("err=%v; want %s", err, tc.want)
		}
	}
	pages, err := parsePages([]string{strconv.Itoa(maxInt), strconv.Itoa(maxInt)})
	if err != nil || !reflect.DeepEqual(pages, []int{maxInt}) {
		t.Fatalf("maximum page: pages=%v err=%v", pages, err)
	}
}

func TestRespondTextContentValidationIgnoresExtension(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"nul.pdf", "text\x00"},
		{"invalid", "\xff"},
		{"empty.rtf", " \n"},
		{"large.pdf", strings.Repeat("x", chat.MaxRenderedPromptBytes+1)},
	} {
		path := writeRespondFile(t, tc.name, tc.body)
		r := &pdfRecordingRunner{}
		_, _, err := runRespond(t, r, "--model", "local", "--file", path, "Summarize")
		if ExitCode(err) != 2 || r.calls != 0 || len(r.extracts) != 0 {
			t.Fatalf("%s: err=%v", tc.name, err)
		}
	}
	r := &pdfRecordingRunner{}
	_, _, err := runRespond(t, r, "--model", "local", "--file", t.TempDir(), "Summarize")
	if ExitCode(err) != 2 || r.calls != 0 || len(r.extracts) != 0 {
		t.Fatalf("directory: err=%v", err)
	}
}

func TestAgentContextDescribesPDFExtractionSideEffects(t *testing.T) {
	ctx := buildAgentContext(NewRootCmd(func() runner.Runner { return &pdfRecordingRunner{} }))
	for _, command := range ctx.Commands {
		if command.Name != "respond" {
			continue
		}
		effects := strings.Join(command.SideEffects, " ")
		for _, want := range []string{"PDF", "every answering route", "private staged copy", "cloud and chatgpt", "CLI-only", "regardless of extension"} {
			if !strings.Contains(effects, want) {
				t.Fatalf("respond side effects missing %q", want)
			}
		}
		for _, flag := range command.Flags {
			if flag.Name == "file" && strings.Contains(flag.Usage, "any extension") && strings.Contains(flag.Usage, "PDF") {
				return
			}
		}
		t.Fatal("--file does not describe text and PDF content")
	}
	t.Fatal("respond missing from agent-context")
}
