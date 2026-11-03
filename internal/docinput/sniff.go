// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0.

package docinput

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Media is what a document's content is, decided from its bytes. An extension
// never decides it.
type Media int

const (
	MediaText Media = iota
	MediaPDF
	MediaImage
	MediaBinary
)

// MaxSourceBytes bounds any document read before its media is known. A PDF
// may use all of it; text is held to the prompt limit afterwards.
const MaxSourceBytes = 64 << 20

// Sniff classifies document bytes. A PDF must start with its header: the
// helper accepts a header anywhere in the first KiB, so requiring it at offset
// zero here keeps every PDF Hollis routes to extraction a PDF to the helper too,
// and keeps a text file that merely mentions the header out of extraction.
func Sniff(data []byte) Media {
	switch {
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return MediaPDF
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")), bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		return MediaImage
	case utf8.Valid(data) && bytes.IndexByte(data, 0) < 0:
		return MediaText
	}
	return MediaBinary
}

// Source is one --file input after a single bounded read.
type Source struct {
	Name  string
	Media Media
	Data  []byte
}

// ReadSource reads one document under the safe-input rules of ReadFile and
// classifies it from content.
func ReadSource(path string) (Source, error) {
	if strings.TrimSpace(path) == "" {
		return Source{}, errors.New("document path must not be empty")
	}
	data, err := ReadFile(path, "document", MaxSourceBytes)
	if err != nil {
		return Source{}, err
	}
	return Source{Name: filepath.Base(path), Media: Sniff(data), Data: data}, nil
}

// TextDocument applies the text-document rules to a source already read and
// sniffed as text: the byte limit and non-empty content, regardless of name.
func TextDocument(source Source, maxBytes int) (Document, error) {
	if len(source.Data) > maxBytes {
		return Document{}, fmt.Errorf("document %q is %d bytes; the text limit is %d bytes", source.Name, len(source.Data), maxBytes)
	}
	text := string(source.Data)
	if strings.TrimSpace(text) == "" {
		return Document{}, errors.New("document must contain non-empty content")
	}
	return Document{Name: source.Name, Text: text}, nil
}

// Page is one page of extracted document text.
type Page struct {
	Number int
	Text   string
}

// PDFDocument frames extracted pages as one document. Each page gets a marker
// so the model can cite page numbers, and a page OCR found empty keeps its
// marker so the numbering stays honest. Page text is inserted verbatim.
func PDFDocument(name string, pages []Page) Document {
	var text strings.Builder
	for _, page := range pages {
		fmt.Fprintf(&text, "--- Page %d ---\n", page.Number)
		text.WriteString(page.Text)
		text.WriteByte('\n')
	}
	return Document{Name: name, Text: text.String()}
}
