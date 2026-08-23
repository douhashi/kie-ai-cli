package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
)

// schema builds the input schema the way the catalog carries it: decoded JSON,
// so the numbers are float64 and the shapes are the ones a handler really sees.
func schema(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("the test schema is not JSON: %v", err)
	}
	return out
}

var listFixture = []catalog.Model{
	{ID: "a/one", Name: "One", Category: "image", Vendor: "a", Description: "a description far too long for a column"},
	{ID: "bb/two", Name: "Two", Category: "video", Vendor: "bb", Description: "another one", DocsURL: "https://d/two"},
}

// The listing is what a reader scans to find an ID, so it carries the four
// short fields and leaves out the description: the longest one in the catalog
// is 345 characters and would push every other column off the screen.
func TestWriteModelListColumns(t *testing.T) {
	var out bytes.Buffer
	if err := writeModelList(&out, listFixture, false); err != nil {
		t.Fatalf("writeModelList: %v", err)
	}
	want := "a/one   image  a   One\n" +
		"bb/two  video  bb  Two\n"
	if out.String() != want {
		t.Errorf("output =\n%q\nwant\n%q", out.String(), want)
	}
}

// The JSON listing answers with the summary a caller can act on, and nothing
// that would make it a second copy of model show.
func TestWriteModelListJSON(t *testing.T) {
	var out bytes.Buffer
	if err := writeModelList(&out, listFixture, true); err != nil {
		t.Fatalf("writeModelList: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON (%v):\n%s", err, out.String())
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2:\n%s", len(got), out.String())
	}
	want := map[string]any{
		"id": "bb/two", "name": "Two", "description": "another one",
		"category": "video", "vendor": "bb", "docsUrl": "https://d/two",
	}
	if len(got[1]) != len(want) {
		t.Errorf("entry has fields %v, want exactly %v", keysOf(got[1]), keysOf(want))
	}
	for k, v := range want {
		if got[1][k] != v {
			t.Errorf("entry[%q] = %v, want %v", k, got[1][k], v)
		}
	}
}

// An empty result is an empty document, not null: a caller iterating over the
// answer must not have to test for it.
func TestWriteModelListJSONOfNothing(t *testing.T) {
	var out bytes.Buffer
	if err := writeModelList(&out, nil, true); err != nil {
		t.Fatalf("writeModelList: %v", err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("output = %q, want an empty JSON array", out.String())
	}
}

func keysOf(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestFilterModels(t *testing.T) {
	tests := []struct {
		name     string
		category string
		vendor   string
		want     []string
	}{
		{name: "no filter keeps everything", want: []string{"a/one", "bb/two"}},
		{name: "by category", category: "image", want: []string{"a/one"}},
		{name: "the value is matched in lower case", category: "IMAGE", want: []string{"a/one"}},
		{name: "by vendor", vendor: "bb", want: []string{"bb/two"}},
		{name: "both axes are required to match", category: "image", vendor: "bb", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := filterModels(listFixture, tt.category, tt.vendor)
			if err != nil {
				t.Fatalf("filterModels: %v", err)
			}
			var ids []string
			for _, m := range got {
				ids = append(ids, m.ID)
			}
			if strings.Join(ids, ",") != strings.Join(tt.want, ",") {
				t.Errorf("ids = %v, want %v", ids, tt.want)
			}
		})
	}
}

// A value that matches nothing is a typo far more often than a real question,
// and answering it with an empty listing hides that. The reply names the axis
// and the values that do exist, which is what the reader needs to fix it.
func TestFilterModelsRejectsAValueNothingHas(t *testing.T) {
	tests := []struct {
		name     string
		category string
		vendor   string
		want     []string
	}{
		{name: "category", category: "imag", want: []string{"category", `"imag"`, "image, video"}},
		{name: "vendor", vendor: "seedream", want: []string{"vendor", `"seedream"`, "a, bb"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := filterModels(listFixture, tt.category, tt.vendor)
			if err == nil {
				t.Fatal("filterModels accepted a value no model has")
			}
			var ue usageError
			if !errors.As(err, &ue) {
				t.Errorf("error %T is not a usage error; the caller mistyped a value", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err.Error(), want)
				}
			}
		})
	}
}

// A vendor that exists but has nothing in the chosen category is a real
// vendor, so the answer is an empty listing rather than "unknown vendor".
func TestFilterModelsAcceptsACombinationWithNoMatch(t *testing.T) {
	got, err := filterModels(listFixture, "image", "bb")
	if err != nil {
		t.Fatalf("filterModels: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d models, want none", len(got))
	}
}

