package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gordon2/manualbox/internal/id"
)

// open returns a migrated database backed by a real file, since several
// behaviours under test (WAL, a second pool) do not exist for :memory:.
func open(t *testing.T) *DB {
	t.Helper()
	d, err := Open(context.Background(), Options{
		Path: filepath.Join(t.TempDir(), "test.db"),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return d
}

func TestOpenRunsMigrations(t *testing.T) {
	d := open(t)

	version, err := d.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version < 1 {
		t.Errorf("schema version = %d, want at least 1", version)
	}

	for _, table := range []string{"users", "sessions", "settings", "blobs", "jobs"} {
		var name string
		err := d.Read().QueryRowContext(context.Background(),
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing after migration: %v", table, err)
		}
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	first, err := Open(context.Background(), Options{Path: path})
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Re-opening an already-migrated database must be a no-op, not an error.
	second, err := Open(context.Background(), Options{Path: path})
	if err != nil {
		t.Fatalf("second Open on an existing database: %v", err)
	}
	defer second.Close()

	if _, err := second.Version(context.Background()); err != nil {
		t.Errorf("Version after reopen: %v", err)
	}
}

func TestOpenRequiresPath(t *testing.T) {
	if _, err := Open(context.Background(), Options{}); err == nil {
		t.Fatal("Open with no path should fail")
	}
}

func TestPragmasApplied(t *testing.T) {
	d := open(t)
	ctx := context.Background()

	// Both pools must carry the pragmas. Setting them via a one-off Exec would
	// only affect a single pooled connection, so check each pool separately.
	for name, pool := range map[string]*sql.DB{"read": d.Read(), "write": d.Write()} {
		var journal string
		if err := pool.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
			t.Fatalf("%s pool: read journal_mode: %v", name, err)
		}
		if !strings.EqualFold(journal, "wal") {
			t.Errorf("%s pool: journal_mode = %q, want wal", name, journal)
		}

		var fk int
		if err := pool.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatalf("%s pool: read foreign_keys: %v", name, err)
		}
		if fk != 1 {
			t.Errorf("%s pool: foreign_keys = %d, want 1 — the schema relies on cascades", name, fk)
		}
	}
}

// TestFTS5Available guards the assumption M1's whole search feature rests on: the
// pure-Go driver must ship FTS5. Discovering this at the start of M1 instead of
// here would mean rethinking the search design.
func TestFTS5Available(t *testing.T) {
	d := open(t)
	ctx := context.Background()

	if _, err := d.Write().ExecContext(ctx,
		`CREATE VIRTUAL TABLE fts_probe USING fts5(body)`); err != nil {
		t.Fatalf("FTS5 is not available in this SQLite build: %v", err)
	}
	if _, err := d.Write().ExecContext(ctx,
		`INSERT INTO fts_probe(body) VALUES ('entkalken sie das gerät alle drei monate')`); err != nil {
		t.Fatalf("insert into FTS5 table: %v", err)
	}

	var body string
	err := d.Read().QueryRowContext(ctx,
		`SELECT body FROM fts_probe WHERE fts_probe MATCH 'entkalken'`).Scan(&body)
	if err != nil {
		t.Fatalf("FTS5 MATCH query failed: %v", err)
	}
	if !strings.Contains(body, "entkalken") {
		t.Errorf("unexpected match result %q", body)
	}

	// Prefix queries back the search-as-you-type UI.
	var n int
	if err := d.Read().QueryRowContext(ctx,
		`SELECT count(*) FROM fts_probe WHERE fts_probe MATCH 'monat*'`).Scan(&n); err != nil {
		t.Fatalf("FTS5 prefix query failed: %v", err)
	}
	if n != 1 {
		t.Errorf("prefix match returned %d rows, want 1", n)
	}

	// Non-ASCII must be indexed and searchable: the manuals this is built for are
	// German, Ukrainian, and French far more often than English.
	if err := d.Read().QueryRowContext(ctx,
		`SELECT count(*) FROM fts_probe WHERE fts_probe MATCH 'gerät'`).Scan(&n); err != nil {
		t.Fatalf("FTS5 query with a diacritic failed: %v", err)
	}
	if n != 1 {
		t.Errorf("searching for 'gerät' returned %d rows, want 1", n)
	}
}

func TestStrictTablesRejectWrongTypes(t *testing.T) {
	d := open(t)

	// size_bytes is INTEGER in a STRICT table, so text must be refused rather
	// than silently coerced.
	_, err := d.Write().ExecContext(context.Background(),
		`INSERT INTO blobs (sha256, size_bytes, media_type, created_at) VALUES (?, ?, ?, ?)`,
		"deadbeef", "not-a-number", "application/pdf", Now())
	if err == nil {
		t.Error("STRICT table accepted a text value in an INTEGER column")
	}
}

func TestCheckConstraintsEnforced(t *testing.T) {
	d := open(t)
	ctx := context.Background()

	// progress is constrained to 0..1; a stray percentage would break the UI.
	_, err := d.Write().ExecContext(ctx,
		`INSERT INTO jobs (id, kind, state, progress, run_after, created_at, updated_at)
		 VALUES (?, 'test', 'queued', 42, ?, ?, ?)`,
		id.New(id.Job), Now(), Now(), Now())
	if err == nil {
		t.Error("progress = 42 should violate the CHECK constraint")
	}

	// state is a closed set.
	_, err = d.Write().ExecContext(ctx,
		`INSERT INTO jobs (id, kind, state, run_after, created_at, updated_at)
		 VALUES (?, 'test', 'bogus-state', ?, ?, ?)`,
		id.New(id.Job), Now(), Now(), Now())
	if err == nil {
		t.Error("an unknown job state should violate the CHECK constraint")
	}
}

func TestForeignKeyCascade(t *testing.T) {
	d := open(t)
	ctx := context.Background()

	userID := id.New(id.User)
	mustExec(t, d, `INSERT INTO users (id, email, email_folded, password_hash, created_at, updated_at)
	                VALUES (?, ?, ?, 'x', ?, ?)`,
		userID, "a@example.com", "a@example.com", Now(), Now())
	mustExec(t, d, `INSERT INTO sessions (id, user_id, token_hash, created_at, last_seen_at, expires_at)
	                VALUES (?, ?, ?, ?, ?, ?)`,
		id.New(id.Session), userID, []byte("hash"), Now(), Now(), Now()+1000)

	// A session referencing a missing user must be rejected outright.
	_, err := d.Write().ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, token_hash, created_at, last_seen_at, expires_at)
		 VALUES (?, 'usr_nonexistent', ?, ?, ?, ?)`,
		id.New(id.Session), []byte("other"), Now(), Now(), Now()+1000)
	if err == nil {
		t.Error("inserting a session for a nonexistent user should violate the foreign key")
	}

	// Deleting the user must take their sessions with them, so logging out
	// everywhere is a single delete.
	mustExec(t, d, `DELETE FROM users WHERE id = ?`, userID)

	var count int
	if err := d.Read().QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE user_id = ?`, userID).Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 0 {
		t.Errorf("%d sessions survived the user delete; ON DELETE CASCADE is not in effect", count)
	}
}

