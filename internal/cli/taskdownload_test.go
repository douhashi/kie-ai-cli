package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/cli"
	"github.com/douhashi/kie-ai-cli/internal/config"
	"github.com/douhashi/kie-ai-cli/internal/kie"
	"github.com/douhashi/kie-ai-cli/internal/ledger"
	"github.com/douhashi/kie-ai-cli/internal/paths"
)

// file is one result the stub host serves.
type file struct {
	// status is what the host answers with, http.StatusOK unless set.
	status int
	// mediaType is the Content-Type it declares, none when empty.
	mediaType string
	body      string
}

// host stands in for the storage the results sit on and for kie.ai itself: the
// commands under test reach both through the one server they are pointed at.
type host struct {
	url string
	// files is what each path serves, by path.
	files map[string]file

	mu    sync.Mutex
	asked []*http.Request
}

// serveResults starts the stub and points every command at it.
func serveResults(t *testing.T, files map[string]file) *host {
	t.Helper()
	h := &host{files: files}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.asked = append(h.asked, r.Clone(r.Context()))
		h.mu.Unlock()

		f, ok := h.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if f.mediaType != "" {
			w.Header().Set("Content-Type", f.mediaType)
		} else {
			// Otherwise net/http sniffs one from the bytes, which
			// is not the case a test without a type is setting up.
			w.Header()["Content-Type"] = nil
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
		}
		_, _ = w.Write([]byte(f.body))
	}))
	t.Cleanup(srv.Close)
	h.url = srv.URL
	cli.PointAtServer(t, srv.URL, srv.Client())
	return h
}

// requests is the method and path of everything the host was asked for.
func (h *host) requests() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.asked))
	for _, r := range h.asked {
		out = append(out, r.Method+" "+r.URL.Path)
	}
	return out
}

// authorizations is the Authorization header of every request, so that a test
// can say the key reached nothing it should not have.
func (h *host) authorizations() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.asked))
	for _, r := range h.asked {
		out = append(out, r.Header.Get("Authorization"))
	}
	return out
}

// produced records a finished task and the files it produced, the way task run
// and task refresh together would have.
func produced(t *testing.T, layout paths.Layout, taskID string, urls ...string) {
	t.Helper()
	l, err := ledger.Open(t.Context(), layout.Ledger)
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	defer func() { _ = l.Close() }()
	if err := l.Add(t.Context(), taskID, marketModel, kie.StatusSubmitted, nil); err != nil {
		t.Fatalf("ledger.Add(%s): %v", taskID, err)
	}
	result := ledger.Result{Status: kie.StatusSucceeded, ResultURLs: urls}
	if err := l.Update(t.Context(), taskID, result); err != nil {
		t.Fatalf("ledger.Update(%s): %v", taskID, err)
	}
}

// inTempDir moves the process into a directory of its own, which is what the
// default destination -- the current one -- means.
func inTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	// The temporary directory is reached through a symlink on macOS, and
	// the paths the command records are the resolved ones.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	return resolved
}

// saver is an isolated state directory with a key, a working directory to save
// into, and a stub serving the results: everything a download needs except the
// rows in the ledger.
func saver(t *testing.T, files map[string]file) (paths.Layout, string, *host) {
	t.Helper()
	layout := isolate(t)
	t.Setenv(config.APIKeyEnv, secret)
	return layout, inTempDir(t), serveResults(t, files)
}

func read(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

// AC1: a named task's results are written into the current directory, one file
// per result, named after the task, and the ledger records where they went.
func TestTaskDownloadSavesIntoTheCurrentDirectory(t *testing.T) {
	layout, dir, h := saver(t, map[string]file{
		"/results/first.png":  {body: "the first file", mediaType: "image/png"},
		"/results/second.png": {body: "the second file", mediaType: "image/png"},
	})
	produced(t, layout, "task-1", h.url+"/results/first.png", h.url+"/results/second.png")

	got := run(t, "task", "download", "task-1")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}

	want := []string{filepath.Join(dir, "task-1-1.png"), filepath.Join(dir, "task-1-2.png")}
	for i, path := range want {
		if content := read(t, path); content == "" {
			t.Errorf("%s is empty", path)
		} else if i == 0 && content != "the first file" {
			t.Errorf("%s holds %q, want the file the host served", path, content)
		}
	}
	// The paths are on stdout, one per line and nothing else, so that the
	// files can be handed to the next command in a pipeline.
	if lines := strings.Fields(got.stdout); !slices.Equal(lines, want) {
		t.Errorf("stdout = %q, want the saved paths one per line", got.stdout)
	}
	// Absolute, because the ledger is read from wherever the next command
	// happens to be run.
	if saved := recorded(t, layout, "task-1").SavedPaths; !slices.Equal(saved, want) {
		t.Errorf("saved paths = %v, want %v", saved, want)
	}
	// Nothing but the two files: a download does not ask kie.ai for a fresh
	// link it has no reason to think it needs.
	wantRequests := []string{"GET /results/first.png", "GET /results/second.png"}
	if !slices.Equal(h.requests(), wantRequests) {
		t.Errorf("requests = %v, want %v", h.requests(), wantRequests)
	}
}

