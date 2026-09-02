// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

// Package store persists conversations and messages in local SQLite
// (plan §12). Apple's runs are stateless; this store is the only memory, and
// its transcript is replayed each turn (proven by results Test B/C/E).
//
// The driver is modernc.org/sqlite: pure Go, no cgo, single-writer local use.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a conversation id does not exist.
var ErrNotFound = errors.New("not found")

// Store is the persistent chat store. SQLite serializes writers via
// busy_timeout; callers keep concurrency policy (rule 8).
type Store struct {
	db *sql.DB
}

// Conversation is one persistent chat.
type Conversation struct {
	ID        string
	Title     string
	Model     string
	Summary   string
	CreatedAt string
	UpdatedAt string
	Archived  bool
	// Messages is the stored-message count, populated by ListConversations
	// (zero elsewhere; use MessageCount for a single conversation).
	Messages int
}

// Message is one turn in a conversation. Content is stored verbatim; role
// formatting is added only at replay time (rule 6: no invented newlines).
type Message struct {
	ID             int64
	ConversationID string
	Seq            int64
	Role           string // system | user | assistant
	Content        string
	CreatedAt      string
}

const schema = `
CREATE TABLE IF NOT EXISTS conversations (
	id            TEXT PRIMARY KEY,
	title         TEXT,
	model         TEXT NOT NULL,
	summary       TEXT,
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL,
	archived      INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS messages (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	conversation_id   TEXT NOT NULL,
	seq               INTEGER NOT NULL,
	role              TEXT NOT NULL,
	content           TEXT NOT NULL,
	created_at        TEXT NOT NULL,
	metadata_json     TEXT
);
CREATE TABLE IF NOT EXISTS runs (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	conversation_id   TEXT,
	model             TEXT NOT NULL,
	started_at        TEXT NOT NULL,
	duration_ms       INTEGER,
	exit_code         INTEGER,
	error_class       TEXT,
	stderr_excerpt    TEXT
);
CREATE INDEX IF NOT EXISTS idx_messages_conv ON messages(conversation_id, seq);

-- Full-text search (plan §12 addendum): external-content FTS5 kept in
-- sync by triggers. AFTER UPDATE on messages is deliberately absent in
-- v1 — add it when Phase 6 compaction rewrites message bodies.
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
  content, conversation_id UNINDEXED, role UNINDEXED, seq UNINDEXED,
  content='messages', content_rowid='id'
);
CREATE VIRTUAL TABLE IF NOT EXISTS conversations_fts USING fts5(
  title,
  content='conversations', content_rowid='rowid'
);
CREATE TRIGGER IF NOT EXISTS messages_fts_ai AFTER INSERT ON messages BEGIN
  INSERT INTO messages_fts(rowid, content, conversation_id, role, seq)
  VALUES (new.id, new.content, new.conversation_id, new.role, new.seq);
END;
CREATE TRIGGER IF NOT EXISTS messages_fts_bd BEFORE DELETE ON messages BEGIN
  -- External-content FTS5 needs the old row values for 'delete'.
  INSERT INTO messages_fts(messages_fts, rowid, content, conversation_id, role, seq)
  VALUES ('delete', old.id, old.content, old.conversation_id, old.role, old.seq);
END;
CREATE TRIGGER IF NOT EXISTS conversations_fts_ai AFTER INSERT ON conversations BEGIN
  INSERT INTO conversations_fts(rowid, title) VALUES (new.rowid, new.title);
END;
CREATE TRIGGER IF NOT EXISTS conversations_fts_au AFTER UPDATE OF title ON conversations BEGIN
  INSERT INTO conversations_fts(conversations_fts, rowid, title)
  VALUES ('delete', old.rowid, old.title);
  INSERT INTO conversations_fts(rowid, title) VALUES (new.rowid, new.title);
END;
CREATE TRIGGER IF NOT EXISTS conversations_fts_bd BEFORE DELETE ON conversations BEGIN
  INSERT INTO conversations_fts(conversations_fts, rowid, title)
  VALUES ('delete', old.rowid, old.title);
END;
`

// Open opens (creating if needed) the database at path and applies migrations.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("empty database path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.backfillFTS(); err != nil {
		db.Close()
		return nil, fmt.Errorf("backfill search index: %w", err)
	}
	return s, nil
}

