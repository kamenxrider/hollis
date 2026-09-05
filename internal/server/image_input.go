// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const imageStagingPrefix = "hollis-images-"

type decodedImage struct {
	data      []byte
	extension string
}

type encodedImageCandidate struct {
	payload        string
	declaredFormat string
	extension      string
	decodedLen     int
}

// stageImages validates inline PNG/JPEG data URLs and copies the decoded
// images into a request-private directory. The caller owns the returned paths
// and must call Cleanup when it no longer needs them.
func stageImages(ctx context.Context, images []encodedImage) (stagedImages, error) {
	if err := ctx.Err(); err != nil {
		return stagedImages{}, err
	}
	if len(images) == 0 {
		return stagedImages{}, nil
	}
	if len(images) > MaxCloudImages {
		return stagedImages{}, imageLimitExceeded(fmt.Sprintf("image input accepts at most %d images", MaxCloudImages))
	}

	candidates := make([]encodedImageCandidate, 0, len(images))
	var totalBytes int
	for i, encoded := range images {
		if err := ctx.Err(); err != nil {
			return stagedImages{}, err
		}

		payload, declaredFormat, extension, err := splitImageDataURL(encoded.DataURL)
		if err != nil {
			return stagedImages{}, invalidImage(fmt.Sprintf("image %d is not a supported PNG or JPEG data URL", i+1))
		}
		decodedLen, err := exactBase64DecodedLen(payload)
		if err != nil {
			return stagedImages{}, invalidImage(fmt.Sprintf("image %d contains invalid base64 data", i+1))
		}
		if decodedLen > MaxDecodedImageBytes-totalBytes {
			return stagedImages{}, imageLimitExceeded("decoded images exceed the 4 MiB total limit")
		}
		totalBytes += decodedLen
		candidates = append(candidates, encodedImageCandidate{
			payload:        payload,
			declaredFormat: declaredFormat,
			extension:      extension,
			decodedLen:     decodedLen,
		})
	}

	decoded := make([]decodedImage, 0, len(candidates))
	var totalPixels int64
	for i, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return stagedImages{}, err
		}

		data := make([]byte, candidate.decodedLen)
		n, err := base64.StdEncoding.Strict().Decode(data, []byte(candidate.payload))
		if err != nil || n != candidate.decodedLen {
			return stagedImages{}, invalidImage(fmt.Sprintf("image %d contains invalid base64 data", i+1))
		}

		config, actualFormat, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return stagedImages{}, invalidImage(fmt.Sprintf("image %d is not a valid PNG or JPEG", i+1))
		}
		if actualFormat != candidate.declaredFormat {
			return stagedImages{}, invalidImage(fmt.Sprintf("image %d MIME type does not match its image data", i+1))
		}
		pixels, ok := imagePixelCount(config.Width, config.Height)
		if !ok || pixels > MaxImagePixels {
			return stagedImages{}, imageLimitExceeded(fmt.Sprintf("image %d exceeds the 16 megapixel limit", i+1))
		}
		if pixels > int64(MaxAggregateImagePixels)-totalPixels {
			return stagedImages{}, imageLimitExceeded("images exceed the 24 megapixel total limit")
		}
		totalPixels += pixels
		decoded = append(decoded, decodedImage{data: data, extension: candidate.extension})
	}

	// Decode only after every image has passed byte, format, and dimension
	// preflight. This bounds decoder allocations before validating full image
	// structure and catches truncated or corrupt payloads.
	for i, candidate := range decoded {
		if err := ctx.Err(); err != nil {
			return stagedImages{}, err
		}
		if _, _, err := image.Decode(bytes.NewReader(candidate.data)); err != nil {
			return stagedImages{}, invalidImage(fmt.Sprintf("image %d is corrupt or truncated", i+1))
		}
	}

	requestDir, err := os.MkdirTemp("", imageStagingPrefix)
	if err != nil {
		return stagedImages{}, errors.New("could not create private image staging")
	}
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() { _ = os.RemoveAll(requestDir) })
	}
	fail := func(err error) (stagedImages, error) {
		cleanup()
		return stagedImages{}, err
	}
	if err := os.Chmod(requestDir, 0o700); err != nil {
		return fail(errors.New("could not secure private image staging"))
	}

	paths := make([]string, 0, len(decoded))
	for i, candidate := range decoded {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		path := filepath.Join(requestDir, fmt.Sprintf("image-%03d%s", i+1, candidate.extension))
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fail(errors.New("could not stage image input"))
		}
		written, writeErr := file.Write(candidate.data)
		closeErr := file.Close()
		if writeErr != nil || written != len(candidate.data) || closeErr != nil {
			return fail(errors.New("could not stage image input"))
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return fail(errors.New("could not secure staged image input"))
		}
		paths = append(paths, path)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}

	return stagedImages{Paths: paths, cleanup: cleanup}, nil
}

func splitImageDataURL(dataURL string) (payload, format, extension string, err error) {
	switch {
	case strings.HasPrefix(dataURL, "data:image/png;base64,"):
		return strings.TrimPrefix(dataURL, "data:image/png;base64,"), "png", ".png", nil
	case strings.HasPrefix(dataURL, "data:image/jpeg;base64,"):
		return strings.TrimPrefix(dataURL, "data:image/jpeg;base64,"), "jpeg", ".jpg", nil
	default:
		return "", "", "", errors.New("unsupported image data URL")
	}
}

func exactBase64DecodedLen(payload string) (int, error) {
	if payload == "" || len(payload)%4 != 0 {
		return 0, errors.New("invalid base64 length")
	}
	padding := 0
	if strings.HasSuffix(payload, "=") {
		padding++
	}
	if strings.HasSuffix(payload, "==") {
		padding++
	}
	if strings.Contains(payload[:len(payload)-padding], "=") {
		return 0, errors.New("invalid base64 padding")
	}
	return len(payload)/4*3 - padding, nil
}

func imagePixelCount(width, height int) (int64, bool) {
	if width <= 0 || height <= 0 || width > MaxImagePixels || height > MaxImagePixels {
		return 0, false
	}
	return int64(width) * int64(height), true
}
