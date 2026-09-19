package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/config"
)

// configPath names a configuration file inside a directory that only this test
// uses. Where the file lives in a real run is internal/paths' business, and is
// tested there.
func configPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "config", "config.json")
}

// V1: the configuration file holds a credential, so it must never be readable
// by anyone but its owner — on creation and when replacing a laxer file.
func TestSaveKeepsTheFilePrivate(t *testing.T) {
	path := configPath(t)

	if err := config.Save(path, config.Settings{APIKey: "first"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	assertPerm(t, filepath.Dir(path), 0o700)
	assertPerm(t, path, 0o600)

	// A file left world-readable by an earlier version or by hand must not
	// keep that mode when it is written again.
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	if err := config.Save(path, config.Settings{APIKey: "second"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	assertPerm(t, path, 0o600)

	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.APIKey != "second" {
		t.Errorf("APIKey = %q, want %q", got.APIKey, "second")
	}
}

// Save must not leave the temporary file it writes through behind.
func TestSaveLeavesNoTemporaryFile(t *testing.T) {
	path := configPath(t)
	if err := config.Save(path, config.Settings{APIKey: "k"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Errorf("directory holds %v, want only %s", names(entries), filepath.Base(path))
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	path := configPath(t)

	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != (config.Settings{}) {
		t.Errorf("Load() = %+v, want zero value", got)
	}
}

func TestLoadRejectsBrokenFile(t *testing.T) {
	path := configPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := config.Load(path); err == nil {
		t.Fatal("Load() = nil error, want a parse error")
	} else if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the file", err)
	}
}

func TestResolveAPIKey(t *testing.T) {
	tests := []struct {
		name       string
		env        string
		file       string
		wantValue  string
		wantSource config.Source
	}{
		// V2: both present — the environment variable decides.
		{name: "env wins over file", env: "from-env", file: "from-file", wantValue: "from-env", wantSource: config.SourceEnv},
		{name: "env only", env: "from-env", wantValue: "from-env", wantSource: config.SourceEnv},
		{name: "file only", file: "from-file", wantValue: "from-file", wantSource: config.SourceFile},
		{name: "neither", wantSource: config.SourceUnset},
		// An exported-but-empty variable is how a shell says "not set".
		{name: "empty env falls through", env: "", file: "from-file", wantValue: "from-file", wantSource: config.SourceFile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := configPath(t)
			t.Setenv(config.APIKeyEnv, tt.env)
			if tt.file != "" {
				if err := config.Save(path, config.Settings{APIKey: tt.file}); err != nil {
					t.Fatalf("Save: %v", err)
				}
			}

			got, err := config.ResolveAPIKey(path)
			if err != nil {
				t.Fatalf("ResolveAPIKey: %v", err)
			}
			if got.Value != tt.wantValue || got.Source != tt.wantSource {
				t.Errorf("ResolveAPIKey() = %+v, want value %q source %q", got, tt.wantValue, tt.wantSource)
			}
			if got.IsSet() != (tt.wantSource != config.SourceUnset) {
				t.Errorf("IsSet() = %v for source %q", got.IsSet(), got.Source)
			}
		})
	}
}

func TestMasked(t *testing.T) {
	tests := []struct {
		name string
		key  config.APIKey
		want string
	}{
		{name: "unset shows nothing", key: config.APIKey{Source: config.SourceUnset}, want: ""},
		{name: "keeps the last four", key: config.APIKey{Value: "abcdefgh1234", Source: config.SourceEnv}, want: "****1234"},
		// The mask is the same width whatever the key is, so it does not
		// disclose the length of the key.
		{name: "same width for a longer key", key: config.APIKey{Value: strings.Repeat("x", 200) + "1234", Source: config.SourceEnv}, want: "****1234"},
		{name: "short key is hidden entirely", key: config.APIKey{Value: "abc1234", Source: config.SourceFile}, want: "****"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.key.Masked(); got != tt.want {
				t.Errorf("Masked() = %q, want %q", got, tt.want)
			}
		})
	}
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s has mode %04o, want %04o", path, got, want)
	}
}

func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// V1: a rate stored next to a key leaves the key as it was, and the rate comes
// back as the one that was stored.
func TestSaveKeepsTheRateNextToTheKey(t *testing.T) {
	path := configPath(t)
	rate := 0.004
	if err := config.Save(path, config.Settings{APIKey: "k", USDPerCredit: &rate}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.APIKey != "k" || got.USDPerCredit == nil || *got.USDPerCredit != rate {
		t.Errorf("Load() = %+v, want key %q and rate %v", got, "k", rate)
	}
}

func TestResolveUSDPerCredit(t *testing.T) {
	t.Run("default when the file says nothing", func(t *testing.T) {
		path := configPath(t)
		if err := config.Save(path, config.Settings{APIKey: "k"}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		got, err := config.ResolveUSDPerCredit(path)
		if err != nil {
			t.Fatalf("ResolveUSDPerCredit: %v", err)
		}
		want := config.USDPerCredit{Value: config.DefaultUSDPerCredit, Source: config.SourceDefault}
		if got != want {
			t.Errorf("ResolveUSDPerCredit() = %+v, want %+v", got, want)
		}
		if config.DefaultUSDPerCredit != 0.005 {
			t.Errorf("DefaultUSDPerCredit = %v, want 0.005", config.DefaultUSDPerCredit)
		}
	})
	t.Run("default when there is no file", func(t *testing.T) {
		got, err := config.ResolveUSDPerCredit(configPath(t))
		if err != nil {
			t.Fatalf("ResolveUSDPerCredit: %v", err)
		}
		if got.Source != config.SourceDefault {
			t.Errorf("source = %q, want %q", got.Source, config.SourceDefault)
		}
	})
	t.Run("the file wins over the default", func(t *testing.T) {
		path := configPath(t)
		rate := 0.01
		if err := config.Save(path, config.Settings{USDPerCredit: &rate}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		got, err := config.ResolveUSDPerCredit(path)
		if err != nil {
			t.Fatalf("ResolveUSDPerCredit: %v", err)
		}
		want := config.USDPerCredit{Value: rate, Source: config.SourceFile}
		if got != want {
			t.Errorf("ResolveUSDPerCredit() = %+v, want %+v", got, want)
		}
	})
	// A rate written into the file by hand is held to the same rule as one
	// given to config set, so that no listing is ever priced at 0 or less.
	for _, stored := range []string{"0", "-0.005"} {
		t.Run("rejects a stored rate of "+stored, func(t *testing.T) {
			path := configPath(t)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(path, []byte(`{"usd_per_credit":`+stored+`}`), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if _, err := config.ResolveUSDPerCredit(path); err == nil {
				t.Fatal("ResolveUSDPerCredit() = nil error, want the stored rate refused")
			} else if !strings.Contains(err.Error(), path) {
				t.Errorf("error %q does not name the file", err)
			}
		})
	}
}

func TestParseUSDPerCredit(t *testing.T) {
	for _, in := range []string{"0.005", "1", "1e-3"} {
		if _, err := config.ParseUSDPerCredit(in); err != nil {
			t.Errorf("ParseUSDPerCredit(%q): %v, want it accepted", in, err)
		}
	}
	for _, in := range []string{"0", "-0.005", "-0", "NaN", "Inf", "+Inf", "-Inf", "abc", "0.005usd", ""} {
		if v, err := config.ParseUSDPerCredit(in); err == nil {
			t.Errorf("ParseUSDPerCredit(%q) = %v, want it refused", in, v)
		}
	}
}
