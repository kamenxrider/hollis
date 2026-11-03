// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func imageReady() string {
	return nativePrint(nativeLine("status", map[string]any{
		"available": true,
		"capabilities": map[string]any{"images": map[string]any{
			"supported": true, "max_images": 1, "max_side_px": 4096, "formats": []string{"png", "jpeg"},
		}},
	}))
}

func TestNativeImageStagesPrivateBytesAndCleansUp(t *testing.T) {
	image := writeTestImage(t, "source.png")
	r, request, staged := captureExtract(t, imageReady()+nativeDone("red", true))
	result, err := r.RunCompleteWithImages(t.Context(), ModelLocal, "Describe", []string{image})
	if err != nil || result.Text != "red" || result.Usage == nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	raw, _ := os.ReadFile(request)
	var wire nativeRequest
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Operation != "complete" || len(wire.Images) != 1 || wire.Images[0].Path == image ||
		wire.Source != nil || wire.ToolCalling != "" || len(wire.Schema) != 0 {
		t.Fatalf("unexpected request shape: %+v", wire)
	}
	want, _ := os.ReadFile(image)
	got, _ := os.ReadFile(staged)
	if string(want) != string(got) {
		t.Fatal("staged image changed")
	}
	modes, _ := os.ReadFile(staged + ".mode")
	if string(modes) != "600\n700\n" {
		t.Fatalf("modes=%q", modes)
	}
	if _, err := os.Stat(wire.Images[0].Path); !os.IsNotExist(err) {
		t.Fatal("private staged image survived completion")
	}
}

func TestNativeImageRefusalsAndCleanup(t *testing.T) {
	image := writeTestImage(t, "source.png")
	for _, tc := range []struct {
		response string
		kind     Kind
	}{
		{nativeReady() + nativeDone("must not be accepted", true), KindUnsupportedCapability},
		{imageReady() + nativePrint(nativeLine("error", map[string]any{"kind": "invalid_image"})), KindInvalidImage},
		{imageReady() + nativePrint(nativeLine("error", map[string]any{"kind": "unsupported_os"})), KindUnsupportedOS},
		{imageReady() + nativePrint(nativeLine("error", map[string]any{"kind": "unsupported_media"})), KindUnsupportedMedia},
	} {
		r, _, staged := captureExtract(t, tc.response)
		_, err := r.RunCompleteWithImages(t.Context(), ModelLocal, "Describe", []string{image})
		requireNativeKind(t, err, tc.kind)
		path, _ := os.ReadFile(staged + ".path")
		if _, err := os.Stat(strings.TrimSpace(string(path))); !os.IsNotExist(err) {
			t.Fatal("staged image survived failure")
		}
	}
}

func TestNativeImageCountRefusedBeforeFileRead(t *testing.T) {
	r := &NativeRunner{}
	_, err := r.RunCompleteWithImages(t.Context(), ModelLocal, "Describe", []string{"/missing/one.png", "/missing/two.png"})
	requireNativeKind(t, err, KindUsage)
	if !strings.Contains(err.Error(), "maximum is 1") {
		t.Fatal(err)
	}
}

func TestNativeImagesRefuseToolModesBeforeHelperAccess(t *testing.T) {
	for _, mode := range []string{"allowed", "disallowed"} {
		// No helper is installed on this runner. A usage error proves that
		// the mixed request is refused before helper lookup or file access.
		r := &NativeRunner{}
		_, _, err := r.invoke(t.Context(), ModelLocal, "complete", "Describe", nil, nil,
			nativeOptions{ToolCalling: mode, Images: []nativeSource{{Path: "/absent/image.png"}}})
		requireNativeKind(t, err, KindUsage)
	}
}

func TestNativeImageStreamingUsesAttachment(t *testing.T) {
	image := writeTestImage(t, "source.png")
	response := imageReady() + nativePrint(nativeLine("snapshot", map[string]any{"text": "red"})) +
		nativePrint(nativeLine("snapshot", map[string]any{"text": "red circle"})) + nativeDone("red circle", false)
	r, request, _ := captureExtract(t, response)
	var text strings.Builder
	result, err := r.StreamWithImages(t.Context(), ModelLocal, "Describe", []string{image}, func(delta string) error {
		text.WriteString(delta)
		return nil
	})
	if err != nil || result.Text != "red circle" || text.String() != result.Text || result.Usage != nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	raw, _ := os.ReadFile(request)
	var wire nativeRequest
	_ = json.Unmarshal(raw, &wire)
	if wire.Operation != "stream" || len(wire.Images) != 1 {
		t.Fatal("stream lost image attachment")
	}
}
