package kie_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/kie"
)

const (
	urlUploadPath    = "/api/file-url-upload"
	streamUploadPath = "/api/file-stream-upload"
)

// storedFile is the answer kie.ai gives to either upload endpoint.
const storedFile = `{"success":true,"code":200,"msg":"success","data":{` +
	`"fileName":"photo.png","filePath":"kie-ai-cli/0011223344556677/photo.png",` +
	`"downloadUrl":"https://file.kie.ai/kie-ai-cli/0011223344556677/photo.png",` +
	`"fileSize":5,"mimeType":"image/png","uploadedAt":"2026-08-24T03:04:05Z"}}`

// wantUpload is storedFile read back as the caller sees it.
var wantUpload = kie.Upload{
	FileName:    "photo.png",
	FilePath:    "kie-ai-cli/0011223344556677/photo.png",
	DownloadURL: "https://file.kie.ai/kie-ai-cli/0011223344556677/photo.png",
	FileSize:    5,
	MimeType:    "image/png",
	UploadedAt:  "2026-08-24T03:04:05Z",
}

// uploadPathPattern is the directory every upload is put in. A new one is
// chosen for each call, so only its shape can be asserted on.
var uploadPathPattern = regexp.MustCompile(`^kie-ai-cli/[0-9a-f]{16}$`)

// The upload endpoints are not on the host the rest of the API is on.
// api.kie.ai answers all three of them with 404, and the host below answers
// everything else with a 404 that lists those three paths as all it serves.
// The OpenAPI documents claim otherwise; the curl examples beside them are
// right. Checked against the live API, so the constant cannot drift back.
func TestNewPointsUploadsAtTheirOwnHost(t *testing.T) {
	c := kie.New(testKey)
	if c.UploadBaseURL != "https://kieai.redpandaai.co" {
		t.Errorf("UploadBaseURL = %q, want the host that serves the upload endpoints", c.UploadBaseURL)
	}
	if c.UploadBaseURL == c.BaseURL {
		t.Errorf("UploadBaseURL = BaseURL = %q; the upload endpoints are not on the API host", c.BaseURL)
	}
}

func TestUploadStreamSendsTheFile(t *testing.T) {
	s := serve(t, http.StatusOK, storedFile)

	got, err := s.client.UploadStream(t.Context(), "photo.png", strings.NewReader("bytes"))
	if err != nil {
		t.Fatalf("UploadStream: %v", err)
	}
	if got != wantUpload {
		t.Errorf("upload = %+v, want %+v", got, wantUpload)
	}

	if s.last.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", s.last.Method)
	}
	if s.last.URL.Path != streamUploadPath {
		t.Errorf("path = %q, want %q", s.last.URL.Path, streamUploadPath)
	}
	if want := "Bearer " + testKey; s.last.Header.Get("Authorization") != want {
		t.Errorf("Authorization = %q, want the key as a bearer token", s.last.Header.Get("Authorization"))
	}

	form := s.form(t)
	if got := form.Value["fileName"]; len(got) != 1 || got[0] != "photo.png" {
		t.Errorf("fileName = %v, want the name the caller gave", got)
	}
	if got := form.Value["uploadPath"]; len(got) != 1 || !uploadPathPattern.MatchString(got[0]) {
		t.Errorf("uploadPath = %v, want a fresh directory under kie-ai-cli", got)
	}
	files := form.File["file"]
	if len(files) != 1 {
		t.Fatalf("the form carries %d file parts, want one named \"file\"", len(files))
	}
	if files[0].Filename != "photo.png" {
		t.Errorf("the file part is named %q, want the name of the file", files[0].Filename)
	}
	if body := readPart(t, files[0]); body != "bytes" {
		t.Errorf("the file part carries %q, want what the reader held", body)
	}
}

// The file is piped onto the connection rather than assembled in memory first,
// so that a 100MB upload does not become 100MB of process. A request with no
// Content-Length is what that looks like from the far side.
func TestUploadStreamDoesNotBufferTheFile(t *testing.T) {
	s := serve(t, http.StatusOK, storedFile)

	if _, err := s.client.UploadStream(t.Context(), "photo.png", strings.NewReader("bytes")); err != nil {
		t.Fatalf("UploadStream: %v", err)
	}
	if !slices.Contains(s.last.TransferEncoding, "chunked") {
		t.Errorf("the request declared Content-Length %d; the body was assembled before it was sent", s.last.ContentLength)
	}
}

// kie.ai addresses a stored file by uploadPath and fileName together, and has
// no endpoint that deletes one. A fixed directory would therefore let a second
// upload of photo.png replace the first one without saying anything.
func TestEachUploadGetsItsOwnDirectory(t *testing.T) {
	s := serve(t, http.StatusOK, storedFile)

	var seen []string
	for range 8 {
		if _, err := s.client.UploadStream(t.Context(), "photo.png", strings.NewReader("bytes")); err != nil {
			t.Fatalf("UploadStream: %v", err)
		}
		seen = append(seen, s.form(t).Value["uploadPath"][0])
	}
	slices.Sort(seen)
	if compacted := slices.Compact(slices.Clone(seen)); len(compacted) != len(seen) {
		t.Errorf("the same directory was used twice: %v", seen)
	}
}

