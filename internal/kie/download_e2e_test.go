//go:build e2e

// The unit tests prove that a download behaves the way this package says it
// does against a server this package wrote. They cannot prove that kie.ai
// serves a stored file to an unauthenticated GET, or that its re-issue
// endpoint answers with a bare string, so both are established here against
// the real API. Run it with `infisical run -- go test -tags e2e ./internal/kie`.
//
// It costs nothing: an upload and a download create no work, so this needs no
// task and spends no credits.
package kie

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// apiKeyEnv is the variable the key is read from. It is written out rather
// than taken from internal/config, which is the package that owns it: this one
// is under it, and a test must not be what reverses that.
const apiKeyEnv = "KIE_AI_API_KEY"

// V1, V2: a file kie.ai holds is served to a plain GET that carries no
// credential, and the endpoint that re-issues links is reached and read.
//
// The stored URL working on its own is what makes the re-issue a fallback
// rather than a step: if it did not, every download would be two requests.
func TestDownloadAgainstTheRealAPI(t *testing.T) {
	key := os.Getenv(apiKeyEnv)
	if key == "" {
		t.Skipf("%s is not set; run this test through `infisical run --`", apiKeyEnv)
	}
	ctx := context.Background()
	c := New(key)

	// A file of this test's own, so that nothing here depends on a task
	// having been run or on a result that may have expired.
	const content = "kie-ai-cli e2e: the bytes a download has to bring back\n"
	up, err := c.UploadStream(ctx, "kie-ai-cli-e2e.txt", strings.NewReader(content))
	if err != nil {
		t.Fatalf("UploadStream: %v", err)
	}
	t.Logf("uploaded to %s (%d bytes, %s)", up.DownloadURL, up.FileSize, up.MimeType)

	var got bytes.Buffer
	mediaType, err := c.Download(ctx, up.DownloadURL, &got)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	t.Logf("plain GET -> %d bytes, media type %q", got.Len(), mediaType)
	if got.String() != content {
		t.Errorf("the file came back as %q, want what was uploaded", got.String())
	}

	// The same file through a client that has no key at all: what makes it
	// safe to withhold the credential is that the host serving the results
	// never wanted one.
	resp, err := http.Get(up.DownloadURL)
	if err != nil {
		t.Fatalf("an unauthenticated GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	received, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		t.Fatalf("reading the unauthenticated answer: %v", err)
	}
	t.Logf("unauthenticated GET -> HTTP %d, Content-Length %d, received %d bytes",
		resp.StatusCode, resp.ContentLength, received)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("an unauthenticated GET answered HTTP %d, want the file", resp.StatusCode)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != received {
		t.Errorf("received %d bytes, want the %d the host declared", received, resp.ContentLength)
	}
	if received != int64(len(content)) {
		t.Errorf("received %d bytes, want the %d that were uploaded", received, len(content))
	}

	// V1: the re-issue endpoint is reached, authenticated, and its answer
	// read. It re-issues links for what a task produced and refuses
	// anything else -- an uploaded file included, which is what this asks
	// it for -- so what is established here is that the call arrives as the
	// endpoint expects it and that a refusal is reported as itself. That it
	// answers a result URL with a bare string is what docs.kie.ai says and
	// what the live call in .tmp/spira-evidence records.
	_, err = c.directURL(ctx, up.DownloadURL)
	if err == nil {
		t.Fatal("the endpoint re-issued a link for an uploaded file; it is documented to take a result URL")
	}
	var refused *APIError
	if !errors.As(err, &refused) {
		t.Fatalf("directURL: %v, want the endpoint's own refusal", err)
	}
	t.Logf("directURL on an uploaded file -> HTTP %d, code %d: %s", refused.Status, refused.Code, refused.Msg)
	if refused.Code != 422 {
		t.Errorf("code = %d, want 422, the code for a URL the endpoint will not take", refused.Code)
	}
	if strings.Contains(err.Error(), key) {
		t.Error("the error carries the API key")
	}
}
