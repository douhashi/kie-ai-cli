//go:build e2e

package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/config"
	"github.com/douhashi/kie-ai-cli/internal/ledger"
)

// submitted is how long a submission is allowed to take. The command creates
// the task and returns; waiting for the result is another command's job, and a
// submission that took longer than this would mean it is waiting for something.
const submitted = 5 * time.Second

// V1, V2: the two kinds of endpoint -- the Market path and a standard API of
// its own -- are submitted by the same command with the same flags, each
// answers with a task id, and each task is in the ledger by the time the
// command returns.
//
// Every run spends real credits on two tasks that kie.ai has no way to cancel,
// so the two models are one test rather than two, and each is given the one
// required field and nothing else.
func TestTaskRunSubmitsToTheRealAPI(t *testing.T) {
	key := realKey(t)
	t.Logf("credits before: %s", balance(t, key))

	tests := []struct {
		name   string
		model  string
		prompt string
		style  string
		extra  []string
	}{
		{
			name:   "a Market model",
			model:  "qwen/text-to-image",
			prompt: "a paper boat on a puddle, kie-ai-cli e2e",
			style:  "market",
		},
		{
			name:   "a standard API",
			model:  "suno-api/generate-lyrics",
			prompt: "a short song about a paper boat, kie-ai-cli e2e",
			style:  "direct",
			// This tool never invents a callBackUrl, and this
			// endpoint insists on one: asked without it, kie.ai
			// answers HTTP 200 with code 422 and creates nothing.
			// The catalog does not list it as required -- the
			// OpenAPI document it is generated from does not --
			// which only the real API says, and is why this test
			// exists. The address is one nothing is listening on:
			// the task is polled for, not called back.
			extra: []string{"--callBackUrl", "https://example.com/kie-ai-cli-e2e"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layout := isolate(t)
			t.Setenv(config.APIKeyEnv, key)

			started := time.Now()
			got := run(t, append([]string{"task", "run", tt.model, "--prompt", tt.prompt}, tt.extra...)...)
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
			t.Logf("kie task run %s (%s) -> %s in %s", tt.model, tt.style, taskID, elapsed.Round(time.Millisecond))

			task := recorded(t, layout, taskID)
			if task.ModelID != tt.model {
				t.Errorf("model_id = %q, want %q", task.ModelID, tt.model)
			}
			if task.Status != ledger.StatusSubmitted {
				t.Errorf("status = %q, want %q", task.Status, ledger.StatusSubmitted)
			}
			// The ledger holds the input itself for both styles, so
			// that a Market task and a standard-API task read the
			// same way afterwards.
			want := map[string]any{"prompt": tt.prompt}
			for i := 0; i+1 < len(tt.extra); i += 2 {
				want[strings.TrimPrefix(tt.extra[i], "--")] = tt.extra[i+1]
			}
			if canonical(t, task.Input) != canonical(t, want) {
				t.Errorf("input = %s, want %s", canonical(t, task.Input), canonical(t, want))
			}
		})
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
