// Package ledger records the tasks submitted to kie.ai in a local SQLite
// database, so that a later command can report what became of them.
//
// Each kie-ai-cli invocation is its own process working on the same file, so
// the ledger is opened, used and closed within one command and expects other
// processes to be doing the same.
package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/kie"
	"modernc.org/sqlite" // the cgo-free driver; importing it registers "sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrNotFound reports that the ledger holds no task with the given id.
var ErrNotFound = errors.New("task not found")

// timeLayout is RFC3339 with a fixed nine-digit fraction. The fraction is
// fixed rather than trimmed so that lexicographic order over the stored text
// is chronological order, which is what ORDER BY relies on.
const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// busyTimeout is how long a command waits out another one holding the ledger
// before it gives up.
const busyTimeout = 5 * time.Second

// columns lists the task columns in the order scanTask reads them.
const columns = `task_id, model_id, input, status, result_urls, error, saved_paths, credits_consumed, created_at, updated_at`

// Task is one submitted task as the ledger holds it.
type Task struct {
	TaskID     string
	ModelID    string
	Input      map[string]any
	Status     string
	ResultURLs []string
	// Error is why the task failed, empty for every other state.
	Error string
	// SavedPaths is where what the task produced was written on this
	// machine, empty for a task nothing has saved yet.
	SavedPaths []string
	// CreditsConsumed is what kie.ai said the task cost, nil when no
	// answer has said -- which is not the same as costing nothing.
	CreditsConsumed *float64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Result is what a task turned out to be: the state a query reported, what it
// produced, why it failed, and what it cost. They travel together because they
// are read out of one answer and only make sense as one.
type Result struct {
	Status          string
	ResultURLs      []string
	Error           string
	CreditsConsumed *float64
}

// Ledger is an open task ledger.
type Ledger struct {
	db *sql.DB
}

// Open opens the ledger at path, creating the database and bringing its schema
// up to date. The caller closes it.
func Open(ctx context.Context, path string) (*Ledger, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open ledger %s: %w", path, err)
	}
	// One connection per process. Concurrent commands are separate processes
	// that SQLite serialises for us (see the busy_timeout in dsn), so a pool
	// would only add contention this process inflicts on itself.
	db.SetMaxOpenConns(1)

	if err := enableWAL(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open ledger %s: enable WAL: %w", path, err)
	}
	if err := migrate(ctx, db, migrations); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open ledger %s: %w", path, err)
	}
	return &Ledger{db: db}, nil
}

