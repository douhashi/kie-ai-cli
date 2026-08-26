package cli

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// AC5: the skill describes every command this binary has. The table it is
// rendered from is the same one the usage text and the completion scripts are
// built from, so a command cannot be added without the agent being told about
// it -- and cannot be renamed while the skill still names the old one.
func TestSkillDescribesEveryCommand(t *testing.T) {
	rendered, err := renderSkill()
	if err != nil {
		t.Fatalf("renderSkill: %v", err)
	}
	for _, c := range commands() {
		line := shortName + " " + c.noun + " " + c.verb
		if !strings.Contains(rendered, line) {
			t.Errorf("the skill does not mention %q:\n%s", line, rendered)
		}
		if !strings.Contains(rendered, c.summary) {
			t.Errorf("the skill does not say what %s %s is for", c.noun, c.verb)
		}
	}
}

// The catalog is 161 models and `catalog update` replaces it. A skill holding a
// copy of any of it would be wrong the moment that happens, so it holds the
// commands that ask instead.
func TestSkillHasNoCatalogInIt(t *testing.T) {
	rendered, err := renderSkill()
	if err != nil {
		t.Fatalf("renderSkill: %v", err)
	}
	for _, ask := range []string{"model list", "model show"} {
		if !strings.Contains(rendered, ask) {
			t.Errorf("the skill does not tell the agent to run %q", ask)
		}
	}
	// One model is named as an example of what to type; the point is that
	// the listing is not written out.
	if n := strings.Count(rendered, "bytedance/"); n > 1 {
		t.Errorf("the skill names %d models; it should ask the catalog instead", n)
	}
}

// Claude Code reads the front matter to decide when the skill applies, and
// matches its name against the directory it was found in.
func TestSkillFrontMatter(t *testing.T) {
	rendered, err := renderSkill()
	if err != nil {
		t.Fatalf("renderSkill: %v", err)
	}
	const fence = "---\n"
	if !strings.HasPrefix(rendered, fence) {
		t.Fatalf("the skill does not open with front matter:\n%s", rendered)
	}
	end := strings.Index(rendered[len(fence):], fence)
	if end < 0 {
		t.Fatalf("the front matter is not closed:\n%s", rendered)
	}
	var front struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(rendered[len(fence):len(fence)+end]), &front); err != nil {
		t.Fatalf("the front matter is not YAML: %v", err)
	}
	if front.Name != skillName {
		t.Errorf("name = %q, want the directory it is installed under, %q", front.Name, skillName)
	}
	if front.Description == "" {
		t.Error("the description is empty, so nothing would ever invoke the skill")
	}
	// What the description is for: the agent reads it alone to decide.
	for _, w := range []string{"kie.ai", shortName} {
		if !strings.Contains(front.Description, w) {
			t.Errorf("the description does not mention %q: %q", w, front.Description)
		}
	}
}

// The marker is what tells a file this command wrote from one somebody else
// did. Without it every install would either refuse or overwrite blindly.
func TestSkillCarriesTheMarker(t *testing.T) {
	rendered, err := renderSkill()
	if err != nil {
		t.Fatalf("renderSkill: %v", err)
	}
	if !strings.Contains(rendered, markerPrefix) {
		t.Errorf("the skill has no marker:\n%s", rendered)
	}
	if !strings.Contains(rendered, version()) {
		t.Error("the marker does not say which version wrote the file")
	}
}
