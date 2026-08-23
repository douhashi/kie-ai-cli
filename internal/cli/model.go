package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
)

func bindModelList(fs *flag.FlagSet) handler {
	category := fs.String("category", "", "only the models in this category")
	vendor := fs.String("vendor", "", "only the models from this vendor")
	return func(e *env, args []string) error {
		return runModelList(e, args, *category, *vendor)
	}
}

func runModelList(e *env, args []string, category, vendor string) error {
	if len(args) != 0 {
		return usagef("model list: expected no arguments, got %d", len(args))
	}
	c, err := loadCatalog(e)
	if err != nil {
		return err
	}
	models, err := filterModels(c.Models, category, vendor)
	if err != nil {
		return err
	}
	return writeModelList(e.stdout, models, e.json)
}

func runModelShow(e *env, args []string) error {
	if len(args) != 1 {
		return usagef("model show: expected <model-id>, got %d arguments", len(args))
	}
	c, err := loadCatalog(e)
	if err != nil {
		return err
	}
	id := args[0]
	i := slices.IndexFunc(c.Models, func(m catalog.Model) bool { return m.ID == id })
	if i < 0 {
		return fmt.Errorf("unknown model %q: run `%s model list` to see every model ID", id, name)
	}
	return writeModelShow(e.stdout, c.Models[i], e.json)
}

// loadCatalog reads the catalog in effect -- the downloaded one if there is
// one -- and reports its age on stderr once it is old enough to be misleading.
// The warning is not an error: an old catalog still answers, and the caller
// decides whether that is good enough. Keeping it off stdout leaves --json a
// document a machine can read.
func loadCatalog(e *env) (catalog.Catalog, error) {
	c, err := catalog.Load(e.paths.Catalog)
	if err != nil {
		return catalog.Catalog{}, err
	}
	if warning := c.StaleWarning(e.now); warning != "" {
		fmt.Fprintf(e.stderr, "%s: %s\n", name, warning)
	}
	return c, nil
}

// axis is one of the two fields model list narrows by.
type axis struct {
	name   string
	plural string
	want   string
	of     func(catalog.Model) string
}

func (a axis) matches(m catalog.Model) bool {
	return a.want == "" || strings.ToLower(a.of(m)) == a.want
}

// filterModels keeps the models that match every axis the caller named.
//
// A value that no model carries is rejected rather than answered with an empty
// listing, which would read as "there are none" and hide the typo.
func filterModels(models []catalog.Model, category, vendor string) ([]catalog.Model, error) {
	axes := []axis{
		{
			name: "category", plural: "categories", want: strings.ToLower(category),
			of: func(m catalog.Model) string { return m.Category },
		},
		{
			name: "vendor", plural: "vendors", want: strings.ToLower(vendor),
			of: func(m catalog.Model) string { return m.Vendor },
		},
	}
	// Each value is checked against the whole catalog, so that a real vendor
	// with nothing in the chosen category is reported as an empty listing
	// rather than as a vendor that does not exist.
	for _, a := range axes {
		if a.want == "" {
			continue
		}
		known := valuesOf(models, a.of)
		if !slices.Contains(known, a.want) {
			return nil, usagef("model list: unknown %s %q; known %s: %s",
				a.name, a.want, a.plural, strings.Join(known, ", "))
		}
	}

	var kept []catalog.Model
	for _, m := range models {
		matched := true
		for _, a := range axes {
			matched = matched && a.matches(m)
		}
		if matched {
			kept = append(kept, m)
		}
	}
	return kept, nil
}

// valuesOf lists the distinct values the models carry for one axis, in lower
// case and in order, which is the form both the comparison and the message use.
func valuesOf(models []catalog.Model, of func(catalog.Model) string) []string {
	seen := make(map[string]bool, len(models))
	var out []string
	for _, m := range models {
		if v := strings.ToLower(of(m)); !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return out
}

// modelSummary is the JSON contract of model list: what identifies a model and
// where to read more, without the input schema, which model show carries.
type modelSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Vendor      string `json:"vendor"`
	DocsURL     string `json:"docsUrl"`
}

func writeModelList(w io.Writer, models []catalog.Model, asJSON bool) error {
	if asJSON {
		summaries := make([]modelSummary, 0, len(models))
		for _, m := range models {
			summaries = append(summaries, modelSummary{
				ID: m.ID, Name: m.Name, Description: m.Description,
				Category: m.Category, Vendor: m.Vendor, DocsURL: m.DocsURL,
			})
		}
		return writeJSON(w, summaries)
	}
	// The description is left out on purpose: the longest is 345 characters,
	// and one line per model is what makes the listing scannable.
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, m := range models {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.ID, m.Category, m.Vendor, m.Name)
	}
	return tw.Flush()
}