// enableWAL switches the ledger to WAL, which lets a reading command run while
// another one writes.
//
// The switch is retried here rather than left to busy_timeout: it upgrades a
// read lock to an exclusive one, and SQLite answers a contended upgrade with
// SQLITE_BUSY at once instead of calling the busy handler, since waiting could
// deadlock. Commands opening a fresh ledger together would otherwise fail
// within a millisecond of each other.
func enableWAL(ctx context.Context, db *sql.DB) error {
	deadline := time.Now().Add(busyTimeout)
	for {
		_, err := db.ExecContext(ctx, "PRAGMA journal_mode = WAL")
		if !isBusy(err) || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// isBusy reports whether err is SQLite finding the database locked by another
// connection. The extended codes all keep SQLITE_BUSY in their low byte.
func isBusy(err error) bool {
	var e *sqlite.Error
	return errors.As(err, &e) && e.Code()&0xff == sqlite3.SQLITE_BUSY
}

// Close releases the underlying database.
func (l *Ledger) Close() error {
	return l.db.Close()
}

// Add records a newly submitted task. input is the request that was sent for
// it, stored as canonical JSON so that the same request reads back the same
// way whatever built it.
func (l *Ledger) Add(ctx context.Context, taskID, modelID, status string, input map[string]any) error {
	if input == nil {
		input = map[string]any{}
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("add task %s: encode input: %w", taskID, err)
	}
	now := timestamp()

	// The task has produced nothing yet, so its result urls are the empty
	// array, it has no reason to have failed for, there is nothing of it on
	// disk, and nothing has said what it cost.
	const query = `INSERT INTO tasks (` + columns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := l.db.ExecContext(ctx, query, taskID, modelID, string(encoded), status, "[]", "", "[]", nil, now, now); err != nil {
		return fmt.Errorf("add task %s: %w", taskID, err)
	}
	return nil
}

// Get returns the recorded task, or ErrNotFound.
func (l *Ledger) Get(ctx context.Context, taskID string) (Task, error) {
	const query = `SELECT ` + columns + ` FROM tasks WHERE task_id = ?`
	task, err := scanTask(l.db.QueryRowContext(ctx, query, taskID))
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, fmt.Errorf("get task %s: %w", taskID, ErrNotFound)
	}
	if err != nil {
		return Task{}, fmt.Errorf("get task %s: %w", taskID, err)
	}
	return task, nil
}

// List returns the recorded tasks, newest first. With no status it returns
// every one of them: the ledger holds one user's own submissions, so it is
// small enough to hand back whole.
//
// A status that no task is in gives an empty listing rather than every task,
// so that a question about one state is never answered with another.
func (l *Ledger) List(ctx context.Context, statuses ...string) ([]Task, error) {
	return l.list(ctx, nil, nil, statuses)
}

// unsavedWhere selects the tasks that produced something no path has been
// recorded for.
//
// A success with no result urls is not one of them. The lyrics endpoint
// answers with the words themselves and records no file, so a task of that
// kind has nothing to save and would sit in the listing for ever -- which is
// the one thing a listing of what is left to do must not do.
const unsavedWhere = `status = ? AND result_urls <> '[]' AND saved_paths = '[]'`

// ListUnsaved returns the tasks that produced something this machine does not
// have a copy of, newest first, narrowed further by statuses when given.
func (l *Ledger) ListUnsaved(ctx context.Context, statuses ...string) ([]Task, error) {
	return l.list(ctx, []string{unsavedWhere}, []any{kie.StatusSucceeded}, statuses)
}

// list runs one listing: the conditions given, and the statuses asked for.
//
// conditions and args are positional, so an argument belongs to the condition
// at the same place in the clause; the status filter is appended after both.
func (l *Ledger) list(ctx context.Context, conditions []string, args []any, statuses []string) ([]Task, error) {
	// rowid breaks ties so that two tasks recorded in the same instant still
	// come back in a stable, newest-first order.
	query := `SELECT ` + columns + ` FROM tasks`
	if len(statuses) > 0 {
		placeholders := make([]string, len(statuses))
		for i, status := range statuses {
			placeholders[i], args = "?", append(args, status)
		}
		conditions = append(conditions, `status IN (`+strings.Join(placeholders, ", ")+`)`)
	}
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, " AND ")
	}
	query += ` ORDER BY created_at DESC, rowid DESC`

	rows, err := l.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	tasks := []Task{}
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("list tasks: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	return tasks, nil
}

