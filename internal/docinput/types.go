// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0.

// Package docinput reads and prepares local text input for Hollis prompts.
package docinput

// Document is one named piece of local source material.
//
// Name is a display label supplied by the caller. Text is the validated UTF-8
// content read from the document.
type Document struct {
	Name string
	Text string
}