// V6: the key is the account's, and the hosts serving the results are not
// kie.ai. Sending it to them would give a third party the credential.
func TestTaskDownloadWithholdsTheKeyFromTheHostServingTheResult(t *testing.T) {
	layout, _, h := saver(t, map[string]file{"/results/a.png": {body: "bytes", mediaType: "image/png"}})
	produced(t, layout, "task-1", h.url+"/results/a.png")

	got := run(t, "task", "download", "task-1")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	for i, authorization := range h.authorizations() {
		if authorization != "" {
			t.Errorf("request %d carried %q; the result host must not be sent the key", i, authorization)
		}
	}
	assertNoLeak(t, got.stdout+got.stderr)
}

// The destination is a directory of the caller's choosing, made if it is not
// there: saving a hundred images into whatever directory the shell happens to
// be in is not what a user of --unsaved is asking for.
func TestTaskDownloadSavesIntoTheDirectoryItIsGiven(t *testing.T) {
	layout, _, h := saver(t, map[string]file{"/results/a.png": {body: "bytes", mediaType: "image/png"}})
	produced(t, layout, "task-1", h.url+"/results/a.png")
	dir := filepath.Join(t.TempDir(), "made", "on", "the", "way")

	got := run(t, "task", "download", "task-1", "--dir", dir)
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	if content := read(t, filepath.Join(dir, "task-1-1.png")); content != "bytes" {
		t.Errorf("saved %q, want the file the host served", content)
	}
	// Nothing is left behind beside it: the file is written under a
	// temporary name and renamed, so a directory holding two entries means
	// a half-written file was kept.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("the directory holds %d entries, want the one saved file", len(entries))
	}
}

// AC3: what is already on disk is not fetched again. A result is a file that
// was paid for and may have been edited since; re-fetching it would cost the
// bandwidth of a video to overwrite the user's own copy.
func TestTaskDownloadDoesNotFetchWhatIsAlreadySaved(t *testing.T) {
	layout, dir, h := saver(t, map[string]file{"/results/a.png": {body: "bytes", mediaType: "image/png"}})
	produced(t, layout, "task-1", h.url+"/results/a.png")

	if got := run(t, "task", "download", "task-1"); got.code != 0 {
		t.Fatalf("the first download: code %d, stderr %q", got.code, got.stderr)
	}
	saved := filepath.Join(dir, "task-1-1.png")
	before, err := os.Stat(saved)
	if err != nil {
		t.Fatalf("stat %s: %v", saved, err)
	}
	asked := len(h.requests())

	got := run(t, "task", "download", "task-1")
	if got.code != 0 {
		t.Fatalf("the second download: code %d, stderr %q", got.code, got.stderr)
	}
	if now := len(h.requests()); now != asked {
		t.Errorf("the second run made %d requests, want none", now-asked)
	}
	after, err := os.Stat(saved)
	if err != nil {
		t.Fatalf("stat %s: %v", saved, err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("the file was rewritten: %s, was %s", after.ModTime(), before.ModTime())
	}
	// Why nothing happened is said, and on stderr: the answer to the
	// command is the paths, and a skipped task adds none.
	if !strings.Contains(got.stderr, "task-1") || !strings.Contains(got.stderr, saved) {
		t.Errorf("stderr does not say the task is already saved:\n%s", got.stderr)
	}
	if got.stdout != "" {
		t.Errorf("stdout = %q, want nothing saved by this run", got.stdout)
	}
}

// The extension says what a file is to every program that opens it. It comes
// from the URL, which is where kie.ai puts it, and from what the host declared
// when the URL says nothing.
func TestTaskDownloadNamesTheFileAfterWhatItIs(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		mediaType string
		want      string
	}{
		{name: "from the URL", path: "/results/a.mp4", want: "task-1-1.mp4"},
		{name: "from the URL over the type", path: "/results/a.png", mediaType: "audio/mpeg", want: "task-1-1.png"},
		{name: "from the type", path: "/results/a", mediaType: "image/png", want: "task-1-1.png"},
		{name: "from neither", path: "/results/a", want: "task-1-1"},
		// A query string is not part of the name, and an extension that
		// is not one is no extension at all.
		{name: "not from the query", path: "/results/a.webp", want: "task-1-1.webp"},
		{name: "not from a dotted directory", path: "/results/v1.2/a", want: "task-1-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layout, dir, h := saver(t, map[string]file{tt.path: {body: "bytes", mediaType: tt.mediaType}})
			produced(t, layout, "task-1", h.url+tt.path+"?token=abc")

			if got := run(t, "task", "download", "task-1"); got.code != 0 {
				t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
			}
			if _, err := os.Stat(filepath.Join(dir, tt.want)); err != nil {
				entries, _ := os.ReadDir(dir)
				names := make([]string, 0, len(entries))
				for _, entry := range entries {
					names = append(names, entry.Name())
				}
				t.Errorf("%s was not saved; the directory holds %v", tt.want, names)
			}
		})
	}
}