func TestJobDedupeIndexBlocksDuplicatePendingWork(t *testing.T) {
	d := open(t)

	insert := func(state string) error {
		_, err := d.Write().ExecContext(context.Background(),
			`INSERT INTO jobs (id, kind, state, dedupe_key, run_after, created_at, updated_at)
			 VALUES (?, 'convert', ?, 'doc_123:convert', ?, ?, ?)`,
			id.New(id.Job), state, Now(), Now(), Now())
		return err
	}

	if err := insert("queued"); err != nil {
		t.Fatalf("first queued job: %v", err)
	}
	if err := insert("queued"); err == nil {
		t.Error("a second pending job with the same dedupe key should be rejected")
	}

	// Once the first job finishes, the same work can be queued again — the index
	// is partial precisely so that re-running later is allowed.
	mustExec(t, d, `UPDATE jobs SET state = 'succeeded' WHERE dedupe_key = 'doc_123:convert'`)
	if err := insert("queued"); err != nil {
		t.Errorf("re-queueing after completion should be allowed, got: %v", err)
	}
}

func TestJobsWithoutDedupeKeyAreUnconstrained(t *testing.T) {
	d := open(t)

	for range 3 {
		_, err := d.Write().ExecContext(context.Background(),
			`INSERT INTO jobs (id, kind, state, run_after, created_at, updated_at)
			 VALUES (?, 'convert', 'queued', ?, ?, ?)`,
			id.New(id.Job), Now(), Now(), Now())
		if err != nil {
			t.Fatalf("NULL dedupe_key rows must not collide: %v", err)
		}
	}
}

