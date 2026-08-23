package cli_test

import (
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/config"
)

// Without a key there is nothing to ask kie.ai with. The command says so before
// it reaches the network, and names both places a key can come from, so the
// reader does not have to find the documentation to get past it.
func TestCreditsShowWithoutAKey(t *testing.T) {
	isolate(t)

	got := run(t, "credits", "show")
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
	// A missing key is a state of the machine, not a mistake in the call, so
	// repeating the usage text would only bury the one line that matters.
	if strings.Contains(got.stderr, "Usage:") {
		t.Errorf("a missing key is not a usage mistake:\n%s", got.stderr)
	}
}
