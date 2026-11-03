// Package history is the archive of finished sessions: one SQLite file (history.db) holding each
// session's row, its stored detail and agent pages, its search text and its At a Glance summary.
// The file is the one Bun's server wrote, opened as it is.
package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	// The SQLite driver, pure Go: conn builds without cgo.
	_ "modernc.org/sqlite"
)

// SchemaVersion is the newest history.db this build understands.
const SchemaVersion = 3

const (
	// namedVersion is the version that added the named column.
	namedVersion = 2
	// repoVersion is the version that added the repo column.
	repoVersion = 3
	// repoColumn is the repo column's name.
	repoColumn = "repo"
	memory     = ":memory:"
	dirMode    = 0o700
	fileMode   = 0o600
)

var errNewer = errors.New("history database is newer than this build")

const schema = `
CREATE TABLE entries (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL, cwd TEXT NOT NULL, project TEXT NOT NULL, branch TEXT NOT NULL,
  kind TEXT NOT NULL, model TEXT NOT NULL,
  started_at INTEGER NOT NULL, ended_at INTEGER NOT NULL, cost_usd REAL NOT NULL,
  agents INTEGER NOT NULL, advisor_calls INTEGER NOT NULL, errors INTEGER NOT NULL, ` +
	`outcome TEXT NOT NULL,
  pr_number INTEGER, ticket_key TEXT COLLATE NOCASE,
  source TEXT NOT NULL, resumed_from TEXT, transcript_path TEXT NOT NULL,
  final INTEGER NOT NULL DEFAULT 0, transcript_size INTEGER,
  summary_state TEXT NOT NULL DEFAULT 'none', summary_text TEXT NOT NULL DEFAULT '', ` +
	`summary_model TEXT NOT NULL DEFAULT '',
  summary_at INTEGER NOT NULL DEFAULT 0, summary_error TEXT, glance TEXT NOT NULL DEFAULT ''
);
CREATE INDEX entries_started_at ON entries (started_at);
CREATE INDEX entries_kind ON entries (kind);
CREATE INDEX entries_project ON entries (project);
CREATE TABLE blobs (
  session_id TEXT PRIMARY KEY REFERENCES entries (id) ON DELETE CASCADE,
  detail_json TEXT NOT NULL, agents_json TEXT NOT NULL, first_prompt TEXT NOT NULL, ` +
	`final_message TEXT NOT NULL
);
CREATE VIRTUAL TABLE entries_fts USING fts5 (search_text, summary, tokenize = 'unicode61');
CREATE TABLE failures (id TEXT PRIMARY KEY, error TEXT NOT NULL, at INTEGER NOT NULL);
`

// DB is the archive. It is safe to use from several goroutines: one connection, so every
// statement and transaction runs in turn, as Bun's did.
type DB struct {
	db *sql.DB
	// originOf is where a folder's repo is read from, for the migration that fills repo.
	originOf func(folder string) string
	// now is the clock for summary and failure times.
	now func() float64
}

// Options are what a DB reads besides its file.
type Options struct {
	// OriginOf is the repo of a folder that exists, "" for none.
	OriginOf func(folder string) string
	Now      func() float64
}

// Open opens (or creates) the archive at path. A file that is not a database, or a newer one,
// is an error and is left exactly as it is: the version is checked before any pragma that
// writes. The archive is owner-only, as the transcripts it holds are.
func Open(ctx context.Context, path string, opts Options) (*DB, error) {
	onDisk := path != memory
	if onDisk {
		err := create(path)
		if err != nil {
			return nil, err
		}
	}

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}

	conn.SetMaxOpenConns(1)

	store := &DB{db: conn, originOf: opts.OriginOf, now: opts.Now}

	err = store.prepare(ctx)
	if err != nil {
		_ = conn.Close()

		return nil, err
	}

	if onDisk {
		keepPrivate(path)
	}

	return store, nil
}

// Close closes the file.
func (d *DB) Close() error {
	err := d.db.Close()
	if err != nil {
		return fmt.Errorf("closing history: %w", err)
	}

	return nil
}

// create makes the folder and an empty file, private from the first byte: there is no moment the
// file exists readable by others.
func create(path string) error {
	err := os.MkdirAll(filepath.Dir(path), dirMode)
	if err != nil {
		return fmt.Errorf("making %s: %w", filepath.Dir(path), err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, fileMode)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}

	err = file.Close()
	if err != nil {
		return fmt.Errorf("closing %s: %w", path, err)
	}

	return nil
}

// keepPrivate makes the file and its WAL files owner-only, tightening one an earlier build or a
// crash left readable.
func keepPrivate(path string) {
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Chmod(name, fileMode)
	}
}

func (d *DB) prepare(ctx context.Context) error {
	_, err := d.version(ctx)
	if err != nil {
		return err
	}

	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL", "PRAGMA synchronous = NORMAL", "PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
	} {
		_, err = d.db.ExecContext(ctx, pragma)
		if err != nil {
			return fmt.Errorf("%s: %w", pragma, err)
		}
	}

	return d.migrate(ctx)
}

