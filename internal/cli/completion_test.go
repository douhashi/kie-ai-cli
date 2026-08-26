package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
)

// bakedID is a model the binary is built with, so completing it needs no
// downloaded catalog.
const bakedID = "bytedance/seedream-v4-text-to-image"

// AC1: the ids the shell completes a model with come from the catalog in
// effect, so every model that can be run can be completed.
func TestCompletionListModel(t *testing.T) {
	isolate(t)

	got := run(t, "completion", "list", "model")
	if got.code != 0 {
		t.Fatalf("completion list model: code %d, stderr %q", got.code, got.stderr)
	}
	ids := lines(got.stdout)
	if !slices.Contains(ids, bakedID) {
		t.Errorf("the listing lacks %s (%d values)", bakedID, len(ids))
	}
	models := run(t, "model", "list")
	if want := len(lines(models.stdout)); len(ids) != want {
		t.Errorf("completion list model has %d values, model list %d lines", len(ids), want)
	}
}

// The two axes model list narrows by. Each value appears once, so the shell
// does not offer the same word 54 times.
func TestCompletionListCategoryAndVendor(t *testing.T) {
	isolate(t)

	for axis, want := range map[string][]string{
		"category": {"image", "music", "video"},
		"vendor":   nil,
	} {
		t.Run(axis, func(t *testing.T) {
			got := run(t, "completion", "list", axis)
			if got.code != 0 {
				t.Fatalf("completion list %s: code %d, stderr %q", axis, got.code, got.stderr)
			}
			values := lines(got.stdout)
			if want != nil && !slices.Equal(values, want) {
				t.Errorf("values = %v, want %v", values, want)
			}
			if !slices.IsSorted(values) {
				t.Errorf("values are not sorted: %v", values)
			}
			for i := 1; i < len(values); i++ {
				if values[i] == values[i-1] {
					t.Errorf("%q is listed twice", values[i])
				}
			}
			// Every value has to be one model list accepts, or completing
			// it would produce a call that is then refused.
			for _, v := range values {
				if listed := run(t, "model", "list", "--"+axis, v); listed.code != 0 {
					t.Errorf("model list --%s %s: code %d, stderr %q", axis, v, listed.code, listed.stderr)
				}
			}
		})
	}
}

// AC3: what the shell completes follows the catalog in effect, so a model
// added since this binary was built is completed once it is downloaded, and is
// gone again once the download is deleted.
func TestCompletionListFollowsTheDownloadedCatalog(t *testing.T) {
	layout := isolate(t)
	download(t, layout, "2026-08-24")

	got := run(t, "completion", "list", "model")
	if got.code != 0 {
		t.Fatalf("completion list model: code %d, stderr %q", got.code, got.stderr)
	}
	if ids := lines(got.stdout); !slices.Equal(ids, []string{newModelID}) {
		t.Errorf("ids = %v, want only the downloaded %s", ids, newModelID)
	}

	if err := os.RemoveAll(layout.Catalog); err != nil {
		t.Fatalf("remove the downloaded catalog: %v", err)
	}
	back := run(t, "completion", "list", "model")
	if back.code != 0 {
		t.Fatalf("completion list model: code %d, stderr %q", back.code, back.stderr)
	}
	if ids := lines(back.stdout); !slices.Contains(ids, bakedID) {
		t.Errorf("deleting the download did not bring back the models this binary carries")
	}
}

// A downloaded catalog with no index beside it is refused rather than answered
// from the embedded index, which would complete models the CLI cannot run.
func TestCompletionListRejectsADownloadedCatalogWithNoIndex(t *testing.T) {
	layout := isolate(t)
	download(t, layout, "2026-08-24")
	if err := os.Remove(filepath.Join(layout.Catalog, catalog.IndexFile)); err != nil {
		t.Fatalf("remove the index: %v", err)
	}

	got := run(t, "completion", "list", "model")
	if got.code != 1 {
		t.Errorf("code = %d, want 1 (stdout %q, stderr %q)", got.code, got.stdout, got.stderr)
	}
	if got.stdout != "" {
		t.Errorf("stdout = %q, want nothing to complete from", got.stdout)
	}
}

// AC1: the script the shell loads offers every command this binary has, and
// asks this binary for the values that change with the catalog.
func TestCompletionShowCoversEveryCommand(t *testing.T) {
	isolate(t)

	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			got := run(t, "completion", "show", shell)
			if got.code != 0 {
				t.Fatalf("completion show %s: code %d, stderr %q", shell, got.code, got.stderr)
			}
			want := []string{
				// Every noun and verb, including the one being read.
				"catalog", "update", "config", "credits", "file", "upload",
				"model", "list", "show", "task", "run", "completion",
				"skill", "install",
				// The flags, which come from the commands themselves.
				"--json", "--category", "--vendor", "--input", "--scope",
				// The axes, asked for rather than baked in.
				"completion list model", "completion list category", "completion list vendor",
				// Both names the tool is installed under.
				"kie", "kie-ai-cli",
			}
			for _, w := range want {
				if !strings.Contains(got.stdout, w) {
					t.Errorf("the %s script lacks %q:\n%s", shell, w, got.stdout)
				}
			}
			// Baking the ids in would freeze them into a file that no
			// `catalog update` rewrites.
			if strings.Contains(got.stdout, bakedID) {
				t.Errorf("the %s script has model ids baked into it", shell)
			}
		})
	}
}

// A script the shell cannot parse would break every completion in the session
// it is loaded into, so what is generated is handed to the shell it is for.
func TestCompletionShowIsValidShellSyntax(t *testing.T) {
	isolate(t)

	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			path, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s is not installed here", shell)
			}
			got := run(t, "completion", "show", shell)
			if got.code != 0 {
				t.Fatalf("completion show %s: code %d, stderr %q", shell, got.code, got.stderr)
			}
			script := filepath.Join(t.TempDir(), "completion."+shell)
			if err := os.WriteFile(script, []byte(got.stdout), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			// -n parses without running: what is checked is the script, not
			// what a shell would do with the completion afterwards.
			out, err := exec.Command(path, "-n", script).CombinedOutput()
			if err != nil {
				t.Errorf("%s cannot parse the script it was given (%v):\n%s", shell, err, out)
			}
		})
	}
}

func TestCompletionUsageErrors(t *testing.T) {
	tests := map[string][]string{
		"a shell nobody asked for":  {"completion", "show", "fish"},
		"no shell":                  {"completion", "show"},
		"two shells":                {"completion", "show", "bash", "zsh"},
		"an axis that is not one":   {"completion", "list", "colour"},
		"no axis":                   {"completion", "list"},
		"a script as a document":    {"completion", "show", "bash", "--json"},
		"a listing as a document":   {"completion", "list", "model", "--json"},
		"a verb completion has not": {"completion", "print", "bash"},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			isolate(t)

			got := run(t, args...)
			if got.code != 2 {
				t.Errorf("code = %d, want 2 (stderr %q)", got.code, got.stderr)
			}
			if got.stdout != "" {
				t.Errorf("stdout = %q, want nothing on the success stream", got.stdout)
			}
		})
	}
}
