// Package db opens and migrates the SQLite database.
//
// # Why two connection pools
//
// SQLite allows many concurrent readers but only one writer. Left to a single
// pool, the HTTP handlers and the background workers contend for write locks and
// users see intermittent "database is locked" errors — the most common way a
// Go+SQLite application goes wrong in production.
//
// So writes go through a pool capped at one connection, which turns lock
// contention into ordinary queueing inside the process, and reads go through a
// separate pool that WAL mode lets run concurrently with the writer. The cost is
// remembering which pool a query belongs to; [DB.Read] and [DB.Write] make that
// explicit at the call site.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"runtime"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // pure-Go SQLite driver, so builds stay CGO-free
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// driverName is the database/sql driver registered by modernc.org/sqlite.
const driverName = "sqlite"

// DB holds the reader and writer pools for one SQLite database.
type DB struct {
	// write is capped at a single connection. All INSERT, UPDATE, and DELETE
	// traffic goes here.
	write *sql.DB
	// read serves SELECTs concurrently.
	read *sql.DB

	path string
	log  *slog.Logger
}

// Options configures [Open].
type Options struct {
	// Path is the database file. The special value ":memory:" opens a private
	// in-memory database, which is only useful for tests.
	Path string
	// Logger receives migration and lifecycle messages.
	Logger *slog.Logger
	// MaxReaders caps the read pool. Zero picks a value from the CPU count.
	MaxReaders int
	// BusyTimeout is how long SQLite waits on a locked database before giving
	// up. The single-writer pool makes contention rare, but a concurrent process
	// (a backup, or a user in a sqlite3 shell) can still hold a lock briefly.
	BusyTimeout time.Duration
}

// Open opens the database, applies connection pragmas, and runs all pending
// migrations. The returned DB is ready for use.
func Open(ctx context.Context, opts Options) (*DB, error) {
	if opts.Path == "" {
		return nil, errors.New("db: Path is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.MaxReaders <= 0 {
		opts.MaxReaders = max(4, runtime.NumCPU())
	}
	if opts.BusyTimeout <= 0 {
		opts.BusyTimeout = 5 * time.Second
	}

	memory := opts.Path == ":memory:"

	write, err := openPool(opts, memory, true)
	if err != nil {
		return nil, fmt.Errorf("open write pool: %w", err)
	}
	// A single connection: SQLite serializes writers anyway, and doing the
	// queueing here yields a clean wait instead of a SQLITE_BUSY error.
	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)
	write.SetConnMaxIdleTime(0)

	if err := write.PingContext(ctx); err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("open %s: %w", opts.Path, err)
	}

	d := &DB{write: write, path: opts.Path, log: opts.Logger}

	if err := d.migrate(ctx); err != nil {
		_ = write.Close()
		return nil, err
	}

	// An in-memory database is private to its connection, so a second pool would
	// see an entirely different, empty database. Share the one connection.
	if memory {
		d.read = write
		return d, nil
	}

	read, err := openPool(opts, memory, false)
	if err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("open read pool: %w", err)
	}
	read.SetMaxOpenConns(opts.MaxReaders)
	read.SetMaxIdleConns(opts.MaxReaders)
	if err := read.PingContext(ctx); err != nil {
		_ = write.Close()
		_ = read.Close()
		return nil, fmt.Errorf("ping read pool: %w", err)
	}
	d.read = read

	return d, nil
}

// openPool builds one pool. Pragmas are set through the DSN so that every new
// connection gets them; setting them with a one-off Exec would apply to a single
// pooled connection and silently miss the rest.
func openPool(opts Options, memory, writer bool) (*sql.DB, error) {
	q := url.Values{}
	// WAL lets readers run concurrently with the writer and survives a crash
	// without losing committed transactions.
	if !memory {
		q.Add("_pragma", "journal_mode(WAL)")
	}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", opts.BusyTimeout.Milliseconds()))
	// Referential integrity is off by default in SQLite; the schema relies on it.
	q.Add("_pragma", "foreign_keys(1)")
	// NORMAL is the accepted WAL pairing: durable across process crashes, and
	// only at risk from an OS-level crash, which is the right trade for a
	// household server against a full fsync on every commit.
	q.Add("_pragma", "synchronous(NORMAL)")
	if writer {
		// Fail fast rather than hang if another process holds the write lock.
		q.Add("_txlock", "immediate")
	}

	dsn := opts.Path
	if memory {
		dsn = ":memory:"
	}
	return sql.Open(driverName, dsn+"?"+q.Encode())
}

// migrate applies pending migrations using the embedded SQL files.
func (d *DB) migrate(ctx context.Context) error {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("locate embedded migrations: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectSQLite3, d.write, sub)
	if err != nil {
		return fmt.Errorf("init migrations: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	for _, r := range results {
		d.log.Info("applied migration", "version", r.Source.Version, "name", r.Source.Path, "duration", r.Duration)
	}

	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	d.log.Debug("database ready", "schema_version", version, "migrations_applied", len(results))
	return nil
}

// Read returns the pool for queries that do not modify data.
func (d *DB) Read() *sql.DB { return d.read }

// Write returns the pool for statements that modify data.
func (d *DB) Write() *sql.DB { return d.write }

// Tx runs fn inside a write transaction, committing on success and rolling back
// on error or panic.
func (d *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			// Rollback failure after a failed body has nothing useful to add;
			// the body's error is the one worth reporting.
			_ = tx.Rollback()
		}
	}()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}

// Version reports the applied schema version, for diagnostics.
func (d *DB) Version(ctx context.Context) (int64, error) {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return 0, err
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, d.write, sub)
	if err != nil {
		return 0, err
	}
	return provider.GetDBVersion(ctx)
}

// Close shuts both pools down.
func (d *DB) Close() error {
	var errs []error
	if d.read != nil && d.read != d.write {
		if err := d.read.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close read pool: %w", err))
		}
	}
	if d.write != nil {
		if err := d.write.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close write pool: %w", err))
		}
	}
	return errors.Join(errs...)
}
