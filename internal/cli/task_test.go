package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/cli"
	"github.com/douhashi/kie-ai-cli/internal/config"
	"github.com/douhashi/kie-ai-cli/internal/kie"
	"github.com/douhashi/kie-ai-cli/internal/ledger"
	"github.com/douhashi/kie-ai-cli/internal/paths"
)

// submittedID is the task id the stub answers every submission with.
const submittedID = "task-e2f1c0"

// kieStub stands in for kie.ai and records what reached it. A submission is
// charged for, so a test that means to reject one asserts on calls rather than
// on the absence of a body.
type kieStub struct {
	calls int
	path  string
	body  map[string]any
}

// stubKie points every command in this test at a server of its own.
func stubKie(t *testing.T) *kieStub {
	t.Helper()
	s := &kieStub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls++
		s.path = r.URL.Path
		// UseNumber so that what was sent is compared as it was written:
		// through float64 an integer would read back as one either way.
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		s.body = nil
		if err := dec.Decode(&s.body); err != nil {
			t.Errorf("the request body is not a JSON object: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"msg":"success","data":{"taskId":"` + submittedID + `"}}`))
	}))
	t.Cleanup(srv.Close)
	cli.PointAtServer(t, srv.URL, srv.Client())
	return s
}

// submitter is an isolated state directory with a key and a stubbed kie.ai:
// everything a submission needs except the arguments.
func submitter(t *testing.T) (paths.Layout, *kieStub) {
	t.Helper()
	layout := isolate(t)
	t.Setenv(config.APIKeyEnv, secret)
	return layout, stubKie(t)
}

// canonical renders a value as the JSON it would be sent as, so that inputs
// built by different routes -- flags, a file, the ledger -- are compared by
// what they mean rather than by the Go types they happen to be held in.
func canonical(t *testing.T, v any) string {
	t.Helper()
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(encoded)
}

// recorded reads back the one task the ledger holds for id.
func recorded(t *testing.T, layout paths.Layout, id string) ledger.Task {
	t.Helper()
	l, err := ledger.Open(t.Context(), layout.Ledger)
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	defer func() { _ = l.Close() }()
	task, err := l.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("ledger.Get: %v", err)
	}
	return task
}

// AC1, AC3: a Market model is submitted to the one Market path, wrapped in the
// model name it selects, and the task id it answers with is on stdout and in
// the ledger by the time the command returns.
func TestTaskRunSubmitsAMarketModel(t *testing.T) {
	layout, stub := submitter(t)

	got := run(t, "task", "run", "qwen/text-to-image",
		"--prompt", "a cat in a hat",
		"--num_inference_steps", "12",
		"--enable_safety_checker=false",
		"--image_size", "square")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	if got.stderr != "" {
		t.Errorf("stderr = %q, want nothing on a success", got.stderr)
	}
	if got.stdout != "taskId\t"+submittedID+"\n" {
		t.Errorf("stdout = %q, want the task id as one tab-separated row", got.stdout)
	}
	assertNoLeak(t, got.stdout+got.stderr)

	if stub.path != "/api/v1/jobs/createTask" {
		t.Errorf("path = %q, want the Market create path", stub.path)
	}
	wantInput := map[string]any{
		"prompt":                "a cat in a hat",
		"num_inference_steps":   json.Number("12"),
		"enable_safety_checker": false,
		"image_size":            "square",
	}
	want := map[string]any{"model": "qwen/text-to-image", "input": wantInput}
	if canonical(t, stub.body) != canonical(t, want) {
		t.Errorf("body = %s, want %s", canonical(t, stub.body), canonical(t, want))
	}
	// No callback is sent: this tool polls, and a URL nobody is listening
	// on would have kie.ai retrying against nothing.
	if input, _ := stub.body["input"].(map[string]any); input["callBackUrl"] != nil {
		t.Errorf("the body carries a callBackUrl: %s", canonical(t, stub.body))
	}

	task := recorded(t, layout, submittedID)
	if task.ModelID != "qwen/text-to-image" {
		t.Errorf("model_id = %q, want the model that was run", task.ModelID)
	}
	if task.Status != kie.StatusSubmitted {
		t.Errorf("status = %q, want %q", task.Status, kie.StatusSubmitted)
	}
	// The ledger holds the input, not the envelope: a Market task and a
	// standard-API task have to read the same way afterwards.
	if canonical(t, task.Input) != canonical(t, wantInput) {
		t.Errorf("input = %s, want %s", canonical(t, task.Input), canonical(t, wantInput))
	}
}

