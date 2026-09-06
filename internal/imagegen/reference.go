// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package imagegen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"os"
	"strings"
	"syscall"
)

const (
	// MaxReferenceBytes bounds one reference image before it is encoded into
	// the JSON input accepted by a parameterized image Shortcut.
	MaxReferenceBytes int64 = 4 << 20

	// MaxReferencePixels bounds the decoded dimensions of one reference image.
	// Keep this aligned with the public image-input contract.
	MaxReferencePixels int64 = 16_000_000
)

var (
	// ErrInvalidReference marks a reference whose bytes or metadata do not
	// describe one complete supported PNG or JPEG image.
	ErrInvalidReference = errors.New("image reference is invalid")
	// ErrReferenceTooLarge marks a reference over the bounded input limit.
	ErrReferenceTooLarge = errors.New("image reference exceeds the 4 MiB limit")
	// ErrReferenceChecksumMismatch marks a path reference that did not match
	// the caller's expected content hash.
	ErrReferenceChecksumMismatch = errors.New("image reference checksum does not match")
)

// ReferenceImage is a validated, in-memory PNG or JPEG attachment for an
// image-generation request. Bytes are the complete original file contents;
// they are never fetched from a URL or interpreted as a filesystem path by
// the Shortcut transport.
//
// Callers should use NewReferenceImage or NewReferenceImageFromPath. The
// transport validates all fields again so a handcrafted or subsequently
// mutated value cannot bypass the image and checksum limits.
type ReferenceImage struct {
	Bytes    []byte
	MIMEType string
	Width    int
	Height   int
	SHA256   string
}

// NewReferenceImage validates data and returns a defensive copy with its
// format, dimensions, and SHA-256 checksum populated.
func NewReferenceImage(data []byte) (*ReferenceImage, error) {
	if int64(len(data)) > MaxReferenceBytes {
		return nil, ErrReferenceTooLarge
	}
	if len(data) == 0 {
		return nil, ErrInvalidReference
	}
	copyOfData := bytes.Clone(data)
	config, mimeType, err := decodeReference(copyOfData)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(copyOfData)
	return &ReferenceImage{
		Bytes:    copyOfData,
		MIMEType: mimeType,
		Width:    config.Width,
		Height:   config.Height,
		SHA256:   hex.EncodeToString(hash[:]),
	}, nil
}

// NewReferenceImageFromPath reads one local regular PNG or JPEG file under
// the reference limits. expectedSHA256 may be empty; when supplied it is
// compared with the bytes read from the file before the value is returned.
// No URL or network access is performed.
func NewReferenceImageFromPath(path, expectedSHA256 string) (*ReferenceImage, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("%w: path is empty", ErrInvalidReference)
	}
	if expectedSHA256 != "" {
		if len(expectedSHA256) != sha256.Size*2 {
			return nil, fmt.Errorf("%w: expected SHA-256 must be 64 hexadecimal characters", ErrInvalidReference)
		}
		if _, err := hex.DecodeString(expectedSHA256); err != nil {
			return nil, fmt.Errorf("%w: expected SHA-256 is not hexadecimal", ErrInvalidReference)
		}
	}

	// Inspect before opening so a FIFO or device cannot make the bounded read
	// block indefinitely. O_NOFOLLOW then protects the open itself from a
	// symlink race on the Unix hosts supported by Hollis.
	lstat, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect reference file: %w", ErrInvalidReference, classifyReferenceFileError(err))
	}
	if lstat.Mode()&os.ModeSymlink != 0 || !lstat.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: reference path is not a regular file", ErrInvalidReference)
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: open reference file: %w", ErrInvalidReference, classifyReferenceFileError(err))
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("%w: inspect opened reference file: %w", ErrInvalidReference, classifyReferenceFileError(err))
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: reference path is not a regular file", ErrInvalidReference)
	}
	if info.Size() > MaxReferenceBytes {
		return nil, ErrReferenceTooLarge
	}

	data, err := io.ReadAll(io.LimitReader(file, MaxReferenceBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read reference file: %w", ErrInvalidReference, classifyReferenceFileError(err))
	}
	if int64(len(data)) > MaxReferenceBytes {
		return nil, ErrReferenceTooLarge
	}
	reference, err := NewReferenceImage(data)
	if err != nil {
		return nil, err
	}
	if expectedSHA256 != "" && !strings.EqualFold(expectedSHA256, reference.SHA256) {
		return nil, ErrReferenceChecksumMismatch
	}
	return reference, nil
}

// Avoid propagating os.PathError, whose Error method includes the caller's
// path. Keep useful filesystem sentinels discoverable while reducing all other
// operating-system detail to one path-free diagnostic.
func classifyReferenceFileError(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fs.ErrNotExist
	case errors.Is(err, fs.ErrPermission):
		return fs.ErrPermission
	default:
		return errors.New("file access failed")
	}
}

// ValidateReferenceImage revalidates all bytes and metadata in reference.
// It is safe to call before passing a value through another Generator seam.
func ValidateReferenceImage(reference *ReferenceImage) error {
	if reference == nil {
		return fmt.Errorf("%w: reference is nil", ErrInvalidReference)
	}
	if int64(len(reference.Bytes)) > MaxReferenceBytes {
		return ErrReferenceTooLarge
	}
	if len(reference.Bytes) == 0 {
		return ErrInvalidReference
	}
	config, mimeType, err := decodeReference(reference.Bytes)
	if err != nil {
		return err
	}
	if reference.MIMEType != mimeType {
		return fmt.Errorf("%w: MIME type does not match image data", ErrInvalidReference)
	}
	if reference.Width != config.Width || reference.Height != config.Height {
		return fmt.Errorf("%w: dimensions do not match image data", ErrInvalidReference)
	}
	hash := sha256.Sum256(reference.Bytes)
	if !strings.EqualFold(reference.SHA256, hex.EncodeToString(hash[:])) {
		return fmt.Errorf("%w: checksum does not match image data", ErrInvalidReference)
	}
	return nil
}

func decodeReference(data []byte) (image.Config, string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return image.Config{}, "", fmt.Errorf("%w: decode image header: %v", ErrInvalidReference, err)
	}
	var mimeType string
	switch format {
	case "png":
		mimeType = "image/png"
	case "jpeg":
		mimeType = "image/jpeg"
	default:
		return image.Config{}, "", fmt.Errorf("%w: only PNG and JPEG are supported", ErrInvalidReference)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return image.Config{}, "", fmt.Errorf("%w: image dimensions are empty", ErrInvalidReference)
	}
	width := int64(config.Width)
	height := int64(config.Height)
	if width > MaxReferencePixels || height > MaxReferencePixels || width > MaxReferencePixels/height {
		return image.Config{}, "", fmt.Errorf("%w: image exceeds the 16 megapixel limit", ErrReferenceTooLarge)
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return image.Config{}, "", fmt.Errorf("%w: decode image pixels: %v", ErrInvalidReference, err)
	}
	return config, mimeType, nil
}
