//go:build e2e

// The unit tests prove that the CLI does what this package says it does. They
// cannot prove that what it says matches kie.ai, so these tests run the command
// against the real API. Run them with
// `infisical run -- go test -tags e2e ./internal/cli`.
package cli_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/config"
)

// rejectedKey is not a credential. kie.ai answers it with an authentication
// failure, which is the whole point of it.
const rejectedKey = "kie-ai-cli-e2e-rejected-key"

// V1: the balance can be read from the real account, on both output paths.
func TestCreditsShowReadsTheRealBalance(t *testing.T) {
	key := realKey(t)

	t.Run("as one row", func(t *testing.T) {
		isolate(t)
		t.Setenv(config.APIKeyEnv, key)

		got := run(t, "credits", "show")
		if got.code != 0 {
			t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
		}
		if got.stderr != "" {
			t.Errorf("stderr = %q, want nothing on a success", got.stderr)
		}
		name, value, ok := strings.Cut(strings.TrimRight(got.stdout, "\n"), "\t")
		if !ok || name != "credits" {
			t.Fatalf("stdout = %q, want a tab-separated credits row", got.stdout)
		}
		assertBalance(t, json.Number(value))
		assertKeyNotIn(t, key, got.stdout+got.stderr)
		t.Logf("kie credits show -> %q", strings.TrimRight(got.stdout, "\n"))
	})

	t.Run("as JSON", func(t *testing.T) {
		isolate(t)
		t.Setenv(config.APIKeyEnv, key)

		got := run(t, "credits", "show", "--json")
		if got.code != 0 {
			t.Fatalf("code = %d, stderr %q", got.code, got.stderr)
		}
		var result struct {
			Credits json.Number `json:"credits"`
		}
		if err := json.Unmarshal([]byte(got.stdout), &result); err != nil {
			t.Fatalf("stdout is not JSON (%v):\n%s", err, got.stdout)
		}
		assertBalance(t, result.Credits)
		assertKeyNotIn(t, key, got.stdout+got.stderr)
		t.Logf("kie credits show --json -> %s", strings.Join(strings.Fields(got.stdout), " "))
	})
}

// V2: kie.ai reports an authentication failure as HTTP 200 with code 401 in the
// body, so this is the case a client that reads the status line alone would
// report as a success with an empty balance. The command has to fail, name the
// reason, and leave the success stream empty.
func TestCreditsShowRejectsAnInvalidKey(t *testing.T) {
	realKey(t)
	isolate(t)
	t.Setenv(config.APIKeyEnv, rejectedKey)

	got := run(t, "credits", "show")
	// One is the exit code every runtime failure ends with; two is reserved
	// for a mistake in how the command was called, which this is not.
	if got.code != 1 {
		t.Errorf("code = %d, want 1", got.code)
	}
	if got.stdout != "" {
		t.Errorf("stdout = %q, want nothing on the success stream", got.stdout)
	}
	if !strings.Contains(got.stderr, "401") {
		t.Errorf("stderr does not carry the code kie.ai answered with:\n%s", got.stderr)
	}
	t.Logf("kie credits show with a rejected key -> code %d, stderr %q", got.code, strings.TrimRight(got.stderr, "\n"))
}

// realKey reports the key the run was given, or skips: these tests are about
// the real API and prove nothing without one.
func realKey(t *testing.T) string {
	t.Helper()
	key := os.Getenv(config.APIKeyEnv)
	if key == "" {
		t.Skipf("%s is not set; run this test through `infisical run --`", config.APIKeyEnv)
	}
	return key
}

// assertBalance checks that what was printed is a number. Its value belongs to
// the account and is not asserted on; that it parses is what the format
// promises.
func assertBalance(t *testing.T, balance json.Number) {
	t.Helper()
	if _, err := balance.Float64(); err != nil {
		t.Errorf("balance %q is not a number: %v", balance, err)
	}
}