// The name is what the stored file is addressed by, so sending an empty one
// would leave the caller with a file it cannot describe. It is refused here
// rather than sent as an empty part.
func TestUploadStreamNeedsAFileName(t *testing.T) {
	s := serve(t, http.StatusOK, storedFile)

	if _, err := s.client.UploadStream(t.Context(), "", strings.NewReader("bytes")); err == nil {
		t.Error("UploadStream accepted an empty file name")
	} else if !strings.Contains(err.Error(), "name") {
		t.Errorf("error %q does not say what was missing", err)
	}
	if s.last != nil {
		t.Error("a request was sent for an upload that could not be described")
	}
}

func TestUploadURLAsksKieAIToFetchIt(t *testing.T) {
	s := serve(t, http.StatusOK, storedFile)

	got, err := s.client.UploadURL(t.Context(), "https://example.com/a/photo.png", "photo.png")
	if err != nil {
		t.Fatalf("UploadURL: %v", err)
	}
	if got != wantUpload {
		t.Errorf("upload = %+v, want %+v", got, wantUpload)
	}

	if s.last.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", s.last.Method)
	}
	if s.last.URL.Path != urlUploadPath {
		t.Errorf("path = %q, want %q", s.last.URL.Path, urlUploadPath)
	}
	if ct := s.last.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}

	var body map[string]any
	if err := json.Unmarshal(s.sent, &body); err != nil {
		t.Fatalf("the request body is not JSON (%v): %s", err, s.sent)
	}
	if body["fileUrl"] != "https://example.com/a/photo.png" {
		t.Errorf("fileUrl = %v, want the address the caller gave", body["fileUrl"])
	}
	if body["fileName"] != "photo.png" {
		t.Errorf("fileName = %v, want the name the caller gave", body["fileName"])
	}
	dir, _ := body["uploadPath"].(string)
	if !uploadPathPattern.MatchString(dir) {
		t.Errorf("uploadPath = %v, want a fresh directory under kie-ai-cli", body["uploadPath"])
	}
}

// A URL need not end in anything nameable. kie.ai invents a name when it is not
// given one, which it can only do if the field is left out altogether.
func TestUploadURLLeavesOutAnEmptyFileName(t *testing.T) {
	s := serve(t, http.StatusOK, storedFile)

	if _, err := s.client.UploadURL(t.Context(), "https://example.com/", ""); err != nil {
		t.Fatalf("UploadURL: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(s.sent, &body); err != nil {
		t.Fatalf("the request body is not JSON (%v): %s", err, s.sent)
	}
	if _, ok := body["fileName"]; ok {
		t.Errorf("the request names the file %v; an absent name is what lets kie.ai choose one", body["fileName"])
	}
}

// The upload endpoints answer in the same envelope as everything else, so the
// same rule applies: the status and the code both have to say it worked. What
// is new is the answer itself, which is of no use without a download URL.
func TestUploadRejectsWhatIsNotAStoredFile(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "authentication fails behind a 200",
			body: `{"success":false,"code":401,"msg":"Unauthorized - Authentication failed."}`,
			want: []string{"401", "Authentication failed"},
		},
		{
			name: "the request is refused behind a 200",
			body: `{"success":false,"code":400,"msg":"Missing required parameter: fileUrl"}`,
			want: []string{"400", "fileUrl"},
		},
		{
			name: "there is no download URL to return",
			body: `{"success":true,"code":200,"msg":"success","data":{"fileName":"photo.png"}}`,
			want: []string{"download"},
		},
		{
			name: "the answer is not an upload",
			body: `{"success":true,"code":200,"msg":"success","data":"stored"}`,
			want: []string{"upload"},
		},
	}
	for _, tt := range tests {
		for _, call := range []struct {
			kind string
			run  func(*kie.Client) (kie.Upload, error)
		}{
			{"from a URL", func(c *kie.Client) (kie.Upload, error) {
				return c.UploadURL(t.Context(), "https://example.com/photo.png", "photo.png")
			}},
			{"from a file", func(c *kie.Client) (kie.Upload, error) {
				return c.UploadStream(t.Context(), "photo.png", strings.NewReader("bytes"))
			}},
		} {
			t.Run(tt.name+" "+call.kind, func(t *testing.T) {
				s := serve(t, http.StatusOK, tt.body)

				got, err := call.run(s.client)
				if err == nil {
					t.Fatalf("the upload returned %+v and no error", got)
				}
				if got != (kie.Upload{}) {
					t.Errorf("upload = %+v, want nothing alongside an error", got)
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
}

// form reads back the multipart body of the last request.
func (s *stub) form(t *testing.T) *multipart.Form {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(s.sent))
	req.Header.Set("Content-Type", s.last.Header.Get("Content-Type"))
	if err := req.ParseMultipartForm(1 << 20); err != nil {
		t.Fatalf("the request body is not a multipart form (%v): %s", err, s.sent)
	}
	return req.MultipartForm
}

func readPart(t *testing.T, fh *multipart.FileHeader) string {
	t.Helper()
	f, err := fh.Open()
	if err != nil {
		t.Fatalf("opening the file part: %v", err)
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("reading the file part: %v", err)
	}
	return string(body)
}
