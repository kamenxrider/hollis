// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package imagegen

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var (
	// ErrInvalidDestination marks a destination that cannot receive a PNG.
	ErrInvalidDestination = errors.New("image destination is invalid")
	// ErrDestinationExists marks a preflight or publication collision. It is
	// deliberately distinct from overwrite support, which this package does
	// not provide.
	ErrDestinationExists = errors.New("image destination already exists")
)

// PublishedImage is the metadata for one file published at its final path.
// Format is always PNG for this narrow package.
type PublishedImage struct {
	Path   string
	Format string
	Bytes  int64
	Width  int
	Height int
	SHA256 string
}

// PreflightDestination validates only the destination before an expensive
// generation call. It rejects empty paths, non-PNG extensions, missing or
// symlinked parents, and any existing destination including symlinks.
func PreflightDestination(destination string) error {
	if strings.TrimSpace(destination) == "" {
		return &Error{
			Kind: KindUsage,
			Err:  fmt.Errorf("%w: destination path is empty", ErrInvalidDestination),
		}
	}
	if ext := filepath.Ext(destination); !strings.EqualFold(ext, ".png") {
		return &Error{
			Kind: KindUsage,
			Err:  fmt.Errorf("%w: output must have a .png extension", ErrInvalidDestination),
		}
	}

	dir := filepath.Dir(destination)
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Error{
				Kind: KindOutputInspection,
				Err:  fmt.Errorf("%w: output directory does not exist: %w", ErrInvalidDestination, err),
			}
		}
		return &Error{
			Kind: KindOutputInspection,
			Err:  fmt.Errorf("%w: inspect output directory: %w", ErrInvalidDestination, err),
		}
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return &Error{
			Kind: KindOutputInspection,
			Err:  fmt.Errorf("%w: output parent must be a real directory, not a symlink", ErrInvalidDestination),
		}
	}

	info, err = os.Lstat(destination)
	if err == nil {
		return &Error{
			Kind: KindOutputInspection,
			Err:  fmt.Errorf("%w: destination is already present", ErrDestinationExists),
		}
	}
	if !errors.Is(err, os.ErrNotExist) {
		return &Error{
			Kind: KindOutputInspection,
			Err:  fmt.Errorf("%w: inspect destination: %w", ErrInvalidDestination, err),
		}
	}
	return nil
}

// Publish independently validates the staged PNG and publishes a complete
// copy at destination without replacing an existing entry.
//
// The final file appears only after a same-directory temporary file has been
// written and synced and a hard link is created at destination. Link creation
// is no-replace, so a collision occurring after preflight fails atomically.
// The caller remains responsible for Result.Cleanup.
func Publish(stagedPath, destination string) (PublishedImage, error) {
	if err := PreflightDestination(destination); err != nil {
		return PublishedImage{}, err
	}
	validated, err := validateStagedPNG(stagedPath)
	if err != nil {
		return PublishedImage{}, err
	}

	tempFile, err := os.CreateTemp(filepath.Dir(destination), ".hollis-image-*.tmp")
	if err != nil {
		return PublishedImage{}, &Error{
			Kind: KindOutputInspection,
			Err:  fmt.Errorf("create private image publish temporary: %w", err),
		}
	}
	tempPath := tempFile.Name()
	defer func() {
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
	}()

	if err := tempFile.Chmod(0o600); err != nil {
		_ = tempFile.Close()
		return PublishedImage{}, &Error{
			Kind: KindOutputInspection,
			Err:  fmt.Errorf("protect image publish temporary: %w", err),
		}
	}
	if _, err := tempFile.Write(validated.contents); err != nil {
		_ = tempFile.Close()
		return PublishedImage{}, &Error{
			Kind: KindOutputInspection,
			Err:  fmt.Errorf("write image publish temporary: %w", err),
		}
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return PublishedImage{}, &Error{
			Kind: KindOutputInspection,
			Err:  fmt.Errorf("sync image publish temporary: %w", err),
		}
	}
	if err := tempFile.Close(); err != nil {
		return PublishedImage{}, &Error{
			Kind: KindOutputInspection,
			Err:  fmt.Errorf("close image publish temporary: %w", err),
		}
	}

	if err := os.Link(tempPath, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return PublishedImage{}, &Error{
				Kind: KindOutputInspection,
				Err:  fmt.Errorf("%w: destination appeared during generation", ErrDestinationExists),
			}
		}
		return PublishedImage{}, &Error{
			Kind: KindOutputInspection,
			Err:  fmt.Errorf("publish image without replacement: %w", err),
		}
	}
	// The destination and temporary name now share one inode. Removing the
	// temporary name does not affect the published file.
	_ = os.Remove(tempPath)
	tempPath = ""

	return PublishedImage{
		Path:   destination,
		Format: "PNG",
		Bytes:  int64(len(validated.contents)),
		Width:  validated.width,
		Height: validated.height,
		SHA256: validated.sha256,
	}, nil
}