// AC3: a standard API takes the input as the whole body and has a path of its
// own, and is otherwise submitted and recorded exactly like a Market model.
func TestTaskRunSubmitsAStandardAPI(t *testing.T) {
	layout, stub := submitter(t)

	// This model takes a callBackUrl because kie.ai refuses the request
	// without one; the CLI never invents it (#33).
	got := run(t, "task", "run", "suno-api/generate-lyrics",
		"--prompt", "a song about rain", "--callBackUrl", "https://example.test/hook", "--json")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	var result struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &result); err != nil {
		t.Fatalf("stdout is not JSON (%v):\n%s", err, got.stdout)
	}
	if result.TaskID != submittedID {
		t.Errorf("taskId = %q, want %q", result.TaskID, submittedID)
	}

	if stub.path != "/api/v1/lyrics" {
		t.Errorf("path = %q, want the endpoint of this API", stub.path)
	}
	want := map[string]any{"prompt": "a song about rain", "callBackUrl": "https://example.test/hook"}
	if canonical(t, stub.body) != canonical(t, want) {
		t.Errorf("body = %s, want the input itself: %s", canonical(t, stub.body), canonical(t, want))
	}
	if task := recorded(t, layout, submittedID); canonical(t, task.Input) != canonical(t, want) {
		t.Errorf("input = %s, want %s", canonical(t, task.Input), canonical(t, want))
	}
}

// V4: the three ways of giving the same input produce the same request and the
// same ledger row, and a flag beats the document it was given beside.
func TestTaskRunBuildsTheSameInputWhicheverWayItIsGiven(t *testing.T) {
	document := `{"prompt":"a cat in a hat","num_inference_steps":12,"enable_safety_checker":false}`
	file := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(file, []byte(document), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tests := []struct {
		name  string
		stdin string
		args  []string
	}{
		{
			name: "as flags",
			args: []string{"--prompt", "a cat in a hat", "--num_inference_steps", "12", "--enable_safety_checker=false"},
		},
		{
			name: "as a document",
			args: []string{"--input", file},
		},
		{
			name:  "as a document on standard input",
			stdin: document,
			args:  []string{"--input", "-"},
		},
		{
			name:  "a flag overrides the document",
			stdin: `{"prompt":"something else","num_inference_steps":12,"enable_safety_checker":false}`,
			args:  []string{"--input", "-", "--prompt", "a cat in a hat"},
		},
	}
	want := map[string]any{
		"model": "qwen/text-to-image",
		"input": map[string]any{
			"prompt":                "a cat in a hat",
			"num_inference_steps":   json.Number("12"),
			"enable_safety_checker": false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layout, stub := submitter(t)

			got := runStdin(t, tt.stdin, append([]string{"task", "run", "qwen/text-to-image"}, tt.args...)...)
			if got.code != 0 {
				t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
			}
			if canonical(t, stub.body) != canonical(t, want) {
				t.Errorf("body = %s, want %s", canonical(t, stub.body), canonical(t, want))
			}
			task := recorded(t, layout, submittedID)
			if canonical(t, task.Input) != canonical(t, want["input"]) {
				t.Errorf("input = %s, want %s", canonical(t, task.Input), canonical(t, want["input"]))
			}
		})
	}
}

