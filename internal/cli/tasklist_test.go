package cli_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
	"github.com/douhashi/kie-ai-cli/internal/cli"
	"github.com/douhashi/kie-ai-cli/internal/config"
	"github.com/douhashi/kie-ai-cli/internal/kie"
	"github.com/douhashi/kie-ai-cli/internal/ledger"
	"github.com/douhashi/kie-ai-cli/internal/paths"
)

// The models the listing tests use: one that produces files, and one that
// answers with text and so produces none.
const (
	marketModel = "qwen/text-to-image"
	lyricsModel = "ai-music-api/generate-lyrics"
)

// add records one task the way task run would have.
func add(t *testing.T, layout paths.Layout, taskID, modelID, status string) {
	t.Helper()
	l, err := ledger.Open(t.Context(), layout.Ledger)
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	defer func() { _ = l.Close() }()
	if err := l.Add(t.Context(), taskID, modelID, status, nil); err != nil {
		t.Fatalf("ledger.Add(%s): %v", taskID, err)
	}
}

// costed records one finished task and what kie.ai said it cost, the way task
// refresh would have.
func costed(t *testing.T, layout paths.Layout, taskID string, consumed *float64) {
	t.Helper()
	add(t, layout, taskID, marketModel, kie.StatusSubmitted)
	l, err := ledger.Open(t.Context(), layout.Ledger)
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	defer func() { _ = l.Close() }()
	result := ledger.Result{Status: kie.StatusSucceeded, CreditsConsumed: consumed}
	if err := l.Update(t.Context(), taskID, result); err != nil {
		t.Fatalf("ledger.Update(%s): %v", taskID, err)
	}
}

// credits is the address of a consumed-credit figure, which is how a recorded
// figure is told apart from no record at all.
func credits(v float64) *float64 {
	return &v
}

// queryStub answers task queries with a body chosen by path, and records what
// it was asked and how many questions were in flight at once.
//
// With hold set, a request waits until that many are in flight before it is
// answered, so that a command asking them one at a time is caught rather than
// merely being slower than one that overlaps them.
type queryStub struct {
	answers map[string]string
	hold    int

	mu       sync.Mutex
	asked    []string
	inFlight int
	peak     int
	released chan struct{}
}

// stubQueries points every command at a server that answers only the paths it
// was given a body for.
func stubQueries(t *testing.T, answers map[string]string) *queryStub {
	t.Helper()
	s := &queryStub{answers: answers, released: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.asked = append(s.asked, r.URL.Path+"?"+r.URL.RawQuery)
		s.inFlight++
		s.peak = max(s.peak, s.inFlight)
		enough := s.hold > 0 && s.inFlight >= s.hold
		s.mu.Unlock()

		if enough {
			s.releaseOnce()
		}
		if s.hold > 0 {
			// The wait ends either way: a command that asks one
			// question at a time must fail on the count it reached,
			// not by hanging until the test binary is killed.
			select {
			case <-s.released:
			case <-time.After(2 * time.Second):
			}
		}

		s.mu.Lock()
		s.inFlight--
		// An answer for one task wins over the answer for its endpoint,
		// since every model is asked through the same one.
		body, ok := s.answers[r.URL.Path+"?"+r.URL.RawQuery]
		if !ok {
			body, ok = s.answers[r.URL.Path]
		}
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if !ok {
			t.Errorf("the API was asked for %s, which this test gave no answer for", r.URL.Path)
			body = `{"code":404,"msg":"Task not found"}`
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	cli.PointAtServer(t, srv.URL, srv.Client())
	return s
}

func (s *queryStub) releaseOnce() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.released:
	default:
		close(s.released)
	}
}

func (s *queryStub) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.asked...)
}

// AC1: what a task is and what it produced is read out of the ledger, with no
// call to kie.ai at all -- kie.ai has no endpoint that lists tasks, and asking
// after each one would make the listing slower the more there is to list.
func TestTaskListReadsTheLedgerAlone(t *testing.T) {
	layout := isolate(t)
	t.Setenv(config.APIKeyEnv, secret)
	stub := stubQueries(t, nil)
	add(t, layout, "task-1", marketModel, kie.StatusSubmitted)
	add(t, layout, "task-2", lyricsModel, kie.StatusSucceeded)

	got := run(t, "task", "list")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	if calls := stub.calls(); len(calls) != 0 {
		t.Errorf("the API was asked %v; task list reads the ledger", calls)
	}
	for _, want := range []string{"task-1", "task-2", marketModel, lyricsModel, kie.StatusSubmitted, kie.StatusSucceeded} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("the listing lacks %q:\n%s", want, got.stdout)
		}
	}
	// A task nothing has asked about since it was submitted may well have
	// finished, and the listing cannot know: it says so rather than
	// letting the row be read as current.
	if !strings.Contains(got.stderr, "refresh") {
		t.Errorf("stderr does not say the listing may be behind:\n%s", got.stderr)
	}
}

