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
CREATE INDEX IF NOT EXISTS idx_messages_conv ON messages(conversation_id, seq);
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
	return &Store{db: db}, nil
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

// ListConversations returns conversations newest-first.
func (s *Store) ListConversations(includeArchived bool) ([]Conversation, error) {
	rows, err := s.db.Query(`SELECT id, title, model, summary, created_at, updated_at, archived
		FROM conversations ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		var archived int
		if err := rows.Scan(&c.ID, &c.Title, &c.Model, &c.Summary, &c.CreatedAt, &c.UpdatedAt, &archived); err != nil {
			return nil, err
		}
		c.Archived = archived != 0
		if c.Archived && !includeArchived {
			continue
		}
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
func (s *Store) AppendMessage(convID, role, content string) (Message, error) {
	created := now()
	var seq int64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(seq), -1) + 1 FROM messages WHERE conversation_id = ?`, convID).Scan(&seq); err != nil {
		return Message{}, err
	}
	res, err := s.db.Exec(`INSERT INTO messages (conversation_id, seq, role, content, created_at)
		VALUES (?, ?, ?, ?, ?)`, convID, seq, role, content, created)
	if err != nil {
		return Message{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
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
