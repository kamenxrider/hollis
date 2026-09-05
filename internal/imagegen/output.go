// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package imagegen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// OutputOptions describes deterministic, local post-processing for a
// generated PNG. The native Image Playground action does not expose these
// controls, so Hollis applies them after generation in its private staging
// directory.
type OutputOptions struct {
	AspectRatio string `json:"aspect_ratio,omitempty"`
	Size        string `json:"size,omitempty"`
	Fit         string `json:"fit,omitempty"`
}

var (
	// ErrInvalidOutputOptions identifies malformed or contradictory output
	// controls. It is kept separate from image decoding and filesystem errors
	// so callers can report a usage failure without retrying generation.
	ErrInvalidOutputOptions = errors.New("invalid image output options")
	// ErrOutputTooLarge identifies a valid-looking output that cannot be
	// represented within the bounded image contract.
	ErrOutputTooLarge = errors.New("requested image output exceeds the image limit")
)

type parsedOutputOptions struct {
	hasAspect bool
	hasSize   bool
	ratioW    uint64
	ratioH    uint64
	sizeW     int
	sizeH     int
	fit       string
}

// ValidateOutputOptions validates post-generation output controls without
// touching a file or invoking a provider.
func ValidateOutputOptions(options OutputOptions) error {
	_, err := parseOutputOptions(options)
	return err
}

