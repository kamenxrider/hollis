// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestContinuationLockIsPrivateAndContextAware(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hollis.db")
	first := mustOpen(t, path)
	defer first.Close()
	second := mustOpen(t, path)
	defer second.Close()

	unlock, err := first.LockContinuation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	info, err := os.Stat(path + ".chat.lock")
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("continuation lock mode=%#o, want 0600", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := second.LockContinuation(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock error=%v, want context deadline", err)
	}
}

func TestAppendMessageSequencing(t *testing.T) {
	st := openTemp(t)
	defer st.Close()
	conv, err := st.CreateConversation("cloud", "")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		m, err := st.AppendMessage(conv.ID, "user", "msg")
		if err != nil {
			t.Fatal(err)
		}
		if m.Seq != int64(i) {
			t.Fatalf("seq = %d, want %d", m.Seq, i)
		}
	}
	if n, _ := st.MessageCount(conv.ID); n != 3 {
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
	conv, err := st.CreateConversation("cloud", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordRun(conv.ID, "cloud", time.Now(), 1234, 0, "", ""); err != nil {
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

func TestDefaultPathHonorsAbsoluteStateDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOLLIS_STATE_DIR", dir)
	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "hollis.db")
	if got != want {
		t.Fatalf("DefaultPath = %q, want %q", got, want)
	}

	t.Setenv("HOLLIS_STATE_DIR", filepath.Base(dir))
	if _, err := DefaultStateDir(); err == nil {
		t.Fatal("relative HOLLIS_STATE_DIR should fail")
	}
}

func TestOpenProtectsStateDirectoryAndDatabase(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hollis.db")
	st := mustOpen(t, path)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	st = mustOpen(t, path)
	defer st.Close()
	for _, tc := range []struct {
		path string
		want os.FileMode
	}{
		{dir, 0o700},
		{path, 0o600},
		{path + "-wal", 0o600},
		{path + "-shm", 0o600},
	} {
		info, err := os.Stat(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != tc.want {
			t.Fatalf("mode(%s) = %o, want %o", tc.path, got, tc.want)
		}
	}
}

func TestOpenRejectsSymlinkedDatabaseAndStateDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.db")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dbLink := filepath.Join(dir, "linked.db")
	if err := os.Symlink(target, dbLink); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dbLink); err == nil {
		t.Fatal("symlinked database should be rejected")
	}

	stateTarget := t.TempDir()
	stateLink := filepath.Join(t.TempDir(), "state-link")
	if err := os.Symlink(stateTarget, stateLink); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(stateLink, "hollis.db")); err == nil {
		t.Fatal("symlinked state directory should be rejected")
	}
}

func TestOpenHandlesStatePathContainingQueryCharacter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state?profile=quiet")
	path := filepath.Join(dir, "hollis.db")
	st := mustOpen(t, path)
	defer st.Close()
	if _, err := st.CreateConversation("cloud", "encoded path"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database was not created at the literal path: %v", err)
	}
}

func TestOpenRejectsNewerSchemaWithoutDowngradingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("newer schema was accepted")
	}
	db, err = sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 99 {
		t.Fatalf("future schema was changed to %d", version)
	}
}

