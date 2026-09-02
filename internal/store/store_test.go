// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	return mustOpen(t, filepath.Join(t.TempDir(), "hollis.db"))
}

func mustOpen(t *testing.T, path string) *Store {
	t.Helper()
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestConversationLifecycle(t *testing.T) {
	st := openTemp(t)
	defer st.Close()

	conv, err := st.CreateConversation("cloud", "")
	if err != nil {
		t.Fatal(err)
	}
	if conv.ID == "" {
		t.Fatal("empty conversation id")
	}

	got, err := st.GetConversation(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != conv.ID || got.Model != "cloud" {
		t.Fatalf("conversation mismatch: %+v", got)
	}

	if _, err := st.AppendMessage(conv.ID, "user", "Remember VANTA-ORBIT-7319"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(conv.ID, "assistant", "ACK"); err != nil {
		t.Fatal(err)
	}
	msgs, err := st.Messages(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("message count = %d, want 2", len(msgs))
	}
	if msgs[0].Seq != 0 || msgs[1].Seq != 1 {
		t.Fatalf("seqs = %d,%d want 0,1", msgs[0].Seq, msgs[1].Seq)
	}
	if n, _ := st.MessageCount(conv.ID); n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}

	if err := st.SetTitle(conv.ID, "Gateway design"); err != nil {
		t.Fatal(err)
	}
	got, err = st.GetConversation(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Gateway design" {
		t.Fatalf("title = %q", got.Title)
	}

	if err := st.DeleteConversation(conv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetConversation(conv.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound after delete, got %v", err)
	}
}

func TestAppendMessageSequencing(t *testing.T) {
	st := openTemp(t)
	defer st.Close()

	for i := 0; i < 3; i++ {
		m, err := st.AppendMessage("conv-x", "user", "msg")
		if err != nil {
			t.Fatal(err)
		}
		if m.Seq != int64(i) {
			t.Fatalf("seq = %d, want %d", m.Seq, i)
		}
	}
	if n, _ := st.MessageCount("conv-x"); n != 3 {
		t.Fatalf("count = %d, want 3", n)
	}
}

func TestDeleteRemovesEverything(t *testing.T) {
	st := openTemp(t)
	defer st.Close()

	if _, err := st.CreateConversation("cloud", "t"); err != nil {
		t.Fatal(err)
	}
	convs, _ := st.ListConversations(true)
	if len(convs) != 1 {
		t.Fatalf("want 1 conversation, got %d", len(convs))
	}
	id := convs[0].ID
	_, _ = st.AppendMessage(id, "user", "hello")
	if err := st.DeleteConversation(id); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetConversation(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound after delete, got %v", err)
	}
}

func TestRecordRun(t *testing.T) {
	st := openTemp(t)
	defer st.Close()
	if err := st.RecordRun("conv-x", "cloud", time.Now(), 1234, 0, "", ""); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
}

func TestSearchFindsMessagePhrase(t *testing.T) {
	st := openTemp(t)
	defer st.Close()

	a, err := st.CreateConversation("cloud", "orbit chat")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateConversation("cloud", "flat chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(a.ID, "user", "Remember the codeword VANTA-ORBIT-7319 for later"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(b.ID, "user", "The heating is broken again"); err != nil {
		t.Fatal(err)
	}

	matches, err := st.Search("VANTA-ORBIT-7319", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].ID != a.ID {
		t.Fatalf("matches = %+v, want only %s", matches, a.ID)
	}
	if len(matches[0].Hits) == 0 || matches[0].Hits[0].Seq != 0 {
		t.Fatalf("hits = %+v, want one seq-0 hit", matches[0].Hits)
	}
	hit := matches[0].Hits[0]
	if hit.Role != "user" || !strings.Contains(hit.Snippet, "VANTA-ORBIT-7319") {
		t.Fatalf("hit = %+v, want user snippet with the phrase", hit)
	}
}

func TestSearchPhraseOrderMatters(t *testing.T) {
	st := openTemp(t)
	defer st.Close()

	conv, err := st.CreateConversation("cloud-pro", "Gateway design notes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(conv.ID, "user", "body without those words"); err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateConversation("cloud", "design first, gateway later")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(other.ID, "user", "body"); err != nil {
		t.Fatal(err)
	}

	matches, err := st.Search("gateway design", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].ID != conv.ID {
		t.Fatalf("matches = %+v, want only title-phrase %s", matches, conv.ID)
	}
	if len(matches[0].Hits) != 0 {
		t.Fatalf("title-only match should have no message hits: %+v", matches[0].Hits)
	}
}

func TestSearchEscapesEmbeddedQuotes(t *testing.T) {
	st := openTemp(t)
	defer st.Close()

	conv, err := st.CreateConversation("cloud", "quotes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(conv.ID, "user", `she said "hello there" and left`); err != nil {
		t.Fatal(err)
	}

	matches, err := st.Search(`said "hello there"`, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || len(matches[0].Hits) == 0 {
		t.Fatalf("matches = %+v, want the quoted phrase", matches)
	}
}

func TestSearchEmptyQueryErrors(t *testing.T) {
	st := openTemp(t)
	defer st.Close()
	if _, err := st.Search("   ", "", 20); err == nil {
		t.Fatal("want error for empty query")
	}
}

func TestSearchNoHits(t *testing.T) {
	st := openTemp(t)
	defer st.Close()
	matches, err := st.Search("zzz-unfindable-token", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("matches = %+v, want none", matches)
	}
}

func TestSearchAfterDelete(t *testing.T) {
	st := openTemp(t)
	defer st.Close()

	conv, err := st.CreateConversation("cloud", "deleteme")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(conv.ID, "user", "unique zebra stamp collection"); err != nil {
		t.Fatal(err)
	}
	if m, _ := st.Search("zebra stamp", "", 20); len(m) != 1 {
		t.Fatalf("want 1 match before delete, got %d", len(m))
	}
	if err := st.DeleteConversation(conv.ID); err != nil {
		t.Fatal(err)
	}
	m, err := st.Search("zebra stamp", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Fatalf("want 0 matches after delete, got %+v", m)
	}
}

func TestSearchFollowsRename(t *testing.T) {
	st := openTemp(t)
	defer st.Close()

	conv, err := st.CreateConversation("cloud", "old title words")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetTitle(conv.ID, "fresh codename project"); err != nil {
		t.Fatal(err)
	}
	if m, _ := st.Search("fresh codename", "", 20); len(m) != 1 {
		t.Fatalf("want new title match, got %+v", m)
	}
	if m, _ := st.Search("old title words", "", 20); len(m) != 0 {
		t.Fatalf("old title must vanish after rename, got %+v", m)
	}
}

func TestSearchModelFilter(t *testing.T) {
	st := openTemp(t)
	defer st.Close()

	a, err := st.CreateConversation("cloud-pro", "filter me")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateConversation("on-device", "filter me too")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []Conversation{a, b} {
		if _, err := st.AppendMessage(c.ID, "user", "shared marker xyzzy"); err != nil {
			t.Fatal(err)
		}
	}

	matches, err := st.Search("shared marker xyzzy", "on-device", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].ID != b.ID {
		t.Fatalf("matches = %+v, want only on-device %s", matches, b.ID)
	}
}

func TestSearchSkipsArchived(t *testing.T) {
	st := openTemp(t)
	defer st.Close()

	conv, err := st.CreateConversation("cloud", "archive me")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(conv.ID, "user", "buried treasure map"); err != nil {
		t.Fatal(err)
	}
	// No public archive setter yet; set the column directly (in-package test).
	if _, err := st.db.Exec(`UPDATE conversations SET archived = 1 WHERE id = ?`, conv.ID); err != nil {
		t.Fatal(err)
	}
	matches, err := st.Search("buried treasure", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("archived conversation surfaced: %+v", matches)
	}
}

// TestBackfillRebuildsLegacyDatabase reproduces the production failure:
// a database created before the FTS migration has rows but no search
// index, and COUNT(*) on an external-content FTS table mirrors the base
// rows, so the old emptiness check never fired and every Search came
// back empty. Opening the legacy file must now populate the index.
func TestBackfillRebuildsLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hollis.db")

	legacy, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	legacyDDL := `
	CREATE TABLE conversations (
		id          TEXT PRIMARY KEY,
		title       TEXT,
		model       TEXT NOT NULL,
		summary     TEXT,
		created_at  TEXT NOT NULL,
		updated_at  TEXT NOT NULL,
		archived    INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE messages (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		conversation_id   TEXT NOT NULL,
		seq               INTEGER NOT NULL,
		role              TEXT NOT NULL,
		content           TEXT NOT NULL,
		created_at        TEXT NOT NULL,
		metadata_json     TEXT
	);
	INSERT INTO conversations (id, title, model, created_at, updated_at)
	VALUES ('legacy-1', 'legacy chat', 'cloud', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z');
	INSERT INTO messages (conversation_id, seq, role, content, created_at)
	VALUES ('legacy-1', 0, 'user', 'the legacy heating complaint phrase', '2026-09-01T00:00:00Z');
	`
	if _, err := legacy.Exec(legacyDDL); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	legacy.Close()

	st := mustOpen(t, path)
	defer st.Close()

	matches, err := st.Search("legacy heating complaint", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].ID != "legacy-1" || len(matches[0].Hits) == 0 {
		t.Fatalf("backfilled index found %+v, want legacy-1 with a message hit", matches)
	}
}

func TestSearchLimit(t *testing.T) {
	st := openTemp(t)
	defer st.Close()
	for i := 0; i < 3; i++ {
		conv, err := st.CreateConversation("cloud", fmt.Sprintf("limit %d", i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.AppendMessage(conv.ID, "user", "limit marker qwerty"); err != nil {
			t.Fatal(err)
		}
	}
	matches, err := st.Search("limit marker qwerty", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches = %d, want 2", len(matches))
	}
}
