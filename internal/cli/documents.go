// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/docinput"
	"github.com/kamenxrider/hollis/internal/runner"
)

// documentInput is one --file in flag order: a text document ready to frame,
// or PDF bytes still to extract.
type documentInput struct {
	document docinput.Document
	pdf      []byte
	name     string
}

func (d documentInput) isPDF() bool { return d.pdf != nil }

// readDocumentInputs reads and sniffs every --file before anything spawns.
// Content decides the route: a PDF goes to on-device extraction, a PNG or JPEG
// belongs to --image, and anything that is neither UTF-8 text nor a PDF is
// refused rather than read as best-effort text.
func readDocumentInputs(paths []string, instructionBytes int) ([]documentInput, error) {
	inputs := make([]documentInput, 0, len(paths))
	textBytes := instructionBytes
	for i, path := range paths {
		source, err := docinput.ReadSource(path)
		if err != nil {
			return nil, usageErr(fmt.Errorf("read document %d: %w", i+1, err))
		}
		switch source.Media {
		case docinput.MediaPDF:
			inputs = append(inputs, documentInput{pdf: source.Data, name: source.Name})
			continue
		case docinput.MediaImage:
			return nil, codedUsageErr("unsupported_media", fmt.Errorf("document %d (%q) is a PNG or JPEG image; pass images with --image", i+1, source.Name))
		case docinput.MediaBinary:
			return nil, codedUsageErr("unsupported_media", fmt.Errorf("document %d (%q) is neither UTF-8 text nor a PDF; other formats are not read", i+1, source.Name))
		}
		document, err := docinput.TextDocument(source, chat.MaxRenderedPromptBytes)
		if err != nil {
			return nil, usageErr(fmt.Errorf("read document %d: %w", i+1, err))
		}
		textBytes += len(document.Text)
		if textBytes > chat.MaxRenderedPromptBytes {
			return nil, usageErr(fmt.Errorf("instruction and document text are at least %d bytes; the prompt limit is %d bytes", textBytes, chat.MaxRenderedPromptBytes))
		}
		inputs = append(inputs, documentInput{document: document, name: source.Name})
	}
	return inputs, nil
}

var pageToken = regexp.MustCompile(`^([0-9]+)(?:-([0-9]+))?$`)

// parsePages reads every --pages value strictly. A token that is not a page or
// an ascending range is refused by its exact spelling; nothing is guessed or
// partly applied. Ranges are merged before counting, so a huge range is
// refused without being expanded.
func parsePages(values []string) ([]int, error) {
	type span struct{ from, to int }
	var spans []span
	tokens := 0
	for _, value := range values {
		tokens += strings.Count(value, ",") + 1
	}
	if tokens > runner.ExtractMaxPages {
		return nil, pagesErr(fmt.Errorf("--pages lists %d tokens; the limit is %d", tokens, runner.ExtractMaxPages))
	}
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			match := pageToken.FindStringSubmatch(token)
			if match == nil {
				return nil, pagesErr(fmt.Errorf("--pages token %q is not a page number or a range such as 3-7", token))
			}
			from, err := strconv.Atoi(match[1])
			if err != nil || from < 1 {
				return nil, pagesErr(fmt.Errorf("--pages token %q: pages are numbered from 1", token))
			}
			to := from
			if match[2] != "" {
				to, err = strconv.Atoi(match[2])
				if err != nil || to < 1 {
					return nil, pagesErr(fmt.Errorf("--pages token %q: pages are numbered from 1", token))
				}
				if to < from {
					return nil, pagesErr(fmt.Errorf("--pages token %q is a reversed range", token))
				}
			}
			spans = append(spans, span{from, to})
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].from < spans[j].from })
	merged := spans[:0]
	for _, s := range spans {
		if n := len(merged); n > 0 && s.from-1 <= merged[n-1].to {
			merged[n-1].to = max(merged[n-1].to, s.to)
			continue
		}
		merged = append(merged, s)
	}
	selected := 0
	for _, s := range merged {
		selected += s.to - s.from + 1
	}
	if selected > runner.ExtractMaxPages {
		return nil, pagesErr(fmt.Errorf("--pages selects %d pages; the limit is %d", selected, runner.ExtractMaxPages))
	}
	pages := make([]int, 0, selected)
	for _, s := range merged {
		for page := s.from; ; page++ {
			pages = append(pages, page)
			if page == s.to {
				break
			}
		}
	}
	return pages, nil
}

func pagesErr(err error) error { return codedUsageErr("unsupported_parameter", err) }

// extractDocuments turns every PDF input into a framed document, in flag
// order. Each extraction is its own on-device helper run bounded by the
// request timeout; no model is called.
func extractDocuments(ctx context.Context, r runner.Runner, inputs []documentInput, pages []int, pageSpec string, timeout time.Duration) ([]docinput.Document, error) {
	documents := make([]docinput.Document, 0, len(inputs))
	for i, input := range inputs {
		if !input.isPDF() {
			documents = append(documents, input.document)
			continue
		}
		extractor, ok := r.(runner.ExtractRunner)
		if !ok {
			return nil, transportErr(errors.New("configured runner does not support PDF extraction"))
		}
		extractCtx, cancel := context.WithTimeout(ctx, timeout)
		extraction, err := extractor.ExtractPDF(extractCtx, input.pdf, pages)
		cancel()
		if err != nil {
			return nil, extractCLIError(err, i+1, input.name, pageSpec)
		}
		framed := make([]docinput.Page, 0, len(extraction.Pages))
		for _, page := range extraction.Pages {
			framed = append(framed, docinput.Page{Number: page.Page, Text: page.Text})
		}
		documents = append(documents, docinput.PDFDocument(input.name, framed))
	}
	return documents, nil
}

func extractCLIError(err error, index int, name, pageSpec string) error {
	var re *runner.Error
	if errors.As(err, &re) && re.Kind == runner.KindPageOutOfRange && pageSpec != "" {
		return codedUsageErr(string(re.Kind), fmt.Errorf("--pages %s asks for page %d, but document %d (%q) has %d pages (page_count %d)", pageSpec, re.Page, index, name, re.PageCount, re.PageCount))
	}
	if errors.As(err, &re) {
		switch re.Kind {
		case runner.KindUnsupportedMedia, runner.KindExtractMediaMismatch, runner.KindInvalidDocument, runner.KindExtractNoText, runner.KindContextCapacity:
			converted := toCLIError(err)
			var ce *cliError
			if errors.As(converted, &ce) {
				return &cliError{code: ce.code, machineCode: ce.machineCode, err: fmt.Errorf("document %d (%q): %w", index, name, ce.err)}
			}
		}
	}
	return toCLIError(err)
}

// extractedDeclinedNote is appended when a request that carried PDF text is
// declined: the refusal concerns the extracted text, not the file's
// readability.
const extractedDeclinedNote = " The request included text extracted on-device from a PDF; Apple's guardrails can refuse such text, and this refusal does not mean the file was unreadable."