// A task is saved whole or not at all. Recording half of a task's results as
// saved would take it out of the unsaved listing with a file still missing,
// and nothing would ever go back for it.
func TestTaskDownloadRecordsNothingWhenOneResultCannotBeFetched(t *testing.T) {
	layout, _, h := saver(t, map[string]file{
		"/results/a.png": {body: "bytes", mediaType: "image/png"},
		"/results/b.png": {status: http.StatusForbidden, body: "gone"},
	})
	produced(t, layout, "task-1", h.url+"/results/a.png", h.url+"/results/b.png")

	got := run(t, "task", "download", "task-1")
	if got.code != 1 {
		t.Errorf("code = %d, want 1 (stderr %q)", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "task-1") {
		t.Errorf("stderr does not name the task that could not be saved:\n%s", got.stderr)
	}
	if saved := recorded(t, layout, "task-1").SavedPaths; len(saved) != 0 {
		t.Errorf("saved paths = %v, want the task left unsaved", saved)
	}
	// The refusal is the one answer a fresh link can change, so it was
	// asked for -- once.
	if got := strings.Count(strings.Join(h.requests(), "\n"), "POST /api/v1/common/download-url"); got != 1 {
		t.Errorf("asked for a fresh link %d times, want once: %v", got, h.requests())
	}
}

// AC2: --unsaved saves everything there is to save and says which of them
// could not be, rather than stopping at the first failure and leaving the rest
// unfetched.
func TestTaskDownloadUnsavedSavesEveryOneItCan(t *testing.T) {
	layout, dir, h := saver(t, map[string]file{
		"/results/a.png": {body: "a", mediaType: "image/png"},
		"/results/c.png": {body: "c", mediaType: "image/png"},
	})
	produced(t, layout, "task-a", h.url+"/results/a.png")
	produced(t, layout, "task-b", h.url+"/results/b.png") // the host does not have it
	produced(t, layout, "task-c", h.url+"/results/c.png")
	// A success with nothing behind it -- the lyrics endpoint answers with
	// the words themselves -- and a task that has not finished.
	produced(t, layout, "task-lyrics")
	add(t, layout, "task-running", marketModel, kie.StatusRunning)

	got := run(t, "task", "download", "--unsaved")
	if got.code != 1 {
		t.Errorf("code = %d, want 1: one of the tasks could not be saved (stderr %q)", got.code, got.stderr)
	}
	for _, taskID := range []string{"task-a", "task-c"} {
		saved := recorded(t, layout, taskID).SavedPaths
		want := []string{filepath.Join(dir, taskID+"-1.png")}
		if !slices.Equal(saved, want) {
			t.Errorf("%s saved %v, want %v", taskID, saved, want)
		}
	}
	if saved := recorded(t, layout, "task-b").SavedPaths; len(saved) != 0 {
		t.Errorf("task-b saved %v, want nothing", saved)
	}
	if !strings.Contains(got.stderr, "task-b") {
		t.Errorf("stderr does not name the task that failed:\n%s", got.stderr)
	}
	if saved := recorded(t, layout, "task-lyrics").SavedPaths; len(saved) != 0 {
		t.Errorf("the task with no results saved %v, want nothing", saved)
	}
	// The three results, and the one fresh link asked for after the one
	// refusal: neither the task with no files nor the unfinished one was
	// fetched, and nothing was fetched twice.
	want := []string{
		"GET /results/a.png", "GET /results/b.png", "GET /results/c.png",
		"POST /api/v1/common/download-url",
	}
	if got := slices.Sorted(slices.Values(h.requests())); !slices.Equal(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

// The answer as a document: which task each file belongs to, which the paths
// alone say only by convention.
func TestTaskDownloadJSON(t *testing.T) {
	layout, dir, h := saver(t, map[string]file{"/results/a.png": {body: "a", mediaType: "image/png"}})
	produced(t, layout, "task-1", h.url+"/results/a.png")

	got := run(t, "task", "download", "task-1", "--json")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	var saved []struct {
		TaskID     string   `json:"taskId"`
		SavedPaths []string `json:"savedPaths"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &saved); err != nil {
		t.Fatalf("stdout is not JSON (%v):\n%s", err, got.stdout)
	}
	if len(saved) != 1 || saved[0].TaskID != "task-1" {
		t.Fatalf("saved = %+v, want the one task", saved)
	}
	if want := []string{filepath.Join(dir, "task-1-1.png")}; !slices.Equal(saved[0].SavedPaths, want) {
		t.Errorf("savedPaths = %v, want %v", saved[0].SavedPaths, want)
	}
}

// A task that has produced nothing to save is refused when it is named, with
// the reason: it is a different reason each time, and "nothing happened" would
// leave the caller to guess which.
func TestTaskDownloadRefusesATaskWithNothingToSave(t *testing.T) {
	tests := []struct {
		name   string
		status string
		urls   []string
		want   string
	}{
		{name: "still running", status: kie.StatusRunning, want: kie.StatusRunning},
		{name: "failed", status: kie.StatusFailed, want: kie.StatusFailed},
		{name: "succeeded with no files", status: kie.StatusSucceeded, want: "no files"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layout, _, _ := saver(t, nil)
			add(t, layout, "task-1", marketModel, tt.status)

			got := run(t, "task", "download", "task-1")
			if got.code != 1 {
				t.Errorf("code = %d, want 1 (stderr %q)", got.code, got.stderr)
			}
			if !strings.Contains(got.stderr, tt.want) {
				t.Errorf("stderr does not say why there is nothing to save (%q):\n%s", tt.want, got.stderr)
			}
		})
	}
}

func TestTaskDownloadUnknownTask(t *testing.T) {
	saver(t, nil)

	got := run(t, "task", "download", "no-such-task")
	if got.code != 1 {
		t.Errorf("code = %d, want 1 (stderr %q)", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "no-such-task") {
		t.Errorf("stderr does not name the task that is not in the ledger:\n%s", got.stderr)
	}
}

// Saying neither which task nor "all of them" is a mistake in the call, not a
// command that does nothing: the caller asked for something to be saved.
func TestTaskDownloadUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "neither a task nor --unsaved", args: []string{"task", "download"}},
		{name: "both", args: []string{"task", "download", "task-1", "--unsaved"}},
		{name: "two tasks", args: []string{"task", "download", "task-1", "task-2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saver(t, nil)

			got := run(t, tt.args...)
			if got.code != 2 {
				t.Errorf("code = %d, want 2 (stderr %q)", got.code, got.stderr)
			}
			if !strings.Contains(got.stderr, "task download") {
				t.Errorf("stderr does not name the command:\n%s", got.stderr)
			}
		})
	}
}

// --unsaved with nothing to save is a success that says so, rather than a
// command that prints nothing and reads as broken.
func TestTaskDownloadUnsavedWithNothingToSave(t *testing.T) {
	saver(t, nil)

	got := run(t, "task", "download", "--unsaved")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	if got.stdout != "" {
		t.Errorf("stdout = %q, want nothing saved", got.stdout)
	}
	if got.stderr == "" {
		t.Error("stderr says nothing; a command that printed nothing at all reads as broken")
	}
}
