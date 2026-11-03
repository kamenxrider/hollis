// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package store

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

func completedToolMessages() []Message {
	return []Message{
		{Role: "user", Content: "private question"},
		{Role: "assistant", Content: `{"hollis_tool":1,"calls":[{"id":"call_1_0","name":"fixture__read","arguments":"{\"key\":\"private argument\"}"}]}`},
		{Role: "tool", Content: `{"hollis_tool":1,"call_id":"call_1_0","output":"framed private result"}`},
		{Role: "assistant", Content: "private answer"},
	}
}

func TestToolTurnsPersistOrderedTranscriptWithoutDiagnosticContent(t *testing.T) {
	st := openTemp(t)
	defer st.Close()
	messages := completedToolMessages()
	run := RunRecord{ModelRequested: "local", ModelUsed: "local", StartedAt: time.Now(), RequestBytes: 80, ResponseBytes: 20}
	conv, err := st.CreateConversationWithToolTurn("local", "tool turn", messages, run)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendToolTurn(conv.ID, messages, run); err != nil {
		t.Fatal(err)
	}
	got, err := st.Messages(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2*len(messages) {
		t.Fatalf("stored %d messages, want %d", len(got), 2*len(messages))
	}
	for i, message := range got {
		want := messages[i%len(messages)]
		if message.Seq != int64(i) || message.Role != want.Role || message.Content != want.Content || message.ImageArtifact {
			t.Fatalf("message %d = %+v, want role %q content %q without image provenance", i, message, want.Role, want.Content)
		}
		var metadata sql.NullString
		if err := st.db.QueryRow(`SELECT metadata_json FROM messages WHERE id = ?`, message.ID).Scan(&metadata); err != nil {
			t.Fatal(err)
		}
		if metadata.Valid != (i%len(messages) == len(messages)-1) {
			t.Fatalf("message %d metadata = %+v; only final assistant gets diagnostics", i, metadata)
		}
	}
	rows, err := st.db.Query(`SELECT * FROM runs`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(fmt.Sprint(values), "private") || strings.Contains(fmt.Sprint(values), "hollis_tool") {
			t.Fatalf("run diagnostics contain transcript content: %v", values)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("run count = %d, want 2", count)
	}
}

func TestToolTurnRollbackIncludesConversationMessagesAndDiagnostics(t *testing.T) {
	for _, failure := range []string{"tool", "final"} {
		t.Run(failure, func(t *testing.T) {
			st := openTemp(t)
			defer st.Close()
			run := RunRecord{Model: "local", StartedAt: time.Now()}
			conv, err := st.CreateConversationWithTurn("local", "existing", "before", "prior answer", run)
			if err != nil {
				t.Fatal(err)
			}
			condition := "NEW.role = 'tool'"
			if failure == "final" {
				condition = "NEW.content = 'private answer'"
			}
			if _, err := st.db.Exec(`CREATE TRIGGER reject_tool_turn BEFORE INSERT ON messages WHEN ` + condition + ` BEGIN SELECT RAISE(ABORT, 'injected write failure'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err := st.CreateConversationWithToolTurn("local", "failed new", completedToolMessages(), run); err == nil {
				t.Fatal("new tool turn unexpectedly succeeded")
			}
			if err := st.AppendToolTurn(conv.ID, completedToolMessages(), run); err == nil {
				t.Fatal("existing tool turn unexpectedly succeeded")
			}
			var conversations, messages, runs int
			if err := st.db.QueryRow(`SELECT (SELECT COUNT(*) FROM conversations), (SELECT COUNT(*) FROM messages), (SELECT COUNT(*) FROM runs)`).Scan(&conversations, &messages, &runs); err != nil {
				t.Fatal(err)
			}
			if conversations != 1 || messages != 2 || runs != 1 {
				t.Fatalf("rollback left conversations/messages/runs = %d/%d/%d, want 1/2/1", conversations, messages, runs)
			}
			after, err := st.GetConversation(conv.ID)
			if err != nil || after.UpdatedAt != conv.UpdatedAt {
				t.Fatalf("failed append changed conversation: %+v, %v", after, err)
			}
		})
	}
}

func TestToolTurnsRejectIncompleteTranscript(t *testing.T) {
	for _, messages := range [][]Message{
		nil,
		{{Role: "user", Content: "question"}},
		{{Role: "assistant"}, {Role: "assistant"}},
		{{Role: "user"}, {Role: "tool"}},
		{{Role: "user"}, {Role: "system"}, {Role: "assistant"}},
	} {
		st := openTemp(t)
		if _, err := st.CreateConversationWithToolTurn("local", "invalid", messages, RunRecord{}); err == nil {
			t.Errorf("accepted incomplete turn %+v", messages)
		}
		conversations, err := st.ListConversations(true)
		if err != nil || len(conversations) != 0 {
			t.Fatalf("invalid turn left conversations %+v, %v", conversations, err)
		}
		st.Close()
	}
}
