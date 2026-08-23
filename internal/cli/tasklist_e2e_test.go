//go:build e2e

package cli_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/config"
	"github.com/douhashi/kie-ai-cli/internal/kie"
)

// How long a real task is given to finish, and how long to wait between
// asking. Both endpoints under test answer in well under a minute; the limit
// is there so that a service that has stopped answering fails the test rather
// than holding the suite open.
const (
	finished     = 6 * time.Minute
	betweenAsks  = 10 * time.Second
	imagePrompt  = "a paper boat on a puddle, kie-ai-cli e2e"
	lyricsPrompt = "a short song about a paper boat, kie-ai-cli e2e"
)

// V1, V2: a Market task and a task on an API of its own are followed to the
// end by the same command, and what kie.ai said about each -- the state and
// what it produced -- is in the ledger when it returns.
//
// Every run spends real credits on two tasks kie.ai has no way to cancel, so
// the two models are one test and each is given the one required field.
func TestTaskRefreshFollowsRealTasksToTheEnd(t *testing.T) {
	key := realKey(t)
	t.Logf("credits before: %s", balance(t, key))

	layout := isolate(t)
	t.Setenv(config.APIKeyEnv, key)

	// The lyrics endpoint refuses a submission without a callBackUrl, which
	// only the live API says; see TestTaskRunSubmitsToTheRealAPI.
	market := submit(t, "qwen/text-to-image", "--prompt", imagePrompt)
	lyrics := submit(t, lyricsModel, "--prompt", lyricsPrompt,
		"--callBackUrl", "https://example.com/kie-ai-cli-e2e")

	listed := follow(t, key)
	for _, taskID := range []string{market, lyrics} {
		state, ok := listed[taskID]
		if !ok {
			t.Fatalf("%s is not in the listing task refresh printed", taskID)
		}
		if state.Status != kie.StatusSucceeded {
			t.Errorf("%s is %q (%s), want %q", taskID, state.Status, state.Error, kie.StatusSucceeded)
		}
	}
	// The Market endpoint answers with the files it made; the lyrics
	// endpoint answers with the words themselves, so a success there has
	// nothing to download and an empty list is the right record of it.
	if urls := recorded(t, layout, market).ResultURLs; len(urls) == 0 {
		t.Error("the Market task succeeded with no result URL recorded")
	} else {
		t.Logf("market result: %v", urls)
	}
	if urls := recorded(t, layout, lyrics).ResultURLs; len(urls) != 0 {
		t.Errorf("the lyrics task recorded %v, want no URLs from an endpoint that returns none", urls)
	}

	t.Logf("credits after: %s", balance(t, key))
}

// V3: a task the real API does not know is reported with its id and left in
// the ledger as it was. It costs nothing: a query creates no work.
func TestTaskRefreshReportsATaskTheRealAPIDoesNotKnow(t *testing.T) {
	key := realKey(t)
	layout := isolate(t)
	t.Setenv(config.APIKeyEnv, key)
	const missing = "task-kie-ai-cli-e2e-does-not-exist"
	add(t, layout, missing, marketModel, kie.StatusSubmitted)

	got := run(t, "task", "refresh")
	if got.code != 1 {
		t.Errorf("code = %d, want 1 (stderr %q)", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, missing) {
		t.Errorf("stderr does not name the task that could not be asked about:\n%s", got.stderr)
	}
	assertKeyNotIn(t, key, got.stdout+got.stderr)
	if task := recorded(t, layout, missing); task.Status != kie.StatusSubmitted {
		t.Errorf("status = %q, want the row left as it was", task.Status)
	}
	t.Logf("kie task refresh on an unknown task -> code %d, stderr %q", got.code, strings.TrimRight(got.stderr, "\n"))
}

// V4: an endpoint this build has no decoder for is refused without asking, so
// a model outside the three supported query paths costs nothing to find out
// about and leaves its row alone.
func TestTaskRefreshRefusesAnUnreadableEndpointAgainstTheRealAPI(t *testing.T) {
	key := realKey(t)
	layout := isolate(t)
	t.Setenv(config.APIKeyEnv, key)
	const unread = "task-kie-ai-cli-e2e-unreadable"
	add(t, layout, unread, veoModel, kie.StatusSubmitted)

	got := run(t, "task", "refresh")
	if got.code != 1 {
		t.Errorf("code = %d, want 1 (stderr %q)", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "/api/v1/veo/record-info") {
		t.Errorf("stderr does not name the endpoint that cannot be read:\n%s", got.stderr)
	}
	t.Logf("kie task refresh on an unsupported endpoint -> code %d, stderr %q", got.code, strings.TrimRight(got.stderr, "\n"))
}

// submit runs one task and returns the id kie.ai gave it.
func submit(t *testing.T, model string, args ...string) string {
	t.Helper()
	got := run(t, append([]string{"task", "run", model}, args...)...)
	if got.code != 0 {
		t.Fatalf("task run %s: code %d, stderr %q", model, got.code, got.stderr)
	}
	_, taskID, ok := strings.Cut(strings.TrimRight(got.stdout, "\n"), "\t")
	if !ok || taskID == "" {
		t.Fatalf("task run %s: stdout = %q, want a tab-separated taskId row", model, got.stdout)
	}
	t.Logf("submitted %s as %s", model, taskID)
	return taskID
}

// listedTask is the part of the JSON contract this test reads.
type listedTask struct {
	TaskID string `json:"taskId"`
	Status string `json:"status"`
	Error  string `json:"error"`
}

// follow asks kie.ai about the unfinished tasks until none of them can move
// any further, and returns the last state of every task the ledger holds.
func follow(t *testing.T, key string) map[string]listedTask {
	t.Helper()
	deadline := time.Now().Add(finished)
	for attempt := 1; ; attempt++ {
		got := run(t, "task", "refresh", "--json")
		assertKeyNotIn(t, key, got.stdout+got.stderr)
		if got.code != 0 {
			t.Fatalf("task refresh: code %d, stderr %q", got.code, got.stderr)
		}
		var refreshed []listedTask
		if err := json.Unmarshal([]byte(got.stdout), &refreshed); err != nil {
			t.Fatalf("task refresh: stdout is not JSON (%v):\n%s", err, got.stdout)
		}
		t.Logf("refresh %d: %+v", attempt, refreshed)

		if len(refreshed) == 0 {
			break
		}
		if !slices.ContainsFunc(refreshed, func(l listedTask) bool {
			return slices.Contains(kie.Unfinished, l.Status)
		}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the tasks were still unfinished after %s: %+v", finished, refreshed)
		}
		time.Sleep(betweenAsks)
	}

	got := run(t, "task", "list", "--json")
	if got.code != 0 {
		t.Fatalf("task list: code %d, stderr %q", got.code, got.stderr)
	}
	var all []listedTask
	if err := json.Unmarshal([]byte(got.stdout), &all); err != nil {
		t.Fatalf("task list: stdout is not JSON (%v):\n%s", err, got.stdout)
	}
	listed := make(map[string]listedTask, len(all))
	for _, l := range all {
		listed[l.TaskID] = l
	}
	return listed
}