// Update records what a task turned out to be.
//
// Every field of the result is written, the reason and the cost included: a
// task that succeeded on a later attempt would otherwise keep the reason of the
// one before it and read as a failure that produced a result, and a figure no
// answer stands behind any more would go on reading as one that does.
func (l *Ledger) Update(ctx context.Context, taskID string, result Result) error {
	if result.ResultURLs == nil {
		result.ResultURLs = []string{}
	}
	encoded, err := json.Marshal(result.ResultURLs)
	if err != nil {
		return fmt.Errorf("update task %s: encode result urls: %w", taskID, err)
	}
	now := timestamp()

	const query = `UPDATE tasks SET status = ?, result_urls = ?, error = ?, credits_consumed = ?, updated_at = ? WHERE task_id = ?`
	res, err := l.db.ExecContext(ctx, query, result.Status, string(encoded), result.Error, result.CreditsConsumed, now, taskID)
	if err != nil {
		return fmt.Errorf("update task %s: %w", taskID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update task %s: %w", taskID, err)
	}
	if affected == 0 {
		return fmt.Errorf("update task %s: %w", taskID, ErrNotFound)
	}
	return nil
}

// MarkSaved records where what a task produced was written to. The paths are
// what makes a task saved, so recording none of them is refused: it would
// write the value that means the opposite.
//
// updated_at moves with them. What the ledger holds about the task has
// changed, which is what that column has always meant.
func (l *Ledger) MarkSaved(ctx context.Context, taskID string, paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("mark task %s saved: no paths were saved", taskID)
	}
	encoded, err := json.Marshal(paths)
	if err != nil {
		return fmt.Errorf("mark task %s saved: encode saved paths: %w", taskID, err)
	}

	const query = `UPDATE tasks SET saved_paths = ?, updated_at = ? WHERE task_id = ?`
	res, err := l.db.ExecContext(ctx, query, string(encoded), timestamp(), taskID)
	if err != nil {
		return fmt.Errorf("mark task %s saved: %w", taskID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark task %s saved: %w", taskID, err)
	}
	if affected == 0 {
		return fmt.Errorf("mark task %s saved: %w", taskID, ErrNotFound)
	}
	return nil
}

// scanner is what sql.Row and sql.Rows have in common.
type scanner interface {
	Scan(dest ...any) error
}

func scanTask(s scanner) (Task, error) {
	var (
		task                          Task
		input, resultURLs, savedPaths string
		credits                       sql.NullFloat64
		createdAt, updatedAt          string
	)
	if err := s.Scan(&task.TaskID, &task.ModelID, &input, &task.Status, &resultURLs, &task.Error, &savedPaths, &credits, &createdAt, &updatedAt); err != nil {
		return Task{}, err
	}
	if credits.Valid {
		task.CreditsConsumed = &credits.Float64
	}
	if err := json.Unmarshal([]byte(input), &task.Input); err != nil {
		return Task{}, fmt.Errorf("decode input of %s: %w", task.TaskID, err)
	}
	if err := json.Unmarshal([]byte(resultURLs), &task.ResultURLs); err != nil {
		return Task{}, fmt.Errorf("decode result urls of %s: %w", task.TaskID, err)
	}
	if err := json.Unmarshal([]byte(savedPaths), &task.SavedPaths); err != nil {
		return Task{}, fmt.Errorf("decode saved paths of %s: %w", task.TaskID, err)
	}
	var err error
	if task.CreatedAt, err = parseTime(createdAt); err != nil {
		return Task{}, fmt.Errorf("decode created_at of %s: %w", task.TaskID, err)
	}
	if task.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Task{}, fmt.Errorf("decode updated_at of %s: %w", task.TaskID, err)
	}
	return task, nil
}

// timestamp is the current time as the ledger stores it.
func timestamp() string {
	return time.Now().UTC().Format(timeLayout)
}

func parseTime(value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// dsn builds the connection string for the ledger file.
//
// The driver hands a "file:" DSN to sqlite3_open_v2 with SQLITE_OPEN_URI, so
// the path is read as a URI and has to be escaped as one — url.URL does that,
// including for a path holding '?' or '#'. Each _pragma value is run verbatim
// as a PRAGMA on the new connection. WAL is not among them: switching to it has
// to be retried, which a failed connection cannot be (see enableWAL).
func dsn(path string) string {
	u := url.URL{
		Scheme: "file",
		Path:   uriPath(path),
		RawQuery: url.Values{
			"_pragma": {
				// Wait out another process's write instead of failing the command.
				fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()),
				// Off by default and per-connection, so it belongs here rather
				// than beside the first schema version that needs it.
				"foreign_keys(1)",
			},
			// Take the write lock when a transaction begins. A transaction
			// that reads first and writes later has to upgrade its lock, and
			// SQLite fails a contended upgrade at once rather than wait on
			// busy_timeout.
			"_txlock": {"immediate"},
		}.Encode(),
	}
	return u.String()
}

// uriPath turns a filesystem path into the path part of a file: URI, which is
// slash-separated and rooted ("C:\x" becomes "/C:/x").
func uriPath(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}
