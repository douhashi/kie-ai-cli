package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
)

// embedded is the catalog this binary carries. The expectations are derived
// from it rather than written down, because it is regenerated weekly and a
// number copied into a test would turn every new model into a failure.
func embedded(t *testing.T) catalog.Catalog {
	t.Helper()
	c, err := catalog.Load(t.TempDir())
	if err != nil {
		t.Fatalf("catalog.Load: %v", err)
	}
	return c
}

func lines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// V1: the listing is where a model ID comes from, so every model in the
// catalog has a line and every line starts with the ID to copy.
func TestModelListShowsEveryModel(t *testing.T) {
	isolate(t)
	c := embedded(t)

	got := run(t, "model", "list")
	if got.code != 0 {
		t.Fatalf("model list: code %d, stderr %q", got.code, got.stderr)
	}
	if got.stderr != "" {
		t.Errorf("a fresh catalog says nothing on stderr: %q", got.stderr)
	}
	rows := lines(got.stdout)
	if len(rows) != len(c.Models) {
		t.Fatalf("got %d lines, want one per model (%d)", len(rows), len(c.Models))
	}
	for i, m := range c.Models {
		if !strings.HasPrefix(rows[i], m.ID+" ") {
			t.Fatalf("line %d = %q, want it to start with the model ID %q", i, rows[i], m.ID)
		}
		for _, want := range []string{m.Category, m.Vendor, m.Name} {
			if !strings.Contains(rows[i], want) {
				t.Errorf("line %d lacks %q:\n%s", i, want, rows[i])
			}
		}
	}
}

// V1: each axis narrows the listing to exactly the models that carry the value.
func TestModelListFilters(t *testing.T) {
	isolate(t)
	c := embedded(t)

	tests := []struct {
		name string
		args []string
		want func(catalog.Model) bool
	}{
		{
			name: "by category",
			args: []string{"--category", "image"},
			want: func(m catalog.Model) bool { return m.Category == "image" },
		},
		{
			name: "by vendor",
			args: []string{"--vendor", "seedream"},
			want: func(m catalog.Model) bool { return m.Vendor == "seedream" },
		},
		{
			name: "by both",
			args: []string{"--category", "image", "--vendor", "seedream"},
			want: func(m catalog.Model) bool { return m.Category == "image" && m.Vendor == "seedream" },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var want []string
			for _, m := range c.Models {
				if tt.want(m) {
					want = append(want, m.ID)
				}
			}
			if len(want) == 0 {
				t.Fatalf("the catalog has no model to filter for; the test proves nothing")
			}

			got := run(t, append([]string{"model", "list"}, tt.args...)...)
			if got.code != 0 {
				t.Fatalf("model list: code %d, stderr %q", got.code, got.stderr)
			}
			rows := lines(got.stdout)
			if len(rows) != len(want) {
				t.Fatalf("got %d lines, want %d", len(rows), len(want))
			}
			for i, id := range want {
				if !strings.HasPrefix(rows[i], id+" ") {
					t.Errorf("line %d = %q, want the model %q", i, rows[i], id)
				}
			}
		})
	}
}

// V1: a value nothing carries is answered with the values that exist. An empty
// listing would let a typo pass for "there are none".
func TestModelListRejectsAValueNothingHas(t *testing.T) {
	isolate(t)

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "category", args: []string{"--category", "picture"}, want: []string{"category", "image", "music", "video"}},
		{name: "vendor", args: []string{"--vendor", "seedreem"}, want: []string{"vendor", "seedream"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := run(t, append([]string{"model", "list"}, tt.args...)...)
			if got.code != 2 {
				t.Fatalf("code = %d, want 2 (stderr %q)", got.code, got.stderr)
			}
			if got.stdout != "" {
				t.Errorf("stdout = %q, want nothing on the success stream", got.stdout)
			}
			for _, want := range tt.want {
				if !strings.Contains(got.stderr, want) {
					t.Errorf("stderr does not mention %q:\n%s", want, got.stderr)
				}
			}
		})
	}
}

// summary is the JSON contract of model list.
type summary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Vendor      string `json:"vendor"`
	DocsURL     string `json:"docsUrl"`
}

func TestModelListJSON(t *testing.T) {
	isolate(t)
	c := embedded(t)

	got := run(t, "model", "list", "--json")
	if got.code != 0 {
		t.Fatalf("model list --json: code %d, stderr %q", got.code, got.stderr)
	}
	var list []summary
	if err := json.Unmarshal([]byte(got.stdout), &list); err != nil {
		t.Fatalf("output is not JSON (%v):\n%s", err, got.stdout)
	}
	if len(list) != len(c.Models) {
		t.Fatalf("got %d entries, want one per model (%d)", len(list), len(c.Models))
	}
	first := c.Models[0]
	want := summary{
		ID: first.ID, Name: first.Name, Description: first.Description,
		Category: first.Category, Vendor: first.Vendor, DocsURL: first.DocsURL,
	}
	if list[0] != want {
		t.Errorf("first entry = %+v, want %+v", list[0], want)
	}
}

