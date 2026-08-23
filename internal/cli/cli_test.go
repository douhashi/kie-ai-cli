package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/cli"
	"github.com/douhashi/kie-ai-cli/internal/config"
	"github.com/douhashi/kie-ai-cli/internal/paths"
)

// secret is distinctive enough that any run of it leaking into the output can
// be told apart from the paths that legitimately appear there.
const secret = "kie-live-QRSTUVWX90ab7f31"

type result struct {
	code   int
	stdout string
	stderr string
}

// run invokes the CLI against a state directory that only this test uses.
func run(t *testing.T, args ...string) result {
	t.Helper()
	return runStdin(t, "", args...)
}

// runStdin is run for the one command that reads standard input.
func runStdin(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, strings.NewReader(stdin), &stdout, &stderr)
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// isolate points the state directory at a fresh temporary directory and clears
// the API key variable, so the tests neither read nor write the real ones.
func isolate(t *testing.T) paths.Layout {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv(config.APIKeyEnv, "")
	layout, err := paths.Resolve()
	if err != nil {
		t.Fatalf("paths.Resolve: %v", err)
	}
	return layout
}

func TestConfigSetThenShow(t *testing.T) {
	layout := isolate(t)

	if got := run(t, "config", "set", "api_key", secret); got.code != 0 {
		t.Fatalf("config set: code %d, stderr %q", got.code, got.stderr)
	}
	stored, err := config.Load(layout.Config)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.APIKey != secret {
		t.Errorf("stored key = %q, want the one that was set", stored.APIKey)
	}

	got := run(t, "config", "show")
	if got.code != 0 {
		t.Fatalf("config show: code %d, stderr %q", got.code, got.stderr)
	}
	assertNoLeak(t, got.stdout)
	for _, want := range []string{"set", "file", "****7f31", layout.Root, layout.Config, layout.Catalog, layout.Ledger} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("config show output lacks %q:\n%s", want, got.stdout)
		}
	}
}

func TestConfigShowReportsTheKeyInEffect(t *testing.T) {
	isolate(t)
	if got := run(t, "config", "set", "api_key", "file-key-0000"); got.code != 0 {
		t.Fatalf("config set: code %d, stderr %q", got.code, got.stderr)
	}
	// The environment variable wins, so what is shown must follow it rather
	// than the file that was just written.
	t.Setenv(config.APIKeyEnv, secret)

	got := run(t, "config", "show")
	if got.code != 0 {
		t.Fatalf("config show: code %d, stderr %q", got.code, got.stderr)
	}
	assertNoLeak(t, got.stdout)
	if !strings.Contains(got.stdout, "env") || !strings.Contains(got.stdout, "****7f31") {
		t.Errorf("config show does not report the key from the environment:\n%s", got.stdout)
	}
}