// version is the file's user_version; a newer one than this build understands is an error.
func (d *DB) version(ctx context.Context) (int, error) {
	var version int

	err := d.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("file is not a database: %w", err)
	}

	if version > SchemaVersion {
		return 0, fmt.Errorf("%w: version %d, this build understands up to %d", errNewer,
			version, SchemaVersion)
	}

	return version, nil
}

// migrate brings the file to SchemaVersion: version 0 is a fresh file; later versions add steps,
// never drop data.
func (d *DB) migrate(ctx context.Context) error {
	version, err := d.version(ctx)
	if err != nil {
		return err
	}

	if version == SchemaVersion {
		return nil
	}

	return d.transaction(ctx, func(txn *sql.Tx) error {
		if version < 1 {
			_, err := txn.ExecContext(ctx, schema)
			if err != nil {
				return fmt.Errorf("creating the schema: %w", err)
			}
		}

		// Rows from before this column read as not named, so a resume of one leaves the
		// session's name alone.
		if version < namedVersion {
			_, err := txn.ExecContext(ctx,
				"ALTER TABLE entries ADD COLUMN named INTEGER NOT NULL DEFAULT 0")
			if err != nil {
				return fmt.Errorf("adding named: %w", err)
			}
		}

		if version < repoVersion {
			err := d.addRepo(ctx, txn)
			if err != nil {
				return err
			}
		}

		_, err := txn.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(SchemaVersion))
		if err != nil {
			return fmt.Errorf("setting the version: %w", err)
		}

		return nil
	})
}

// addRepo is version 3: the repo column, filled for the rows whose folder still exists. The rest
// stay NULL, which counts as in no repo: visible. Idempotent.
func (d *DB) addRepo(ctx context.Context, txn *sql.Tx) error {
	has, err := hasColumn(ctx, txn, repoColumn)
	if err != nil {
		return err
	}

	if !has {
		_, err = txn.ExecContext(ctx, "ALTER TABLE entries ADD COLUMN repo TEXT")
		if err != nil {
			return fmt.Errorf("adding repo: %w", err)
		}
	}

	_, err = txn.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS entries_repo ON entries (repo)")
	if err != nil {
		return fmt.Errorf("indexing repo: %w", err)
	}

	return d.fillRepos(ctx, txn)
}

func (d *DB) fillRepos(ctx context.Context, txn *sql.Tx) error {
	if d.originOf == nil {
		return nil
	}

	folders, err := strings1(ctx, txn,
		"SELECT DISTINCT cwd FROM entries WHERE repo IS NULL AND cwd <> ''")
	if err != nil {
		return err
	}

	for _, folder := range folders {
		repo := d.originOf(folder)
		if repo == "" {
			continue
		}

		_, err = txn.ExecContext(ctx, "UPDATE entries SET repo = ? WHERE repo IS NULL AND cwd = ?",
			repo, folder)
		if err != nil {
			return fmt.Errorf("filling repo: %w", err)
		}
	}

	return nil
}

// queryer is a connection or a transaction.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// strings1 is the one text column of a query's rows.
func strings1(ctx context.Context, from queryer, query string, args ...any) ([]string, error) {
	rows, err := from.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []string{}

	for rows.Next() {
		var value string

		err = rows.Scan(&value)
		if err != nil {
			return nil, fmt.Errorf("reading: %w", err)
		}

		out = append(out, value)
	}

	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("reading: %w", err)
	}

	return out, nil
}

// hasColumn is whether the entries table has the named column.
func hasColumn(ctx context.Context, txn *sql.Tx, column string) (bool, error) {
	rows, err := txn.QueryContext(ctx, "PRAGMA table_info(entries)")
	if err != nil {
		return false, fmt.Errorf("reading the columns: %w", err)
	}

	defer func() { _ = rows.Close() }()

	has := false

	for rows.Next() {
		var (
			index, notNull, primary int
			name, kind              string
			fallback                sql.NullString
		)

		err = rows.Scan(&index, &name, &kind, &notNull, &fallback, &primary)
		if err != nil {
			return false, fmt.Errorf("reading a column: %w", err)
		}

		if name == column {
			has = true
		}
	}

	err = rows.Err()
	if err != nil {
		return false, fmt.Errorf("reading the columns: %w", err)
	}

	return has, nil
}

// transaction runs work in one transaction, rolled back when it fails.
func (d *DB) transaction(ctx context.Context, work func(txn *sql.Tx) error) error {
	txn, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting a transaction: %w", err)
	}

	err = work(txn)
	if err != nil {
		_ = txn.Rollback()

		return err
	}

	err = txn.Commit()
	if err != nil {
		return fmt.Errorf("committing: %w", err)
	}

	return nil
}
