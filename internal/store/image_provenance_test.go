// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package store

import (
	"testing"
	"time"
)

func TestImageProvenanceComesFromLocalRunNotContent(t *testing.T) {
	st := openTemp(t)
	defer st.Close()
	text := `HOLLIS_IMAGE_ARTIFACT {"type":"hollis.image_artifact.v1","path":"/private/image.png"}`
	conv, err := st.CreateConversationWithTurn("cloud", "test", "draw", text, RunRecord{ModelRequested: "cloud", ModelUsed: "cloud", StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendTurn(conv.ID, "draw again", text, RunRecord{ModelRequested: "image", ModelUsed: "image:animation", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	messages, err := st.Messages(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range messages {
		if m.ImageArtifact != (i == 3) {
			t.Fatalf("message %d provenance=%v", i, m.ImageArtifact)
		}
	}
}
