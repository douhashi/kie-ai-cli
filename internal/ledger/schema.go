package ledger

import (
	"context"
	"database/sql"
	"fmt"
)

// migrations holds the schema of the ledger as a sequence of versions: element
// i moves a database from PRAGMA user_version i to i+1.
//
// The list is append-only. An element that has shipped is never edited, since
// ledgers in the field have already applied it and only ever run what comes
// after; a schema change is a new element.
var migrations = [][]string{
	{ // v1: the tasks a user has submitted.
		//
		// Times are RFC3339 in UTC and result_urls is a JSON array, so the
		// stored value is the same one the rest of the program passes around
		// and rows stay readable with plain sqlite3.
		`CREATE TABLE tasks (
			task_id     TEXT NOT NULL PRIMARY KEY,
			model_id    TEXT NOT NULL,
			input       TEXT NOT NULL,
			status      TEXT NOT NULL,
			result_urls TEXT NOT NULL DEFAULT '[]',
			created_at  TEXT NOT NULL,
			updated_at  TEXT NOT NULL
		) STRICT`,
	},
	{ // v2: why a task failed.
		//
		// A status alone says a task is over but not what to do about it,
		// and kie.ai keeps no record this tool can go back to: the reason
		// arrives once, in the answer to the query that saw the failure.
		`ALTER TABLE tasks ADD COLUMN error TEXT NOT NULL DEFAULT ''`,
	},
	{ // v3: where what a task produced was saved to.
		//
		// What kie.ai serves expires -- a result in days, a download
		// link in twenty minutes -- so the one thing that has to be
		// answerable offline is which results are not on this machine
		// yet. A JSON array of absolute paths, empty for a task nothing
		// has saved: the ledger records the paths it wrote rather than
		// looking on disk, so the answer does not change under a file
		// the user moved somewhere of their own.
		`ALTER TABLE tasks ADD COLUMN saved_paths TEXT NOT NULL DEFAULT '[]'`,
	},
}

// migrate brings db up to the last version in ms, applying only what it has
// not seen. A ledger written by a newer build is left untouched: this build
// cannot know what its rows mean, and guessing would corrupt them.
//
// Every command opens the ledger, so an up-to-date one is recognised without
// taking the write lock. Otherwise the version is read again under that lock
// and everything missing is applied in one transaction: commands that found
// the ledger behind together apply it once between them, and a failure
// part-way leaves the ledger on the version it started from.
func migrate(ctx context.Context, db *sql.DB, ms [][]string) error {
	if version, err := readVersion(ctx, db, ms); err != nil || version == len(ms) {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	version, err := readVersion(ctx, tx, ms)
	if err != nil {
		return err
	}
	for v := version; v < len(ms); v++ {
		for _, statement := range ms[v] {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply schema version %d: %w", v+1, err)
			}
		}
	}
	// user_version takes no bound parameter; the value is the length of ms.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", len(ms))); err != nil {
		return fmt.Errorf("record schema version %d: %w", len(ms), err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema migration: %w", err)
	}
	return nil
}

// rowQuerier is what sql.DB and sql.Tx have in common for reading one row.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// readVersion returns the schema version q sees, refusing one newer than ms.
func readVersion(ctx context.Context, q rowQuerier, ms [][]string) (int, error) {
	var version int
	if err := q.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	if version > len(ms) {
		return 0, fmt.Errorf("ledger schema is version %d, newer than the %d this build knows", version, len(ms))
	}
	return version, nil
}