// A listing with nothing left to ask about is current, and saying otherwise
// would train the reader to ignore the line.
func TestTaskListSaysNothingWhenEveryTaskIsFinished(t *testing.T) {
	layout := isolate(t)
	add(t, layout, "task-1", marketModel, kie.StatusSucceeded)

	got := run(t, "task", "list")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	if got.stderr != "" {
		t.Errorf("stderr = %q, want nothing when no task can have moved", got.stderr)
	}
}

func TestTaskListFiltersByStatus(t *testing.T) {
	layout := isolate(t)
	add(t, layout, "task-1", marketModel, kie.StatusSubmitted)
	add(t, layout, "task-2", marketModel, kie.StatusFailed)

	got := run(t, "task", "list", "--status", kie.StatusFailed)
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	if strings.Contains(got.stdout, "task-1") {
		t.Errorf("the listing holds a task in another state:\n%s", got.stdout)
	}
	if !strings.Contains(got.stdout, "task-2") {
		t.Errorf("the listing lacks the task that is in the state asked for:\n%s", got.stdout)
	}
}

// A state that is not one of the four is a mistake in the call, not an empty
// listing: an empty listing reads as "there are none".
func TestTaskListRefusesAnUnknownStatus(t *testing.T) {
	isolate(t)

	got := run(t, "task", "list", "--status", "done")
	if got.code != 2 {
		t.Errorf("code = %d, want 2 (stderr %q)", got.code, got.stderr)
	}
	for _, want := range kie.Statuses {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("stderr does not name the state %q:\n%s", want, got.stderr)
		}
	}
}

