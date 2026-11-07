// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package server

import "github.com/kamenxrider/hollis/internal/store"

// ParseTextMessages shares the server's strict role/content validation with
// one-shot CLI conversations. Developer messages become system instructions.
func ParseTextMessages(data []byte) ([]store.Message, error) {
	incoming, err := parseChatMessagesInput(data)
	if err != nil {
		return nil, err
	}
	messages, err := parseMessages(incoming)
	if err != nil {
		return nil, err
	}
	out := make([]store.Message, len(messages))
	for i, m := range messages {
		out[i] = store.Message{Role: m.Role, Content: m.Content}
	}
	return out, nil
}
