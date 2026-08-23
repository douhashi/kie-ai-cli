package kie_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/kie"
)

// testKey is distinctive enough that any run of it appearing in an error can be
// told apart from the rest of the message.
const testKey = "kie-live-QRSTUVWX90ab7f31"

// creditPath is the one path these tests exercise. It is written out here
// rather than taken from the package, so that a change to the endpoint has to
// be made in two places and cannot pass unnoticed.
const creditPath = "/api/v1/chat/credit"

// stub stands in for kie.ai. It answers every request with one canned response
// and keeps the last request and the body it carried, which is the only place
// the wire format of an outgoing call can be checked.
type stub struct {
	client *kie.Client
	last   *http.Request
	sent   []byte
}

func serve(t *testing.T, status int, body string) *stub {
	t.Helper()
	s := &stub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read before the clone: cloning shares the body, which is
		// closed as soon as this handler returns.
		sent, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request body: %v", err)
		}
		s.sent = sent
		s.last = r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	// Both hosts are this one server: which endpoint went where is
	// asserted on by path, and TestNewPointsUploadsAtTheirOwnHost is what
	// holds the two apart.
	s.client = &kie.Client{APIKey: testKey, BaseURL: srv.URL, UploadBaseURL: srv.URL, HTTP: srv.Client()}
	return s
}

func TestNewPointsAtKieAI(t *testing.T) {
	c := kie.New(testKey)
	if c.BaseURL != "https://api.kie.ai" {
		t.Errorf("BaseURL = %q, want the kie.ai API host", c.BaseURL)
	}
	// The time limit is no longer on the shared client: one value there
	// would have to bound both a short read and the sending of a 100MB
	// file. Each call sets its own instead, which the internal test in
	// timeout_test.go is what keeps them from forgetting to do.
	if c.HTTP == nil {
		t.Fatal("HTTP is nil")
	}
	if c.HTTP.Timeout != 0 {
		t.Errorf("HTTP.Timeout = %s, want the limit to belong to each call", c.HTTP.Timeout)
	}
	if c.APIKey != testKey {
		t.Errorf("APIKey = %q, want the key it was built with", c.APIKey)
	}
}

func TestCreditsReadsTheBalance(t *testing.T) {
	s := serve(t, http.StatusOK, `{"code":200,"msg":"success","data":4346.6}`)

	balance, err := s.client.Credits(t.Context())
	if err != nil {
		t.Fatalf("Credits: %v", err)
	}
	// The balance is carried as the API wrote it: a float conversion would
	// round it and the printed value would stop matching the account.
	if balance.String() != "4346.6" {
		t.Errorf("balance = %q, want the literal the API sent", balance)
	}

	if s.last.Method != http.MethodGet {
		t.Errorf("method = %q, want GET", s.last.Method)
	}
	if s.last.URL.Path != creditPath {
		t.Errorf("path = %q, want %q", s.last.URL.Path, creditPath)
	}
	if got := s.last.Header.Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("Authorization = %q, want the key as a bearer token", got)
	}
}

// V3: the response says two things about whether the call worked, and both have
// to agree. kie.ai answers an authentication failure with HTTP 200 and code
// 401, so a client that trusts the status alone reports a missing balance as a
// success.
func TestCreditsRejectsWhatIsNotASuccess(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   []string
	}{
		{
			name:   "authentication fails behind a 200",
			status: http.StatusOK,
			body:   `{"code":401,"msg":"Unauthorized - Authentication failed."}`,
			want:   []string{"401", "Authentication failed"},
		},
		{
			name:   "the status contradicts the code",
			status: http.StatusInternalServerError,
			body:   `{"code":200,"msg":"success","data":1}`,
			want:   []string{"500"},
		},
		{
			name:   "the body is not the common shape",
			status: http.StatusNotFound,
			body:   "<html><body>404 Not Found</body></html>",
			want:   []string{"404", "Not Found"},
		},
		{
			name:   "the body is not JSON at all",
			status: http.StatusOK,
			body:   `{"code":200,`,
			want:   []string{"200"},
		},
		{
			name:   "the balance is missing",
			status: http.StatusOK,
			body:   `{"code":200,"msg":"success"}`,
			want:   []string{"balance"},
		},
		{
			name:   "the balance is null",
			status: http.StatusOK,
			body:   `{"code":200,"msg":"success","data":null}`,
			want:   []string{"balance"},
		},
		{
			name:   "the balance is not a number",
			status: http.StatusOK,
			body:   `{"code":200,"msg":"success","data":{"amount":1}}`,
			want:   []string{"balance"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := serve(t, tt.status, tt.body)

			balance, err := s.client.Credits(t.Context())
			if err == nil {
				t.Fatalf("Credits returned %q and no error", balance)
			}
			if balance != "" {
				t.Errorf("balance = %q, want nothing alongside an error", balance)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
			assertNoLeak(t, err.Error())
		})
	}
}

// The status and the code an APIError carries are what a caller has to branch
// on; the message alone would have to be parsed.
func TestAPIErrorCarriesWhatTheAPISaid(t *testing.T) {
	s := serve(t, http.StatusOK, `{"code":401,"msg":"Unauthorized"}`)

	_, err := s.client.Credits(t.Context())
	var apiErr *kie.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T (%v), want a *kie.APIError", err, err)
	}
	if apiErr.Status != http.StatusOK || apiErr.Code != 401 || apiErr.Msg != "Unauthorized" {
		t.Errorf("APIError = %+v, want status 200, code 401 and the message the API sent", apiErr)
	}
}

// A body that is not the common shape is quoted back so the reader can see what
// answered, but a page of HTML must not become the error message.
func TestUnrecognisedBodyIsReportedBriefly(t *testing.T) {
	s := serve(t, http.StatusBadGateway, "<html>"+strings.Repeat("padding ", 4096)+"</html>")

	_, err := s.client.Credits(t.Context())
	if err == nil {
		t.Fatal("Credits succeeded on a gateway error page")
	}
	if len(err.Error()) > 512 {
		t.Errorf("the error is %d bytes long; a response body must not be repeated in full", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error %q does not mention the status", err)
	}
}

// V4: the key travels in a header and has no business in any message.
func assertNoLeak(t *testing.T, out string) {
	t.Helper()
	const allowed = 4
	for i := 0; i+allowed < len(testKey); i++ {
		if run := testKey[i : i+allowed+1]; strings.Contains(out, run) {
			t.Errorf("output contains %q, a run of the key:\n%s", run, out)
		}
	}
}
