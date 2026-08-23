//go:build e2e

package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/config"
)

// kie.ai has no endpoint that deletes an upload, so everything these tests send
// stays on the account until it expires by itself. One run therefore sends two
// files and nothing more, and each of them is well under a kilobyte.
//
// V1, V2, V3: a file goes up, the URL that comes back is fetchable and holds
// the same bytes, and that URL can be uploaded again through the other
// endpoint. The two uploads are the whole budget for one run, which is why they
// are one test rather than three.
func TestFileUploadStoresAFileAndThenAURL(t *testing.T) {
	key := realKey(t)
	isolate(t)
	t.Setenv(config.APIKeyEnv, key)

	// Unique per run, so that what is fetched back can only be this file.
	content := []byte("kie-ai-cli e2e " + time.Now().UTC().Format(time.RFC3339Nano) + "\n")
	local := filepath.Join(t.TempDir(), "e2e-upload.txt")
	if err := os.WriteFile(local, content, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Upload one of two.
	got := run(t, "file", "upload", local)
	if got.code != 0 {
		t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
	}
	if got.stderr != "" {
		t.Errorf("stderr = %q, want nothing on a success", got.stderr)
	}
	assertKeyNotIn(t, key, got.stdout+got.stderr)

	// The acceptance criterion is that `$(kie file upload ...)` can be
	// handed straight to the next command, so stdout has to be the URL and
	// nothing else.
	url := strings.TrimSuffix(got.stdout, "\n")
	if strings.ContainsAny(url, " \t\n") || !strings.HasPrefix(url, "https://") {
		t.Fatalf("stdout = %q, want one https URL and nothing else", got.stdout)
	}
	t.Logf("kie file upload <local file> -> %s", url)

	fetched := fetch(t, url)
	if !bytes.Equal(fetched, content) {
		t.Errorf("the stored file is %q, want the %d bytes that were sent", fetched, len(content))
	}

	// Upload two of two: the other endpoint, and the JSON contract with it.
	second := run(t, "file", "upload", url, "--json")
	if second.code != 0 {
		t.Fatalf("code = %d, stderr %q", second.code, second.stderr)
	}
	assertKeyNotIn(t, key, second.stdout+second.stderr)

	var stored struct {
		FileName    string `json:"fileName"`
		FilePath    string `json:"filePath"`
		DownloadURL string `json:"downloadUrl"`
		FileSize    int64  `json:"fileSize"`
		MimeType    string `json:"mimeType"`
		UploadedAt  string `json:"uploadedAt"`
	}
	if err := json.Unmarshal([]byte(second.stdout), &stored); err != nil {
		t.Fatalf("stdout is not JSON (%v):\n%s", err, second.stdout)
	}
	if stored.DownloadURL == "" || stored.DownloadURL == url {
		t.Errorf("downloadUrl = %q, want a URL of its own", stored.DownloadURL)
	}
	if stored.FilePath == "" || stored.FileName == "" || stored.UploadedAt == "" {
		t.Errorf("the record is missing fields that the API documents: %+v", stored)
	}
	// Every upload is put in a directory of its own, which is what keeps a
	// second file of the same name from replacing the first. kie.ai returns
	// the path with the account's own prefix in front of the uploadPath
	// that was asked for, so this is a containment and not a prefix.
	if !strings.Contains(stored.FilePath, "/kie-ai-cli/") {
		t.Errorf("filePath = %q, want it under this tool's own directory", stored.FilePath)
	}
	t.Logf("kie file upload <url> --json -> %s", strings.Join(strings.Fields(second.stdout), " "))
}

// V4: the two ways this can fail have to be told apart from a success, which
// for kie.ai means not believing the status line -- a rejected key is HTTP 200
// with code 401 in the body. Neither case sends a file, so neither spends any
// of the budget above.
func TestFileUploadReportsWhyItCouldNot(t *testing.T) {
	key := realKey(t)
	local := filepath.Join(t.TempDir(), "e2e-rejected.txt")
	if err := os.WriteFile(local, []byte("nope\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tests := []struct {
		name   string
		key    string
		source string
		want   string
	}{
		{name: "the key is refused", key: rejectedKey, source: local, want: "401"},
		{name: "there is no such file", key: key, source: filepath.Join(t.TempDir(), "absent.txt"), want: "absent.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolate(t)
			t.Setenv(config.APIKeyEnv, tt.key)

			got := run(t, "file", "upload", tt.source)
			// One is the exit code every runtime failure ends with;
			// two is reserved for a mistake in how the command was
			// called, which neither of these is.
			if got.code != 1 {
				t.Errorf("code = %d, want 1", got.code)
			}
			if got.stdout != "" {
				t.Errorf("stdout = %q, want nothing on the success stream", got.stdout)
			}
			if !strings.Contains(got.stderr, tt.want) {
				t.Errorf("stderr does not say %q:\n%s", tt.want, got.stderr)
			}
			assertKeyNotIn(t, key, got.stdout+got.stderr)
			t.Logf("%s -> code %d, stderr %q", tt.name, got.code, strings.TrimRight(got.stderr, "\n"))
		})
	}
}

// fetch reads back what kie.ai stored. No credential is sent: a download URL
// that needs one could not be handed to a model as an input.
func fetch(t *testing.T, url string) []byte {
	t.Helper()
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("GET %s: reading the body: %v", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: HTTP %d: %s", url, resp.StatusCode, body)
	}
	return body
}
