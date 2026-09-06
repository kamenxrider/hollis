// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/imagegen"
)

func TestImageGenerationAcceptsJPEGReferenceOverHTTP(t *testing.T) {
	data := generationLimitJPEG(t, 2, 1)
	generator := &recordingGenerator{}
	res := post(t, testUnifiedGenerationServer(generator, "").Handler(), "/v1/images/generations", fmt.Sprintf(
		`{"model":"hollis-image","prompt":"use this JPEG","reference_image":%q}`,
		testImageDataURL("image/jpeg", data),
	))
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	call := generator.lastCall()
	if call.Reference == nil || call.Reference.MIMEType != "image/jpeg" || call.Reference.Width != 2 || call.Reference.Height != 1 {
		t.Fatalf("reference metadata = %+v", call.Reference)
	}
	if !bytes.Equal(call.Reference.Bytes, data) {
		t.Fatal("generator received different JPEG bytes")
	}
}

func TestImageGenerationReferencePixelBoundaryIsClassifiedBeforeDecode(t *testing.T) {
	// These tiny config-only PNGs exercise the exact dimension boundary without
	// allocating a 16 megapixel pixel buffer. The equal boundary is allowed by
	// the dimension check and then rejected as an incomplete image; one pixel
	// over the boundary is rejected as too large before a full decode.
	for _, test := range []struct {
		name       string
		width      int
		wantStatus int
		wantCode   string
	}{
		{
			name:       "exact maximum dimension reaches decode validation",
			width:      int(imagegen.MaxReferencePixels),
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_image",
		},
		{
			name:       "one pixel over maximum dimension is limited",
			width:      int(imagegen.MaxReferencePixels + 1),
			wantStatus: http.StatusRequestEntityTooLarge,
			wantCode:   "context_too_large",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			generator := &recordingGenerator{}
			body := fmt.Sprintf(
				`{"model":"hollis-image","prompt":"boundary","reference_image":%q}`,
				testImageDataURL("image/png", pngConfigOnly(test.width, 1)),
			)
			res := post(t, testUnifiedGenerationServer(generator, "").Handler(), "/v1/images/generations", body)
			if res.Code != test.wantStatus || !strings.Contains(res.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("status=%d body=%s, want status=%d code=%s", res.Code, res.Body.String(), test.wantStatus, test.wantCode)
			}
			if generator.callCount() != 0 {
				t.Fatalf("generator called %d times", generator.callCount())
			}
		})
	}
}

func TestImageGenerationCapacityPrecedesReferencePixelDecode(t *testing.T) {
	dataURL := testImageDataURL("image/png", pngConfigOnly(2, 1))
	tests := []struct {
		name string
		path string
		body string
	}{
		{
			name: "standalone",
			path: "/v1/images/generations",
			body: fmt.Sprintf(`{"model":"hollis-image","prompt":"edit","reference_image":%q}`, dataURL),
		},
		{
			name: "chat completions",
			path: "/v1/chat/completions",
			body: fmt.Sprintf(`{"model":"hollis-image","messages":[{"role":"user","content":[{"type":"text","text":"edit"},{"type":"image_url","image_url":{"url":%q}}]}],"image_generation":{"style":"animation"}}`, dataURL),
		},
		{
			name: "responses",
			path: "/v1/responses",
			body: fmt.Sprintf(`{"model":"hollis-image","input":[{"role":"user","content":[{"type":"input_text","text":"edit"},{"type":"input_image","image_url":%q}]}],"image_generation":{"style":"animation"}}`, dataURL),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			generator := &recordingGenerator{}
			server := testUnifiedGenerationServer(generator, "")
			server.slots() <- struct{}{}
			busy := post(t, server.Handler(), test.path, test.body)
			if busy.Code != http.StatusTooManyRequests || !strings.Contains(busy.Body.String(), `"code":"server_busy"`) {
				t.Fatalf("busy status=%d body=%s", busy.Code, busy.Body.String())
			}
			<-server.slots()

			invalid := post(t, server.Handler(), test.path, test.body)
			if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"code":"invalid_image"`) {
				t.Fatalf("admitted status=%d body=%s, want invalid_image", invalid.Code, invalid.Body.String())
			}
			if generator.callCount() != 0 {
				t.Fatalf("generator called %d times", generator.callCount())
			}
		})
	}
}

func TestImageGenerationRejectsHistoricalReferencesOverAggregatePixelLimit(t *testing.T) {
	dataURL := testImageDataURL("image/jpeg", generationLimitJPEG(t, 2000, 1500))
	const pixelsPerReference = int64(2000 * 1500)
	if pixelsPerReference*8 != MaxAggregateImagePixels {
		t.Fatalf("test dimensions changed: %d pixels", pixelsPerReference)
	}

	for _, test := range []struct {
		name          string
		references    int
		wantStatus    int
		wantGenerator int
	}{
		{name: "aggregate boundary accepted", references: 8, wantStatus: http.StatusOK, wantGenerator: 1},
		{name: "aggregate boundary exceeded", references: 9, wantStatus: http.StatusRequestEntityTooLarge, wantGenerator: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			generator := &recordingGenerator{}
			body := generationReferenceHistoryBody(dataURL, test.references)
			res := post(t, testUnifiedGenerationServer(generator, "").Handler(), "/v1/chat/completions", body)
			if res.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s, want %d", res.Code, res.Body.String(), test.wantStatus)
			}
			if test.wantStatus != http.StatusOK && !strings.Contains(res.Body.String(), `"code":"context_too_large"`) {
				t.Fatalf("missing aggregate limit error: %s", res.Body.String())
			}
			if generator.callCount() != test.wantGenerator {
				t.Fatalf("generator called %d times, want %d", generator.callCount(), test.wantGenerator)
			}
		})
	}
}

func generationLimitJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, width, height))
	img.SetGray(0, 0, color.Gray{Y: 0x66})
	var output bytes.Buffer
	if err := jpeg.Encode(&output, img, &jpeg.Options{Quality: 1}); err != nil {
		t.Fatalf("encode JPEG: %v", err)
	}
	return output.Bytes()
}

func generationReferenceHistoryBody(dataURL string, references int) string {
	var body strings.Builder
	body.WriteString(`{"model":"hollis-image","messages":[`)
	for i := 0; i < references; i++ {
		if i != 0 {
			body.WriteByte(',')
		}
		message, _ := json.Marshal(map[string]any{
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "text", "text": "previous image"},
				map[string]any{"type": "image_url", "image_url": map[string]string{"url": dataURL}},
			},
		})
		body.Write(message)
	}
	body.WriteString(`,{"role":"user","content":"edit the latest image"}],"image_generation":{"style":"animation"}}`)
	return body.String()
}