func TestTxCommits(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	key := "instance_id"

	err := d.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)`, key, "abc", Now())
		return err
	})
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}

	var got string
	if err := d.Read().QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&got); err != nil {
		t.Fatalf("committed row not found: %v", err)
	}
	if got != "abc" {
		t.Errorf("value = %q, want abc", got)
	}
}

func TestTxRollsBackOnError(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	sentinel := errors.New("boom")

	err := d.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ('k', 'v', ?)`, Now()); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Tx should return the body's error unchanged, got %v", err)
	}

	var count int
	if err := d.Read().QueryRowContext(ctx, `SELECT count(*) FROM settings`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("%d rows survived a rolled-back transaction", count)
	}
}

// TestConcurrentWritesDoNotLock is the test that justifies the two-pool design.
// With a single shared pool this pattern is what produces intermittent
// "database is locked" failures.
func TestConcurrentWritesDoNotLock(t *testing.T) {
	d := open(t)
	ctx := context.Background()

	const writers, perWriter = 8, 20
	errCh := make(chan error, writers*perWriter)

	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perWriter {
				_, err := d.Write().ExecContext(ctx,
					`INSERT INTO jobs (id, kind, payload, state, run_after, created_at, updated_at)
					 VALUES (?, 'test', ?, 'queued', ?, ?, ?)`,
					id.New(id.Job), `{"w":`+itoa(w)+`,"i":`+itoa(i)+`}`, Now(), Now(), Now())
				if err != nil {
					errCh <- err
				}
			}
		}()
	}

	// Read concurrently with the writes; WAL should let these proceed.
	var readWG sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		readWG.Add(1)
		go func() {
			defer readWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
					var n int
					if err := d.Read().QueryRowContext(ctx, `SELECT count(*) FROM jobs`).Scan(&n); err != nil {
						errCh <- err
						return
					}
				}
			}
		}()
	}

	wg.Wait()
	close(stop)
	readWG.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent access failed: %v", err)
	}

	var total int
	if err := d.Read().QueryRowContext(ctx, `SELECT count(*) FROM jobs`).Scan(&total); err != nil {
		t.Fatalf("final count: %v", err)
	}
	if want := writers * perWriter; total != want {
		t.Errorf("inserted %d rows, want %d", total, want)
	}
}

func TestInMemoryDatabaseSharesOnePool(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, Options{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Open in-memory: %v", err)
	}
	defer d.Close()

	// An in-memory database is private per connection, so a separate read pool
	// would see an empty database. Writing then reading proves they are shared.
	if _, err := d.Write().ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ('k', 'v', ?)`, Now()); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var got string
	if err := d.Read().QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'k'`).Scan(&got); err != nil {
		t.Fatalf("in-memory read pool cannot see the write: %v", err)
	}
	if got != "v" {
		t.Errorf("value = %q, want v", got)
	}
}

func TestCloseIsSafe(t *testing.T) {
	d, err := Open(context.Background(), Options{Path: filepath.Join(t.TempDir(), "x.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// A second close should not panic.
	_ = d.Close()
}

func TestBusyTimeoutConfigured(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, Options{
		Path:        filepath.Join(t.TempDir(), "x.db"),
		BusyTimeout: 1234 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	var ms int
	if err := d.Read().QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&ms); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if ms != 1234 {
		t.Errorf("busy_timeout = %d, want 1234", ms)
	}
}

func mustExec(t *testing.T, d *DB, query string, args ...any) {
	t.Helper()
	if _, err := d.Write().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %s: %v", query, err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
