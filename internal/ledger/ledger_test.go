package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestAddAndGetRoundTrip(t *testing.T) {
	ctx := context.Background()
	l := openTemp(t)

	input := map[string]any{"prompt": "a cat", "size": float64(1024)}
	if err := l.Add(ctx, "task-1", "veo3", "submitted", input); err != nil {
		t.Fatalf("Add() error: %v", err)
	}

	got, err := l.Get(ctx, "task-1")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	want := Task{
		TaskID:     "task-1",
		ModelID:    "veo3",
		Input:      input,
		Status:     "submitted",
		ResultURLs: []string{},
	}
	assertTaskEqual(t, got, want)
	if got.CreatedAt.IsZero() || !got.CreatedAt.Equal(got.UpdatedAt) {
		t.Errorf("timestamps = (%v, %v), want equal and non-zero", got.CreatedAt, got.UpdatedAt)
	}
	if got.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt location = %v, want UTC", got.CreatedAt.Location())
	}
}

func TestGetUnknownTask(t *testing.T) {
	l := openTemp(t)

	if _, err := l.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestListReturnsNewestFirst(t *testing.T) {
	ctx := context.Background()
	l := openTemp(t)

	for _, id := range []string{"task-1", "task-2", "task-3"} {
		if err := l.Add(ctx, id, "veo3", "submitted", nil); err != nil {
			t.Fatalf("Add(%s) error: %v", id, err)
		}
	}

	tasks, err := l.List(ctx)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	got := make([]string, len(tasks))
	for i, task := range tasks {
		got[i] = task.TaskID
	}
	if want := []string{"task-3", "task-2", "task-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("List() ids = %v, want %v", got, want)
	}
}

func TestListEmpty(t *testing.T) {
	tasks, err := openTemp(t).List(context.Background())
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("List() = %v, want empty", tasks)
	}
}

func TestUpdateStatus(t *testing.T) {
	ctx := context.Background()
	l := openTemp(t)
	if err := l.Add(ctx, "task-1", "veo3", "submitted", nil); err != nil {
		t.Fatalf("Add() error: %v", err)
	}
	added, err := l.Get(ctx, "task-1")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	urls := []string{"https://example.com/a.mp4", "https://example.com/b.mp4"}
	if err := l.UpdateStatus(ctx, "task-1", "succeeded", urls); err != nil {
		t.Fatalf("UpdateStatus() error: %v", err)
	}

	got, err := l.Get(ctx, "task-1")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.Status != "succeeded" {
		t.Errorf("Status = %q, want %q", got.Status, "succeeded")
	}
	if !reflect.DeepEqual(got.ResultURLs, urls) {
		t.Errorf("ResultURLs = %v, want %v", got.ResultURLs, urls)
	}
	if !got.CreatedAt.Equal(added.CreatedAt) {
		t.Errorf("CreatedAt = %v, want it left at %v", got.CreatedAt, added.CreatedAt)
	}
	if got.UpdatedAt.Before(added.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want at or after %v", got.UpdatedAt, added.UpdatedAt)
	}
}

func TestUpdateStatusUnknownTask(t *testing.T) {
	err := openTemp(t).UpdateStatus(context.Background(), "missing", "succeeded", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateStatus() error = %v, want ErrNotFound", err)
	}
}

