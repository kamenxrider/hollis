// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package store

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func migrationFixture(t *testing.T, version int) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hollis.db")
	db := rawDatabase(t, path)
	if version == 2 {
		execFixture(t, db, schema)
		execFixture(t, db, `CREATE UNIQUE INDEX idx_messages_conv_seq_unique ON messages(conversation_id,seq)`)
	} else {
		execFixture(t, db, `
			CREATE TABLE conversations (
				id TEXT PRIMARY KEY, title TEXT, model TEXT NOT NULL, summary TEXT,
				created_at TEXT NOT NULL, updated_at TEXT NOT NULL, archived INTEGER NOT NULL DEFAULT 0);
			CREATE TABLE messages (
				id INTEGER PRIMARY KEY AUTOINCREMENT, conversation_id TEXT NOT NULL,
				seq INTEGER NOT NULL, role TEXT NOT NULL, content TEXT NOT NULL,
				created_at TEXT NOT NULL, metadata_json TEXT);
			CREATE TABLE runs (
				id INTEGER PRIMARY KEY AUTOINCREMENT, conversation_id TEXT, model TEXT NOT NULL,
				started_at TEXT NOT NULL, duration_ms INTEGER, exit_code INTEGER,
				error_class TEXT, stderr_excerpt TEXT);`)
	}
	execFixture(t, db, fmt.Sprintf("PRAGMA user_version = %d", version))
	execFixture(t, db, `INSERT INTO conversations VALUES
		('fixture', 'migration title', 'cloud', 'summary', '2026-10-31T16:00:00Z', '2026-10-31T16:00:00.11Z', 0)`)
	execFixture(t, db, `INSERT INTO messages (conversation_id,seq,role,content,created_at,metadata_json)
		VALUES ('fixture',0,'user','migration searchable transcript', '2026-10-31T16:00:00Z','opaque metadata')`)
	return path, db
}

func rawDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	databaseURL := &url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite", databaseURL.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func execFixture(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func backupsFor(t *testing.T, path string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".hollis.db.backup-*"))
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// Include FTS shadow tables so failed migrations cannot hide partial index changes.
func databaseSnapshot(t *testing.T, db *sql.DB) string {
	t.Helper()
	version, err := schemaVersionOf(db)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT name, COALESCE(sql,'') FROM sqlite_master ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "version=%d\n", version)
	var tables []string
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&out, "%s:%s\n", name, ddl)
		if strings.HasPrefix(ddl, "CREATE TABLE") || strings.HasPrefix(ddl, "CREATE VIRTUAL TABLE") {
			tables = append(tables, name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, name := range tables {
		rows, err := db.Query(`SELECT * FROM "` + strings.ReplaceAll(name, `"`, `""`) + `"`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var records []string
		for rows.Next() {
			values := make([]any, len(columns))
			ptrs := make([]any, len(columns))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			records = append(records, fmt.Sprintf("%s:%#v\n", name, values))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		sort.Strings(records)
		for _, record := range records {
			out.WriteString(record)
		}
	}
	return out.String()
}

func TestMigration39LegacyVersionsAndHealthyReopen(t *testing.T) {
	for _, version := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			path, db := migrationFixture(t, version)
			st := mustOpen(t, path)
			msgs, err := st.Messages("fixture")
			if err != nil || len(msgs) != 1 || msgs[0].Content != "migration searchable transcript" {
				t.Fatalf("messages=%+v err=%v", msgs, err)
			}
			var metadata string
			if err := st.db.QueryRow(`SELECT metadata_json FROM messages`).Scan(&metadata); err != nil || metadata != "opaque metadata" {
				t.Fatalf("metadata=%q err=%v", metadata, err)
			}
			conv, err := st.GetConversation("fixture")
			if err != nil || conv.Summary != "summary" || conv.Model != "cloud" || conv.Title != "migration title" || conv.UpdatedAt != "2026-10-31T16:00:00.11Z" {
				t.Fatalf("conversation=%+v err=%v", conv, err)
			}
			matches, err := st.Search("migration searchable", "", 20)
			if err != nil || len(matches) != 1 {
				t.Fatalf("matches=%+v err=%v", matches, err)
			}
			if got, err := schemaVersionOf(db); err != nil || got != 3 {
				t.Fatalf("version=%d err=%v", got, err)
			}
			if len(backupsFor(t, path)) != 1 {
				t.Fatal("upgrade must retain exactly one backup")
			}
			before := databaseSnapshot(t, db)
			st.Close()
			st = mustOpen(t, path)
			st.Close()
			if after := databaseSnapshot(t, db); after != before || len(backupsFor(t, path)) != 1 {
				t.Fatal("healthy reopen changed database or created another backup")
			}
		})
	}
}

