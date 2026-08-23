package kie_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// createPath is the Market endpoint every model without an API of its own is
// submitted to. It is written out here rather than taken from the catalog, so
// that the wire format is asserted against a fixed string.
const createPath = "/api/v1/jobs/createTask"

func TestCreateTaskSendsTheBodyAndReturnsTheID(t *testing.T) {
	s := serve(t, http.StatusOK, `{"code":200,"msg":"success","data":{"taskId":"task-abc123"}}`)

	body := map[string]any{"model": "qwen/text-to-image", "input": map[string]any{"prompt": "a cat"}}
	taskID, err := s.client.CreateTask(t.Context(), createPath, body)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if taskID != "task-abc123" {
		t.Errorf("taskID = %q, want the id the API answered with", taskID)
	}

	if s.last.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", s.last.Method)
	}
	if s.last.URL.Path != createPath {
		t.Errorf("path = %q, want %q", s.last.URL.Path, createPath)
	}
	if got := s.last.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := s.last.Header.Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("Authorization = %q, want the key as a bearer token", got)
	}
	var sent map[string]any
	if err := json.Unmarshal(s.sent, &sent); err != nil {
		t.Fatalf("the request body is not JSON (%v): %s", err, s.sent)
	}
	if sent["model"] != "qwen/text-to-image" {
		t.Errorf("the request body is %s, want the one it was given", s.sent)
	}
}

// A submission has been paid for by the time it is answered, so an answer this
// package cannot read the id out of is a failure with a message that says what
// came back -- not an empty id travelling on as if the call had worked.
func TestCreateTaskRejectsAnAnswerWithoutATaskID(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{
			name:   "the data holds no task id",
			status: http.StatusOK,
			body:   `{"code":200,"msg":"success","data":{"recordId":"x"}}`,
			want:   "task id",
		},
		{
			name:   "the task id is empty",
			status: http.StatusOK,
			body:   `{"code":200,"msg":"success","data":{"taskId":""}}`,
			want:   "task id",
		},
		{
			name:   "the data is not an object",
			status: http.StatusOK,
			body:   `{"code":200,"msg":"success","data":"task-abc123"}`,
			want:   "task id",
		},
		{
			name:   "the key is refused behind a 200",
			status: http.StatusOK,
			body:   `{"code":401,"msg":"Unauthorized - Authentication failed."}`,
			want:   "401",
		},
		{
			name:   "the account is out of credits",
			status: http.StatusPaymentRequired,
			body:   `{"code":402,"msg":"Insufficient credits"}`,
			want:   "402",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := serve(t, tt.status, tt.body)

			taskID, err := s.client.CreateTask(t.Context(), createPath, map[string]any{})
			if err == nil {
				t.Fatalf("CreateTask returned %q and no error", taskID)
			}
			if taskID != "" {
				t.Errorf("taskID = %q, want nothing alongside an error", taskID)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
			assertNoLeak(t, err.Error())
		})
	}
}

// Nothing in this package retries, and a submission is the call where that
// matters: a resend the caller did not ask for would create -- and charge for
// -- a second task.
func TestCreateTaskSendsTheRequestOnce(t *testing.T) {
	calls := 0
	s := serve(t, http.StatusInternalServerError, `{"code":500,"msg":"internal error"}`)
	s.onRequest = func() { calls++ }

	if _, err := s.client.CreateTask(t.Context(), createPath, map[string]any{}); err == nil {
		t.Fatal("CreateTask succeeded against a server that failed")
	}
	if calls != 1 {
		t.Errorf("the server was called %d times, want exactly one submission", calls)
	}
}
