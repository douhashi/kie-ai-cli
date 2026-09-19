package kie_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/kie"
)

// The endpoint whose answers this build reads. It is written out here rather
// than taken from the catalog, so that the wire contract is asserted against a
// fixed string.
const marketQueryPath = "/api/v1/jobs/recordInfo"

// envelope wraps a data document the way every kie.ai answer arrives.
func envelope(data string) string {
	return `{"code":200,"msg":"success","data":` + data + `}`
}

func TestQueryTaskAsksForTheTaskByTheNamedParameter(t *testing.T) {
	s := serve(t, http.StatusOK, envelope(`{"state":"success","resultJson":"{\"resultUrls\":[\"https://example.com/a.jpg\"]}"}`))

	state, err := s.client.QueryTask(t.Context(), marketQueryPath, "taskId", "task-abc123")
	if err != nil {
		t.Fatalf("QueryTask: %v", err)
	}
	if s.last.Method != http.MethodGet {
		t.Errorf("method = %q, want GET", s.last.Method)
	}
	if s.last.URL.Path != marketQueryPath {
		t.Errorf("path = %q, want %q", s.last.URL.Path, marketQueryPath)
	}
	if got := s.last.URL.Query().Get("taskId"); got != "task-abc123" {
		t.Errorf("taskId = %q, want the id it was asked about", got)
	}
	if got := s.last.Header.Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("Authorization = %q, want the key as a bearer token", got)
	}
	want := kie.TaskState{Status: kie.StatusSucceeded, ResultURLs: []string{"https://example.com/a.jpg"}}
	if !reflect.DeepEqual(state, want) {
		t.Errorf("state = %+v, want %+v", state, want)
	}
}

