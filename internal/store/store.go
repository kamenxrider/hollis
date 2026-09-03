// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

// Package store persists conversations and messages in local SQLite
// (plan §12). Apple's runs are stateless; this store is the only memory, and
// its transcript is replayed each turn (proven by results Test B/C/E).
//
// The driver is modernc.org/sqlite: pure Go, no cgo, single-writer local use.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a conversation id does not exist.
var ErrNotFound = errors.New("not found")

// Store is the persistent chat store. SQLite serializes writers via
// busy_timeout; callers keep concurrency policy (rule 8).
type Store struct {
	db                   *sql.DB
	continuationLockPath string
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

// RunRecord contains the non-content diagnostics for one transport run.
// Prompts and responses deliberately do not have fields here: run history
// must never become a second content store.
type RunRecord struct {
	// Model is retained for callers compiled against v0.1. New code should
	// set ModelRequested and ModelUsed explicitly.
	Model          string
	RequestID      string
	ModelRequested string
	ModelUsed      string
	StartedAt      time.Time
	DurationMs     int64
	ExitCode       int
	ErrorClass     string
	FallbackReason string
	RequestBytes   int
	ResponseBytes  int
}

const (
	schemaVersion = 2
	stateDirMode  = 0o700
	stateFileMode = 0o600
)

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
	metadata_json     TEXT,
	UNIQUE (conversation_id, seq)
);
CREATE TABLE IF NOT EXISTS runs (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	conversation_id   TEXT,
	request_id        TEXT NOT NULL,
	model_requested   TEXT NOT NULL,
	model_used        TEXT NOT NULL,
	started_at        TEXT NOT NULL,
	duration_ms       INTEGER,
	exit_code         INTEGER,
	error_class       TEXT,
	fallback_reason   TEXT,
	request_bytes     INTEGER NOT NULL DEFAULT 0,
	response_bytes    INTEGER NOT NULL DEFAULT 0
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
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("empty database path")
	}
	dir := filepath.Dir(path)
	if err := ensurePrivateDir(dir); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	existing, err := prepareDatabaseFile(path)
	if err != nil {
		return nil, err
	}
	databaseURL := &url.URL{Scheme: "file", Path: path}
	query := databaseURL.Query()
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "foreign_keys(ON)")
	databaseURL.RawQuery = query.Encode()
	dsn := databaseURL.String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	version, err := schemaVersionOf(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("read schema version %s: %w", path, err)
	}
	if version > schemaVersion {
		db.Close()
		return nil, fmt.Errorf("database schema version %d is newer than supported version %d", version, schemaVersion)
	}
	needsMigration, err := needsMigration(db, version)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("inspect schema %s: %w", path, err)
	}
	if existing && needsMigration {
		if _, err := backupDatabase(db, dir); err != nil {
			db.Close()
			return nil, fmt.Errorf("back up %s before migration: %w", path, err)
		}
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	s := &Store{db: db, continuationLockPath: path + ".chat.lock"}
	if err := s.migrate(version); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	if err := os.Chmod(path, stateFileMode); err != nil {
		db.Close()
		return nil, fmt.Errorf("protect database %s: %w", path, err)
	}
	if err := s.backfillFTS(); err != nil {
		db.Close()
		return nil, fmt.Errorf("backfill search index: %w", err)
	}
	return s, nil
}

func prepareDatabaseFile(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			file, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, stateFileMode)
			if createErr != nil {
				if os.IsExist(createErr) {
					return prepareDatabaseFile(path)
				}
				return false, fmt.Errorf("create database %s: %w", path, createErr)
			}
			if closeErr := file.Close(); closeErr != nil {
				return false, closeErr
			}
			return false, nil
		}
		return false, fmt.Errorf("stat database %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("database path %s must be a regular file, not a symlink or special file", path)
	}
	if err := os.Chmod(path, stateFileMode); err != nil {
		return false, fmt.Errorf("protect database %s: %w", path, err)
	}
	return info.Size() > 0, nil
}

func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, stateDirMode); err != nil {
		return err
	}
	// A pre-existing directory keeps its old mode after MkdirAll. The CLI
	// passes an application-owned state directory; tighten that exact path,
	// but do not chmod the current directory for a relative bare filename.
	if dir != "." {
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("state path %s must be a real directory, not a symlink", dir)
		}
		if err := os.Chmod(dir, stateDirMode); err != nil {
			return err
		}
	}
	return nil
}