type validatedPNG struct {
	contents []byte
	width    int
	height   int
	sha256   string
}

func validateStagedPNG(path string) (validatedPNG, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return validatedPNG{}, &Error{
				Kind: KindNoOutput,
				Err:  fmt.Errorf("%w: staged image is missing", ErrNoOutput),
			}
		}
		return validatedPNG{}, &Error{
			Kind: KindOutputInspection,
			Err:  fmt.Errorf("%w: inspect staged image: %w", ErrInvalidPNG, err),
		}
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return validatedPNG{}, &Error{
			Kind: KindOutputInspection,
			Err:  fmt.Errorf("%w: staged image is not a regular file", ErrInvalidPNG),
		}
	}
	if info.Size() <= 0 {
		return validatedPNG{}, &Error{
			Kind: KindNoOutput,
			Err:  ErrNoOutput,
		}
	}
	if info.Size() > MaxOutputBytes {
		return validatedPNG{}, &Error{
			Kind: KindImageTooLarge,
			Err:  fmt.Errorf("%w: %d bytes exceeds %d", ErrImageTooLarge, info.Size(), MaxOutputBytes),
		}
	}

	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return validatedPNG{}, &Error{
			Kind: KindOutputInspection,
			Err:  fmt.Errorf("%w: open staged image: %w", ErrInvalidPNG, err),
		}
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, MaxOutputBytes+1))
	if err != nil {
		return validatedPNG{}, &Error{
			Kind: KindOutputInspection,
			Err:  fmt.Errorf("%w: read staged image: %w", ErrInvalidPNG, err),
		}
	}
	if int64(len(contents)) > MaxOutputBytes {
		return validatedPNG{}, &Error{
			Kind: KindImageTooLarge,
			Err:  fmt.Errorf("%w: staged image grew beyond %d bytes", ErrImageTooLarge, MaxOutputBytes),
		}
	}
	if int64(len(contents)) != info.Size() {
		return validatedPNG{}, &Error{
			Kind: KindOutputInspection,
			Err:  fmt.Errorf("%w: staged image changed during validation", ErrInvalidPNG),
		}
	}

	config, err := png.DecodeConfig(bytes.NewReader(contents))
	if err != nil {
		return validatedPNG{}, &Error{
			Kind: KindInvalidPNG,
			Err:  fmt.Errorf("%w: decode PNG header: %w", ErrInvalidPNG, err),
		}
	}
	if config.Width <= 0 || config.Height <= 0 {
		return validatedPNG{}, &Error{
			Kind: KindInvalidPNG,
			Err:  fmt.Errorf("%w: PNG dimensions are empty", ErrInvalidPNG),
		}
	}
	pixels := int64(config.Width) * int64(config.Height)
	if pixels > MaxPixels {
		return validatedPNG{}, &Error{
			Kind: KindImageTooLarge,
			Err:  fmt.Errorf("%w: %d pixels exceeds %d", ErrImageTooLarge, pixels, MaxPixels),
		}
	}
	if _, err := png.Decode(bytes.NewReader(contents)); err != nil {
		return validatedPNG{}, &Error{
			Kind: KindInvalidPNG,
			Err:  fmt.Errorf("%w: decode PNG pixels: %w", ErrInvalidPNG, err),
		}
	}
	sum := sha256.Sum256(contents)
	return validatedPNG{
		contents: contents,
		width:    config.Width,
		height:   config.Height,
		sha256:   fmt.Sprintf("%x", sum),
	}, nil
}