func writeModelShow(w io.Writer, m catalog.Model, asJSON bool) error {
	if asJSON {
		return writeJSON(w, m)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, row := range [][2]string{
		{"id", m.ID},
		{"name", m.Name},
		{"category", m.Category},
		{"vendor", m.Vendor},
		{"docs", m.DocsURL},
		{"description", oneLine(m.Description)},
	} {
		fmt.Fprintf(tw, "%s\t%s\n", row[0], orDash(row[1]))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	return writeInput(w, inputGroups(m.Input))
}

// descriptionLimit is how much of a field's description the listing shows. The
// full text belongs to --json and to the documentation the header links to;
// here it has to share a line with the name, the type and the default.
const descriptionLimit = 80

func writeInput(w io.Writer, groups []inputGroup) error {
	if _, err := fmt.Fprint(w, "\ninput:\n"); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for i, g := range groups {
		// A label has no tabs, so it ends the column block: each group is
		// aligned to its own fields rather than to the widest of all of them.
		// The blank line sets one group off from the last, and there is no
		// last one to set the first off from.
		if g.label != "" {
			if i > 0 {
				fmt.Fprintln(tw)
			}
			fmt.Fprintf(tw, "%s\n", g.label)
		}
		for _, f := range g.fields {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n",
				f.name, f.typ, requirement(f.required), orDash(f.def), orDash(f.summary()))
			if len(f.enum) > 0 {
				// The accepted values are never abridged: a value the caller
				// cannot read is one they cannot send.
				fmt.Fprintf(tw, "  \t\t\t\tone of: %s\n", strings.Join(f.enum, ", "))
			}
		}
	}
	return tw.Flush()
}

// inputGroup is a set of fields that go together: the schema's own properties,
// or one branch of a oneOf/anyOf, which are alternatives to choose between
// rather than fields to combine.
type inputGroup struct {
	label  string
	fields []inputField
}

// inputField is one parameter of the request payload.
type inputField struct {
	name        string
	typ         string
	required    bool
	def         string
	notes       []string
	enum        []string
	description string
}

// summary is the last column: the constraints that change what may be sent,
// followed by as much of the description as fits on the line.
func (f inputField) summary() string {
	description := f.description
	if runes := []rune(description); len(runes) > descriptionLimit {
		description = strings.TrimRight(string(runes[:descriptionLimit]), " ") + "…"
	}
	parts := make([]string, 0, len(f.notes)+1)
	for _, n := range f.notes {
		parts = append(parts, "["+n+"]")
	}
	if description != "" {
		parts = append(parts, description)
	}
	return strings.Join(parts, " ")
}

// noteKeys are the schema keywords shown beside a field, in the order they are
// shown. They are the ones that decide whether a value will be accepted.
var noteKeys = []string{
	"format", "pattern", "minimum", "maximum", "multipleOf",
	"minLength", "maxLength", "minItems", "maxItems",
}

// inputGroups flattens an input schema into what a caller has to supply.
//
// Nine of the catalog's schemas put alternatives in a oneOf or an anyOf at the
// root, and four of those have no properties of their own: reading only the
// properties would describe those models as taking no input at all.
func inputGroups(input map[string]any) []inputGroup {
	var groups []inputGroup
	if fields := fieldsOf(input); len(fields) > 0 {
		groups = append(groups, inputGroup{fields: fields})
	}
	variant := 0
	for _, branch := range append(branchesOf(input, "oneOf"), branchesOf(input, "anyOf")...) {
		fields := fieldsOf(branch)
		if len(fields) == 0 {
			continue
		}
		variant++
		label := fmt.Sprintf("one of (variant %d)", variant)
		if title, ok := branch["title"].(string); ok && title != "" {
			label = fmt.Sprintf("one of (variant %d: %s)", variant, title)
		}
		groups = append(groups, inputGroup{label: label, fields: fields})
	}
	return groups
}

func branchesOf(input map[string]any, key string) []map[string]any {
	raw, _ := input[key].([]any)
	branches := make([]map[string]any, 0, len(raw))
	for _, b := range raw {
		if branch, ok := b.(map[string]any); ok {
			branches = append(branches, branch)
		}
	}
	return branches
}

// fieldsOf reads one object schema, required fields first: those are what the
// caller must supply, and the rest only matters once they are known.
func fieldsOf(schema map[string]any) []inputField {
	properties, _ := schema["properties"].(map[string]any)
	required := make(map[string]bool)
	if names, ok := schema["required"].([]any); ok {
		for _, n := range names {
			if name, ok := n.(string); ok {
				required[name] = true
			}
		}
	}

	fields := make([]inputField, 0, len(properties))
	for name, raw := range properties {
		property, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		fields = append(fields, newInputField(name, property, required[name]))
	}
	slices.SortFunc(fields, func(a, b inputField) int {
		if a.required != b.required {
			if a.required {
				return -1
			}
			return 1
		}
		return strings.Compare(a.name, b.name)
	})
	return fields
}

func newInputField(name string, property map[string]any, required bool) inputField {
	f := inputField{
		name:        name,
		typ:         stringOf(property["type"]),
		required:    required,
		description: oneLine(stringOf(property["description"])),
	}
	if v, ok := property["default"]; ok {
		f.def = renderValue(v)
	}
	if deprecated, _ := property["deprecated"].(bool); deprecated {
		f.notes = append(f.notes, "deprecated")
	}
	for _, key := range noteKeys {
		if v, ok := property[key]; ok {
			f.notes = append(f.notes, key+" "+renderValue(v))
		}
	}
	if values, ok := property["enum"].([]any); ok {
		for _, v := range values {
			f.enum = append(f.enum, renderValue(v))
		}
	}
	return f
}

// renderValue writes a schema value the way it would be typed: a string as
// itself, anything else as the JSON it is.
func renderValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(encoded)
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

// oneLine collapses the runs of whitespace a documentation string carries, so
// that a description written as a bulleted list still occupies one line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func requirement(required bool) string {
	if required {
		return "required"
	}
	return "optional"
}

// orDash keeps an empty cell from ending a line in the padding of the one
// before it, and reads as "nothing here" rather than as a missing column.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
