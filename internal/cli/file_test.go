package cli_test

import (
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/config"
)

// An upload is refused before the file is opened when there is no key to send
// it with, for the same reason credits show is: the missing key is the problem,
// and reporting anything else first would hide it.
func TestFileUploadWithoutAKey(t *testing.T) {
	isolate(t)

	got := run(t, "file", "upload", "photo.png")
	if got.code != 1 {
		t.Fatalf("code = %d, want 1 (stderr %q)", got.code, got.stderr)
	}
	if got.stdout != "" {
		t.Errorf("stdout = %q, want nothing on the success stream", got.stdout)
	}
	for _, want := range []string{config.APIKeyEnv, "config set api_key"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("stderr does not mention %q:\n%s", want, got.stderr)
		}
	}
	if strings.Contains(got.stderr, "Usage:") {
		t.Errorf("a missing key is not a usage mistake:\n%s", got.stderr)
	}
}

// A path that names nothing is the caller's mistake about the machine, not
// about the command, so it is reported plainly and without the usage text.
func TestFileUploadOfAPathThatIsNotThere(t *testing.T) {
	isolate(t)
	t.Setenv(config.APIKeyEnv, secret)

	got := run(t, "file", "upload", "/nonexistent/kie-ai-cli/photo.png")
	if got.code != 1 {
		t.Fatalf("code = %d, want 1 (stderr %q)", got.code, got.stderr)
	}
	if got.stdout != "" {
		t.Errorf("stdout = %q, want nothing on the success stream", got.stdout)
	}
	if !strings.Contains(got.stderr, "photo.png") {
		t.Errorf("stderr does not name the file that was not found:\n%s", got.stderr)
	}
	if strings.Contains(got.stderr, "Usage:") {
		t.Errorf("a missing file is not a usage mistake:\n%s", got.stderr)
	}
	assertNoLeak(t, got.stdout+got.stderr)
}
