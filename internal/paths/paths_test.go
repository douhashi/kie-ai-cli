package paths_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/paths"
)

func TestRootUsesXDGDataHome(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", base)

	root, err := paths.Root()
	if err != nil {
		t.Fatalf("Root() error: %v", err)
	}
	if want := filepath.Join(base, "kie-ai-cli"); root != want {
		t.Errorf("Root() = %q, want %q", root, want)
	}
	assertPrivateDir(t, root)
}

func TestRootFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")

	root, err := paths.Root()
	if err != nil {
		t.Fatalf("Root() error: %v", err)
	}
	if want := filepath.Join(home, ".local", "share", "kie-ai-cli"); root != want {
		t.Errorf("Root() = %q, want %q", root, want)
	}
	assertPrivateDir(t, root)
}

// A relative XDG_DATA_HOME is invalid per the XDG Base Directory
// specification: honouring it would scatter data into whatever directory the
// command happened to run in.
func TestRootIgnoresRelativeXDGDataHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "relative/data")

	root, err := paths.Root()
	if err != nil {
		t.Fatalf("Root() error: %v", err)
	}
	if want := filepath.Join(home, ".local", "share", "kie-ai-cli"); root != want {
		t.Errorf("Root() = %q, want %q", root, want)
	}
}

func TestLedgerLivesUnderRoot(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  func(t *testing.T) string // sets the environment, returns the expected root
	}{
		{
			name: "XDG_DATA_HOME set",
			env: func(t *testing.T) string {
				base := t.TempDir()
				t.Setenv("XDG_DATA_HOME", base)
				return filepath.Join(base, "kie-ai-cli")
			},
		},
		{
			name: "XDG_DATA_HOME unset",
			env: func(t *testing.T) string {
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("XDG_DATA_HOME", "")
				return filepath.Join(home, ".local", "share", "kie-ai-cli")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantRoot := tc.env(t)

			got, err := paths.Ledger()
			if err != nil {
				t.Fatalf("Ledger() error: %v", err)
			}
			if want := filepath.Join(wantRoot, "ledger.db"); got != want {
				t.Errorf("Ledger() = %q, want %q", got, want)
			}
			if !filepath.IsAbs(got) {
				t.Errorf("Ledger() = %q, want an absolute path", got)
			}
			assertPrivateDir(t, wantRoot)
		})
	}
}

func assertPrivateDir(t *testing.T, dir string) {
	t.Helper()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %s: %v", dir, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", dir)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("%s mode = %o, want 700", dir, perm)
	}
}