// Four of the 161 input schemas describe nothing but branches: reading only
// the schema's own properties would show those models as having no input at
// all. Each branch is a set the caller picks between, so it is shown as one.
func TestInputGroups(t *testing.T) {
	groups := inputGroups(schema(t, `{
	  "properties": {"prompt": {"type": "string"}, "seed": {"type": "integer"}},
	  "required": ["prompt"],
	  "anyOf": [
	    {"title": "By id", "properties": {"taskId": {"type": "string"}}, "required": ["taskId"]},
	    {"type": "object"},
	    {"title": "By url", "properties": {"url": {"type": "string"}}}
	  ]
	}`))

	want := []struct {
		label  string
		fields []string
	}{
		{label: "", fields: []string{"prompt", "seed"}},
		{label: "one of (variant 1: By id)", fields: []string{"taskId"}},
		{label: "one of (variant 2: By url)", fields: []string{"url"}},
	}
	if len(groups) != len(want) {
		t.Fatalf("got %d groups, want %d: %+v", len(groups), len(want), groups)
	}
	for i, w := range want {
		if groups[i].label != w.label {
			t.Errorf("group %d label = %q, want %q", i, groups[i].label, w.label)
		}
		var names []string
		for _, f := range groups[i].fields {
			names = append(names, f.name)
		}
		if strings.Join(names, ",") != strings.Join(w.fields, ",") {
			t.Errorf("group %d fields = %v, want %v", i, names, w.fields)
		}
	}
}

// Required fields come first: they are what the caller must supply, and the
// rest of the listing is only interesting once they are known.
func TestInputGroupsPutsRequiredFirst(t *testing.T) {
	groups := inputGroups(schema(t, `{
	  "properties": {"a": {"type": "string"}, "b": {"type": "string"}, "c": {"type": "string"}},
	  "required": ["c"]
	}`))
	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	var got []string
	for _, f := range groups[0].fields {
		got = append(got, f.name)
	}
	if strings.Join(got, ",") != "c,a,b" {
		t.Errorf("fields = %v, want the required one first and the rest by name", got)
	}
}

var showFixture = catalog.Model{
	ID: "v/one", Name: "One", Category: "image", Vendor: "v",
	DocsURL: "https://d", Description: "Desc",
}

// The header names the model and where its documentation is; the input listing
// is one line per field, with the choices unabridged on the line below, since
// a truncated set of values is one the caller cannot use.
func TestWriteModelShow(t *testing.T) {
	m := showFixture
	m.Input = schema(t, `{
	  "properties": {"size": {"type": "string", "description": "Aspect ratio.", "enum": ["1:1", "3:2"], "default": "1:1"}},
	  "required": ["size"]
	}`)

	var out bytes.Buffer
	if err := writeModelShow(&out, m, false); err != nil {
		t.Fatalf("writeModelShow: %v", err)
	}
	want := "id           v/one\n" +
		"name         One\n" +
		"category     image\n" +
		"vendor       v\n" +
		"docs         https://d\n" +
		"description  Desc\n" +
		"\ninput:\n" +
		"  size  string  required  1:1  Aspect ratio.\n" +
		strings.Repeat(" ", 31) + "one of: 1:1, 3:2\n"
	if out.String() != want {
		t.Errorf("output =\n%s\nwant\n%s", out.String(), want)
	}
}

// A field the caller may leave out says so, and says it in the same column, so
// the two kinds can be told apart by scanning rather than by reading.
func TestWriteModelShowMarksOptionalFields(t *testing.T) {
	m := showFixture
	m.Input = schema(t, `{"properties": {"seed": {"type": "integer", "description": "Random seed."}}}`)

	var out bytes.Buffer
	if err := writeModelShow(&out, m, false); err != nil {
		t.Fatalf("writeModelShow: %v", err)
	}
	if !strings.Contains(out.String(), "seed  integer  optional  -  Random seed.") {
		t.Errorf("output does not describe an optional field with no default:\n%s", out.String())
	}
}