// V4: the JSON contract gains creditsConsumed and nothing else changes. A task
// no answer has said the cost of is null there, so that a consumer cannot read
// "no record" as "cost nothing"; zero is zero.
func TestTaskListAsJSON(t *testing.T) {
	layout := isolate(t)
	add(t, layout, "unrecorded", marketModel, kie.StatusSubmitted)
	costed(t, layout, "free", credits(0))
	costed(t, layout, "paid", credits(0.4))

	got := run(t, "task", "list", "--json")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got.stdout), &raw); err != nil {
		t.Fatalf("stdout is not JSON (%v):\n%s", err, got.stdout)
	}
	wantKeys := []string{"createdAt", "creditsConsumed", "modelId", "resultUrls", "savedPaths", "status", "taskId", "updatedAt"}
	wantCredits := map[string]string{"unrecorded": "null", "free": "0", "paid": "0.4"}
	if len(raw) != len(wantCredits) {
		t.Fatalf("listed %d tasks, want %d", len(raw), len(wantCredits))
	}
	for _, row := range raw {
		keys := slices.Sorted(maps.Keys(row))
		if !slices.Equal(keys, wantKeys) {
			t.Errorf("keys = %v, want %v", keys, wantKeys)
		}
		var id string
		if err := json.Unmarshal(row["taskId"], &id); err != nil {
			t.Fatalf("taskId is not a string: %s", row["taskId"])
		}
		if got := string(row["creditsConsumed"]); got != wantCredits[id] {
			t.Errorf("%s: creditsConsumed = %s, want %s", id, got, wantCredits[id])
		}
	}

	var listed []struct {
		TaskID     string   `json:"taskId"`
		ModelID    string   `json:"modelId"`
		ResultURLs []string `json:"resultUrls"`
		SavedPaths []string `json:"savedPaths"`
		CreatedAt  string   `json:"createdAt"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &listed); err != nil {
		t.Fatalf("stdout is not JSON (%v):\n%s", err, got.stdout)
	}
	unrecorded := listed[len(listed)-1]
	if unrecorded.TaskID != "unrecorded" || unrecorded.ModelID != marketModel {
		t.Errorf("listed = %+v, want the recorded task", unrecorded)
	}
	if unrecorded.ResultURLs == nil {
		t.Error("resultUrls is null; a task that has produced nothing has produced an empty list")
	}
	if unrecorded.SavedPaths == nil {
		t.Error("savedPaths is null; a task nothing has saved has an empty list of paths")
	}
	if _, err := time.Parse(time.RFC3339, unrecorded.CreatedAt); err != nil {
		t.Errorf("createdAt = %q, want a timestamp: %v", unrecorded.CreatedAt, err)
	}
}

// The table shows what a task cost in the column before the detail, and a dash
// where nothing has said -- never a 0 that nobody reported.
func TestTaskListShowsWhatEachTaskCost(t *testing.T) {
	layout := isolate(t)
	add(t, layout, "unrecorded", marketModel, kie.StatusSubmitted)
	costed(t, layout, "free", credits(0))
	costed(t, layout, "paid", credits(18))
	costed(t, layout, "fraction", credits(0.4))

	got := run(t, "task", "list")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	want := map[string]string{"unrecorded": "-", "free": "0", "paid": "18", "fraction": "0.4"}
	for _, line := range strings.Split(strings.TrimRight(got.stdout, "\n"), "\n") {
		// id, status, model, created, credits, detail
		fields := strings.Fields(line)
		if len(fields) != 6 {
			t.Fatalf("row %q has %d columns, want 6", line, len(fields))
		}
		if fields[4] != want[fields[0]] {
			t.Errorf("%s: credits column = %q, want %q", fields[0], fields[4], want[fields[0]])
		}
	}
}

// AC2: the listing can be narrowed to what is left to collect, which is the
// question the results expiring makes worth asking. A success with no files
// behind it is not part of the answer: nothing could ever save it, and it
// would sit in the listing for ever.
func TestTaskListUnsaved(t *testing.T) {
	layout, dir, h := saver(t, map[string]file{"/results/a.png": {body: "a", mediaType: "image/png"}})
	produced(t, layout, "has-files", h.url+"/results/a.png")
	produced(t, layout, "no-files")
	add(t, layout, "still-running", marketModel, kie.StatusRunning)
	add(t, layout, "failed", marketModel, kie.StatusFailed)

	got := run(t, "task", "list", "--unsaved")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "has-files") {
		t.Errorf("the listing lacks the task with something to save:\n%s", got.stdout)
	}
	for _, unwanted := range []string{"no-files", "still-running", "failed"} {
		if strings.Contains(got.stdout, unwanted) {
			t.Errorf("the listing holds %q, which has nothing to save:\n%s", unwanted, got.stdout)
		}
	}

	// Once it is saved it is not left to do, and the listing says so
	// without anything having to be asked of kie.ai.
	if saved := run(t, "task", "download", "has-files"); saved.code != 0 {
		t.Fatalf("task download: code %d, stderr %q", saved.code, saved.stderr)
	}
	got = run(t, "task", "list", "--unsaved")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	if strings.TrimSpace(got.stdout) != "" {
		t.Errorf("the listing is not empty after everything was saved:\n%s", got.stdout)
	}
	// The whole ledger is still there; only the question was narrower.
	if all := run(t, "task", "list"); !strings.Contains(all.stdout, "has-files") {
		t.Errorf("the saved task is missing from the full listing:\n%s", all.stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "has-files-1.png")); err != nil {
		t.Errorf("the saved file is not on disk: %v", err)
	}
}

// --status and --unsaved are two filters on one listing, so a state that no
// unsaved task is in answers with nothing rather than with that state.
func TestTaskListUnsavedNarrowsByStatusToo(t *testing.T) {
	layout, _, h := saver(t, nil)
	produced(t, layout, "has-files", h.url+"/results/a.png")

	got := run(t, "task", "list", "--unsaved", "--status", kie.StatusFailed)
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	if strings.TrimSpace(got.stdout) != "" {
		t.Errorf("a failed task was listed as unsaved:\n%s", got.stdout)
	}
}

// AC1, AC3: every unfinished task is asked about by the same command, and what
// came back -- the state and the URLs -- is in the ledger when it returns.
func TestTaskRefreshRecordsWhatKieSays(t *testing.T) {
	layout := isolate(t)
	t.Setenv(config.APIKeyEnv, secret)
	stub := stubQueries(t, map[string]string{
		"/api/v1/jobs/recordInfo?taskId=task-1": `{"code":200,"msg":"success","data":{"state":"success",
			"resultJson":"{\"resultUrls\":[\"https://file.kie.ai/a.jpg\"]}","creditsConsumed":18.0}}`,
		"/api/v1/jobs/recordInfo?taskId=task-2": `{"code":200,"msg":"success","data":{"state":"success",
			"resultJson":"{\"text\":\"[Verse]\"}"}}`,
	})
	add(t, layout, "task-1", marketModel, kie.StatusSubmitted)
	add(t, layout, "task-2", lyricsModel, kie.StatusSubmitted)

	got := run(t, "task", "refresh")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	assertNoLeak(t, got.stdout+got.stderr)
	if calls := stub.calls(); len(calls) != 2 {
		t.Errorf("the API was asked %v, want one question per unfinished task", calls)
	}
	for _, call := range stub.calls() {
		if !strings.Contains(call, "taskId=task-") {
			t.Errorf("the query %q does not carry the task id", call)
		}
	}

	market := recorded(t, layout, "task-1")
	if market.Status != kie.StatusSucceeded {
		t.Errorf("the Market task is %q, want %q", market.Status, kie.StatusSucceeded)
	}
	if len(market.ResultURLs) != 1 || market.ResultURLs[0] != "https://file.kie.ai/a.jpg" {
		t.Errorf("resultUrls = %v, want the URL kie.ai answered with", market.ResultURLs)
	}
	if market.CreditsConsumed == nil || *market.CreditsConsumed != 18 {
		t.Errorf("creditsConsumed = %v, want the 18 kie.ai answered with", market.CreditsConsumed)
	}
	// AC2 of the lyrics kind: a task that answers with the text itself
	// finishes with nothing to download, which is a success.
	lyrics := recorded(t, layout, "task-2")
	if lyrics.Status != kie.StatusSucceeded {
		t.Errorf("the lyrics task is %q, want %q", lyrics.Status, kie.StatusSucceeded)
	}
	if len(lyrics.ResultURLs) != 0 {
		t.Errorf("resultUrls = %v, want none for a task that returns no files", lyrics.ResultURLs)
	}
	// An answer that did not say what the task cost leaves no record of
	// it, rather than a 0 nobody reported.
	if lyrics.CreditsConsumed != nil {
		t.Errorf("creditsConsumed = %v, want no record from an answer that carried none", *lyrics.CreditsConsumed)
	}
	if !strings.Contains(got.stdout, "task-1") || !strings.Contains(got.stdout, "task-2") {
		t.Errorf("refresh does not print what it found:\n%s", got.stdout)
	}
}

// AC2: a failure leaves behind why it failed, which is the only record of it
// -- kie.ai answers with the reason once, to whoever asked.
func TestTaskRefreshRecordsWhyATaskFailed(t *testing.T) {
	layout := isolate(t)
	t.Setenv(config.APIKeyEnv, secret)
	stubQueries(t, map[string]string{
		"/api/v1/jobs/recordInfo": `{"code":200,"msg":"success","data":{"state":"fail",
			"failCode":"501","failMsg":"Generation Failed"}}`,
	})
	add(t, layout, "task-1", marketModel, kie.StatusRunning)

	got := run(t, "task", "refresh")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	task := recorded(t, layout, "task-1")
	if task.Status != kie.StatusFailed {
		t.Errorf("status = %q, want %q", task.Status, kie.StatusFailed)
	}
	if !strings.Contains(task.Error, "Generation Failed") {
		t.Errorf("error = %q, want the reason kie.ai gave", task.Error)
	}
	if !strings.Contains(got.stdout, "Generation Failed") {
		t.Errorf("refresh does not print why the task failed:\n%s", got.stdout)
	}
}

// V4: an endpoint whose answers this build cannot read leaves the row exactly
// as it was and says which task and which endpoint, so the task can be
// collected once a decoder for it exists.
//
// Every model the generator writes is followed through the Market endpoint,
// so the other one comes from a downloaded catalog.
func TestTaskRefreshLeavesAnUnreadableEndpointAlone(t *testing.T) {
	layout := isolate(t)
	t.Setenv(config.APIKeyEnv, secret)
	stub := stubQueries(t, map[string]string{
		"/api/v1/jobs/recordInfo": `{"code":200,"msg":"success","data":{"state":"generating"}}`,
	})
	unreadable := publishedModel()
	unreadable.Query.Path = "/api/v1/veo/record-info"
	models := embedded(t).Models
	i := slices.IndexFunc(models, func(m catalog.Model) bool { return m.ID == marketModel })
	if i < 0 {
		t.Fatalf("%s is not in the embedded catalog", marketModel)
	}
	downloadModels(t, layout, "2026-08-20", []catalog.Model{unreadable, models[i]})
	add(t, layout, "task-1", newModelID, kie.StatusSubmitted)
	add(t, layout, "task-2", marketModel, kie.StatusSubmitted)

	got := run(t, "task", "refresh")
	if got.code != 1 {
		t.Errorf("code = %d, want 1 (stderr %q)", got.code, got.stderr)
	}
	for _, want := range []string{"task-1", "/api/v1/veo/record-info"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("stderr does not name %q:\n%s", want, got.stderr)
		}
	}
	if task := recorded(t, layout, "task-1"); task.Status != kie.StatusSubmitted {
		t.Errorf("status = %q, want the row left as it was", task.Status)
	}
	// The one that could be read is still read: one endpoint this build
	// does not know must not stop the rest of the ledger being collected.
	if task := recorded(t, layout, "task-2"); task.Status != kie.StatusRunning {
		t.Errorf("the readable task is %q, want %q", task.Status, kie.StatusRunning)
	}
	if calls := stub.calls(); len(calls) != 1 {
		t.Errorf("the API was asked %v; an endpoint that cannot be read is not asked at all", calls)
	}
}

// A finished task is never asked about again: kie.ai charges nothing for a
// query, but the answers expire and the ledger is the record that does not.
func TestTaskRefreshAsksOnlyAboutUnfinishedTasks(t *testing.T) {
	layout := isolate(t)
	t.Setenv(config.APIKeyEnv, secret)
	stub := stubQueries(t, map[string]string{
		"/api/v1/jobs/recordInfo": `{"code":200,"msg":"success","data":{"state":"generating"}}`,
	})
	add(t, layout, "task-1", marketModel, kie.StatusSucceeded)
	add(t, layout, "task-2", marketModel, kie.StatusFailed)
	add(t, layout, "task-3", marketModel, kie.StatusRunning)

	got := run(t, "task", "refresh")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	calls := stub.calls()
	if len(calls) != 1 || !strings.Contains(calls[0], "task-3") {
		t.Errorf("the API was asked %v, want the unfinished task alone", calls)
	}
}

// updated_at means "when this task last changed". A query that finds nothing
// new must not move it, or nothing could ever tell a task that is progressing
// from one that has been stuck for a day.
func TestTaskRefreshLeavesUnchangedRowsAlone(t *testing.T) {
	layout := isolate(t)
	t.Setenv(config.APIKeyEnv, secret)
	stubQueries(t, map[string]string{
		"/api/v1/jobs/recordInfo": `{"code":200,"msg":"success","data":{"state":"generating"}}`,
	})
	add(t, layout, "task-1", marketModel, kie.StatusRunning)
	before := recorded(t, layout, "task-1")

	if got := run(t, "task", "refresh"); got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}

	after := recorded(t, layout, "task-1")
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("updated_at moved from %v to %v on an answer that said nothing new", before.UpdatedAt, after.UpdatedAt)
	}
}

// A cost is news on its own: a task still running whose answer now says what
// it has cost is written down even though nothing else about it moved.
func TestTaskRefreshRecordsACostThatChangedAlone(t *testing.T) {
	layout := isolate(t)
	t.Setenv(config.APIKeyEnv, secret)
	stubQueries(t, map[string]string{
		"/api/v1/jobs/recordInfo": `{"code":200,"msg":"success","data":{"state":"generating","creditsConsumed":6}}`,
	})
	add(t, layout, "task-1", marketModel, kie.StatusRunning)

	if got := run(t, "task", "refresh"); got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	after := recorded(t, layout, "task-1")
	if after.CreditsConsumed == nil || *after.CreditsConsumed != 6 {
		t.Errorf("creditsConsumed = %v, want the 6 kie.ai answered with", after.CreditsConsumed)
	}

	// The same figure again is nothing new, and updated_at stays put.
	if got := run(t, "task", "refresh"); got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	if again := recorded(t, layout, "task-1"); !again.UpdatedAt.Equal(after.UpdatedAt) {
		t.Errorf("updated_at moved from %v to %v on an answer that said nothing new", after.UpdatedAt, again.UpdatedAt)
	}
}

// kie.ai is asked about one task at a time, so a ledger with a dozen
// unfinished tasks is a dozen round trips: they overlap, and the number that
// overlap is bounded so that a large ledger is not a burst of requests.
func TestTaskRefreshAsksInParallelWithinABound(t *testing.T) {
	layout := isolate(t)
	t.Setenv(config.APIKeyEnv, secret)
	stub := stubQueries(t, map[string]string{
		"/api/v1/jobs/recordInfo": `{"code":200,"msg":"success","data":{"state":"generating"}}`,
	})
	stub.hold = 4
	for _, id := range []string{"task-1", "task-2", "task-3", "task-4", "task-5", "task-6"} {
		add(t, layout, id, marketModel, kie.StatusRunning)
	}

	got := run(t, "task", "refresh")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	stub.mu.Lock()
	peak := stub.peak
	stub.mu.Unlock()
	if peak != 4 {
		t.Errorf("%d queries were in flight at once, want the four this command overlaps", peak)
	}
}
