//go:build e2e

package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/config"
	"github.com/douhashi/kie-ai-cli/internal/kie"
)

// V4, V5: one real task, followed to the end and then saved.
//
// The whole point of the command is a file that came from kie.ai and is now on
// this machine, and nothing short of a real result establishes that: a stub
// serves what the test told it to. It spends real credits on a task kie.ai has
// no way to cancel, so it is one task, given the one required field, and
// everything the command has to answer for is asserted against it.
func TestTaskDownloadSavesARealResult(t *testing.T) {
	key := realKey(t)
	t.Logf("credits before: %s", balance(t, key))

	layout := isolate(t)
	dir := inTempDir(t)
	t.Setenv(config.APIKeyEnv, key)

	taskID := submit(t, marketModel, "--prompt", imagePrompt)
	listed := follow(t, key)
	if state := listed[taskID]; state.Status != kie.StatusSucceeded {
		t.Fatalf("%s is %q (%s), want %q", taskID, state.Status, state.Error, kie.StatusSucceeded)
	}
	urls := recorded(t, layout, taskID).ResultURLs
	if len(urls) == 0 {
		t.Fatalf("%s succeeded with no result URL recorded", taskID)
	}
	t.Logf("result urls: %v", urls)

	// V5: it is there to be collected, and the listing says so without
	// anything being asked of kie.ai.
	if !slices.Contains(unsavedIDs(t), taskID) {
		t.Errorf("%s is not in `task list --unsaved` before it has been saved", taskID)
	}

	got := run(t, "task", "download", taskID)
	if got.code != 0 {
		t.Fatalf("task download: code %d, stderr %q", got.code, got.stderr)
	}
	assertKeyNotIn(t, key, got.stdout+got.stderr)
	saved := recorded(t, layout, taskID).SavedPaths
	if len(saved) != len(urls) {
		t.Fatalf("saved %v, want one file per result URL %v", saved, urls)
	}
	t.Logf("kie task download %s -> %v", taskID, saved)

	for _, path := range saved {
		if !filepath.IsAbs(path) {
			t.Errorf("the ledger recorded %q, want an absolute path", path)
		}
		if filepath.Dir(path) != dir {
			t.Errorf("%s was saved outside the current directory %s", path, dir)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("the recorded path is not on disk: %v", err)
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", path)
		}
		t.Logf("saved %s (%d bytes)", path, info.Size())
	}
	// The paths the command printed are the paths it recorded.
	if lines := strings.Fields(got.stdout); !slices.Equal(lines, saved) {
		t.Errorf("stdout = %q, want the saved paths", got.stdout)
	}
	// V5: and it is not left to do any more.
	if slices.Contains(unsavedIDs(t), taskID) {
		t.Errorf("%s is still in `task list --unsaved` after it was saved", taskID)
	}

	// V4: asked again, the command fetches nothing. The file is the one
	// that was paid for and may have been edited since.
	before := modTimes(t, saved)
	again := run(t, "task", "download", taskID)
	if again.code != 0 {
		t.Fatalf("the second download: code %d, stderr %q", again.code, again.stderr)
	}
	if again.stdout != "" {
		t.Errorf("the second download saved %q, want nothing", again.stdout)
	}
	if !strings.Contains(again.stderr, taskID) {
		t.Errorf("the second download does not say the task is already saved:\n%s", again.stderr)
	}
	if now := modTimes(t, saved); !slices.Equal(now, before) {
		t.Errorf("the files were rewritten: %v, were %v", now, before)
	}
	t.Logf("kie task download %s a second time -> stderr %q", taskID, strings.TrimRight(again.stderr, "\n"))

	t.Logf("credits after: %s", balance(t, key))
}

// unsavedIDs is what `task list --unsaved` says is left to collect.
func unsavedIDs(t *testing.T) []string {
	t.Helper()
	got := run(t, "task", "list", "--unsaved", "--json")
	if got.code != 0 {
		t.Fatalf("task list --unsaved: code %d, stderr %q", got.code, got.stderr)
	}
	var listed []listedTask
	if err := json.Unmarshal([]byte(got.stdout), &listed); err != nil {
		t.Fatalf("task list --unsaved: stdout is not JSON (%v):\n%s", err, got.stdout)
	}
	ids := make([]string, 0, len(listed))
	for _, l := range listed {
		ids = append(ids, l.TaskID)
	}
	return ids
}

func modTimes(t *testing.T, paths []string) []string {
	t.Helper()
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		out = append(out, info.ModTime().String())
	}
	return out
}