// TransformOutput applies validated crop, pad, and resize controls to a
// verified PNG. The original staged file remains untouched. A successful
// transformed result shares the original cleanup state and adds its private
// output path to that state; callers therefore keep the usual one Cleanup
// call regardless of whether transformation occurred.
func TransformOutput(ctx context.Context, result Result, options OutputOptions) (Result, error) {
	parsed, err := parseOutputOptions(options)
	if err != nil {
		return Result{}, err
	}
	if !parsed.hasAspect && !parsed.hasSize {
		return result, nil
	}
	if ctx == nil {
		return Result{}, errors.New("image output transformation requires a non-nil context")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	// Revalidate the input before decoding it. verifyPNG bounds both the
	// encoded bytes and decoded pixel count, and gives us a checksum to compare
	// against the bounded read below in case the file changes unexpectedly.
	verified, err := verifyPNG(result.Path)
	if err != nil {
		return Result{}, err
	}
	contents, err := readBoundedPNG(result.Path, verified)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	source, err := png.Decode(bytes.NewReader(contents))
	if err != nil {
		return Result{}, fmt.Errorf("%w: decode source PNG: %v", ErrInvalidPNG, err)
	}

	inputWidth, inputHeight := verified.Width, verified.Height
	fitWidth, fitHeight, err := fitDimensions(inputWidth, inputHeight, parsed)
	if err != nil {
		return Result{}, err
	}

	var transformed *image.RGBA
	switch parsed.fit {
	case "crop":
		transformed, err = cropImage(ctx, source, fitWidth, fitHeight)
	case "pad":
		transformed, err = padImage(ctx, source, fitWidth, fitHeight)
	default:
		// parseOutputOptions makes this unreachable for a non-zero request.
		err = fmt.Errorf("%w: fit must be crop or pad", ErrInvalidOutputOptions)
	}
	if err != nil {
		return Result{}, err
	}

	if parsed.hasSize {
		transformed, err = resizeBilinear(ctx, transformed, parsed.sizeW, parsed.sizeH)
		if err != nil {
			return Result{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	return writeTransformed(ctx, result, transformed)
}

func parseOutputOptions(options OutputOptions) (parsedOutputOptions, error) {
	parsed := parsedOutputOptions{fit: options.Fit}
	parsed.hasAspect = options.AspectRatio != ""
	parsed.hasSize = options.Size != ""
	if parsed.hasAspect && parsed.hasSize {
		return parsed, fmt.Errorf("%w: aspect_ratio and size are mutually exclusive", ErrInvalidOutputOptions)
	}
	if !parsed.hasAspect && !parsed.hasSize {
		if options.Fit != "" {
			return parsed, fmt.Errorf("%w: fit requires aspect_ratio or size", ErrInvalidOutputOptions)
		}
		return parsed, nil
	}
	if options.Fit != "crop" && options.Fit != "pad" {
		return parsed, fmt.Errorf("%w: fit must be exactly crop or pad", ErrInvalidOutputOptions)
	}

	if parsed.hasAspect {
		width, height, err := parsePair(options.AspectRatio, ':', "aspect_ratio")
		if err != nil {
			return parsed, err
		}
		parsed.ratioW, parsed.ratioH = width, height
	}
	if parsed.hasSize {
		width, height, err := parsePair(options.Size, 'x', "size")
		if err != nil {
			return parsed, err
		}
		if width > uint64(MaxPixels) || height > uint64(MaxPixels) || width > uint64(maxInt()) || height > uint64(maxInt()) {
			return parsed, fmt.Errorf("%w: size dimensions are too large", ErrOutputTooLarge)
		}
		if width > uint64(MaxPixels)/height {
			return parsed, fmt.Errorf("%w: size has more than %d pixels", ErrOutputTooLarge, MaxPixels)
		}
		parsed.sizeW, parsed.sizeH = int(width), int(height)
		parsed.ratioW, parsed.ratioH = width, height
	}
	return parsed, nil
}

func parsePair(value string, separator byte, label string) (uint64, uint64, error) {
	value = strings.TrimSpace(value)
	parts := strings.Split(value, string(separator))
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%w: %s must be WIDTH%cHEIGHT", ErrInvalidOutputOptions, label, separator)
	}
	width, err := parsePositiveUint(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("%w: invalid %s width: %v", ErrInvalidOutputOptions, label, err)
	}
	height, err := parsePositiveUint(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("%w: invalid %s height: %v", ErrInvalidOutputOptions, label, err)
	}
	return width, height, nil
}

func parsePositiveUint(value string) (uint64, error) {
	if value == "" {
		return 0, errors.New("value is empty")
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return 0, errors.New("value must contain only decimal digits")
		}
	}
	valueNumber, err := strconv.ParseUint(value, 10, 64)
	if err != nil || valueNumber == 0 {
		if err != nil {
			return 0, errors.New("value overflows an unsigned integer")
		}
		return 0, errors.New("value must be positive")
	}
	return valueNumber, nil
}

func maxInt() int {
	return int(^uint(0) >> 1)
}

func fitDimensions(inputWidth, inputHeight int, options parsedOutputOptions) (int, int, error) {
	targetWidth, targetHeight := options.ratioW, options.ratioH
	if targetWidth == 0 || targetHeight == 0 {
		return 0, 0, fmt.Errorf("%w: output ratio is empty", ErrInvalidOutputOptions)
	}

	inputW, inputH := uint64(inputWidth), uint64(inputHeight)
	comparison := compareRatio(inputW, inputH, targetWidth, targetHeight)
	var width, height uint64
	switch {
	case comparison == 0:
		width, height = inputW, inputH
	case comparison > 0: // source is wider than the requested ratio
		if options.fit == "crop" {
			height = inputH
			width = scaledFloor(inputH, targetWidth, targetHeight)
		} else {
			width = inputW
			height = scaledCeil(inputW, targetHeight, targetWidth)
		}
	default: // source is taller/narrower than the requested ratio
		if options.fit == "crop" {
			width = inputW
			height = scaledFloor(inputW, targetHeight, targetWidth)
		} else {
			height = inputH
			width = scaledCeil(inputH, targetWidth, targetHeight)
		}
	}
	if width == 0 || height == 0 {
		return 0, 0, fmt.Errorf("%w: requested ratio cannot be represented by positive pixels", ErrInvalidOutputOptions)
	}
	if width > uint64(maxInt()) || height > uint64(maxInt()) {
		return 0, 0, fmt.Errorf("%w: fitted dimensions overflow the platform integer size", ErrOutputTooLarge)
	}
	if width > uint64(MaxPixels)/height {
		return 0, 0, fmt.Errorf("%w: fitted image has more than %d pixels", ErrOutputTooLarge, MaxPixels)
	}
	return int(width), int(height), nil
}

func compareRatio(width, height, ratioWidth, ratioHeight uint64) int {
	left := new(big.Int).Mul(new(big.Int).SetUint64(width), new(big.Int).SetUint64(ratioHeight))
	right := new(big.Int).Mul(new(big.Int).SetUint64(height), new(big.Int).SetUint64(ratioWidth))
	return left.Cmp(right)
}

func scaledFloor(value, numerator, denominator uint64) uint64 {
	product := new(big.Int).Mul(new(big.Int).SetUint64(value), new(big.Int).SetUint64(numerator))
	product.Quo(product, new(big.Int).SetUint64(denominator))
	if !product.IsUint64() {
		return 0
	}
	return product.Uint64()
}

func scaledCeil(value, numerator, denominator uint64) uint64 {
	product := new(big.Int).Mul(new(big.Int).SetUint64(value), new(big.Int).SetUint64(numerator))
	divisor := new(big.Int).SetUint64(denominator)
	product.Add(product, new(big.Int).Sub(divisor, big.NewInt(1)))
	product.Quo(product, divisor)
	if !product.IsUint64() {
		return 0
	}
	return product.Uint64()
}

func cropImage(ctx context.Context, source image.Image, width, height int) (*image.RGBA, error) {
	bounds := source.Bounds()
	left := bounds.Min.X + (bounds.Dx()-width)/2
	top := bounds.Min.Y + (bounds.Dy()-height)/2
	destination := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := 0; x < width; x++ {
			if x&255 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			destination.SetRGBA(x, y, color.RGBAModel.Convert(source.At(left+x, top+y)).(color.RGBA))
		}
	}
	return destination, nil
}

func padImage(ctx context.Context, source image.Image, width, height int) (*image.RGBA, error) {
	bounds := source.Bounds()
	left := (width - bounds.Dx()) / 2
	top := (height - bounds.Dy()) / 2
	destination := image.NewRGBA(image.Rect(0, 0, width, height))
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	for y := 0; y < height; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := 0; x < width; x++ {
			if x&255 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			destination.SetRGBA(x, y, white)
		}
	}
	for y := 0; y < bounds.Dy(); y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := 0; x < bounds.Dx(); x++ {
			if x&255 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			pixel := color.RGBAModel.Convert(source.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.RGBA)
			destination.SetRGBA(left+x, top+y, overWhite(pixel))
		}
	}
	return destination, nil
}

