package kie_test

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/kie"
)

// downloadURLPath is the endpoint that re-issues a link. Like creditPath it is
// written out here rather than taken from the package, so that a change to it
// has to be made in two places.
const downloadURLPath = "/api/v1/common/download-url"

// The bytes the stub host serves as the result of a task.
const artifact = "\x89PNG\r\n\x1a\nthe file a task produced"

// hosts stands in for kie.ai and for the storage host its results sit on. The
// two are one server here, told apart by path, and every request that reached
// either is kept: what matters about a download is not only what came back but
// what was sent, and to which of the two.
type hosts struct {
	client *kie.Client

	// artifactStatus is what the result URL answers with, in order: the
	// first request gets the first element, and the last element answers
	// every request after it.
	artifactStatus []int
	// issueBody is the answer to a request for a fresh link. Like every
	// kie.ai endpoint it carries its own code, so the status is always 200.
	issueBody string
	// mediaType is the Content-Type the result is served with, if any.
	mediaType string

	mu       sync.Mutex
	requests []*http.Request
	sent     [][]byte
}

const resultPath = "/results/a-task.png"

func newHosts(t *testing.T) *hosts {
	t.Helper()
	h := &hosts{artifactStatus: []int{http.StatusOK}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request body: %v", err)
		}
		h.mu.Lock()
		seen := len(h.requests)
		h.requests = append(h.requests, r.Clone(r.Context()))
		h.sent = append(h.sent, sent)
		h.mu.Unlock()

		if r.URL.Path == downloadURLPath {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, h.issueBody)
			return
		}
		if h.mediaType != "" {
			w.Header().Set("Content-Type", h.mediaType)
		} else {
			// net/http would otherwise sniff one from the bytes,
			// which is not the case being set up here: a host that
			// declares nothing at all.
			w.Header()["Content-Type"] = nil
		}
		w.WriteHeader(h.answerFor(seen))
		_, _ = io.WriteString(w, artifact)
	}))
	t.Cleanup(srv.Close)
	h.client = &kie.Client{APIKey: testKey, BaseURL: srv.URL, UploadBaseURL: srv.URL, HTTP: srv.Client()}
	h.issueBody = `{"code":200,"msg":"success","data":"` + srv.URL + `/tempfile/fresh?sig=abc"}`
	return h
}

// answerFor is the status the nth request to the result URL is answered with,
// where "the nth" counts every request the server saw: a fresh link is asked
// for between the two attempts, so the second attempt is the third request.
func (h *hosts) answerFor(seen int) int {
	if seen >= len(h.artifactStatus) {
		return h.artifactStatus[len(h.artifactStatus)-1]
	}
	return h.artifactStatus[seen]
}

// resultURL is the address of the file, as a task would have recorded it.
func (h *hosts) resultURL() string { return h.client.BaseURL + resultPath }

// paths is the path of every request the two hosts saw, in order.
func (h *hosts) paths() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.requests))
	for _, r := range h.requests {
		out = append(out, r.URL.Path)
	}
	return out
}

func (h *hosts) request(i int) (*http.Request, []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.requests[i], h.sent[i]
}

// V2, V6: a recorded result URL is fetched as it stands -- one plain GET, with
// no fresh link asked for -- and the key is not sent to the host serving it.
//
// The result URLs point at storage that is not kie.ai and that asks for
// nothing. Sending the bearer token there would hand the account's credential
// to a third party for no gain at all, and do it on every download.
func TestDownloadFetchesTheResultWithoutTheKey(t *testing.T) {
	h := newHosts(t)
	h.mediaType = "image/png"

	var got bytes.Buffer
	mediaType, err := h.client.Download(t.Context(), h.resultURL(), &got)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if got.String() != artifact {
		t.Errorf("wrote %q, want the bytes the host served", got.String())
	}
	if mediaType != "image/png" {
		t.Errorf("media type = %q, want the one the host declared", mediaType)
	}

	if paths := h.paths(); len(paths) != 1 || paths[0] != resultPath {
		t.Fatalf("requests = %v, want the result URL alone", paths)
	}
	req, _ := h.request(0)
	if req.Method != http.MethodGet {
		t.Errorf("method = %q, want GET", req.Method)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want the key withheld from the host serving the result", got)
	}
}

// The extension a file is saved under is read from the media type when the URL
// has none, so a declaration with parameters on it must not be carried into
// the name.
func TestDownloadReportsTheMediaTypeWithoutItsParameters(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{header: "image/png", want: "image/png"},
		{header: "audio/mpeg; charset=binary", want: "audio/mpeg"},
		{header: "", want: ""},
		{header: "not a media type", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			h := newHosts(t)
			h.mediaType = tt.header

			mediaType, err := h.client.Download(t.Context(), h.resultURL(), io.Discard)
			if err != nil {
				t.Fatalf("Download: %v", err)
			}
			if mediaType != tt.want {
				t.Errorf("media type = %q, want %q", mediaType, tt.want)
			}
		})
	}
}

