package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/cli"
	"github.com/douhashi/kie-ai-cli/internal/config"
	"github.com/douhashi/kie-ai-cli/internal/kie"
	"github.com/douhashi/kie-ai-cli/internal/ledger"
	"github.com/douhashi/kie-ai-cli/internal/paths"
)

// The models the listing tests use, one per query endpoint that matters here.
const (
	marketModel = "qwen/text-to-image"
	lyricsModel = "suno-api/generate-lyrics"
	// veoModel is queried through an endpoint this build has no decoder
	// for, which is what makes it the unsupported case.
	veoModel = "veo3-api/generate-veo-3-video"
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
		body, ok := s.answers[r.URL.Path]
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

func TestTaskListAsJSON(t *testing.T) {
	layout := isolate(t)
	add(t, layout, "task-1", marketModel, kie.StatusSubmitted)

	got := run(t, "task", "list", "--json")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	var listed []struct {
		TaskID     string   `json:"taskId"`
		ModelID    string   `json:"modelId"`
		Status     string   `json:"status"`
		ResultURLs []string `json:"resultUrls"`
		CreatedAt  string   `json:"createdAt"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &listed); err != nil {
		t.Fatalf("stdout is not JSON (%v):\n%s", err, got.stdout)
	}
	if len(listed) != 1 {
		t.Fatalf("listed %d tasks, want 1", len(listed))
	}
	if listed[0].TaskID != "task-1" || listed[0].ModelID != marketModel {
		t.Errorf("listed = %+v, want the recorded task", listed[0])
	}
	if listed[0].ResultURLs == nil {
		t.Error("resultUrls is null; a task that has produced nothing has produced an empty list")
	}
	if _, err := time.Parse(time.RFC3339, listed[0].CreatedAt); err != nil {
		t.Errorf("createdAt = %q, want a timestamp: %v", listed[0].CreatedAt, err)
	}
}

// AC1, AC3: the two kinds of endpoint are asked in the same way by the same
// command, and what came back -- the state and the URLs -- is in the ledger
// when it returns.
func TestTaskRefreshRecordsWhatKieSays(t *testing.T) {
	layout := isolate(t)
	t.Setenv(config.APIKeyEnv, secret)
	stub := stubQueries(t, map[string]string{
		"/api/v1/jobs/recordInfo": `{"code":200,"msg":"success","data":{"state":"success",
			"resultJson":"{\"resultUrls\":[\"https://file.kie.ai/a.jpg\"]}"}}`,
		"/api/v1/lyrics/record-info": `{"code":200,"msg":"success","data":{"status":"SUCCESS"}}`,
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
	// AC2 of the lyrics kind: an endpoint that answers with the text
	// itself finishes with nothing to download, which is a success.
	lyrics := recorded(t, layout, "task-2")
	if lyrics.Status != kie.StatusSucceeded {
		t.Errorf("the lyrics task is %q, want %q", lyrics.Status, kie.StatusSucceeded)
	}
	if len(lyrics.ResultURLs) != 0 {
		t.Errorf("resultUrls = %v, want none for an endpoint that returns no files", lyrics.ResultURLs)
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
func TestTaskRefreshLeavesAnUnreadableEndpointAlone(t *testing.T) {
	layout := isolate(t)
	t.Setenv(config.APIKeyEnv, secret)
	stub := stubQueries(t, map[string]string{
		"/api/v1/jobs/recordInfo": `{"code":200,"msg":"success","data":{"state":"generating"}}`,
	})
	add(t, layout, "task-1", veoModel, kie.StatusSubmitted)
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