// V3: everything the catalog can tell is wrong is told before anything is sent,
// because a submission cannot be taken back and has already been paid for.
func TestTaskRunRefusesBeforeItSubmits(t *testing.T) {
	document := filepath.Join(t.TempDir(), "unknown.json")
	if err := os.WriteFile(document, []byte(`{"prompt":"a cat","promt":"a typo"}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	mistyped := filepath.Join(t.TempDir(), "mistyped.json")
	if err := os.WriteFile(mistyped, []byte(`{"prompt":5}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tests := []struct {
		name  string
		code  int
		args  []string
		stdin string
		want  []string
	}{
		{
			name: "a required field is missing",
			code: 1,
			args: []string{"task", "run", "qwen/text-to-image", "--image_size", "square"},
			want: []string{"prompt"},
		},
		{
			name: "a flag the model does not take",
			code: 2,
			args: []string{"task", "run", "qwen/text-to-image", "--prompt", "a cat", "--promt", "a typo"},
			want: []string{"promt", "model show qwen/text-to-image"},
		},
		{
			name: "a key the model does not take",
			code: 1,
			args: []string{"task", "run", "qwen/text-to-image", "--input", document},
			want: []string{"promt", "model show qwen/text-to-image", "catalog update"},
		},
		{
			name: "a value of the wrong type",
			code: 1,
			args: []string{"task", "run", "qwen/text-to-image", "--input", mistyped},
			want: []string{"prompt", "string"},
		},
		{
			name: "no alternative is complete",
			code: 1,
			args: []string{"task", "run", "kling-3.0-omni/transformation", "--prompt", "make it snow"},
			want: []string{"video_urls", "Video Input Only", "Video with Images"},
		},
		{
			name:  "the document is not a JSON object",
			code:  1,
			args:  []string{"task", "run", "qwen/text-to-image", "--input", "-"},
			stdin: `["a cat"]`,
			want:  []string{"object"},
		},
		{
			name: "the document is not there",
			code: 1,
			args: []string{"task", "run", "qwen/text-to-image", "--input", "/nonexistent/kie-ai-cli/input.json"},
			want: []string{"input.json"},
		},
		{
			name: "the model is not in the catalog",
			code: 1,
			args: []string{"task", "run", "acme/not-a-model", "--prompt", "a cat"},
			want: []string{"acme/not-a-model", "model list"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stub := submitter(t)

			got := runStdin(t, tt.stdin, tt.args...)
			if got.code != tt.code {
				t.Errorf("code = %d, want %d (stderr %q)", got.code, tt.code, got.stderr)
			}
			if got.stdout != "" {
				t.Errorf("stdout = %q, want nothing on the success stream", got.stdout)
			}
			for _, want := range tt.want {
				if !strings.Contains(got.stderr, want) {
					t.Errorf("stderr does not mention %q:\n%s", want, got.stderr)
				}
			}
			if stub.calls != 0 {
				t.Errorf("the API was called %d times; a rejected call must reach nothing", stub.calls)
			}
			assertNoLeak(t, got.stdout+got.stderr)
		})
	}
}

// The other half of the branch check: one complete alternative is enough, and
// the fields of the other one are not asked for.
func TestTaskRunAcceptsOneCompleteAlternative(t *testing.T) {
	_, stub := submitter(t)

	got := run(t, "task", "run", "kling-3.0-omni/transformation",
		"--prompt", "make it snow",
		"--video_urls", "https://file.kie.ai/a.mp4")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	want := map[string]any{
		"model": "kling-3.0-omni/transformation",
		"input": map[string]any{
			"prompt":     "make it snow",
			"video_urls": []any{"https://file.kie.ai/a.mp4"},
		},
	}
	if canonical(t, stub.body) != canonical(t, want) {
		t.Errorf("body = %s, want %s", canonical(t, stub.body), canonical(t, want))
	}
}

// An array field is given one element per occurrence, so that a value holding
// a comma or a space needs no quoting of its own.
func TestTaskRunCollectsAnArrayFromRepeatedFlags(t *testing.T) {
	_, stub := submitter(t)

	got := run(t, "task", "run", "kling-3.0-omni/transformation",
		"--prompt", "make it snow",
		"--video_urls", "https://file.kie.ai/a.mp4",
		"--image_urls", "https://file.kie.ai/1.png",
		"--image_urls", "https://file.kie.ai/2.png")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	input, _ := stub.body["input"].(map[string]any)
	want := []any{"https://file.kie.ai/1.png", "https://file.kie.ai/2.png"}
	if canonical(t, input["image_urls"]) != canonical(t, want) {
		t.Errorf("image_urls = %s, want %s", canonical(t, input["image_urls"]), canonical(t, want))
	}
}

// The task exists and has been charged for by the time the ledger is written
// to, so an id that cannot be recorded is still printed: it is the only handle
// on what was bought.
func TestTaskRunPrintsTheIDEvenWhenTheLedgerRefusesIt(t *testing.T) {
	layout, stub := submitter(t)
	// The ledger already holds this id, so recording it again cannot work.
	l, err := ledger.Open(t.Context(), layout.Ledger)
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	if err := l.Add(t.Context(), submittedID, "qwen/text-to-image", kie.StatusSubmitted, nil); err != nil {
		t.Fatalf("ledger.Add: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("ledger.Close: %v", err)
	}

	got := run(t, "task", "run", "qwen/text-to-image", "--prompt", "a cat in a hat")
	if got.code != 1 {
		t.Errorf("code = %d, want 1 (stderr %q)", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, submittedID) {
		t.Errorf("stdout = %q, want the id of the task that was paid for", got.stdout)
	}
	if !strings.Contains(got.stderr, submittedID) {
		t.Errorf("stderr does not say which task could not be recorded:\n%s", got.stderr)
	}
	if stub.calls != 1 {
		t.Errorf("the API was called %d times, want one submission", stub.calls)
	}
}

// A broken ledger is found before the money is spent, not after: the id would
// otherwise be bought and then dropped.
func TestTaskRunDoesNotSubmitWhenTheLedgerCannotBeOpened(t *testing.T) {
	layout, stub := submitter(t)
	if err := os.MkdirAll(layout.Ledger, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	got := run(t, "task", "run", "qwen/text-to-image", "--prompt", "a cat in a hat")
	if got.code != 1 {
		t.Errorf("code = %d, want 1 (stderr %q)", got.code, got.stderr)
	}
	if stub.calls != 0 {
		t.Errorf("the API was called %d times; a ledger that cannot be opened must stop the submission", stub.calls)
	}
}

// The model id is the first argument after the verb and nothing else is: a
// flag standing where it belongs would otherwise be read as the model.
func TestTaskRunWantsTheModelIDFirst(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no model at all", args: []string{"task", "run"}},
		{name: "a flag before the model", args: []string{"task", "run", "--prompt", "a cat", "qwen/text-to-image"}},
		{name: "--json before the model", args: []string{"task", "run", "--json", "qwen/text-to-image"}},
		{name: "a second model", args: []string{"task", "run", "qwen/text-to-image", "--prompt", "a cat", "suno-api/generate-lyrics"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stub := submitter(t)

			got := run(t, tt.args...)
			if got.code != 2 {
				t.Errorf("code = %d, want 2 (stderr %q)", got.code, got.stderr)
			}
			if !strings.Contains(got.stderr, "Usage:") {
				t.Errorf("stderr does not show the usage text:\n%s", got.stderr)
			}
			if stub.calls != 0 {
				t.Errorf("the API was called %d times; a misuse must reach nothing", stub.calls)
			}
		})
	}
}

// A missing key is reported as itself, before anything is submitted.
func TestTaskRunWithoutAKey(t *testing.T) {
	isolate(t)
	stub := stubKie(t)

	got := run(t, "task", "run", "qwen/text-to-image", "--prompt", "a cat in a hat")
	if got.code != 1 {
		t.Fatalf("code = %d, want 1 (stderr %q)", got.code, got.stderr)
	}
	for _, want := range []string{config.APIKeyEnv, "config set api_key"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("stderr does not mention %q:\n%s", want, got.stderr)
		}
	}
	if stub.calls != 0 {
		t.Errorf("the API was called %d times without a key", stub.calls)
	}
}
