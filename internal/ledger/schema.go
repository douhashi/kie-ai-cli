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
}

// migrate brings db up to the last version in ms, applying only what it has
// not seen. A ledger written by a newer build is left untouched: this build
// cannot know what its rows mean, and guessing would corrupt them.
func migrate(ctx context.Context, db *sql.DB, ms [][]string) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > len(ms) {
		return fmt.Errorf("ledger schema is version %d, newer than the %d this build knows", version, len(ms))
	}
	for v := version; v < len(ms); v++ {
		if err := applyVersion(ctx, db, v, ms[v]); err != nil {
			return fmt.Errorf("apply schema version %d: %w", v+1, err)
		}
	}
	return nil
}

// applyVersion runs one version's statements and records it, as a single
// transaction: a failure part-way leaves the ledger on the last version that
// completed rather than half-way into this one.
func applyVersion(ctx context.Context, db *sql.DB, index int, statements []string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	// user_version takes no bound parameter; the value is an index of ms.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", index+1)); err != nil {
		return err
	}
	return tx.Commit()
}
