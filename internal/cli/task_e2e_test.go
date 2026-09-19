//go:build e2e

package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/config"
	"github.com/douhashi/kie-ai-cli/internal/kie"
)

// submitted is how long a submission is allowed to take. The command creates
// the task and returns; waiting for the result is another command's job, and a
// submission that took longer than this would mean it is waiting for something.
const submitted = 5 * time.Second

// V1, V2: a model is submitted by task run, answers with a task id, and is in
// the ledger by the time the command returns.
//
// Every run spends real credits on a task kie.ai has no way to cancel, so it is
// a cheap model -- lyrics, 0.4 credits a run -- given the one required field and
// nothing else. It is also one of the pages that moved to the Market endpoint
// with its model fixed by an example alone (#51), so a run that succeeds says
// the id read off that page is the one kie.ai takes. No callBackUrl is sent:
// the task is polled for, not called back.
func TestTaskRunSubmitsToTheRealAPI(t *testing.T) {
	key := realKey(t)
	t.Logf("credits before: %s", balance(t, key))

	layout := isolate(t)
	t.Setenv(config.APIKeyEnv, key)
	const prompt = "a short song about a paper boat, kie-ai-cli e2e"

	started := time.Now()
	got := run(t, "task", "run", lyricsModel, "--prompt", prompt)
	elapsed := time.Since(started)
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	if got.stderr != "" {
		t.Errorf("stderr = %q, want nothing on a success", got.stderr)
	}
	assertKeyNotIn(t, key, got.stdout+got.stderr)
	if elapsed > submitted {
		t.Errorf("the command took %s; a submission does not wait for the result", elapsed)
	}

	label, taskID, ok := strings.Cut(strings.TrimRight(got.stdout, "\n"), "\t")
	if !ok || label != "taskId" || taskID == "" {
		t.Fatalf("stdout = %q, want a tab-separated taskId row", got.stdout)
	}
	t.Logf("kie task run %s -> %s in %s", lyricsModel, taskID, elapsed.Round(time.Millisecond))

	task := recorded(t, layout, taskID)
	if task.ModelID != lyricsModel {
		t.Errorf("model_id = %q, want %q", task.ModelID, lyricsModel)
	}
	if task.Status != kie.StatusSubmitted {
		t.Errorf("status = %q, want %q", task.Status, kie.StatusSubmitted)
	}
	if want := map[string]any{"prompt": prompt}; canonical(t, task.Input) != canonical(t, want) {
		t.Errorf("input = %s, want %s", canonical(t, task.Input), canonical(t, want))
	}

	t.Logf("credits after: %s", balance(t, key))
}

// V3: what the catalog can tell is wrong is told without asking kie.ai, so a
// missing required field costs nothing at all -- including against the real
// account, where the alternative would be a charge for a rejected request.
func TestTaskRunRefusesTheRealSubmissionBeforeItIsSent(t *testing.T) {
	key := realKey(t)
	isolate(t)
	t.Setenv(config.APIKeyEnv, key)

	got := run(t, "task", "run", "qwen/text-to-image", "--image_size", "square")
	if got.code != 1 {
		t.Errorf("code = %d, want 1", got.code)
	}
	if got.stdout != "" {
		t.Errorf("stdout = %q, want nothing on the success stream", got.stdout)
	}
	if !strings.Contains(got.stderr, "prompt") {
		t.Errorf("stderr does not name the field that is missing:\n%s", got.stderr)
	}
	assertKeyNotIn(t, key, got.stdout+got.stderr)
	t.Logf("kie task run without the required field -> code %d, stderr %q", got.code, strings.TrimRight(got.stderr, "\n"))
}

// balance reads the account balance through the CLI, which is the one call
// that creates nothing: it is what the credits this test spends are measured
// against.
func balance(t *testing.T, key string) json.Number {
	t.Helper()
	isolate(t)
	t.Setenv(config.APIKeyEnv, key)

	got := run(t, "credits", "show", "--json")
	if got.code != 0 {
		t.Fatalf("credits show: code %d, stderr %q", got.code, got.stderr)
	}
	var result struct {
		Credits json.Number `json:"credits"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &result); err != nil {
		t.Fatalf("stdout is not JSON (%v):\n%s", err, got.stdout)
	}
	return result.Credits
}