func TestCreateAndAppendTurnAreAtomic(t *testing.T) {
	st := openTemp(t)
	defer st.Close()

	started := time.Now()
	conv, err := st.CreateConversationWithTurn("on-device", "first", "question", "answer", RunRecord{
		ModelRequested: "auto",
		ModelUsed:      "on-device",
		StartedAt:      started,
		DurationMs:     12,
		FallbackReason: "rate_limited",
		RequestBytes:   8,
		ResponseBytes:  6,
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := st.Messages(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Seq != 0 || msgs[1].Seq != 1 {
		t.Fatalf("first messages = %+v", msgs)
	}
	var runs, requestIDBytes, requestBytes, responseBytes int
	var requested, used, fallback string
	if err := st.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(LENGTH(request_id)), 0),
		model_requested, model_used, fallback_reason, request_bytes, response_bytes
		FROM runs WHERE conversation_id = ?`, conv.ID).Scan(&runs, &requestIDBytes, &requested, &used, &fallback, &requestBytes, &responseBytes); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || requestIDBytes == 0 || requested != "auto" || used != "on-device" || fallback != "rate_limited" || requestBytes != 8 || responseBytes != 6 {
		t.Fatalf("run metadata = count:%d id-bytes:%d requested:%s used:%s fallback:%s bytes:%d/%d", runs, requestIDBytes, requested, used, fallback, requestBytes, responseBytes)
	}

	if err := st.AppendTurn(conv.ID, "question 2", "answer 2", RunRecord{Model: "on-device", StartedAt: started}); err != nil {
		t.Fatal(err)
	}
	msgs, err = st.Messages(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 || msgs[2].Seq != 2 || msgs[3].Seq != 3 {
		t.Fatalf("appended messages = %+v", msgs)
	}
}

func TestAppendTurnRollsBackOnAssistantFailure(t *testing.T) {
	st := openTemp(t)
	defer st.Close()
	conv, err := st.CreateConversation("cloud", "rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`CREATE TRIGGER abort_assistant BEFORE INSERT ON messages
		WHEN NEW.role = 'assistant' BEGIN SELECT RAISE(ABORT, 'test assistant failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendTurn(conv.ID, "secret question", "secret answer", RunRecord{Model: "cloud", StartedAt: time.Now()}); err == nil {
		t.Fatal("AppendTurn should fail")
	}
	var messages, runs int
	if err := st.db.QueryRow(`SELECT (SELECT COUNT(*) FROM messages WHERE conversation_id = ?), (SELECT COUNT(*) FROM runs WHERE conversation_id = ?)`, conv.ID, conv.ID).Scan(&messages, &runs); err != nil {
		t.Fatal(err)
	}
	if messages != 0 || runs != 0 {
		t.Fatalf("rollback left messages=%d runs=%d", messages, runs)
	}
}

func TestCreateConversationWithTurnRollsBackOnAssistantFailure(t *testing.T) {
	st := openTemp(t)
	defer st.Close()
	if _, err := st.db.Exec(`CREATE TRIGGER abort_new_assistant BEFORE INSERT ON messages
		WHEN NEW.role = 'assistant' BEGIN SELECT RAISE(ABORT, 'test assistant failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateConversationWithTurn("cloud", "rollback", "question", "answer", RunRecord{Model: "cloud", StartedAt: time.Now()}); err == nil {
		t.Fatal("CreateConversationWithTurn should fail")
	}
	convs, err := st.ListConversations(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(convs) != 0 {
		t.Fatalf("rollback left %d conversations, want 0", len(convs))
	}
	var runs int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Fatalf("rollback left %d runs, want 0", runs)
	}
}

func TestAppendMessageRequiresConversation(t *testing.T) {
	st := openTemp(t)
	defer st.Close()
	if _, err := st.AppendMessage("missing", "user", "content"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AppendMessage error = %v, want ErrNotFound", err)
	}
}

func TestAppendMessagesSerializeAcrossStores(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hollis.db")
	a := mustOpen(t, path)
	defer a.Close()
	b := mustOpen(t, path)
	defer b.Close()
	conv, err := a.CreateConversation("cloud", "concurrent")
	if err != nil {
		t.Fatal(err)
	}
	const count = 12
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			writer := a
			if i%2 == 1 {
				writer = b
			}
			_, err := writer.AppendMessage(conv.ID, "user", fmt.Sprintf("message %d", i))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	msgs, err := a.Messages(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != count {
		t.Fatalf("messages = %d, want %d", len(msgs), count)
	}
	for i, msg := range msgs {
		if msg.Seq != int64(i) {
			t.Fatalf("message %d seq=%d, want %d", i, msg.Seq, i)
		}
	}
}

func TestSetTitleAndSummaryTouchUpdatedAt(t *testing.T) {
	st := openTemp(t)
	defer st.Close()
	conv, err := st.CreateConversation("cloud", "before")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetTitle(conv.ID, "after"); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetConversation(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "after" || got.UpdatedAt == conv.UpdatedAt {
		t.Fatalf("after title update = %+v", got)
	}
	previous := got.UpdatedAt
	time.Sleep(time.Millisecond)
	if err := st.SetSummary(conv.ID, "summary"); err != nil {
		t.Fatal(err)
	}
	got, err = st.GetConversation(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "summary" || got.UpdatedAt == previous {
		t.Fatalf("after summary update = %+v", got)
	}
}

func TestDeleteConversationIsTransactionalAndRemovesRuns(t *testing.T) {
	st := openTemp(t)
	defer st.Close()
	conv, err := st.CreateConversation("cloud", "delete")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(conv.ID, "user", "content"); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordRun(conv.ID, "cloud", time.Now(), 1, 0, "", "sensitive stderr"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteConversation(conv.ID); err != nil {
		t.Fatal(err)
	}
	var messages, runs int
	if err := st.db.QueryRow(`SELECT (SELECT COUNT(*) FROM messages WHERE conversation_id = ?), (SELECT COUNT(*) FROM runs WHERE conversation_id = ?)`, conv.ID, conv.ID).Scan(&messages, &runs); err != nil {
		t.Fatal(err)
	}
	if messages != 0 || runs != 0 {
		t.Fatalf("after delete messages=%d runs=%d", messages, runs)
	}
	if err := st.DeleteConversation(conv.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}

func TestDeleteConversationRollsBackOnChildFailure(t *testing.T) {
	st := openTemp(t)
	defer st.Close()
	conv, err := st.CreateConversation("cloud", "delete rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(conv.ID, "user", "keep me"); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordRun(conv.ID, "cloud", time.Now(), 1, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`CREATE TRIGGER abort_message_delete BEFORE DELETE ON messages
		BEGIN SELECT RAISE(ABORT, 'test delete failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteConversation(conv.ID); err == nil {
		t.Fatal("DeleteConversation should fail")
	}
	if _, err := st.GetConversation(conv.ID); err != nil {
		t.Fatalf("conversation disappeared after rollback: %v", err)
	}
	var messages, runs int
	if err := st.db.QueryRow(`SELECT (SELECT COUNT(*) FROM messages WHERE conversation_id = ?), (SELECT COUNT(*) FROM runs WHERE conversation_id = ?)`, conv.ID, conv.ID).Scan(&messages, &runs); err != nil {
		t.Fatal(err)
	}
	if messages != 1 || runs != 1 {
		t.Fatalf("rollback left messages=%d runs=%d, want 1 each", messages, runs)
	}
}

func TestLegacyDuplicateSequencesAreBackedUpAndRepaired(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hollis.db")
	legacy, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	legacyDDL := `
	CREATE TABLE conversations (
		id TEXT PRIMARY KEY, title TEXT, model TEXT NOT NULL, summary TEXT,
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
		archived INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT, conversation_id TEXT NOT NULL,
		seq INTEGER NOT NULL, role TEXT NOT NULL, content TEXT NOT NULL,
		created_at TEXT NOT NULL, metadata_json TEXT
	);
	CREATE TABLE runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT, conversation_id TEXT, model TEXT NOT NULL,
		started_at TEXT NOT NULL, duration_ms INTEGER, exit_code INTEGER,
		error_class TEXT, stderr_excerpt TEXT
	);
	INSERT INTO conversations (id, title, model, created_at, updated_at)
	VALUES ('legacy', 'legacy', 'cloud', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z');
	INSERT INTO messages (id, conversation_id, seq, role, content, created_at)
	VALUES (1, 'legacy', 2, 'user', 'last', '2026-09-01T00:00:00Z');
	INSERT INTO messages (id, conversation_id, seq, role, content, created_at)
	VALUES (2, 'legacy', 1, 'assistant', 'first duplicate', '2026-09-01T00:00:00Z');
	INSERT INTO messages (id, conversation_id, seq, role, content, created_at)
	VALUES (3, 'legacy', 1, 'user', 'second duplicate', '2026-09-01T00:00:00Z');
	INSERT INTO runs (conversation_id, model, started_at, duration_ms, exit_code, error_class, stderr_excerpt)
	VALUES ('legacy', 'cloud', '2026-09-01T00:00:00Z', 12, 0, '', 'legacy sensitive stderr');`
	if _, err := legacy.Exec(legacyDDL); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	st := mustOpen(t, path)
	defer st.Close()
	msgs, err := st.Messages("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3", len(msgs))
	}
	wantContent := []string{"first duplicate", "second duplicate", "last"}
	for i, want := range wantContent {
		if msgs[i].Seq != int64(i) || msgs[i].Content != want {
			t.Fatalf("message %d = %+v, want seq %d content %q", i, msgs[i], i, want)
		}
	}
	var version int
	if err := st.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
	var requested, used string
	var requestBytes, responseBytes int
	if err := st.db.QueryRow(`SELECT model_requested, model_used, request_bytes, response_bytes FROM runs WHERE conversation_id = 'legacy'`).Scan(&requested, &used, &requestBytes, &responseBytes); err != nil {
		t.Fatal(err)
	}
	if requested != "cloud" || used != "cloud" || requestBytes != 0 || responseBytes != 0 {
		t.Fatalf("migrated run = %s/%s bytes=%d/%d", requested, used, requestBytes, responseBytes)
	}
	var oldColumn int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('runs') WHERE name = 'stderr_excerpt'`).Scan(&oldColumn); err != nil {
		t.Fatal(err)
	}
	if oldColumn != 0 {
		t.Fatal("legacy stderr column survived privacy migration")
	}
	backups, err := filepath.Glob(filepath.Join(dir, ".hollis.db.backup-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want one", backups)
	}
	info, err := os.Stat(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("backup mode = %o, want 600", got)
	}
	if _, err := st.db.Exec(`INSERT INTO messages (conversation_id, seq, role, content, created_at)
		VALUES ('legacy', 0, 'user', 'duplicate', '2026-09-01T00:00:00Z')`); err == nil {
		t.Fatal("unique conversation/seq constraint did not reject duplicate")
	}
}