// backfillFTS rebuilds the FTS tables exactly once, when rows exist but
// the index is empty (e.g. the migration just created the FTS tables over
// a pre-search database). Emptiness is measured on the _docsize shadow
// table — one row per indexed document — because COUNT(*) on an
// external-content FTS table reads through to the content table and
// therefore mirrors the base rows whether or not anything is indexed.
// Triggers keep the index in sync afterwards; this never runs again on a
// synced store.
func (s *Store) backfillFTS() error {
	for table, fts := range map[string]string{"messages": "messages_fts", "conversations": "conversations_fts"} {
		var rows, indexed int
		if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM `+table+`), (SELECT COUNT(*) FROM `+fts+`_docsize)`).Scan(&rows, &indexed); err != nil {
			return err
		}
		if rows > 0 && indexed == 0 {
			if _, err := s.db.Exec(`INSERT INTO ` + fts + `(` + fts + `) VALUES('rebuild')`); err != nil {
				return err
			}
		}
	}
	return nil
}

// DefaultPath returns the canonical per-user database path:
// ~/Library/Application Support/hollis/hollis.db (darwin UserConfigDir).
func DefaultPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "hollis", "hollis.db"), nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// newID returns a random RFC 4122 v4 UUID string.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return hex.EncodeToString(b[0:4]) + "-" +
		hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" +
		hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:16])
}

// CreateConversation inserts a new conversation with a fresh UUID.
func (s *Store) CreateConversation(model, title string) (Conversation, error) {
	ts := now()
	c := Conversation{
		ID:        newID(),
		Title:     title,
		Model:     model,
		CreatedAt: ts,
		UpdatedAt: ts,
	}
	_, err := s.db.Exec(`INSERT INTO conversations (id, title, model, summary, created_at, updated_at, archived)
		VALUES (?, ?, ?, '', ?, ?, 0)`, c.ID, c.Title, c.Model, c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return Conversation{}, err
	}
	return c, nil
}

// GetConversation returns one conversation or an error wrapping ErrNotFound.
func (s *Store) GetConversation(id string) (Conversation, error) {
	row := s.db.QueryRow(`SELECT id, title, model, summary, created_at, updated_at, archived
		FROM conversations WHERE id = ?`, id)
	var c Conversation
	var archived int
	if err := row.Scan(&c.ID, &c.Title, &c.Model, &c.Summary, &c.CreatedAt, &c.UpdatedAt, &archived); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return c, err
	}
	c.Archived = archived != 0
	return c, nil
}

// ListConversations returns conversations newest-first. The archived
// predicate lives in SQL, and each row carries its message count from one
// LEFT JOIN instead of a per-conversation COUNT query.
func (s *Store) ListConversations(includeArchived bool) ([]Conversation, error) {
	query := `SELECT c.id, c.title, c.model, c.summary, c.created_at, c.updated_at, c.archived,
		COUNT(m.id)
		FROM conversations c
		LEFT JOIN messages m ON m.conversation_id = c.id`
	if !includeArchived {
		query += `
			WHERE c.archived = 0`
	}
	query += `
		GROUP BY c.id
		ORDER BY c.updated_at DESC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		var archived, msgCount int
		if err := rows.Scan(&c.ID, &c.Title, &c.Model, &c.Summary, &c.CreatedAt, &c.UpdatedAt, &archived, &msgCount); err != nil {
			return nil, err
		}
		c.Archived = archived != 0
		c.Messages = msgCount
		out = append(out, c)
	}
	return out, rows.Err()
}

// MessageCount returns the number of stored messages for a conversation.
func (s *Store) MessageCount(convID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id = ?`, convID).Scan(&n)
	return n, err
}

// Messages returns all messages of a conversation in seq order.
func (s *Store) Messages(convID string) ([]Message, error) {
	rows, err := s.db.Query(`SELECT id, conversation_id, seq, role, content, created_at
		FROM messages WHERE conversation_id = ? ORDER BY seq ASC`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Seq, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AppendMessage stores a message with the next seq and touches updated_at.
//
// The seq is computed inside the INSERT rather than by a preceding SELECT.
// As two statements, two processes appending to the same conversation — say
// two `hollis chat --continue <id>` runs — could both read the same MAX(seq)
// and write duplicate sequence numbers, which would then replay the
// transcript out of order. One statement makes the read and the write atomic.
func (s *Store) AppendMessage(convID, role, content string) (Message, error) {
	created := now()
	res, err := s.db.Exec(`INSERT INTO messages (conversation_id, seq, role, content, created_at)
		SELECT ?, COALESCE(MAX(seq), -1) + 1, ?, ?, ?
		FROM messages WHERE conversation_id = ?`,
		convID, role, content, created, convID)
	if err != nil {
		return Message{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Message{}, err
	}
	var seq int64
	if err := s.db.QueryRow(`SELECT seq FROM messages WHERE id = ?`, id).Scan(&seq); err != nil {
		return Message{}, err
	}
	if err := s.touchConversation(convID); err != nil {
		return Message{}, err
	}
	return Message{ID: id, ConversationID: convID, Seq: seq, Role: role, Content: content, CreatedAt: created}, nil
}

func (s *Store) touchConversation(convID string) error {
	_, err := s.db.Exec(`UPDATE conversations SET updated_at = ? WHERE id = ?`, now(), convID)
	return err
}

// SetTitle renames a conversation; not-found yields an error wrapping ErrNotFound.
func (s *Store) SetTitle(convID, title string) error {
	res, err := s.db.Exec(`UPDATE conversations SET title = ? WHERE id = ?`, title, convID)
	if err != nil {
		return err
	}
	return requireAffected(res, convID)
}

// SetSummary stores compaction output (future use).
func (s *Store) SetSummary(convID, summary string) error {
	res, err := s.db.Exec(`UPDATE conversations SET summary = ? WHERE id = ?`, summary, convID)
	if err != nil {
		return err
	}
	return requireAffected(res, convID)
}

// Delete removes a conversation and its messages and run records.
func (s *Store) DeleteConversation(convID string) error {
	for _, stmt := range []string{
		`DELETE FROM messages WHERE conversation_id = ?`,
		`DELETE FROM runs WHERE conversation_id = ?`,
		`DELETE FROM conversations WHERE id = ?`,
	} {
		if _, err := s.db.Exec(stmt, convID); err != nil {
			return err
		}
	}
	return nil
}

// RecordRun inserts one transport-run diagnostic row (plan §12 runs table).
func (s *Store) RecordRun(convID, model string, startedAt time.Time, durationMs int64, exitCode int, errClass, stderrExcerpt string) error {
	_, err := s.db.Exec(`INSERT INTO runs (conversation_id, model, started_at, duration_ms, exit_code, error_class, stderr_excerpt)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		convID, model, startedAt.UTC().Format(time.RFC3339Nano), durationMs, exitCode, errClass, stderrExcerpt)
	return err
}

