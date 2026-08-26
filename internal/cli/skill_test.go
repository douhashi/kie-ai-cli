package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// where the skill lands under a .claude directory.
var skillPath = filepath.Join(".claude", "skills", "kie-ai-cli", "SKILL.md")

// AC1: the default scope writes into the directory the command was run from,
// and says where in absolute terms -- the answer has to name a directory, and
// a relative path would not say which one it was relative to.
func TestSkillInstallProjectScope(t *testing.T) {
	isolate(t)
	dir := inTempDir(t)

	got := run(t, "skill", "install")
	if got.code != 0 {
		t.Fatalf("skill install: code %d, stderr %q", got.code, got.stderr)
	}
	installed := filepath.Join(dir, skillPath)
	content := read(t, installed)
	if !strings.HasPrefix(content, "---\nname: kie-ai-cli\n") {
		t.Errorf("what was written does not open with the front matter:\n%s", content)
	}
	for _, want := range []string{"scope", "project", "status", "installed", installed} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("the output does not report %q:\n%s", want, got.stdout)
		}
	}

	// The skill is the user's file to read and to keep in the repository,
	// not state this tool hides: it is written as any other file would be.
	info, err := os.Stat(installed)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o644 {
		t.Errorf("mode = %o, want 644", mode)
	}
	parent, err := os.Stat(filepath.Dir(installed))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := parent.Mode().Perm(); mode != 0o755 {
		t.Errorf("directory mode = %o, want 755", mode)
	}
}

// AC2: the user scope writes where Claude Code keeps a user's own skills,
// following CLAUDE_CONFIG_DIR when it has been moved.
func TestSkillInstallUserScope(t *testing.T) {
	isolate(t)
	// A different directory, so that landing in the project one would fail
	// rather than pass by coincidence.
	inTempDir(t)

	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	got := run(t, "skill", "install", "--scope", "user")
	if got.code != 0 {
		t.Fatalf("skill install --scope user: code %d, stderr %q", got.code, got.stderr)
	}
	if content := read(t, filepath.Join(home, skillPath)); content == "" {
		t.Error("the skill is empty")
	}
	if !strings.Contains(got.stdout, "user") {
		t.Errorf("the output does not report the scope:\n%s", got.stdout)
	}
}

// A relative CLAUDE_CONFIG_DIR is ignored rather than resolved against the
// current directory: a user-scope install that lands somewhere different in
// every directory it is run from is not a user-scope install.
func TestSkillInstallUserScopeFallsBackToHome(t *testing.T) {
	isolate(t)
	inTempDir(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "relative/.claude")
	if got := run(t, "skill", "install", "--scope", "user"); got.code != 0 {
		t.Fatalf("skill install --scope user: code %d, stderr %q", got.code, got.stderr)
	}
	if content := read(t, filepath.Join(home, skillPath)); content == "" {
		t.Error("the skill is empty")
	}
}

// AC3: installing twice with the same binary changes nothing, and says so.
// Reinstalling is how a newer binary's skill is picked up, so it has to be
// safe to run again -- and to leave no diff when it did nothing.
func TestSkillInstallIsIdempotent(t *testing.T) {
	isolate(t)
	dir := inTempDir(t)

	first := run(t, "skill", "install")
	if first.code != 0 {
		t.Fatalf("skill install: code %d, stderr %q", first.code, first.stderr)
	}
	before := read(t, filepath.Join(dir, skillPath))

	second := run(t, "skill", "install", "--json")
	if second.code != 0 {
		t.Fatalf("skill install: code %d, stderr %q", second.code, second.stderr)
	}
	var state struct {
		Name   string `json:"name"`
		Scope  string `json:"scope"`
		Path   string `json:"path"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(second.stdout), &state); err != nil {
		t.Fatalf("the answer is not JSON: %v (%q)", err, second.stdout)
	}
	if state.Status != "unchanged" {
		t.Errorf("status = %q, want unchanged", state.Status)
	}
	if state.Name != "kie-ai-cli" || state.Scope != "project" || state.Path != filepath.Join(dir, skillPath) {
		t.Errorf("state = %+v, want the skill that was just written", state)
	}
	if after := read(t, filepath.Join(dir, skillPath)); after != before {
		t.Error("the second install rewrote the file it had nothing to change")
	}
}

// A skill this command wrote is replaced, which is what makes a newer binary's
// skill reachable.
func TestSkillInstallReplacesItsOwnSkill(t *testing.T) {
	isolate(t)
	dir := inTempDir(t)

	if got := run(t, "skill", "install"); got.code != 0 {
		t.Fatalf("skill install: code %d, stderr %q", got.code, got.stderr)
	}
	path := filepath.Join(dir, skillPath)
	stale := strings.Replace(read(t, path), "kie.ai from the command line", "something older", 1)
	write(t, path, stale)

	got := run(t, "skill", "install")
	if got.code != 0 {
		t.Fatalf("skill install: code %d, stderr %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "updated") {
		t.Errorf("the output does not report the replacement:\n%s", got.stdout)
	}
	if strings.Contains(read(t, path), "something older") {
		t.Error("the older skill is still there")
	}
}

// AC4: a file this command did not write is somebody's own skill under a name
// that happens to collide. It is not overwritten, and the error says how to
// overwrite it anyway.
func TestSkillInstallRefusesAFileItDidNotWrite(t *testing.T) {
	isolate(t)
	dir := inTempDir(t)

	path := filepath.Join(dir, skillPath)
	const mine = "---\nname: kie-ai-cli\n---\n\nMy own skill.\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	write(t, path, mine)

	got := run(t, "skill", "install")
	if got.code != 1 {
		t.Errorf("code = %d, want 1 (stderr %q)", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "--force") {
		t.Errorf("the error does not say how to overwrite it: %q", got.stderr)
	}
	if read(t, path) != mine {
		t.Fatal("the file was overwritten anyway")
	}

	forced := run(t, "skill", "install", "--force")
	if forced.code != 0 {
		t.Fatalf("skill install --force: code %d, stderr %q", forced.code, forced.stderr)
	}
	if read(t, path) == mine {
		t.Error("--force did not overwrite it")
	}
}

func TestSkillInstallUsageErrors(t *testing.T) {
	tests := map[string][]string{
		"a scope that is not one": {"skill", "install", "--scope", "global"},
		"an argument it has none": {"skill", "install", "somewhere"},
		"a verb skill has not":    {"skill", "show"},
	}
	for testName, args := range tests {
		t.Run(testName, func(t *testing.T) {
			isolate(t)
			dir := inTempDir(t)

			got := run(t, args...)
			if got.code != 2 {
				t.Errorf("code = %d, want 2 (stderr %q)", got.code, got.stderr)
			}
			if got.stdout != "" {
				t.Errorf("stdout = %q, want nothing on the success stream", got.stdout)
			}
			if _, err := os.Stat(filepath.Join(dir, ".claude")); !os.IsNotExist(err) {
				t.Error("a call that was refused wrote something anyway")
			}
		})
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