func schemaVersionOf(db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

func needsMigration(db *sql.DB, version int) (bool, error) {
	if version < schemaVersion {
		return true, nil
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_messages_conv_seq_unique'`).Scan(&count); err != nil {
		return false, err
	}
	if count == 0 {
		return true, nil
	}
	columns, err := tableColumns(db, "runs")
	if err != nil {
		return false, err
	}
	return !columns["model_requested"], nil
}

type queryer interface {
	Query(string, ...any) (*sql.Rows, error)
}

func tableColumns(db queryer, table string) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

// backupDatabase creates a consistent SQLite backup before schema migration.
// VACUUM INTO includes WAL content, unlike copying the main database file
// directly. The backup is intentionally retained for manual recovery.
func backupDatabase(db *sql.DB, dir string) (string, error) {
	f, err := os.CreateTemp(dir, ".hollis.db.backup-*")
	if err != nil {
		return "", err
	}
	backup := f.Name()
	if err := f.Close(); err != nil {
		os.Remove(backup)
		return "", err
	}
	if err := os.Remove(backup); err != nil {
		return "", err
	}
	if _, err := db.Exec(`VACUUM INTO ?`, backup); err != nil {
		os.Remove(backup)
		return "", err
	}
	if err := os.Chmod(backup, stateFileMode); err != nil {
		return "", err
	}
	if err := syncDir(dir); err != nil {
		return "", err
	}
	return backup, nil
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (s *Store) migrate(version int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = repairDuplicateSequences(tx); err != nil {
		return err
	}
	if err = migrateRunDiagnostics(tx); err != nil {
		return err
	}
	if _, err = tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_conv_seq_unique
		ON messages(conversation_id, seq)`); err != nil {
		return err
	}
	if _, err = tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return err
	}
	err = tx.Commit()
	return err
}

func migrateRunDiagnostics(tx *sql.Tx) error {
	columns, err := tableColumns(tx, "runs")
	if err != nil {
		return err
	}
	if columns["model_requested"] {
		for _, required := range []string{"request_id", "model_used", "fallback_reason", "request_bytes", "response_bytes"} {
			if !columns[required] {
				return fmt.Errorf("runs table is missing required column %s", required)
			}
		}
		return nil
	}
	if _, err := tx.Exec(`CREATE TABLE runs_v2 (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		conversation_id TEXT,
		request_id TEXT NOT NULL,
		model_requested TEXT NOT NULL,
		model_used TEXT NOT NULL,
		started_at TEXT NOT NULL,
		duration_ms INTEGER,
		exit_code INTEGER,
		error_class TEXT,
		fallback_reason TEXT,
		request_bytes INTEGER NOT NULL DEFAULT 0,
		response_bytes INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		return err
	}
	if columns["model"] {
		if _, err := tx.Exec(`INSERT INTO runs_v2
			(id, conversation_id, request_id, model_requested, model_used, started_at,
			 duration_ms, exit_code, error_class, fallback_reason, request_bytes, response_bytes)
			SELECT id, conversation_id, '', model, model, started_at,
			       duration_ms, exit_code, error_class, '', 0, 0 FROM runs`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DROP TABLE runs`); err != nil {
		return err
	}
	_, err = tx.Exec(`ALTER TABLE runs_v2 RENAME TO runs`)
	return err
}

// repairDuplicateSequences preserves every message while making sequence
// order deterministic. Only conversations with duplicate sequence values are
// rewritten; their rows are ordered by the old seq and then immutable id.
func repairDuplicateSequences(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT conversation_id FROM messages
		GROUP BY conversation_id, seq HAVING COUNT(*) > 1
		ORDER BY conversation_id`)
	if err != nil {
		return err
	}
	var conversations []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if len(conversations) == 0 || conversations[len(conversations)-1] != id {
			conversations = append(conversations, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, conversationID := range conversations {
		messageRows, err := tx.Query(`SELECT id FROM messages
			WHERE conversation_id = ? ORDER BY seq ASC, id ASC`, conversationID)
		if err != nil {
			return err
		}
		var ids []int64
		for messageRows.Next() {
			var id int64
			if err := messageRows.Scan(&id); err != nil {
				messageRows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := messageRows.Err(); err != nil {
			messageRows.Close()
			return err
		}
		if err := messageRows.Close(); err != nil {
			return err
		}
		// Move rows to unique temporary values first, so a future unique
		// constraint cannot observe a transient collision.
		for _, id := range ids {
			if _, err := tx.Exec(`UPDATE messages SET seq = ? WHERE id = ?`, -id-1, id); err != nil {
				return err
			}
		}
		for seq, id := range ids {
			if _, err := tx.Exec(`UPDATE messages SET seq = ? WHERE id = ?`, seq, id); err != nil {
				return err
			}
		}
	}
	return nil
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
	base, err := DefaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "hollis.db"), nil
}

// DefaultStateDir returns the application state directory. HOLLIS_STATE_DIR
// is useful for isolated runs and tests, but must be absolute so changing the
// process working directory cannot redirect persistence unexpectedly.
func DefaultStateDir() (string, error) {
	if raw := strings.TrimSpace(os.Getenv("HOLLIS_STATE_DIR")); raw != "" {
		if !filepath.IsAbs(raw) {
			return "", fmt.Errorf("HOLLIS_STATE_DIR must be an absolute path")
		}
		return filepath.Clean(raw), nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "hollis"), nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// LockContinuation serializes the read-run-append sequence for chat
// continuations across goroutines and Hollis processes. SQLite already makes
// each write atomic; this lock also prevents two callers from both running a
// model against the same history snapshot and exceeding the history limit.
func (s *Store) LockContinuation(ctx context.Context) (func() error, error) {
	fd, err := syscall.Open(s.continuationLockPath, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, stateFileMode)
	if err != nil {
		return nil, fmt.Errorf("open continuation lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), s.continuationLockPath)
	closeWithError := func(lockErr error) (func() error, error) {
		if closeErr := file.Close(); closeErr != nil {
			return nil, errors.Join(lockErr, closeErr)
		}
		return nil, lockErr
	}
	info, err := file.Stat()
	if err != nil {
		return closeWithError(err)
	}
	if !info.Mode().IsRegular() {
		return closeWithError(fmt.Errorf("continuation lock must be a regular file"))
	}
	if err := file.Chmod(stateFileMode); err != nil {
		return closeWithError(err)
	}

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() error {
				unlockErr := syscall.Flock(fd, syscall.LOCK_UN)
				return errors.Join(unlockErr, file.Close())
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return closeWithError(fmt.Errorf("lock continuation: %w", err))
		}
		select {
		case <-ctx.Done():
			return closeWithError(ctx.Err())
		case <-ticker.C:
		}
	}
}

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
		ORDER BY c.updated_at DESC, c.id ASC`
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
		FROM messages WHERE conversation_id = ? ORDER BY seq ASC, id ASC`, convID)
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
// The immediate write transaction serializes sequence allocation with other
// processes using this store.
func (s *Store) AppendMessage(convID, role, content string) (Message, error) {
	created := now()
	var message Message
	err := s.withWriteTx(func(db dbConn) error {
		seq, err := nextMessageSeq(db, convID)
		if err != nil {
			return err
		}
		res, err := db.ExecContext(context.Background(), `INSERT INTO messages (conversation_id, seq, role, content, created_at)
			VALUES (?, ?, ?, ?, ?)`, convID, seq, role, content, created)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if err := touchConversationDB(db, convID); err != nil {
			return err
		}
		message = Message{ID: id, ConversationID: convID, Seq: seq, Role: role, Content: content, CreatedAt: created}
		return nil
	})
	return message, err
}

func (s *Store) touchConversation(convID string) error {
	_, err := s.db.Exec(`UPDATE conversations SET updated_at = ? WHERE id = ?`, now(), convID)
	return err
}

type dbConn interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) withWriteTx(fn func(dbConn) error) (err error) {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()
	if err = fn(conn); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}

func nextMessageSeq(db dbConn, convID string) (int64, error) {
	var exists int
	if err := db.QueryRowContext(context.Background(), `SELECT 1 FROM conversations WHERE id = ?`, convID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("%w: %s", ErrNotFound, convID)
		}
		return 0, err
	}
	var seq int64
	if err := db.QueryRowContext(context.Background(), `SELECT COALESCE(MAX(seq), -1) + 1
		FROM messages WHERE conversation_id = ?`, convID).Scan(&seq); err != nil {
		return 0, err
	}
	return seq, nil
}

func touchConversationDB(db dbConn, convID string) error {
	res, err := db.ExecContext(context.Background(), `UPDATE conversations SET updated_at = ? WHERE id = ?`, now(), convID)
	if err != nil {
		return err
	}
	return requireAffected(res, convID)
}

func insertRun(db dbConn, convID string, run RunRecord) error {
	var conversation any
	if convID != "" {
		conversation = convID
	}
	requested := run.ModelRequested
	used := run.ModelUsed
	if requested == "" {
		requested = run.Model
	}
	if used == "" {
		used = run.Model
	}
	if run.RequestID == "" {
		run.RequestID = newID()
	}
	_, err := db.ExecContext(context.Background(), `INSERT INTO runs
		(conversation_id, request_id, model_requested, model_used, started_at,
		 duration_ms, exit_code, error_class, fallback_reason, request_bytes, response_bytes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		conversation, run.RequestID, requested, used, run.StartedAt.UTC().Format(time.RFC3339Nano),
		run.DurationMs, run.ExitCode, run.ErrorClass, run.FallbackReason, run.RequestBytes, run.ResponseBytes)
	return err
}

// AppendTurn atomically records a completed turn and its run diagnostic.
func (s *Store) AppendTurn(convID, userContent, assistantContent string, run RunRecord) error {
	return s.withWriteTx(func(db dbConn) error {
		seq, err := nextMessageSeq(db, convID)
		if err != nil {
			return err
		}
		if err := insertRun(db, convID, run); err != nil {
			return err
		}
		created := now()
		if _, err := db.ExecContext(context.Background(), `INSERT INTO messages (conversation_id, seq, role, content, created_at)
			VALUES (?, ?, 'user', ?, ?)`, convID, seq, userContent, created); err != nil {
			return err
		}
		if _, err := db.ExecContext(context.Background(), `INSERT INTO messages (conversation_id, seq, role, content, created_at)
			VALUES (?, ?, 'assistant', ?, ?)`, convID, seq+1, assistantContent, now()); err != nil {
			return err
		}
		return touchConversationDB(db, convID)
	})
}

// CreateConversationWithTurn creates a conversation and atomically records
// its first completed turn. A failed operation leaves no visible chat row.
func (s *Store) CreateConversationWithTurn(model, title, userContent, assistantContent string, run RunRecord) (Conversation, error) {
	ts := now()
	c := Conversation{ID: newID(), Title: title, Model: model, CreatedAt: ts, UpdatedAt: ts}
	err := s.withWriteTx(func(db dbConn) error {
		if _, err := db.ExecContext(context.Background(), `INSERT INTO conversations (id, title, model, summary, created_at, updated_at, archived)
			VALUES (?, ?, ?, '', ?, ?, 0)`, c.ID, c.Title, c.Model, c.CreatedAt, c.UpdatedAt); err != nil {
			return err
		}
		if err := insertRun(db, c.ID, run); err != nil {
			return err
		}
		created := now()
		if _, err := db.ExecContext(context.Background(), `INSERT INTO messages (conversation_id, seq, role, content, created_at)
			VALUES (?, 0, 'user', ?, ?)`, c.ID, userContent, created); err != nil {
			return err
		}
		if _, err := db.ExecContext(context.Background(), `INSERT INTO messages (conversation_id, seq, role, content, created_at)
			VALUES (?, 1, 'assistant', ?, ?)`, c.ID, assistantContent, now()); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return Conversation{}, err
	}
	return c, nil
}

// SetTitle renames a conversation; not-found yields an error wrapping ErrNotFound.
func (s *Store) SetTitle(convID, title string) error {
	res, err := s.db.Exec(`UPDATE conversations SET title = ?, updated_at = ? WHERE id = ?`, title, now(), convID)
	if err != nil {
		return err
	}
	return requireAffected(res, convID)
}

// SetSummary stores compaction output (future use).
func (s *Store) SetSummary(convID, summary string) error {
	res, err := s.db.Exec(`UPDATE conversations SET summary = ?, updated_at = ? WHERE id = ?`, summary, now(), convID)
	if err != nil {
		return err
	}
	return requireAffected(res, convID)
}

// Delete removes a conversation and its messages and run records.
func (s *Store) DeleteConversation(convID string) error {
	return s.withWriteTx(func(db dbConn) error {
		var exists int
		if err := db.QueryRowContext(context.Background(), `SELECT 1 FROM conversations WHERE id = ?`, convID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrNotFound, convID)
			}
			return err
		}
		for _, stmt := range []string{
			`DELETE FROM messages WHERE conversation_id = ?`,
			`DELETE FROM runs WHERE conversation_id = ?`,
			`DELETE FROM conversations WHERE id = ?`,
		} {
			if _, err := db.ExecContext(context.Background(), stmt, convID); err != nil {
				return err
			}
		}
		return nil
	})
}

// RecordRun inserts one transport-run diagnostic row (plan §12 runs table).
func (s *Store) RecordRun(convID, model string, startedAt time.Time, durationMs int64, exitCode int, errClass, _ string) error {
	return s.RecordRunMetadata(convID, RunRecord{
		ModelRequested: model,
		ModelUsed:      model,
		StartedAt:      startedAt,
		DurationMs:     durationMs,
		ExitCode:       exitCode,
		ErrorClass:     errClass,
	})
}

// RecordRunMetadata inserts privacy-safe diagnostics without prompt, reply,
// or raw transport output.
func (s *Store) RecordRunMetadata(convID string, record RunRecord) error {
	return s.withWriteTx(func(db dbConn) error {
		if convID != "" {
			var exists int
			if err := db.QueryRowContext(context.Background(), `SELECT 1 FROM conversations WHERE id = ?`, convID).Scan(&exists); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("%w: %s", ErrNotFound, convID)
				}
				return err
			}
		}
		return insertRun(db, convID, record)
	})
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