func TestMigration39RejectsBadTimeWithoutPartialDDL(t *testing.T) {
	for _, text := range []string{"", "not-a-time", "0001-01-01T00:00:00Z", "9999-12-31T23:59:59Z",
		"1677-09-21T00:12:43.145224191Z", "2262-04-11T23:47:16.854775808Z"} {
		t.Run(text, func(t *testing.T) {
			path, db := migrationFixture(t, 0)
			execFixture(t, db, `UPDATE conversations SET updated_at = ?`, text)
			before := databaseSnapshot(t, db)
			st, err := Open(path)
			if st != nil || err == nil || !strings.Contains(err.Error(), `conversation "fixture"`) || !strings.Contains(err.Error(), "invalid or out-of-range updated_at") {
				t.Fatalf("store=%v error=%v", st, err)
			}
			if databaseSnapshot(t, db) != before {
				t.Fatal("failed migration changed schema/data/version")
			}
			backups := backupsFor(t, path)
			if len(backups) != 1 || databaseSnapshot(t, rawDatabase(t, backups[0])) != before {
				t.Fatal("failed migration did not retain the original logical backup")
			}
			execFixture(t, db, `UPDATE conversations SET updated_at = '2026-10-31T16:00:00Z'`)
			st = mustOpen(t, path)
			st.Close()
		})
	}
}

func TestMigration39FailureStagesRollBackAndRetry(t *testing.T) {
	for _, stage := range []string{"ddl", "duplicate repair", "time backfill", "fts rebuild"} {
		t.Run(stage, func(t *testing.T) {
			path, db := migrationFixture(t, 0)
			var undo string
			switch stage {
			case "ddl":
				execFixture(t, db, `CREATE VIEW messages_fts AS SELECT content FROM messages`)
				undo = `DROP VIEW messages_fts`
			case "duplicate repair":
				execFixture(t, db, `INSERT INTO messages (conversation_id,seq,role,content,created_at)
					VALUES ('fixture',0,'assistant','second message','2026-10-31T16:00:00Z')`)
				execFixture(t, db, `CREATE TRIGGER fail_repair BEFORE UPDATE OF seq ON messages
					BEGIN SELECT RAISE(ABORT,'injected duplicate repair failure'); END`)
				undo = `DROP TRIGGER fail_repair`
			case "time backfill":
				execFixture(t, db, `CREATE TRIGGER fail_backfill BEFORE UPDATE ON conversations
					BEGIN SELECT RAISE(ABORT,'injected time backfill failure'); END`)
				undo = `DROP TRIGGER fail_backfill`
			case "fts rebuild":
				execFixture(t, db, schema)
				execFixture(t, db, `CREATE TRIGGER fail_fts BEFORE INSERT ON messages_fts_data
					BEGIN SELECT RAISE(ABORT,'injected FTS failure'); END`)
				undo = `DROP TRIGGER fail_fts`
			}
			before := databaseSnapshot(t, db)
			if st, err := Open(path); err == nil {
				st.Close()
				t.Fatal("injected migration succeeded")
			}
			if databaseSnapshot(t, db) != before {
				t.Fatal("failure left partial schema/data/index/version changes")
			}
			if len(backupsFor(t, path)) != 1 {
				t.Fatal("failure did not retain a backup")
			}
			execFixture(t, db, undo)
			st := mustOpen(t, path)
			defer st.Close()
			matches, err := st.Search("migration searchable", "", 20)
			if err != nil || len(matches) != 1 {
				t.Fatalf("retry search=%+v err=%v", matches, err)
			}
		})
	}
}