// SearchHit is one matching message inside a conversation.
type SearchHit struct {
	Seq     int64
	Role    string
	Snippet string
}

// SearchMatch is one conversation that matched a search, with up to a
// few of its best message hits. A title-only match has no Hits.
type SearchMatch struct {
	ID        string
	Title     string
	Model     string
	UpdatedAt string
	Hits      []SearchHit
}

// quotePhrase wraps the query in double quotes (doubling embedded
// quotes) so the whole input is one FTS5 phrase: predictable,
// injection-safe, and hyphens like VANTA-ORBIT just work. FTS5 query
// operators are therefore not exposed in v1.
func quotePhrase(query string) string {
	return `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
}

// Search full-text searches messages and conversation titles. The whole
// query is one phrase. Archived conversations are skipped; model may be
// empty for all tiers. Results are ordered by best match rank, then
// updated_at desc, one entry per conversation, up to limit.
func (s *Store) Search(query, model string, limit int) ([]SearchMatch, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("empty search query")
	}
	if limit <= 0 {
		limit = 20
	}
	phrase := quotePhrase(query)

	modelFilter := ""
	args := []any{phrase}
	if model != "" {
		modelFilter = " AND c.model = ?"
		args = append(args, model)
	}

	byID := map[string]*SearchMatch{}
	order := []string{}
	add := func(id, title, mdl, updated string, hit *SearchHit) {
		if _, ok := byID[id]; !ok {
			byID[id] = &SearchMatch{ID: id, Title: title, Model: mdl, UpdatedAt: updated}
			order = append(order, id)
		}
		if hit != nil && len(byID[id].Hits) < 3 {
			byID[id].Hits = append(byID[id].Hits, *hit)
		}
	}

	// Message hits, best rank first. bm25() is FTS5's ranking function.
	rows, err := s.db.Query(`
		SELECT m.conversation_id, m.seq, m.role,
		       snippet(messages_fts, 0, '', '', '…', 12),
		       c.title, c.model, c.updated_at
		FROM messages_fts
		JOIN messages m ON m.id = messages_fts.rowid
		JOIN conversations c ON c.id = messages_fts.conversation_id
		WHERE messages_fts MATCH ? AND c.archived = 0`+modelFilter+`
		ORDER BY bm25(messages_fts)
		LIMIT ?`, append(append([]any{phrase}, args[1:]...), limit*20)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var convID, title, mdl, updated, role, snip string
		var seq int64
		if err := rows.Scan(&convID, &seq, &role, &snip, &title, &mdl, &updated); err != nil {
			return nil, err
		}
		add(convID, title, mdl, updated, &SearchHit{Seq: seq, Role: role, Snippet: snip})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	// Title hits, appended after message hits (their rank is over a
	// different index; a direct message hit is the stronger signal).
	trows, err := s.db.Query(`
		SELECT c.id, c.title, c.model, c.updated_at
		FROM conversations_fts
		JOIN conversations c ON c.rowid = conversations_fts.rowid
		WHERE conversations_fts MATCH ? AND c.archived = 0`+modelFilter+`
		ORDER BY bm25(conversations_fts)
		LIMIT ?`, append(append([]any{phrase}, args[1:]...), limit)...)
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	for trows.Next() {
		var id, title, mdl, updated string
		if err := trows.Scan(&id, &title, &mdl, &updated); err != nil {
			return nil, err
		}
		add(id, title, mdl, updated, nil)
	}
	if err := trows.Err(); err != nil {
		return nil, err
	}
	trows.Close()

	if len(order) > limit {
		order = order[:limit]
	}
	out := make([]SearchMatch, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

func requireAffected(res sql.Result, convID string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, convID)
	}
	return nil
}