func TestConfigShowWithoutAKey(t *testing.T) {
	layout := isolate(t)

	got := run(t, "config", "show")
	if got.code != 0 {
		t.Fatalf("config show: code %d, stderr %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "unset") {
		t.Errorf("config show does not report the key as unset:\n%s", got.stdout)
	}
	if !strings.Contains(got.stdout, layout.Config) {
		t.Errorf("config show does not name the configuration file:\n%s", got.stdout)
	}
}

// state mirrors the JSON contract. The masked key deliberately does not live in
// a field named api_key, so that a consumer cannot mistake it for a credential.
type state struct {
	APIKeyState  string `json:"api_key_state"`
	APIKeySource string `json:"api_key_source"`
	APIKeyMasked string `json:"api_key_masked"`
	Root         string `json:"root"`
	ConfigFile   string `json:"config_file"`
	CatalogDir   string `json:"catalog_dir"`
	LedgerFile   string `json:"ledger_file"`
}

func TestConfigSetJSONReturnsTheNewState(t *testing.T) {
	layout := isolate(t)

	got := run(t, "config", "set", "api_key", secret, "--json")
	if got.code != 0 {
		t.Fatalf("config set --json: code %d, stderr %q", got.code, got.stderr)
	}
	assertNoLeak(t, got.stdout)

	var s state
	if err := json.Unmarshal([]byte(got.stdout), &s); err != nil {
		t.Fatalf("output is not JSON (%v):\n%s", err, got.stdout)
	}
	want := state{
		APIKeyState:  "set",
		APIKeySource: "file",
		APIKeyMasked: "****7f31",
		Root:         layout.Root,
		ConfigFile:   layout.Config,
		CatalogDir:   layout.Catalog,
		LedgerFile:   layout.Ledger,
	}
	if s != want {
		t.Errorf("state = %+v, want %+v", s, want)
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(got.stdout), &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := raw["api_key"]; ok {
		t.Error(`JSON has a field named "api_key"; a consumer would read it as a usable credential`)
	}
}

func TestConfigShowJSONWithoutAKey(t *testing.T) {
	isolate(t)

	got := run(t, "config", "show", "--json")
	if got.code != 0 {
		t.Fatalf("config show --json: code %d, stderr %q", got.code, got.stderr)
	}
	var s state
	if err := json.Unmarshal([]byte(got.stdout), &s); err != nil {
		t.Fatalf("output is not JSON (%v):\n%s", err, got.stdout)
	}
	if s.APIKeyState != "unset" || s.APIKeySource != "" || s.APIKeyMasked != "" {
		t.Errorf("state = %+v, want an unset key with no source and no mask", s)
	}
}

// A key that begins with a dash is still a value, not a flag.
func TestConfigSetAcceptsAValueAfterTheTerminator(t *testing.T) {
	layout := isolate(t)

	if got := run(t, "config", "set", "api_key", "--", "-dash-key-1234"); got.code != 0 {
		t.Fatalf("config set: code %d, stderr %q", got.code, got.stderr)
	}
	stored, err := config.Load(layout.Config)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.APIKey != "-dash-key-1234" {
		t.Errorf("stored key = %q, want %q", stored.APIKey, "-dash-key-1234")
	}
}

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "unknown noun", args: []string{"nope", "show"}},
		{name: "unknown verb", args: []string{"config", "nope"}},
		{name: "noun without a verb", args: []string{"config"}},
		{name: "unknown key", args: []string{"config", "set", "nope", "value"}},
		{name: "missing value", args: []string{"config", "set", "api_key"}},
		{name: "empty value", args: []string{"config", "set", "api_key", ""}},
		{name: "too many arguments", args: []string{"config", "show", "extra"}},
		{name: "credits with an argument", args: []string{"credits", "show", "extra"}},
		{name: "catalog show with an argument", args: []string{"catalog", "show", "extra"}},
		{name: "catalog update with an argument", args: []string{"catalog", "update", "extra"}},
		{name: "model list with an argument", args: []string{"model", "list", "extra"}},
		{name: "model show without a model", args: []string{"model", "show"}},
		{name: "model show with two models", args: []string{"model", "show", "a/one", "b/two"}},
		{name: "file upload without an argument", args: []string{"file", "upload"}},
		{name: "file upload with two arguments", args: []string{"file", "upload", "a.png", "b.png"}},
		{name: "task run without a model", args: []string{"task", "run"}},
		{name: "unknown flag", args: []string{"config", "show", "--nope"}},
		{name: "version with an argument", args: []string{"--version", "extra"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layout := isolate(t)

			got := run(t, tt.args...)
			if got.code != 2 {
				t.Errorf("code = %d, want 2 (stderr %q)", got.code, got.stderr)
			}
			if got.stdout != "" {
				t.Errorf("stdout = %q, want nothing on the success stream", got.stdout)
			}
			if !strings.Contains(got.stderr, "Usage:") {
				t.Errorf("stderr does not show the usage text:\n%s", got.stderr)
			}
			if _, err := config.Load(layout.Config); err != nil {
				t.Errorf("Load after a rejected call: %v", err)
			}
		})
	}
}

func TestUsageListsEveryCommand(t *testing.T) {
	isolate(t)

	got := run(t)
	if got.code != 0 {
		t.Fatalf("no arguments: code %d, stderr %q", got.code, got.stderr)
	}
	want := []string{
		"catalog update", "catalog show",
		"config set <key> <value>", "config show", "credits show",
		"file upload <path|url>",
		"task run <model-id> [--<field> <value>...] [--input <file|->]",
		"model list [--category <name>] [--vendor <name>]", "model show <model-id>",
		"--json", "--version",
	}
	for _, want := range want {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("usage lacks %q:\n%s", want, got.stdout)
		}
	}
	if help := run(t, "--help"); help.stdout != got.stdout {
		t.Errorf("--help prints something other than the usage text:\n%s", help.stdout)
	}
}

func TestVersion(t *testing.T) {
	isolate(t)

	got := run(t, "--version")
	if got.code != 0 {
		t.Fatalf("--version: code %d, stderr %q", got.code, got.stderr)
	}
	if strings.TrimSpace(got.stdout) == "" {
		t.Error("--version printed nothing")
	}
}

// A failure that is not the caller's mistake is reported on stderr with the
// error exit code, and without the usage text.
func TestRuntimeErrorIsNotAUsageError(t *testing.T) {
	layout := isolate(t)
	if err := writeBroken(layout.Config); err != nil {
		t.Fatalf("writeBroken: %v", err)
	}

	got := run(t, "config", "show")
	if got.code != 1 {
		t.Errorf("code = %d, want 1 (stderr %q)", got.code, got.stderr)
	}
	if strings.Contains(got.stderr, "Usage:") {
		t.Errorf("a broken configuration file is not a usage mistake:\n%s", got.stderr)
	}
}

// V4: nothing but the last four characters of the key may reach the output.
// Any longer run of the key appearing anywhere is a leak.
func assertNoLeak(t *testing.T, out string) {
	t.Helper()
	assertKeyNotIn(t, secret, out)
}

// assertKeyNotIn is assertNoLeak for a key the test did not choose, which is
// the shape the e2e tests need: they run with the real key.
func assertKeyNotIn(t *testing.T, key, out string) {
	t.Helper()
	const allowed = 4
	for i := 0; i+allowed < len(key); i++ {
		if run := key[i : i+allowed+1]; strings.Contains(out, run) {
			t.Errorf("output contains a run of the key longer than the %d characters the mask keeps:\n%s", allowed, out)
		}
	}
}

func writeBroken(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("{"), 0o600)
}
