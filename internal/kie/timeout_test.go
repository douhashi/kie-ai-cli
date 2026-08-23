package kie

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// V5: the time limit used to sit on the shared http.Client, where one value had
// to serve both a short read and the sending of a file. It moved onto the
// context of each call so that an upload is not cut off after thirty seconds.
// The risk in that move is the opposite failure -- a call that now waits
// forever because nothing sets a deadline any more -- so both kinds of call are
// checked here against a server that never answers.
func TestEveryCallGivesUpWithoutTheCallerSettingADeadline(t *testing.T) {
	tests := []struct {
		name  string
		limit *time.Duration
		call  func(*Client) error
	}{
		{
			name:  "a read",
			limit: &callTimeout,
			call: func(c *Client) error {
				_, err := c.get(context.Background(), "/api/v1/chat/credit")
				return err
			},
		},
		{
			name:  "a task submission",
			limit: &callTimeout,
			call: func(c *Client) error {
				_, err := c.CreateTask(context.Background(), "/api/v1/jobs/createTask", map[string]any{})
				return err
			},
		},
		{
			name:  "an upload from a URL",
			limit: &uploadTimeout,
			call: func(c *Client) error {
				_, err := c.UploadURL(context.Background(), "https://example.com/a.png", "a.png")
				return err
			},
		},
		{
			name:  "an upload of a file",
			limit: &uploadTimeout,
			call: func(c *Client) error {
				_, err := c.UploadStream(context.Background(), "a.png", strings.NewReader("bytes"))
				return err
			},
		},
		{
			name:  "a download",
			limit: &downloadTimeout,
			call: func(c *Client) error {
				_, err := c.Download(context.Background(), c.BaseURL+"/results/a.png", io.Discard)
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, done := clientOfASilentServer(t)
			defer close(done)

			restore := *tt.limit
			*tt.limit = 50 * time.Millisecond
			defer func() { *tt.limit = restore }()

			// The call runs beside the test so that a missing deadline
			// fails here rather than hanging until the whole run is
			// killed. The wait is far longer than the limit set above
			// and far shorter than the real ones.
			const patience = 10 * time.Second
			errs := make(chan error, 1)
			go func() { errs <- tt.call(c) }()

			var err error
			select {
			case err = <-errs:
			case <-time.After(patience):
				t.Fatalf("the call was still waiting after %s; it set no deadline of its own", patience)
			}
			if err == nil {
				t.Fatal("the call succeeded against a server that never answered")
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("error = %v, want the deadline the call set for itself", err)
			}
		})
	}
}

// An upload has to be allowed to take longer than a read: the read is a short
// answer, the upload carries the file. Sharing one limit is what forced the
// move off http.Client in the first place.
func TestAnUploadIsAllowedLongerThanARead(t *testing.T) {
	if uploadTimeout <= callTimeout {
		t.Errorf("uploadTimeout = %s, callTimeout = %s; an upload must be given more room", uploadTimeout, callTimeout)
	}
}

// clientOfASilentServer answers a client whose every request is accepted and
// never replied to. Closing the returned channel releases the handler.
func clientOfASilentServer(t *testing.T) (*Client, chan struct{}) {
	t.Helper()
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-done
	}))
	// Registered first, so it runs after the handler has been released.
	t.Cleanup(srv.Close)
	return &Client{APIKey: "unused", BaseURL: srv.URL, UploadBaseURL: srv.URL, HTTP: srv.Client()}, done
}