func overWhite(pixel color.RGBA) color.RGBA {
	alpha := uint32(pixel.A)
	red := (uint32(pixel.R)*alpha + 255*(255-alpha) + 127) / 255
	green := (uint32(pixel.G)*alpha + 255*(255-alpha) + 127) / 255
	blue := (uint32(pixel.B)*alpha + 255*(255-alpha) + 127) / 255
	return color.RGBA{R: uint8(red), G: uint8(green), B: uint8(blue), A: 255}
}

func resizeBilinear(ctx context.Context, source image.Image, width, height int) (*image.RGBA, error) {
	bounds := source.Bounds()
	sourceWidth, sourceHeight := bounds.Dx(), bounds.Dy()
	destination := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sourceY := (float64(y)+0.5)*float64(sourceHeight)/float64(height) - 0.5
		y0 := int(math.Floor(sourceY))
		fractionY := sourceY - float64(y0)
		if y0 < 0 {
			y0, fractionY = 0, 0
		}
		y1 := y0 + 1
		if y1 >= sourceHeight {
			y1 = sourceHeight - 1
		}
		for x := 0; x < width; x++ {
			if x&255 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			sourceX := (float64(x)+0.5)*float64(sourceWidth)/float64(width) - 0.5
			x0 := int(math.Floor(sourceX))
			fractionX := sourceX - float64(x0)
			if x0 < 0 {
				x0, fractionX = 0, 0
			}
			x1 := x0 + 1
			if x1 >= sourceWidth {
				x1 = sourceWidth - 1
			}
			c00 := color.RGBAModel.Convert(source.At(bounds.Min.X+x0, bounds.Min.Y+y0)).(color.RGBA)
			c10 := color.RGBAModel.Convert(source.At(bounds.Min.X+x1, bounds.Min.Y+y0)).(color.RGBA)
			c01 := color.RGBAModel.Convert(source.At(bounds.Min.X+x0, bounds.Min.Y+y1)).(color.RGBA)
			c11 := color.RGBAModel.Convert(source.At(bounds.Min.X+x1, bounds.Min.Y+y1)).(color.RGBA)
			destination.SetRGBA(x, y, bilinearPixel(c00, c10, c01, c11, fractionX, fractionY))
		}
	}
	return destination, nil
}

