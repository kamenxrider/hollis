// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package imagegen

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// ConversationMessage is the text context supplied to an image generation
// turn. Generated pixels are deliberately not represented here.
type ConversationMessage struct {
	Role    string
	Content string
}

const (
	cliArtifactPrefix        = "HOLLIS_IMAGE_ARTIFACT "
	apiReplayMarkerSuffix    = "). The server does not retain image pixels; replay the complete assistant image message to supply a later reference. Pixel editing is not guaranteed.]"
	apiMarkerPrefix          = "[Hollis generated an image for "
	apiReferenceMarkerSuffix = "). A supplied reference image was sent to the configured Shortcut; Hollis does not guarantee that a backend reused or edited its pixels.]"
	apiMarkerSuffix          = "). The image pixels are included in this response but are not retained or reused; replay this text to preserve the generation record. A later request uses text context to generate a new image, not pixel editing.]"
)

var apiMarkerDetails = regexp.MustCompile(`^ with style (?:any|animation|genmoji|illustration|sketch|chatgpt) \(native [1-9][0-9]*x[1-9][0-9]*, final [1-9][0-9]*x[1-9][0-9]*, (?:no output transform|local (?:crop|pad) fit to (?:aspect ratio [1-9][0-9]*:[1-9][0-9]*|size [1-9][0-9]*x[1-9][0-9]*)), SHA-256 [0-9a-f]{64}$`)

// RenderConversationPrompt renders image-specific text context. It removes
// only Hollis' own machine-readable image records; all other conversation
// text remains in order. A simple image revision uses the prompt shape proven
// against Image Playground rather than the text-chat replay instructions.
func RenderConversationPrompt(messages []ConversationMessage) string {
	filtered := make([]ConversationMessage, 0, len(messages))
	removedArtifact := false
	for _, message := range messages {
		if message.Role == "assistant" {
			if isCLIGenerationArtifact(message.Content) {
				removedArtifact = true
				continue
			}
			if request, ok := apiGenerationMarkerRequest(message.Content); ok {
				removedArtifact = true
				if !hasUserContent(messages, request) {
					filtered = append(filtered, ConversationMessage{Role: "user", Content: request})
				}
				continue
			}
		}
		filtered = append(filtered, message)
	}

	if len(filtered) == 1 && filtered[0].Role == "user" {
		return filtered[0].Content
	}
	if removedArtifact && allUserMessages(filtered) && len(filtered) > 1 {
		return renderImageRevisions(filtered)
	}

	var output strings.Builder
	for i, message := range filtered {
		if i > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(strings.ToUpper(message.Role))
		output.WriteString(":\n")
		output.WriteString(message.Content)
	}
	return output.String()
}

// RenderConversationPromptWithReference puts the latest revision first while
// retaining the earlier subject and text context. Live controls showed that
// chronological revisions could bury a new setting, while latest-only text
// could lose the subject even with a reference attached. Callers still validate
// the complete unfiltered transcript before this rendering step.
func RenderConversationPromptWithReference(messages []ConversationMessage, hasReference bool) string {
	if !hasReference || len(messages) == 0 || messages[len(messages)-1].Role != "user" {
		return RenderConversationPrompt(messages)
	}
	last := messages[len(messages)-1].Content
	prior := RenderConversationPrompt(messages[:len(messages)-1])
	if strings.TrimSpace(prior) == "" {
		return last
	}
	return "Requested revision:\n" + last + "\n\nPrevious image context:\n" + prior
}

func allUserMessages(messages []ConversationMessage) bool {
	for _, message := range messages {
		if message.Role != "user" {
			return false
		}
	}
	return true
}

func renderImageRevisions(messages []ConversationMessage) string {
	var output strings.Builder
	output.WriteString("Original image description:\n")
	output.WriteString(messages[0].Content)
	for i, message := range messages[1:] {
		output.WriteString("\n\nRequested revision")
		if len(messages) > 2 {
			output.WriteString(" ")
			output.WriteString(strconv.Itoa(i + 1))
		}
		output.WriteString(":\n")
		output.WriteString(message.Content)
	}
	return output.String()
}

func hasUserContent(messages []ConversationMessage, content string) bool {
	for _, message := range messages {
		if message.Role == "user" && message.Content == content {
			return true
		}
	}
	return false
}

func isCLIGenerationArtifact(content string) bool {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, cliArtifactPrefix) {
		var artifact struct {
			Type string `json:"type"`
		}
		payload := strings.TrimPrefix(trimmed, cliArtifactPrefix)
		return json.Unmarshal([]byte(payload), &artifact) == nil && artifact.Type == "hollis.image_artifact.v1"
	}
	return false
}

func apiGenerationMarkerRequest(content string) (string, bool) {
	content = strings.TrimSpace(content)
	suffix := apiMarkerSuffix
	if strings.HasSuffix(content, apiReferenceMarkerSuffix) {
		suffix = apiReferenceMarkerSuffix
	} else if strings.HasSuffix(content, apiReplayMarkerSuffix) {
		suffix = apiReplayMarkerSuffix
	}
	if !strings.HasPrefix(content, apiMarkerPrefix) || !strings.HasSuffix(content, suffix) {
		return "", false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(content, apiMarkerPrefix), suffix)
	quotedEnd := quotedStringEnd(body)
	if quotedEnd < 0 {
		return "", false
	}
	request, err := strconv.Unquote(body[:quotedEnd])
	if err != nil {
		return "", false
	}
	return request, apiMarkerDetails.MatchString(body[quotedEnd:])
}

func quotedStringEnd(value string) int {
	if len(value) == 0 || value[0] != '"' {
		return -1
	}
	escaped := false
	for i := 1; i < len(value); i++ {
		if escaped {
			escaped = false
			continue
		}
		switch value[i] {
		case '\\':
			escaped = true
		case '"':
			return i + 1
		}
	}
	return -1
}
