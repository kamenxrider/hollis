//go:build hollis_live

package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/imagegen"
)

// Explicit build tag plus runtime opt-in: ordinary tests cannot call a model.
// A known installed bridge and a new private output directory are required.
// This exercises the actual experimental CLI and transport, at most two calls.
func TestLiveImageGeneration(t *testing.T) {
	if os.Getenv("HOLLIS_LIVE_IMAGE") != "1" {
		t.Skip("requires HOLLIS_LIVE_IMAGE=1 and explicit bridge/output")
	}
	bridge, directory := os.Getenv("HOLLIS_IMAGE_BRIDGE"), os.Getenv("HOLLIS_IMAGE_OUTPUT")
	if bridge == "" || !filepath.IsAbs(directory) {
		t.Fatal("explicit bridge and absolute new output directory required")
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for index, prompt := range []string{
		"A simple red circle centered on a plain white background. No text.",
		"A simple blue square centered on a plain white background. No text.",
	} {
		if index > 0 {
			time.Sleep(15 * time.Second)
		}
		name := []string{"red-circle", "blue-square"}[index]
		output := filepath.Join(directory, name+".png")
		generator := imagegen.New()
		generator.TempDir = directory
		cmd, _ := newRootCmdWithImageGenerator(newRunnerDefault, generator)
		cmd.SetContext(t.Context())
		cmd.SetArgs([]string{"--json", "image", "generate", prompt, "--bridge", bridge, "--output", output, "--timeout", "120s"})
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		started := time.Now()
		err := cmd.Execute()
		record := map[string]any{"bridge": bridge, "prompt": prompt, "seconds": time.Since(started).Seconds(),
			"stdout": stdout.String(), "stderr": stderr.String(), "success": err == nil}
		if err != nil {
			record["error"] = err.Error()
		}
		encoded, marshalErr := json.MarshalIndent(record, "", "  ")
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if writeErr := os.WriteFile(filepath.Join(directory, name+".json"), append(encoded, '\n'), 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
		if err != nil {
			t.Fatalf("stopping after first unsuccessful generation: %v", err)
		}
		data, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := png.Decode(bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		var metadata struct {
			Path     string `json:"path"`
			Bytes    int    `json:"bytes"`
			Checksum string `json:"checksum"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &metadata); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		if metadata.Path != output || metadata.Bytes != len(data) || metadata.Checksum != hex.EncodeToString(digest[:]) {
			t.Fatal("CLI metadata does not match actual PNG")
		}
		t.Logf("%s: %dx%d PNG, %d bytes, elapsed %.1fs; visual and unattended UI checks still require observation", name, config.Width, config.Height, len(data), time.Since(started).Seconds())
	}
}