// Every command runs as its own process against the same file, so opening an
// existing ledger must neither fail nor disturb what is already recorded.
func TestOpenExistingLedger(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ledger.db")

	first := open(t, path)
	if err := first.Add(ctx, "task-1", "veo3", "submitted", map[string]any{"prompt": "a cat"}); err != nil {
		t.Fatalf("Add() error: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	second := open(t, path)
	got, err := second.Get(ctx, "task-1")
	if err != nil {
		t.Fatalf("Get() after reopen error: %v", err)
	}
	if got.ModelID != "veo3" {
		t.Errorf("ModelID = %q, want %q", got.ModelID, "veo3")
	}
	if v := schemaVersion(t, second); v != len(migrations) {
		t.Errorf("user_version = %d, want %d", v, len(migrations))
	}
}

func TestOpenCreatesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")

	open(t, path)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
}

// A ledger in the field already holds the user's submissions, so a schema
// update must carry them forward rather than start over.
func TestMigrateKeepsExistingRows(t *testing.T) {
	ctx := context.Background()
	l := openTemp(t)
	if err := l.Add(ctx, "task-1", "veo3", "submitted", map[string]any{"prompt": "a cat"}); err != nil {
		t.Fatalf("Add() error: %v", err)
	}

	next := append(append([][]string{}, migrations...), []string{
		`ALTER TABLE tasks ADD COLUMN note TEXT NOT NULL DEFAULT ''`,
	})
	if err := migrate(ctx, l.db, next); err != nil {
		t.Fatalf("migrate() error: %v", err)
	}

	if v := schemaVersion(t, l); v != len(next) {
		t.Errorf("user_version = %d, want %d", v, len(next))
	}
	got, err := l.Get(ctx, "task-1")
	if err != nil {
		t.Fatalf("Get() after migration error: %v", err)
	}
	if !reflect.DeepEqual(got.Input, map[string]any{"prompt": "a cat"}) {
		t.Errorf("Input = %v, want it preserved", got.Input)
	}

	// Applying the same list again is a no-op rather than a re-run.
	if err := migrate(ctx, l.db, next); err != nil {
		t.Fatalf("migrate() second run error: %v", err)
	}
	if v := schemaVersion(t, l); v != len(next) {
		t.Errorf("user_version after second run = %d, want %d", v, len(next))
	}
}

func TestMigrateRejectsNewerSchema(t *testing.T) {
	ctx := context.Background()
	l := openTemp(t)

	if err := migrate(ctx, l.db, nil); err == nil {
		t.Fatal("migrate() with no known versions succeeded, want an error")
	}
}

// A task submitted twice with the same values must read back the same way,
// whatever order the fields arrived in.
func TestAddNormalisesInput(t *testing.T) {
	ctx := context.Background()
	l := openTemp(t)

	inputs := []string{
		`{"prompt": "a cat", "aspect_ratio": "16:9", "nested": {"b": 2, "a": 1}}`,
		`{"nested": {"a": 1, "b": 2}, "aspect_ratio": "16:9", "prompt": "a cat"}`,
	}
	for i, raw := range inputs {
		var input map[string]any
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			t.Fatalf("unmarshal %d: %v", i, err)
		}
		if err := l.Add(ctx, []string{"task-1", "task-2"}[i], "veo3", "submitted", input); err != nil {
			t.Fatalf("Add(%d) error: %v", i, err)
		}
	}

	first, second := storedInput(t, l, "task-1"), storedInput(t, l, "task-2")
	if first != second {
		t.Errorf("stored input differs:\n %s\n %s", first, second)
	}
	if want := `{"aspect_ratio":"16:9","nested":{"a":1,"b":2},"prompt":"a cat"}`; first != want {
		t.Errorf("stored input = %s, want %s", first, want)
	}
}

func TestAddWithoutInput(t *testing.T) {
	ctx := context.Background()
	l := openTemp(t)

	if err := l.Add(ctx, "task-1", "veo3", "submitted", nil); err != nil {
		t.Fatalf("Add() error: %v", err)
	}

	if got := storedInput(t, l, "task-1"); got != "{}" {
		t.Errorf("stored input = %s, want {}", got)
	}
	task, err := l.Get(ctx, "task-1")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if len(task.Input) != 0 {
		t.Errorf("Input = %v, want empty", task.Input)
	}
}

func TestAddRejectsDuplicateTaskID(t *testing.T) {
	ctx := context.Background()
	l := openTemp(t)
	if err := l.Add(ctx, "task-1", "veo3", "submitted", nil); err != nil {
		t.Fatalf("Add() error: %v", err)
	}

	if err := l.Add(ctx, "task-1", "veo3", "submitted", nil); err == nil {
		t.Fatal("Add() with a duplicate task id succeeded, want an error")
	}
}

func openTemp(t *testing.T) *Ledger {
	t.Helper()
	return open(t, filepath.Join(t.TempDir(), "ledger.db"))
}

func open(t *testing.T, path string) *Ledger {
	t.Helper()
	l, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(%s) error: %v", path, err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func schemaVersion(t *testing.T, l *Ledger) int {
	t.Helper()
	var v int
	if err := l.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	return v
}

func storedInput(t *testing.T, l *Ledger, taskID string) string {
	t.Helper()
	var input string
	if err := l.db.QueryRow("SELECT input FROM tasks WHERE task_id = ?", taskID).Scan(&input); err != nil {
		t.Fatalf("read input of %s: %v", taskID, err)
	}
	return input
}

func assertTaskEqual(t *testing.T, got, want Task) {
	t.Helper()
	got.CreatedAt, got.UpdatedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("task = %+v, want %+v", got, want)
	}
}

// The DSN is the contract with the driver: it is the only place the pragmas
// are stated, and a typo in one would silently leave the ledger on defaults.
func TestOpenAppliesConnectionPragmas(t *testing.T) {
	l := openTemp(t)

	for _, tc := range []struct{ pragma, want string }{
		{"busy_timeout", "5000"},
		{"journal_mode", "wal"},
		{"foreign_keys", "1"},
	} {
		var got string
		if err := l.db.QueryRow("PRAGMA " + tc.pragma).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", tc.pragma, err)
		}
		if got != tc.want {
			t.Errorf("%s = %s, want %s", tc.pragma, got, tc.want)
		}
	}
}

// A data directory can sit anywhere, including under a name that means
// something else in a URI.
func TestDSNEscapesThePath(t *testing.T) {
	got := dsn("/tmp/a b?c/ledger.db")
	want := "file:///tmp/a%20b%3Fc/ledger.db" +
		"?_pragma=busy_timeout%285000%29&_pragma=journal_mode%28WAL%29&_pragma=foreign_keys%281%29"
	if got != want {
		t.Errorf("dsn() = %s, want %s", got, want)
	}
}