// A refusal is the one answer a fresh link can change, so it is the only one
// that costs a second round trip: the link expires in twenty minutes and asking
// for one before every download would double the requests to no purpose.
func TestDownloadAsksForAFreshLinkOnlyWhenTheResultIsRefused(t *testing.T) {
	h := newHosts(t)
	h.artifactStatus = []int{http.StatusForbidden, http.StatusOK}

	var got bytes.Buffer
	if _, err := h.client.Download(t.Context(), h.resultURL(), &got); err != nil {
		t.Fatalf("Download: %v", err)
	}
	// Exactly the file, once: a retry that appended to what the refusal had
	// already written would save a corrupt file and call it a success.
	if got.String() != artifact {
		t.Errorf("wrote %q, want the bytes the host served exactly once", got.String())
	}

	want := []string{resultPath, downloadURLPath, "/tempfile/fresh"}
	if paths := h.paths(); !slices.Equal(paths, want) {
		t.Fatalf("requests = %v, want %v", paths, want)
	}

	// V1: the endpoint is asked with the recorded URL, as an authenticated
	// POST -- it is a kie.ai API call, unlike the two around it.
	issue, sent := h.request(1)
	if issue.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", issue.Method)
	}
	if got := issue.Header.Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("Authorization = %q, want the key as a bearer token", got)
	}
	var body map[string]string
	if err := json.Unmarshal(sent, &body); err != nil {
		t.Fatalf("the request body is not JSON (%v): %s", err, sent)
	}
	if !slices.Equal(slices.Sorted(maps.Keys(body)), []string{"url"}) || body["url"] != h.resultURL() {
		t.Errorf("body = %s, want the recorded result URL under \"url\"", sent)
	}

	// The fresh link is a credential of its own, and it goes to the storage
	// host, so it carries no key either.
	fresh, _ := h.request(2)
	if got := fresh.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want the key withheld from the host serving the result", got)
	}
}

// The retry is one attempt, not a loop: a result that has expired is refused
// however many links are issued for it, and the caller is told so rather than
// waited on.
func TestDownloadGivesUpWhenAFreshLinkIsRefusedToo(t *testing.T) {
	h := newHosts(t)
	h.artifactStatus = []int{http.StatusForbidden}

	var got bytes.Buffer
	_, err := h.client.Download(t.Context(), h.resultURL(), &got)
	if err == nil {
		t.Fatal("Download succeeded against a host that refused every attempt")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %v, want it to carry what the host answered", err)
	}
	if got.Len() != 0 {
		t.Errorf("wrote %q, want nothing from a refusal", got.String())
	}
	want := []string{resultPath, downloadURLPath, "/tempfile/fresh"}
	if paths := h.paths(); !slices.Equal(paths, want) {
		t.Errorf("requests = %v, want %v", paths, want)
	}
}

// When no fresh link can be had, both halves of what went wrong are reported:
// the refusal on its own reads as an expired result, and the refusal to issue a
// link on its own reads as an account problem.
func TestDownloadReportsWhyNoFreshLinkCouldBeIssued(t *testing.T) {
	h := newHosts(t)
	h.artifactStatus = []int{http.StatusNotFound}
	h.issueBody = `{"code":422,"msg":"invalid url","data":null}`

	_, err := h.client.Download(t.Context(), h.resultURL(), io.Discard)
	if err == nil {
		t.Fatal("Download succeeded with neither the result nor a link to it")
	}
	for _, want := range []string{"404", "422", "invalid url"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %v does not carry %q", err, want)
		}
	}
	if strings.Contains(err.Error(), testKey) {
		t.Errorf("error carries the API key: %v", err)
	}
}

// The answer to a request for a link is a bare string, and anything else is
// refused rather than saved as the name of a file that was never fetched.
func TestDownloadRejectsALinkThatIsNotOne(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "an object", body: `{"code":200,"msg":"success","data":{"url":"https://x"}}`},
		{name: "nothing", body: `{"code":200,"msg":"success","data":""}`},
		{name: "null", body: `{"code":200,"msg":"success","data":null}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHosts(t)
			h.artifactStatus = []int{http.StatusForbidden}
			h.issueBody = tt.body

			if _, err := h.client.Download(t.Context(), h.resultURL(), io.Discard); err == nil {
				t.Fatal("Download succeeded on an answer that carries no link")
			}
		})
	}
}
