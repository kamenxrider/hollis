// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Extraction limits the helper advertises and enforces. Go checks what it can
// see before a spawn and re-checks what the helper reports.
const (
	ExtractMaxPages     = 2000
	ExtractMaxTextBytes = 131_072
	ExtractMaxFileBytes = 64 << 20
)

// Extraction is the on-device text of one document. PageCount is the whole
// document's page count, not the number of pages returned; Pages holds only the
// selected pages in ascending order. Text is verbatim from the text layer or
// from OCR and is never trimmed.
type Extraction struct {
	Media     string
	PageCount int
	Pages     []ExtractedPage
	TextBytes int
}

type ExtractedPage struct {
	Page   int
	Text   string
	Method string
}

// ExtractRunner reads a document's text without a model call.
type ExtractRunner interface {
	// ExtractPDF extracts pages (1-based, ascending, unique; nil means all)
	// from PDF bytes the caller already read and sniffed.
	ExtractPDF(ctx context.Context, data []byte, pages []int) (Extraction, error)
}

// ExtractCapability is the protocol-3 extract advertisement. MaxPages is the
// whole-document page ceiling, not a selection limit.
type ExtractCapability struct {
	PDF          bool `json:"pdf"`
	OCR          bool `json:"ocr"`
	Pages        bool `json:"pages"`
	MaxPages     int  `json:"max_pages"`
	MaxTextBytes int  `json:"max_text_bytes"`
}

type nativeSource struct {
	Path string `json:"path"`
}

type nativePage struct {
	Page   *int    `json:"page"`
	Text   *string `json:"text"`
	Method string  `json:"method"`
}

// ExtractPDF stages the bytes in a private directory and asks the helper to
// read that copy. The helper never sees the caller's path, so a file replaced
// after Go sniffed it cannot change what is extracted, and no caller path can
// reach an event or a message.
func (r *NativeRunner) ExtractPDF(ctx context.Context, data []byte, pages []int) (Extraction, error) {
	if len(data) > ExtractMaxFileBytes {
		err := nativeError(KindInvalidDocument, fmt.Sprintf("document is %d bytes; the limit is %d bytes", len(data), ExtractMaxFileBytes))
		err.Limit, err.Actual = ExtractMaxFileBytes, len(data)
		return Extraction{}, err
	}
	if err := validatePageSelection(pages); err != nil {
		return Extraction{}, err
	}
	path, cleanup, err := stageDocument(data)
	if err != nil {
		return Extraction{}, err
	}
	defer cleanup()
	result, _, err := r.invoke(ctx, ModelLocal, "extract", "", nil, nil, nativeOptions{Source: path, Pages: pages, Media: "pdf"})
	if err != nil {
		return Extraction{}, err
	}
	return *result.extraction, nil
}

func validatePageSelection(pages []int) error {
	if pages == nil {
		return nil
	}
	if len(pages) == 0 || len(pages) > ExtractMaxPages {
		return nativeError(KindUsage, "page selection must name 1 to 2000 pages")
	}
	for i, page := range pages {
		if page < 1 || (i > 0 && page <= pages[i-1]) {
			return nativeError(KindUsage, "page selection must be ascending, unique and 1-based")
		}
	}
	return nil
}

func stageDocument(data []byte) (string, func(), error) {
	dir, err := os.MkdirTemp("", "hollis-document-")
	if err != nil {
		return "", nil, nativeError(KindTransport, "could not create private document staging directory")
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	fail := func() (string, func(), error) {
		cleanup()
		return "", nil, nativeError(KindTransport, "could not stage document")
	}
	if os.Chmod(dir, 0o700) != nil {
		return fail()
	}
	path := filepath.Join(dir, "document.pdf")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fail()
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fail()
	}
	if file.Close() != nil {
		return fail()
	}
	return path, cleanup, nil
}