func TestMigration39WALBackupAndExplicitRecovery(t *testing.T) {
	path, db := migrationFixture(t, 0)
	execFixture(t, db, `PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0`)
	execFixture(t, db, `INSERT INTO messages (conversation_id,seq,role,content,created_at)
		VALUES ('fixture',0,'assistant','committed WAL message','2026-10-31T16:00:00Z')`)
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("fixture has no WAL content: %v", err)
	}
	before := databaseSnapshot(t, db)
	prior := filepath.Join(filepath.Dir(path), ".hollis.db.backup-prior")
	if err := os.WriteFile(prior, []byte("preserve prior backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := mustOpen(t, path)
	defer st.Close()
	var backup string
	for _, candidate := range backupsFor(t, path) {
		if candidate != prior {
			backup = candidate
		}
	}
	if backup == "" || databaseSnapshot(t, rawDatabase(t, backup)) != before {
		t.Fatal("backup omitted original version, duplicate sequence, or committed WAL data")
	}
	if info, err := os.Stat(backup); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode: info=%v err=%v", info, err)
	}
	if data, err := os.ReadFile(prior); err != nil || string(data) != "preserve prior backup" {
		t.Fatal("prior backup changed")
	}
	if _, err := st.CreateConversation("cloud", "after migration"); err != nil {
		t.Fatal(err)
	}
	restore := filepath.Join(t.TempDir(), "recovered.db")
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(restore, data, 0o600); err != nil {
		t.Fatal(err)
	}
	recovered := mustOpen(t, restore)
	defer recovered.Close()
	msgs, err := recovered.Messages("fixture")
	if err != nil || len(msgs) != 2 || msgs[1].Content != "committed WAL message" || msgs[1].Seq != 1 {
		t.Fatalf("recovered messages=%+v err=%v", msgs, err)
	}
	matches, err := recovered.Search("committed WAL", "", 20)
	if err != nil || len(matches) != 1 {
		t.Fatalf("recovered search=%+v err=%v", matches, err)
	}
	original, err := st.ListConversations(true)
	if err != nil || len(original) != 2 {
		t.Fatal("explicit recovery changed the upgraded database")
	}
	older, err := recovered.ListConversations(true)
	if err != nil || len(older) != 1 {
		t.Fatal("backup should not contain post-migration conversations")
	}
}

func TestMigration39BackupFailureLeavesSourceUnchanged(t *testing.T) {
	_, db := migrationFixture(t, 0)
	before := databaseSnapshot(t, db)
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := backupDatabase(db, blocker); err == nil {
		t.Fatal("backup into non-directory succeeded")
	}
	if databaseSnapshot(t, db) != before {
		t.Fatal("backup failure changed source")
	}
}

func TestMigration39RepairsEmptyFTSAtomically(t *testing.T) {
	path, db := migrationFixture(t, 2)
	st := mustOpen(t, path)
	st.Close()
	execFixture(t, db, `INSERT INTO messages_fts(messages_fts) VALUES('delete-all')`)
	execFixture(t, db, `CREATE TRIGGER fail_fts_recovery BEFORE INSERT ON messages_fts_data
		BEGIN SELECT RAISE(ABORT,'injected recovery failure'); END`)
	before := databaseSnapshot(t, db)
	if st, err := Open(path); err == nil {
		st.Close()
		t.Fatal("injected FTS recovery succeeded")
	}
	if databaseSnapshot(t, db) != before || len(backupsFor(t, path)) != 2 {
		t.Fatal("FTS repair failure changed original or lost backup")
	}
	execFixture(t, db, `DROP TRIGGER fail_fts_recovery`)
	st = mustOpen(t, path)
	defer st.Close()
	matches, err := st.Search("migration searchable", "", 20)
	if err != nil || len(matches) != 1 {
		t.Fatalf("recovery matches=%+v err=%v", matches, err)
	}
}

