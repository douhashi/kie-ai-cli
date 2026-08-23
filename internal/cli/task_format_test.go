package cli

import (
	"encoding/json"
	"flag"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
)

// bind registers the flags of one schema the way task run does: on a set that
// already carries the flags every command has, so that a clash with them is a
// failure here rather than a panic in front of a user.
func bind(t *testing.T, input map[string]any) (*flag.FlagSet, []*field) {
	t.Helper()
	fs := flag.NewFlagSet("task run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Bool(flagJSON, false, "print the result as JSON")
	fs.String(flagInput, "", "read the input from a JSON document")
	return fs, bindFields(fs, newInputSchema(input))
}

// The flag package panics on a name it has already been given, so a model
// whose schema names a field after one of this tool's own flags would take the
// whole command down. Every model in the catalog is bound here for that
// reason: the catalog is regenerated from someone else's documentation, and a
// new field name arrives without anyone reading it first.
func TestEveryModelInTheCatalogBinds(t *testing.T) {
	c, err := catalog.Load(t.TempDir())
	if err != nil {
		t.Fatalf("catalog.Load: %v", err)
	}
	for _, m := range c.Models {
		t.Run(m.ID, func(t *testing.T) {
			fs, fields := bind(t, m.Input)
			bound := make(map[string]bool, len(fields))
			for _, f := range fields {
				if fs.Lookup(f.name) == nil {
					t.Errorf("field %q was returned but not registered", f.name)
				}
				if !flaggable(f.name) {
					t.Errorf("field %q cannot be written as a flag", f.name)
				}
				bound[f.name] = true
			}
			// What was left out is left out on purpose, and only
			// --input reaches it. Three fields in the catalog are
			// declared with a trailing space, which is not a typo
			// of this project's: the catalog is generated from
			// documentation it does not own.
			for _, p := range newInputSchema(m.Input).properties() {
				if !bound[p.name] {
					t.Logf("%q is reachable only through --input", p.name)
				}
			}
		})
	}
}

// The fields that cannot be flags are the ones that would be unwritable or
// would take a name this tool already uses. They are not lost: --input reaches
// them, which is why they are skipped rather than renamed into something the
// documentation does not mention.
func TestFieldsThatCannotBeFlagsAreLeftToTheDocument(t *testing.T) {
	input := schema(t, `{
		"properties": {
			"prompt":       {"type": "string"},
			"image_urls ":  {"type": "array", "items": {"type": "string"}},
			"json":         {"type": "boolean"},
			"input":        {"type": "string"},
			"--odd":        {"type": "string"},
			"a=b":          {"type": "string"}
		}
	}`)
	_, fields := bind(t, input)

	var names []string
	for _, f := range fields {
		names = append(names, f.name)
	}
	if !slices.Equal(names, []string{"prompt"}) {
		t.Errorf("bound %v, want only the one name that can be typed as a flag", names)
	}
}

func TestFieldReadsAValueAsTheSchemaDeclaresIt(t *testing.T) {
	// alsoArray is the one model in the catalog that declares a field as a
	// string in one branch and as an array in another: the value then has
	// to be read as whichever of the two it looks like.
	const alsoArray = `, "oneOf": [{"properties": {"f": {"type": "array", "items": {"type": "string"}}}}]`

	tests := []struct {
		name      string
		property  string
		alsoArray bool
		values    []string
		want      string
		wantErr   string
	}{
		{
			name:     "a string is itself",
			property: `{"type": "string"}`,
			values:   []string{"a cat in a hat"},
			want:     `"a cat in a hat"`,
		},
		{
			name:     "a number keeps the digits it was written with",
			property: `{"type": "number"}`,
			values:   []string{"2.50"},
			want:     `2.50`,
		},
		{
			name:     "an integer is a whole number",
			property: `{"type": "integer"}`,
			values:   []string{"42"},
			want:     `42`,
		},
		{
			name:     "an integer refuses a fraction",
			property: `{"type": "integer"}`,
			values:   []string{"2.5"},
			wantErr:  "integer",
		},
		{
			name:     "a number refuses a word",
			property: `{"type": "number"}`,
			values:   []string{"many"},
			wantErr:  "number",
		},
		{
			name:     "a boolean takes the value the flag was given",
			property: `{"type": "boolean"}`,
			values:   []string{"false"},
			want:     `false`,
		},
		{
			name:     "an array collects one element per occurrence",
			property: `{"type": "array", "items": {"type": "string"}}`,
			values:   []string{"https://a.png", "https://b.png"},
			want:     `["https://a.png","https://b.png"]`,
		},
		{
			name:     "an array of objects reads each element as JSON",
			property: `{"type": "array", "items": {"type": "object"}}`,
			values:   []string{`{"text":"hello"}`},
			want:     `[{"text":"hello"}]`,
		},
		{
			name:     "an array of objects refuses an element that is not one",
			property: `{"type": "array", "items": {"type": "object"}}`,
			values:   []string{"hello"},
			wantErr:  "object",
		},
		{
			name:      "a field declared twice over takes the JSON reading",
			property:  `{"type": "string"}`,
			alsoArray: true,
			values:    []string{`["https://a.png"]`},
			want:      `["https://a.png"]`,
		},
		{
			name:      "a field declared twice over falls back to the string",
			property:  `{"type": "string"}`,
			alsoArray: true,
			values:    []string{"https://a.png"},
			want:      `"https://a.png"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			declaration := `{"properties": {"f": ` + tt.property + `}`
			if tt.alsoArray {
				declaration += alsoArray
			}
			fs, fields := bind(t, schema(t, declaration+`}`))
			if len(fields) != 1 {
				t.Fatalf("bound %d fields, want one", len(fields))
			}

			var args []string
			for _, v := range tt.values {
				args = append(args, "--f="+v)
			}
			err := fs.Parse(args)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("--f=%v was accepted as %s", tt.values, canonicalOf(t, fields[0].value))
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error %q does not say what the field takes (%q)", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !fields[0].set {
				t.Error("the field was parsed but not marked as given")
			}
			if got := canonicalOf(t, fields[0].value); got != tt.want {
				t.Errorf("value = %s, want %s", got, tt.want)
			}
		})
	}
}

// A boolean is written as a flag on its own, which is what --f=false relies on:
// without it the word would be read as a positional argument instead.
func TestABooleanFieldNeedsNoValue(t *testing.T) {
	_, fields := bind(t, schema(t, `{"properties": {"f": {"type": "boolean"}}}`))
	if len(fields) != 1 || !fields[0].IsBoolFlag() {
		t.Fatalf("a boolean field is not a boolean flag: %+v", fields)
	}
	_, other := bind(t, schema(t, `{"properties": {"f": {"type": "string"}}}`))
	if other[0].IsBoolFlag() {
		t.Error("a string field must take a value of its own")
	}
}

func TestValidateInput(t *testing.T) {
	const modelID = "acme/one"
	plain := schema(t, `{
		"properties": {
			"prompt": {"type": "string"},
			"count":  {"type": "integer"},
			"scale":  {"type": "number"},
			"urls":   {"type": "array", "items": {"type": "string"}}
		},
		"required": ["prompt"]
	}`)
	branching := schema(t, `{
		"required": ["prompt"],
		"oneOf": [
			{"title": "From a task", "properties": {"prompt": {"type": "string"}, "task_id": {"type": "string"}}, "required": ["task_id"]},
			{"properties": {"prompt": {"type": "string"}, "image_url": {"type": "string"}}, "required": ["image_url"]}
		]
	}`)

	tests := []struct {
		name   string
		schema map[string]any
		input  map[string]any
		want   []string
	}{
		{
			name:   "everything it declares",
			schema: plain,
			input:  map[string]any{"prompt": "a cat", "count": json.Number("2"), "scale": json.Number("1.5"), "urls": []any{"https://a.png"}},
		},
		{
			name:   "an integer is a number",
			schema: plain,
			input:  map[string]any{"prompt": "a cat", "scale": json.Number("2")},
		},
		{
			name:   "a required field is missing",
			schema: plain,
			input:  map[string]any{"count": json.Number("2")},
			want:   []string{"prompt"},
		},
		{
			name:   "a field the model does not take",
			schema: plain,
			input:  map[string]any{"prompt": "a cat", "promt": "a typo"},
			want:   []string{"promt", "model show " + modelID, "catalog update"},
		},
		{
			name:   "every mistake is reported at once",
			schema: plain,
			input:  map[string]any{"promt": "a typo", "count": "two"},
			want:   []string{"prompt", "promt", "count"},
		},
		{
			name:   "a number where a string belongs",
			schema: plain,
			input:  map[string]any{"prompt": json.Number("5")},
			want:   []string{"prompt", "string"},
		},
		{
			name:   "a fraction where a whole number belongs",
			schema: plain,
			input:  map[string]any{"prompt": "a cat", "count": json.Number("1.5")},
			want:   []string{"count", "integer"},
		},
		{
			name:   "one alternative is complete",
			schema: branching,
			input:  map[string]any{"prompt": "a cat", "task_id": "t-1"},
		},
		{
			name:   "the other alternative is complete",
			schema: branching,
			input:  map[string]any{"prompt": "a cat", "image_url": "https://a.png"},
		},
		{
			name:   "no alternative is complete",
			schema: branching,
			input:  map[string]any{"prompt": "a cat"},
			want:   []string{"From a task", "task_id", "image_url"},
		},
		{
			name:   "the root is checked even when an alternative is complete",
			schema: branching,
			input:  map[string]any{"task_id": "t-1"},
			want:   []string{"prompt"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateInput(newInputSchema(tt.schema), tt.input, modelID)
			if len(tt.want) == 0 {
				if err != nil {
					t.Fatalf("validateInput: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("validateInput accepted an input the schema refuses")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// canonicalOf renders a value as the JSON it would be sent as.
func canonicalOf(t *testing.T, v any) string {
	t.Helper()
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(encoded)
}