// An endpoint whose answers have not been read against the live API is
// refused. A decoder written from the documentation alone would report a
// success as a failure and the other way round -- the integer 2 of
// successFlag is "failed" on one family and "generating" on another -- and
// the caller cannot tell a wrong answer from a right one.
func TestQueryTaskRefusesAnEndpointItCannotRead(t *testing.T) {
	s := serve(t, http.StatusOK, envelope(`{"successFlag":2}`))

	_, err := s.client.QueryTask(t.Context(), "/api/v1/veo/record-info", "taskId", "task-abc123")
	if err == nil {
		t.Fatal("QueryTask against an unsupported endpoint succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "/api/v1/veo/record-info") {
		t.Errorf("error = %v, want it to name the endpoint", err)
	}
	if s.last != nil {
		t.Error("the request was sent; an endpoint that cannot be read is refused before anything is asked")
	}
}

func TestQueryTaskReadsTheAnswersItSupports(t *testing.T) {
	tests := []struct {
		name string
		path string
		data string
		want kie.TaskState
	}{
		{
			// The example the Market documentation prints.
			name: "a Market task that succeeded",
			path: marketQueryPath,
			data: `{"taskId":"task_12345678","model":"grok-imagine/text-to-image","state":"success",
				"resultJson":"{\"resultUrls\":[\"https://example.com/generated-content.jpg\"]}",
				"failCode":"","failMsg":"","costTime":15000,"creditsConsumed":18.0}`,
			want: kie.TaskState{
				Status:          kie.StatusSucceeded,
				ResultURLs:      []string{"https://example.com/generated-content.jpg"},
				CreditsConsumed: credits(18),
			},
		},
		{
			// A lyrics task answers through the same endpoint, and what
			// it cost is read the same way: a fraction is kept as one.
			name: "a Market task that cost a fraction of a credit",
			path: marketQueryPath,
			data: `{"state":"success","resultJson":"{\"resultObject\":{}}","creditsConsumed":0.4}`,
			want: kie.TaskState{Status: kie.StatusSucceeded, ResultURLs: []string{}, CreditsConsumed: credits(0.4)},
		},
		{
			// Zero is an answer -- a failure kie.ai did not charge for --
			// and is kept apart from an answer that says nothing.
			name: "a Market task that cost nothing",
			path: marketQueryPath,
			data: `{"state":"fail","failCode":"500","failMsg":"timed out","creditsConsumed":0.0}`,
			want: kie.TaskState{Status: kie.StatusFailed, ResultURLs: []string{}, Error: "500: timed out", CreditsConsumed: credits(0)},
		},
		{
			name: "a Market answer whose credits are null",
			path: marketQueryPath,
			data: `{"state":"generating","creditsConsumed":null}`,
			want: kie.TaskState{Status: kie.StatusRunning, ResultURLs: []string{}},
		},
		{
			// Not every Market model answers with URLs: the OmniHuman
			// subject detection models answer with a result object.
			name: "a Market task whose result is not a URL",
			path: marketQueryPath,
			data: `{"state":"success","resultJson":"{\"resultObject\":{\"subject_status\":1}}"}`,
			want: kie.TaskState{Status: kie.StatusSucceeded, ResultURLs: []string{}},
		},
		{
			name: "a Market task that is queued",
			path: marketQueryPath,
			data: `{"state":"queuing","resultJson":""}`,
			want: kie.TaskState{Status: kie.StatusRunning, ResultURLs: []string{}},
		},
		{
			name: "a Market task that is waiting",
			path: marketQueryPath,
			data: `{"state":"waiting"}`,
			want: kie.TaskState{Status: kie.StatusRunning, ResultURLs: []string{}},
		},
		{
			name: "a Market task that is generating",
			path: marketQueryPath,
			data: `{"state":"generating"}`,
			want: kie.TaskState{Status: kie.StatusRunning, ResultURLs: []string{}},
		},
		{
			name: "a Market task that failed",
			path: marketQueryPath,
			data: `{"state":"fail","failCode":"501","failMsg":"Generation Failed"}`,
			want: kie.TaskState{Status: kie.StatusFailed, ResultURLs: []string{}, Error: "501: Generation Failed"},
		},
		{
			// failCode is documented as a string and may arrive as an
			// integer, so neither spelling may decide whether the row can
			// be read.
			name: "a Market failure whose code is a number",
			path: marketQueryPath,
			data: `{"state":"fail","failCode":501,"failMsg":"Generation Failed"}`,
			want: kie.TaskState{Status: kie.StatusFailed, ResultURLs: []string{}, Error: "501: Generation Failed"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := serve(t, http.StatusOK, envelope(tt.data))

			got, err := s.client.QueryTask(t.Context(), tt.path, "taskId", "task-abc123")
			if err != nil {
				t.Fatalf("QueryTask: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("state = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// An answer this build cannot place is a failure. Falling back to any of the
// four states would write a guess into the ledger, and the one guess that
// matters -- "it is still running" -- is indistinguishable from the truth
// until the task is long expired.
func TestQueryTaskRefusesAnAnswerItCannotPlace(t *testing.T) {
	tests := []struct {
		name string
		path string
		data string
	}{
		{name: "the Market state is missing", path: marketQueryPath, data: `{"resultJson":""}`},
		{name: "the Market state is unknown", path: marketQueryPath, data: `{"state":"paused"}`},
		{name: "the Market data is null", path: marketQueryPath, data: `null`},
		{
			name: "the Market credits are not a number",
			path: marketQueryPath,
			data: `{"state":"success","creditsConsumed":"18"}`,
		},
		{
			name: "the Market result is not the JSON it says it is",
			path: marketQueryPath,
			data: `{"state":"success","resultJson":"not json at all"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := serve(t, http.StatusOK, envelope(tt.data))

			state, err := s.client.QueryTask(t.Context(), tt.path, "taskId", "task-abc123")
			if err == nil {
				t.Fatalf("QueryTask read %+v, want an error", state)
			}
			if !strings.Contains(err.Error(), tt.path) {
				t.Errorf("error = %v, want it to name the endpoint", err)
			}
		})
	}
}

// The states are one vocabulary, written once. A caller filtering a listing
// and the ledger holding a row have to agree on the spelling.
func TestStatusesAreTheWholeVocabulary(t *testing.T) {
	want := []string{"submitted", "running", "succeeded", "failed"}
	if !reflect.DeepEqual(kie.Statuses, want) {
		t.Errorf("Statuses = %v, want %v", kie.Statuses, want)
	}
	// The states a task can still move out of are the ones task refresh
	// asks about, so they are a subset of the vocabulary rather than a
	// second list of it.
	if want := []string{"submitted", "running"}; !reflect.DeepEqual(kie.Unfinished, want) {
		t.Errorf("Unfinished = %v, want %v", kie.Unfinished, want)
	}
}

// credits is the address of a consumed-credit figure, which is how an answer
// that carried one is told apart from an answer that did not.
func credits(v float64) *float64 {
	return &v
}