// Descriptions run to several hundred characters and contain newlines. Neither
// may reach the listing: one line per field is the whole point of the shape.
func TestWriteModelShowKeepsOneLinePerField(t *testing.T) {
	long := strings.Repeat("ab ", 60)
	m := showFixture
	m.Input = schema(t, `{"properties": {"prompt": {"type": "string", "maxLength": 5000, "deprecated": true,
	  "description": "First\nsecond   third `+long+`"}}}`)

	var out bytes.Buffer
	if err := writeModelShow(&out, m, false); err != nil {
		t.Fatalf("writeModelShow: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	field := lines[len(lines)-1]
	for _, want := range []string{"[deprecated]", "[maxLength 5000]", "First second third", "…"} {
		if !strings.Contains(field, want) {
			t.Errorf("field line lacks %q:\n%s", want, field)
		}
	}
	if len([]rune(field)) > 160 {
		t.Errorf("field line is %d characters long:\n%s", len([]rune(field)), field)
	}
}

// The JSON answer is the catalog entry itself, input schema and endpoints
// included: it is what a generator would build a request from.
func TestWriteModelShowJSON(t *testing.T) {
	m := showFixture
	m.Input = schema(t, `{"properties": {"size": {"type": "string"}}}`)

	var out bytes.Buffer
	if err := writeModelShow(&out, m, true); err != nil {
		t.Fatalf("writeModelShow: %v", err)
	}
	var got catalog.Model
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON (%v):\n%s", err, out.String())
	}
	if got.ID != m.ID {
		t.Errorf("id = %q, want %q", got.ID, m.ID)
	}
	if _, ok := got.Input["properties"]; !ok {
		t.Errorf("JSON does not carry the input schema:\n%s", out.String())
	}
}

// V5: an old catalog is reported, once, on stderr -- so that a caller reading
// the JSON gets a document and not a document with a warning glued to it.
func TestStaleCatalogIsReportedOnStderr(t *testing.T) {
	c, err := catalog.Load()
	if err != nil {
		t.Fatalf("catalog.Load: %v", err)
	}
	commands := []struct {
		name string
		run  func(*env) error
	}{
		{name: "model list", run: func(e *env) error { return runModelList(e, nil, "", "") }},
		{name: "model show", run: func(e *env) error { return runModelShow(e, []string{c.Models[0].ID}) }},
	}
	ages := []struct {
		name string
		days int
		want bool
	}{
		{name: "a day before the limit", days: 89, want: false},
		{name: "a day past it", days: 91, want: true},
	}
	for _, cmd := range commands {
		for _, age := range ages {
			for _, asJSON := range []bool{false, true} {
				t.Run(cmd.name+"/"+age.name+"/json="+boolWord(asJSON), func(t *testing.T) {
					var stdout, stderr bytes.Buffer
					e := &env{
						stdout: &stdout,
						stderr: &stderr,
						json:   asJSON,
						now:    c.GeneratedAt.Add(time.Duration(age.days) * 24 * time.Hour),
					}
					if err := cmd.run(e); err != nil {
						t.Fatalf("%s: %v", cmd.name, err)
					}
					if got := strings.Contains(stderr.String(), "catalog"); got != age.want {
						t.Errorf("warned = %v, want %v (stderr %q)", got, age.want, stderr.String())
					}
					if age.want && !strings.Contains(stderr.String(), c.GeneratedAt.Format(time.DateOnly)) {
						t.Errorf("the warning does not say when the catalog was generated: %q", stderr.String())
					}
					if asJSON {
						var doc any
						if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
							t.Errorf("stdout is not JSON (%v):\n%s", err, stdout.String())
						}
					}
				})
			}
		}
	}
}

func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// Fifteen of the catalog's models carry no description at all. An empty cell
// would leave the line ending in the padding of the column before it, which
// reads as a column that failed to print rather than as one with nothing in it.
func TestWriteModelShowHasNoBlankValuesOrTrailingSpace(t *testing.T) {
	m := showFixture
	m.Description = ""
	m.Input = schema(t, `{
	  "oneOf": [
	    {"title": "By id", "properties": {"taskId": {"type": "string"}}, "required": ["taskId"]}
	  ]
	}`)

	var out bytes.Buffer
	if err := writeModelShow(&out, m, false); err != nil {
		t.Fatalf("writeModelShow: %v", err)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if line != strings.TrimRight(line, " ") {
			t.Errorf("line ends in whitespace: %q", line)
		}
	}
	if !strings.Contains(out.String(), "description  -\n") {
		t.Errorf("a model with no description does not say so:\n%s", out.String())
	}
	// The first group follows the header directly; only the groups after it
	// are set off by a blank line.
	if !strings.Contains(out.String(), "\ninput:\none of (variant 1: By id)\n") {
		t.Errorf("the first group is not written directly under the header:\n%s", out.String())
	}
}