func TestMigration39ConcurrentOpeners(t *testing.T) {
	path, _ := migrationFixture(t, 2)
	const count = 8
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for range count {
		wg.Go(func() {
			st, err := Open(path)
			if err == nil {
				err = st.Close()
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(backupsFor(t, path)) != 1 {
		t.Fatal("serialized migration created redundant backups")
	}
}

func TestMigration39LockIsPrivateBoundedAndRejectsSymlinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hollis.db")
	unlock, err := lockStateFile(context.Background(), path+".migration.lock")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := lockStateFile(ctx, path+".migration.lock"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock error=%v", err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path + ".migration.lock"); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode: %v %v", info, err)
	}
	other := filepath.Join(t.TempDir(), "linked.db")
	if err := os.Symlink(path+".migration.lock", other+".migration.lock"); err != nil {
		t.Fatal(err)
	}
	if st, err := Open(other); err == nil {
		st.Close()
		t.Fatal("symlinked migration lock accepted")
	}
}

func TestMigration39FreshAndFutureSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hollis.db")
	st := mustOpen(t, path)
	if version, err := schemaVersionOf(st.db); err != nil || version != 3 || len(backupsFor(t, path)) != 0 {
		t.Fatalf("fresh version=%d err=%v backups=%v", version, err, backupsFor(t, path))
	}
	execFixture(t, st.db, `PRAGMA user_version=99`)
	before := databaseSnapshot(t, st.db)
	if other, err := Open(path); err == nil {
		other.Close()
		t.Fatal("future schema accepted")
	}
	if databaseSnapshot(t, st.db) != before || len(backupsFor(t, path)) != 0 {
		t.Fatal("future-version refusal changed database or created backup")
	}
	st.Close()
}

func TestMigration39InterruptedUpgradeRollsBack(t *testing.T) {
	const crashPath = "HOLLIS_TEST_MIGRATION_CRASH_PATH"
	if path := os.Getenv(crashPath); path != "" {
		db := rawDatabase(t, path)
		st := &Store{db: db}
		err := st.withWriteTx(func(conn dbConn) error {
			tx := migrationConn{conn}
			for _, step := range []func() error{
				func() error { _, err := tx.Exec(schema); return err },
				func() error { return migrateLegacy(tx) },
				func() error { return migrateConversationTime(tx) },
				func() error { return backfillFTS(tx) },
				func() error { _, err := tx.Exec("PRAGMA user_version=3"); return err },
			} {
				if err := step(); err != nil {
					return err
				}
			}
			fmt.Println("migration pending")
			// The parent kills this process before the transaction can commit.
			time.Sleep(time.Hour)
			return errors.New("interrupted migration helper was not killed")
		})
		t.Fatal(err)
	}
	path, db := migrationFixture(t, 0)
	before := databaseSnapshot(t, db)
	if _, err := backupDatabase(db, filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMigration39InterruptedUpgradeRollsBack$")
	cmd.Env = append(os.Environ(), crashPath+"="+path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var childError bytes.Buffer
	cmd.Stderr = &childError
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "migration pending\n" {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("child did not reach uncommitted migration: %q %v %s", line, err, childError.String())
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	var exitErr *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &exitErr) || exitErr.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("child was not killed during migration: %v %s", err, childError.String())
	}
	if databaseSnapshot(t, db) != before || len(backupsFor(t, path)) != 1 {
		t.Fatal("interrupted migration changed original or lost backup")
	}
	st := mustOpen(t, path)
	st.Close()
}

func TestMigration39OlderRuntimeRefusesV3(t *testing.T) {
	binary := os.Getenv("HOLLIS_SCHEMA_V2_RUNTIME")
	if binary == "" {
		t.Skip("set HOLLIS_SCHEMA_V2_RUNTIME to the pinned 0.4.0 executable for the compatibility gate")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("HOLLIS_SCHEMA_V2_RUNTIME must be absolute")
	}
	path, db := migrationFixture(t, 2)
	st := mustOpen(t, path)
	postMigration, err := st.CreateConversationWithTurn("cloud", "after migration", "new question", "new answer", RunRecord{Model: "cloud"})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	before := databaseSnapshot(t, db)
	backups := backupsFor(t, path)
	var backupBytes [][]byte
	for _, path := range backups {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		backupBytes = append(backupBytes, data)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "chats", "list", "--json")
	t.Setenv("HOLLIS_STATE_DIR", filepath.Dir(path))
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 10 ||
		!strings.Contains(string(output), "database schema version 3 is newer than supported version 2") {
		t.Fatalf("older executable did not clearly refuse v3: err=%v output=%s", err, output)
	}
	if databaseSnapshot(t, db) != before || !reflect.DeepEqual(backupsFor(t, path), backups) {
		t.Fatal("older executable changed schema/version/data or backup inventory")
	}
	for i, path := range backups {
		data, err := os.ReadFile(path)
		if err != nil || !reflect.DeepEqual(data, backupBytes[i]) {
			t.Fatal("older executable replaced a backup")
		}
	}
	st = mustOpen(t, path)
	defer st.Close()
	if _, err := st.GetConversation(postMigration.ID); err != nil {
		t.Fatalf("post-migration conversation lost: %v", err)
	}
	t.Log("schema-v2 executable refused v3 with exit 10; logical data and backups unchanged")
}

func TestTimestamp16LegacyOrderingAndIntegerBoundaries(t *testing.T) {
	path, db := migrationFixture(t, 2)
	execFixture(t, db, `DELETE FROM messages; DELETE FROM conversations`)
	fixtures := []struct{ id, text string }{
		{"a-tie", "2026-10-31T16:00:00.11Z"},
		{"b-tie", "2026-10-31T18:00:00.110000000+02:00"},
		{"older", "2026-10-31T16:00:00.1Z"},
		{"whole", "2026-10-31T16:00:00Z"},
		{"newer-ns", "2026-10-31T16:00:00.110000001Z"},
		{"old-offset", "2026-10-31T18:00:00.11+03:00"},
		{"pre-epoch", "1969-12-31T23:59:59.999999999Z"},
		{"min", time.Unix(0, math.MinInt64).UTC().Format(time.RFC3339Nano)},
		{"max", time.Unix(0, math.MaxInt64).UTC().Format(time.RFC3339Nano)},
	}
	for _, fixture := range fixtures {
		execFixture(t, db, `INSERT INTO conversations (id,title,model,summary,created_at,updated_at)
			VALUES (?,?,'cloud','',?,?)`, fixture.id, fixture.id, fixture.text, fixture.text)
	}
	execFixture(t, db, `INSERT INTO messages (conversation_id,seq,role,content,created_at)
		VALUES ('older',0,'user','count me','2026-10-31T16:00:00Z')`)
	st := mustOpen(t, path)
	defer st.Close()
	want := []string{"max", "newer-ns", "a-tie", "b-tie", "older", "whole", "old-offset", "pre-epoch", "min"}
	for _, archived := range []bool{false, true} {
		convs, err := st.ListConversations(archived)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, c := range convs {
			ids = append(ids, c.ID)
			if c.ID == "older" && c.Messages != 1 || c.ID != "older" && c.Messages != 0 {
				t.Fatalf("wrong count for %+v", c)
			}
		}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("ids=%v want=%v", ids, want)
		}
	}
	execFixture(t, db, `UPDATE conversations SET archived=1 WHERE id='max'`)
	convs, err := st.ListConversations(false)
	if err != nil || len(convs) != len(want)-1 || convs[0].ID != "newer-ns" {
		t.Fatalf("archived filter=%+v err=%v", convs, err)
	}
	for _, fixture := range fixtures {
		var text string
		var ns int64
		if err := db.QueryRow(`SELECT updated_at,updated_at_ns FROM conversations WHERE id=?`, fixture.id).Scan(&text, &ns); err != nil {
			t.Fatal(err)
		}
		parsed, _ := time.Parse(time.RFC3339Nano, fixture.text)
		if text != fixture.text || ns != parsed.UnixNano() {
			t.Fatalf("backfill changed timestamp or lost precision: %s/%d", text, ns)
		}
	}
}

func TestTimestamp16EveryWriteKeepsTimeRepresentationsTogether(t *testing.T) {
	st := openTemp(t)
	defer st.Close()
	for _, op := range []string{"create", "create turn", "append message", "append turn", "title", "summary", "touch"} {
		t.Run(op, func(t *testing.T) {
			c, err := st.CreateConversation("cloud", op)
			if err != nil {
				t.Fatal(err)
			}
			epoch := time.Unix(0, 0).UTC().Format(time.RFC3339Nano)
			if op != "create" {
				execFixture(t, st.db, `UPDATE conversations SET updated_at=?,updated_at_ns=0 WHERE id=?`, epoch, c.ID)
			}
			switch op {
			case "create turn":
				c, err = st.CreateConversationWithTurn("cloud", op, "question", "answer", RunRecord{Model: "cloud"})
			case "append message":
				_, err = st.AppendMessage(c.ID, "user", "message")
			case "append turn":
				err = st.AppendTurn(c.ID, "question", "answer", RunRecord{Model: "cloud"})
			case "title":
				err = st.SetTitle(c.ID, "new title")
			case "summary":
				err = st.SetSummary(c.ID, "new summary")
			case "touch":
				err = st.touchConversation(c.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			var text string
			var ns int64
			if err := st.db.QueryRow(`SELECT updated_at,updated_at_ns FROM conversations WHERE id=?`, c.ID).Scan(&text, &ns); err != nil {
				t.Fatal(err)
			}
			at, err := time.Parse(time.RFC3339Nano, text)
			if err != nil || ns == 0 || at.UnixNano() != ns {
				t.Fatalf("time representations disagree: text=%s ns=%d err=%v", text, ns, err)
			}
		})
	}
}

func TestTimestamp16ListUsesOrderingIndexes(t *testing.T) {
	st := openTemp(t)
	defer st.Close()
	for _, archived := range []bool{false, true} {
		rows, err := st.db.Query("EXPLAIN QUERY PLAN " + listConversationsQuery(archived))
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		plan := strings.Join(details, "\n")
		index := "idx_conversations_updated"
		if !archived {
			index = "idx_conversations_archived_updated"
		}
		if !strings.Contains(plan, index) || strings.Contains(plan, "TEMP B-TREE FOR ORDER BY") {
			t.Fatalf("unexpected list query plan:\n%s", plan)
		}
	}
}