func bilinearPixel(topLeft, topRight, bottomLeft, bottomRight color.RGBA, fractionX, fractionY float64) color.RGBA {
	interpolate := func(a, b, c, d uint8) uint8 {
		upper := float64(a)*(1-fractionX) + float64(b)*fractionX
		lower := float64(c)*(1-fractionX) + float64(d)*fractionX
		value := upper*(1-fractionY) + lower*fractionY
		if value < 0 {
			return 0
		}
		if value > 255 {
			return 255
		}
		return uint8(value + 0.5)
	}
	return color.RGBA{
		R: interpolate(topLeft.R, topRight.R, bottomLeft.R, bottomRight.R),
		G: interpolate(topLeft.G, topRight.G, bottomLeft.G, bottomRight.G),
		B: interpolate(topLeft.B, topRight.B, bottomLeft.B, bottomRight.B),
		A: interpolate(topLeft.A, topRight.A, bottomLeft.A, bottomRight.A),
	}
}

func readBoundedPNG(path string, verified Result) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect source PNG: %v", ErrInvalidPNG, err)
	}
	if info.Size() <= 0 {
		return nil, ErrNoOutput
	}
	if info.Size() > MaxOutputBytes {
		return nil, fmt.Errorf("%w: source PNG exceeds %d bytes", ErrImageTooLarge, MaxOutputBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: open source PNG: %v", ErrInvalidPNG, err)
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, MaxOutputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read source PNG: %v", ErrInvalidPNG, err)
	}
	if int64(len(contents)) != info.Size() || int64(len(contents)) > MaxOutputBytes {
		return nil, fmt.Errorf("%w: source PNG changed during validation", ErrInvalidPNG)
	}
	hash := sha256.Sum256(contents)
	if fmt.Sprintf("%x", hash) != verified.SHA256 {
		return nil, fmt.Errorf("%w: source PNG changed during decode", ErrInvalidPNG)
	}
	return contents, nil
}

func writeTransformed(ctx context.Context, original Result, imageToWrite image.Image) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	directory := filepath.Dir(original.Path)
	temporary, err := os.CreateTemp(directory, ".hollis-image-transform-*.tmp")
	if err != nil {
		return Result{}, fmt.Errorf("%w: create transformed staging file: %v", ErrInvalidPNG, err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	defer func() {
		if temporaryPath != "" {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		removeTemporary()
		return Result{}, fmt.Errorf("%w: protect transformed staging file: %v", ErrInvalidPNG, err)
	}
	if err := png.Encode(temporary, imageToWrite); err != nil {
		removeTemporary()
		return Result{}, fmt.Errorf("%w: encode transformed PNG: %v", ErrInvalidPNG, err)
	}
	if err := temporary.Sync(); err != nil {
		removeTemporary()
		return Result{}, fmt.Errorf("%w: sync transformed PNG: %v", ErrInvalidPNG, err)
	}
	if err := temporary.Close(); err != nil {
		removeTemporary()
		return Result{}, fmt.Errorf("%w: close transformed PNG: %v", ErrInvalidPNG, err)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	// The random temporary name is already unique inside the private staging
	// directory. Rename it to a second private name so no caller can observe a
	// partially written PNG, while never replacing the original result path.
	finalPath := temporaryPath + ".png"
	if _, err := os.Lstat(finalPath); err == nil {
		return Result{}, fmt.Errorf("%w: transformed staging path already exists", ErrInvalidPNG)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, fmt.Errorf("%w: inspect transformed staging path: %v", ErrInvalidPNG, err)
	}
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return Result{}, fmt.Errorf("%w: commit transformed PNG: %v", ErrInvalidPNG, err)
	}
	temporaryPath = ""
	validated, err := verifyPNG(finalPath)
	if err != nil {
		_ = os.Remove(finalPath)
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		_ = os.Remove(finalPath)
		return Result{}, err
	}
	cleanup := original.cleanup
	if cleanup == nil {
		cleanup = &cleanupState{}
	}
	cleanup.paths = append(cleanup.paths, finalPath)
	validated.cleanup = cleanup
	return validated, nil
}