// readExtraction validates an extract completion against the request. The
// helper is trusted for text, not for shape: a page outside the selection, a
// count that disagrees, or a total over the ceiling is a protocol error rather
// than text to be framed into a prompt.
func readExtraction(event nativeEvent, opts nativeOptions) (*Extraction, error) {
	invalid := func() (*Extraction, error) {
		return nil, nativeError(KindNativeProtocol, "invalid native extraction")
	}
	if event.Text != nil || event.Usage != nil || len(event.Structured) != 0 ||
		event.Media != "pdf" || event.PageCount == nil || event.TextBytes == nil || len(event.Pages) == 0 {
		return invalid()
	}
	pageCount := *event.PageCount
	if pageCount < 1 || pageCount > ExtractMaxPages {
		return invalid()
	}
	if opts.Pages == nil && len(event.Pages) != pageCount {
		return invalid()
	}
	if opts.Pages != nil && len(event.Pages) != len(opts.Pages) {
		return invalid()
	}
	extraction := &Extraction{Media: event.Media, PageCount: pageCount, Pages: make([]ExtractedPage, 0, len(event.Pages))}
	total, sawText := 0, false
	for i, page := range event.Pages {
		if page.Page == nil || page.Text == nil || (page.Method != "text" && page.Method != "ocr") {
			return invalid()
		}
		want := i + 1
		if opts.Pages != nil {
			want = opts.Pages[i]
		}
		if *page.Page != want || !utf8.ValidString(*page.Text) {
			return invalid()
		}
		// Only OCR may legitimately find nothing; an empty text layer is
		// what sends a page to OCR in the first place.
		if strings.TrimSpace(*page.Text) == "" {
			if page.Method != "ocr" {
				return invalid()
			}
		} else {
			sawText = true
		}
		total += len(*page.Text)
		extraction.Pages = append(extraction.Pages, ExtractedPage{Page: *page.Page, Text: *page.Text, Method: page.Method})
	}
	if !sawText || total != *event.TextBytes || total > ExtractMaxTextBytes {
		return invalid()
	}
	extraction.TextBytes = total
	return extraction, nil
}

// extractFailure maps a helper extract error event. Every number is an integer
// the helper measured; no text from the event reaches the message.
func extractFailure(event nativeEvent) error {
	limitPair := func() (int, int, bool) {
		if event.Limit == nil || event.Actual == nil || *event.Limit <= 0 || *event.Actual <= *event.Limit {
			return 0, 0, false
		}
		return *event.Limit, *event.Actual, true
	}
	switch event.Kind {
	case KindUnsupportedMedia:
		return nativeError(event.Kind, "document content is not a supported format; PDF is the only format read on-device")
	case KindExtractMediaMismatch:
		if event.Media == "" || event.Media == "pdf" {
			return nativeError(KindNativeProtocol, "invalid native extraction failure")
		}
		return nativeError(event.Kind, fmt.Sprintf("the helper detected %s content where Hollis detected pdf", event.Media))
	case KindInvalidDocument:
		if event.Limit == nil && event.Actual == nil {
			return nativeError(event.Kind, "the document could not be read: it is damaged, encrypted, or a page could not be rendered or recognized")
		}
		limit, actual, ok := limitPair()
		if !ok {
			return nativeError(KindNativeProtocol, "invalid native extraction failure")
		}
		err := nativeError(event.Kind, fmt.Sprintf("document is %d bytes; the limit is %d bytes", actual, limit))
		if limit == ExtractMaxPages {
			err = nativeError(event.Kind, fmt.Sprintf("document has %d pages; the limit is %d", actual, limit))
		}
		err.Limit, err.Actual = limit, actual
		return err
	case KindPageOutOfRange:
		if event.Page == nil || event.PageCount == nil || *event.PageCount < 1 || *event.PageCount > ExtractMaxPages || *event.Page <= *event.PageCount {
			return nativeError(KindNativeProtocol, "invalid native extraction failure")
		}
		err := nativeError(event.Kind, fmt.Sprintf("page %d is beyond the document's %d pages", *event.Page, *event.PageCount))
		err.Page, err.PageCount = *event.Page, *event.PageCount
		return err
	case KindExtractNoText:
		return nativeError(event.Kind, "no non-whitespace text was found in the selected pages, from the text layer or on-device OCR")
	case KindContextCapacity:
		limit, actual, ok := limitPair()
		if !ok {
			return nativeError(KindNativeProtocol, "invalid native extraction failure")
		}
		err := nativeError(event.Kind, fmt.Sprintf("extracted text reached %d bytes before extraction stopped; the limit is %d bytes and nothing was truncated", actual, limit))
		err.Limit, err.Actual = limit, actual
		return err
	}
	return nil
}

// requireExtract refuses an extract request the helper did not advertise.
func requireExtract(status LocalStatus, opts nativeOptions) *Error {
	var extract *ExtractCapability
	if status.Capabilities != nil {
		extract = status.Capabilities.Extract
	}
	if extract != nil && extract.PDF && (opts.Pages == nil || extract.Pages) {
		return nil
	}
	return capabilityRefusal("extract", "")
}