// V2: every model can be shown, and every one of them lists at least one input
// field -- including the four whose schema is nothing but branches.
func TestModelShowDescribesEveryModel(t *testing.T) {
	isolate(t)
	c := embedded(t)

	for _, m := range c.Models {
		got := run(t, "model", "show", m.ID)
		if got.code != 0 {
			t.Fatalf("model show %s: code %d, stderr %q", m.ID, got.code, got.stderr)
		}
		if !strings.Contains(got.stdout, m.ID) {
			t.Errorf("model show %s does not name the model:\n%s", m.ID, got.stdout)
		}
		if fields := inputLines(got.stdout); len(fields) == 0 {
			t.Errorf("model show %s lists no input field:\n%s", m.ID, got.stdout)
		}
	}
}

// inputLines returns the field lines of the input listing: everything after
// the "input:" header that is indented, which is how a field is written.
func inputLines(out string) []string {
	_, after, ok := strings.Cut(out, "\ninput:\n")
	if !ok {
		return nil
	}
	var fields []string
	for _, line := range lines(after) {
		if strings.HasPrefix(line, "  ") && strings.TrimSpace(line) != "" {
			fields = append(fields, line)
		}
	}
	return fields
}

// V2: a field says whether it must be supplied, what it defaults to and which
// values it accepts, which is what the caller needs to build the request.
func TestModelShowNamesRequirementsAndChoices(t *testing.T) {
	isolate(t)

	got := run(t, "model", "show", "4o-image-api/generate-4-o-image")
	if got.code != 0 {
		t.Fatalf("model show: code %d, stderr %q", got.code, got.stderr)
	}
	var size string
	for _, line := range inputLines(got.stdout) {
		if strings.HasPrefix(strings.TrimSpace(line), "size ") {
			size = line
		}
	}
	if size == "" {
		t.Fatalf("model show does not list the size field:\n%s", got.stdout)
	}
	if !strings.Contains(size, "string") || !strings.Contains(size, "required") {
		t.Errorf("the size line does not report a required string: %q", size)
	}
	if !strings.Contains(got.stdout, "one of: 1:1, 3:2, 2:3") {
		t.Errorf("model show does not list the accepted sizes:\n%s", got.stdout)
	}
	if !strings.Contains(got.stdout, "https://docs.kie.ai/") {
		t.Errorf("model show does not link the documentation:\n%s", got.stdout)
	}
}

// One of the four models whose input lives entirely in branches. Reading only
// the schema's own properties would print a header and nothing under it.
func TestModelShowListsTheBranchesOfASchemaWithoutProperties(t *testing.T) {
	isolate(t)

	got := run(t, "model", "show", "pixverse-v6/extend")
	if got.code != 0 {
		t.Fatalf("model show: code %d, stderr %q", got.code, got.stderr)
	}
	for _, want := range []string{"one of (variant 1", "one of (variant 2", "taskId", "video_url"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, got.stdout)
		}
	}
}

// A model ID that is not in the catalog is a run-time failure, not a mistake
// in how the command was called: the shape of the call was right.
func TestModelShowUnknownID(t *testing.T) {
	isolate(t)

	got := run(t, "model", "show", "nope/nothing")
	if got.code != 1 {
		t.Fatalf("code = %d, want 1 (stderr %q)", got.code, got.stderr)
	}
	if got.stdout != "" {
		t.Errorf("stdout = %q, want nothing on the success stream", got.stdout)
	}
	if !strings.Contains(got.stderr, "model list") {
		t.Errorf("stderr does not say where the model IDs come from:\n%s", got.stderr)
	}
	if strings.Contains(got.stderr, "Usage:") {
		t.Errorf("an unknown model is not a usage mistake:\n%s", got.stderr)
	}
}

func TestModelShowJSON(t *testing.T) {
	isolate(t)

	got := run(t, "model", "show", "4o-image-api/generate-4-o-image", "--json")
	if got.code != 0 {
		t.Fatalf("model show --json: code %d, stderr %q", got.code, got.stderr)
	}
	var m catalog.Model
	if err := json.Unmarshal([]byte(got.stdout), &m); err != nil {
		t.Fatalf("output is not JSON (%v):\n%s", err, got.stdout)
	}
	if m.ID != "4o-image-api/generate-4-o-image" {
		t.Errorf("id = %q, want the model that was asked for", m.ID)
	}
	if m.Create.Path == "" || m.Query.Path == "" {
		t.Errorf("JSON does not carry both endpoints: %+v", m)
	}
	if _, ok := m.Input["properties"]; !ok {
		t.Errorf("JSON does not carry the input schema:\n%s", got.stdout)
	}
}
