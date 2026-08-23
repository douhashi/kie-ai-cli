package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
	"github.com/douhashi/kie-ai-cli/internal/catalog/gen"
)

// committed returns the generated catalog as the binary ships it: the copy
// embedded from catalog.json at build time, which an empty directory is what
// it takes to reach. Checking it is a separate matter from checking the
// generator that produced it.
func committed(t *testing.T) catalog.Catalog {
	t.Helper()
	read, err := catalog.Load(t.TempDir())
	if err != nil {
		t.Fatalf("load the embedded catalog: %v", err)
	}
	if read.SchemaVersion != catalog.SchemaVersion {
		t.Fatalf("schemaVersion = %d, want %d", read.SchemaVersion, catalog.SchemaVersion)
	}
	if len(read.Models) == 0 {
		t.Fatal("catalog holds no models")
	}
	return read
}

// AC1: every model carries an id, both endpoints and an input schema, which is
// everything `task run` needs to submit and then follow a task.
func TestCommittedCatalogIsComplete(t *testing.T) {
	for _, model := range committed(t).Models {
		if model.ID == "" {
			t.Fatalf("a model has no id: %+v", model)
		}
		for field, value := range map[string]string{
			"name":          model.Name,
			"category":      model.Category,
			"vendor":        model.Vendor,
			"docsUrl":       model.DocsURL,
			"create.method": model.Create.Method,
			"create.path":   model.Create.Path,
			"create.style":  string(model.Create.Style),
			"query.method":  model.Query.Method,
			"query.path":    model.Query.Path,
			"query.param":   model.Query.Param,
		} {
			if value == "" {
				t.Errorf("%s: %s is empty", model.ID, field)
			}
		}
		if len(model.Input) == 0 {
			t.Errorf("%s: input schema is empty", model.ID)
		}
	}
}

func TestCommittedCatalogHasUniqueSortedIDs(t *testing.T) {
	seen := map[string]bool{}
	previous := ""
	for _, model := range committed(t).Models {
		if seen[model.ID] {
			t.Errorf("duplicate id %s", model.ID)
		}
		seen[model.ID] = true
		if model.ID < previous {
			t.Errorf("id %s comes after %s, but the catalog must be sorted", model.ID, previous)
		}
		previous = model.ID
	}
}

// The style decides how the CLI builds the request, so an unknown one would
// leave the model unrunnable.
func TestCommittedCatalogStylesAgreeWithTheirModelField(t *testing.T) {
	for _, model := range committed(t).Models {
		switch model.Create.Style {
		case catalog.StyleMarket:
			if model.Create.Model == "" {
				t.Errorf("%s: a Market create needs the model value to post", model.ID)
			}
		case catalog.StyleDirect:
			if model.Create.Model != "" {
				t.Errorf("%s: a direct create posts to its own path and takes no model value", model.ID)
			}
		default:
			t.Errorf("%s: unknown create style %q", model.ID, model.Create.Style)
		}
	}
}

// Half the pages open with a heading that llms.txt copies verbatim. A catalog
// full of "## Create Task" would make `model list` useless.
func TestCommittedCatalogDescriptionsAreProse(t *testing.T) {
	for _, model := range committed(t).Models {
		if strings.HasPrefix(model.Description, "#") || strings.HasPrefix(model.Description, ":::") {
			t.Errorf("%s: description is markup, not prose: %q", model.ID, model.Description)
		}
	}
}

// An input schema is shown to the user and validated against, so the docs
// site's own bookkeeping has no business being in it.
func TestCommittedCatalogInputsCarryNoVendorExtensions(t *testing.T) {
	for _, model := range committed(t).Models {
		encoded, err := json.Marshal(model.Input)
		if err != nil {
			t.Fatalf("%s: marshal input: %v", model.ID, err)
		}
		if strings.Contains(string(encoded), `"x-`) {
			t.Errorf("%s: input schema carries a vendor extension", model.ID)
		}
	}
}

// #8 filters on these two axes.
func TestCommittedCatalogAxesAreSlugs(t *testing.T) {
	for _, model := range committed(t).Models {
		for field, value := range map[string]string{"category": model.Category, "vendor": model.Vendor} {
			if value != strings.ToLower(value) || strings.ContainsAny(value, " \t") {
				t.Errorf("%s: %s %q is not typable as a filter value", model.ID, field, value)
			}
		}
	}
}

// AC1: the whole catalog is readable with no network of its own, because the
// bytes are already in the binary.
func TestEmbeddedCatalogHoldsEveryModel(t *testing.T) {
	onDisk, err := os.ReadFile("catalog.json")
	if err != nil {
		t.Fatalf("read catalog.json: %v", err)
	}
	var want catalog.Catalog
	if err := json.Unmarshal(onDisk, &want); err != nil {
		t.Fatalf("decode catalog.json: %v", err)
	}
	got := committed(t).Models
	if len(got) != len(want.Models) {
		t.Fatalf("the embedded catalog holds %d models, the file %d", len(got), len(want.Models))
	}
	for i, model := range got {
		if model.ID != want.Models[i].ID {
			t.Fatalf("model %d is %s embedded and %s on disk", i, model.ID, want.Models[i].ID)
		}
	}
}

// The date is embedded from a file of its own, so the two must agree.
func TestEmbeddedCatalogCarriesItsGenerationDate(t *testing.T) {
	got := committed(t).GeneratedAt
	if got.IsZero() {
		t.Fatal("the catalog carries no generation date")
	}
	if got.Location() != time.UTC {
		t.Errorf("generation date %s is not UTC", got)
	}
	raw, err := os.ReadFile(catalog.GeneratedAtFile)
	if err != nil {
		t.Fatalf("read %s: %v", catalog.GeneratedAtFile, err)
	}
	if want := got.Format(time.DateOnly) + "\n"; string(raw) != want {
		t.Errorf("%s is %q, want %q", catalog.GeneratedAtFile, raw, want)
	}
}

// AC2: a binary carrying a catalog older than MaxAge says so, and one carrying
// a fresh catalog stays quiet.
func TestStaleWarningFiresOnlyPastMaxAge(t *testing.T) {
	generated := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	subject := catalog.Catalog{GeneratedAt: generated, Origin: catalog.OriginBuiltIn}

	for _, tc := range []struct {
		days int
		warn bool
	}{
		// A clock behind the one that generated the catalog is not staleness.
		{days: -30, warn: false},
		{days: 0, warn: false},
		{days: 89, warn: false},
		{days: 90, warn: true},
		{days: 91, warn: true},
	} {
		now := generated.AddDate(0, 0, tc.days)
		got := subject.StaleWarning(now)
		if (got != "") != tc.warn {
			t.Errorf("%d days after generation: StaleWarning = %q, want warning: %v", tc.days, got, tc.warn)
			continue
		}
		if !tc.warn {
			continue
		}
		// A reader can only judge the warning with the date and the age in it.
		for _, want := range []string{"2026-08-23", strconv.Itoa(tc.days)} {
			if !strings.Contains(got, want) {
				t.Errorf("%d days after generation: StaleWarning = %q, want it to mention %s", tc.days, got, want)
			}
		}
	}
}

// The date rides beside the catalog rather than inside it: catalog.json is
// committed, so a regeneration that finds nothing new must produce no diff.
func TestCommittedCatalogHoldsNoGenerationDate(t *testing.T) {
	raw, err := os.ReadFile("catalog.json")
	if err != nil {
		t.Fatalf("read catalog.json: %v", err)
	}
	for _, unwanted := range []string{"generatedAt", committed(t).GeneratedAt.Format(time.DateOnly)} {
		if bytes.Contains(raw, []byte(unwanted)) {
			t.Errorf("catalog.json holds %q, which would make every regeneration a diff", unwanted)
		}
	}
}

// The warning names where the catalog in effect came from: the two are fixed by
// entirely different acts -- rebuilding the binary, or downloading again --
// and a reader who cannot tell which one they hold cannot pick.
func TestStaleWarningNamesTheOrigin(t *testing.T) {
	generated := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	now := generated.Add(catalog.MaxAge)

	for origin, other := range map[catalog.Origin]catalog.Origin{
		catalog.OriginBuiltIn:    catalog.OriginDownloaded,
		catalog.OriginDownloaded: catalog.OriginBuiltIn,
	} {
		got := catalog.Catalog{GeneratedAt: generated, Origin: origin}.StaleWarning(now)
		if !strings.Contains(got, string(origin)) {
			t.Errorf("StaleWarning for a %s catalog = %q, want it to say so", origin, got)
		}
		if strings.Contains(got, string(other)) {
			t.Errorf("StaleWarning for a %s catalog calls it %s: %q", origin, other, got)
		}
	}
}

// requiredNames reads the names one object schema insists on, in the order it
// lists them. The catalog is read straight from the committed JSON, so this is
// the schema's own list rather than anything the generator says about it.
func requiredNames(schema map[string]any) []string {
	raw, _ := schema["required"].([]any)
	names := make([]string, 0, len(raw))
	for _, value := range raw {
		if name, ok := value.(string); ok {
			names = append(names, name)
		}
	}
	return names
}

// walkObjects calls visit on every object schema reachable from schema,
// together with the names it lists as required. The catalog is checked by
// walking the committed JSON itself rather than by asking the generator, so a
// rule that stopped being applied shows up here.
func walkObjects(schema map[string]any, visit func(properties map[string]any, required map[string]bool)) {
	required := map[string]bool{}
	for _, name := range requiredNames(schema) {
		required[name] = true
	}
	properties, _ := schema["properties"].(map[string]any)
	if properties != nil {
		visit(properties, required)
	}
	for _, property := range properties {
		if property, ok := property.(map[string]any); ok {
			walkObjects(property, visit)
		}
	}
	for _, keyword := range []string{"oneOf", "anyOf", "allOf"} {
		members, _ := schema[keyword].([]any)
		for _, member := range members {
			if member, ok := member.(map[string]any); ok {
				walkObjects(member, visit)
			}
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		walkObjects(items, visit)
	}
}

// AC3: kie.ai says in prose that some properties are needed on every request
// while leaving them out of the required list. Each such disagreement is
// settled by a request made against the real API and pinned in
// internal/catalog/gen/required.go, so the committed catalog must hold, for
// every model, either the corrected required list or the measurement saying
// the endpoint takes the request without it.
func TestCommittedCatalogAgreesWithTheMeasuredRequirements(t *testing.T) {
	// The four disagreements known when this was written, and the verdict
	// measured for each. Listing them also keeps the test from passing
	// because the walk stopped reaching anything.
	seen := map[string]bool{}
	want := map[string]bool{
		"suno-api/generate-lyrics.callBackUrl":     true,
		"suno-api/cover-suno.callBackUrl":          true,
		"runway-api/extend-ai-video.callBackUrl":   false,
		"runway-api/generate-ai-video.callBackUrl": false,
	}
	for _, model := range committed(t).Models {
		walkObjects(model.Input, func(properties map[string]any, required map[string]bool) {
			for name, property := range properties {
				property, ok := property.(map[string]any)
				if !ok {
					continue
				}
				description, _ := property["description"].(string)
				if !gen.UnconditionallyRequired(description) {
					continue
				}
				measured, pinned := gen.MeasuredRequired(model.ID, name)
				path := model.ID + "." + name
				if _, known := want[path]; known {
					seen[path] = true
					if pinned && measured != want[path] {
						t.Errorf("%s: measured as required=%v, want %v", path, measured, want[path])
					}
				}
				// Where a request was made, its answer is the authority
				// in both directions: the field is required exactly when
				// kie.ai refused to do without it.
				if pinned {
					if required[name] != measured {
						t.Errorf("%s: kie.ai refuses a request without it: %v, but the catalog requires it: %v",
							path, measured, required[name])
					}
					continue
				}
				if !required[name] {
					t.Errorf("%s: described as required for every request, absent from required, and not measured", path)
				}
			}
		})
	}
	for path := range want {
		if !seen[path] {
			t.Errorf("%s no longer carries the description the correction reads; the walk is not reaching it", path)
		}
	}
}

// The correction reads the wording, and over-requiring is the same mistake in
// the other direction: a property that is required only in some mode must stay
// optional, or `task run` would refuse a request kie.ai accepts.
func TestCommittedCatalogLeavesConditionallyRequiredPropertiesOptional(t *testing.T) {
	conditional := map[string][]string{
		"suno-api/generate-music":                 {"style", "title"},
		"suno-api/extend-music":                   {"continueAt", "prompt", "style", "title"},
		"suno-api/upload-and-cover-audio":         {"style", "title"},
		"flux-kontext-api/generate-or-edit-image": {"inputImage"},
		"4o-image-api/generate-4-o-image":         {"prompt"},
	}
	byID := map[string]catalog.Model{}
	for _, model := range committed(t).Models {
		byID[model.ID] = model
	}
	for id, names := range conditional {
		model, ok := byID[id]
		if !ok {
			t.Errorf("%s is no longer in the catalog", id)
			continue
		}
		required := requiredNames(model.Input)
		properties, _ := model.Input["properties"].(map[string]any)
		for _, name := range names {
			if _, ok := properties[name]; !ok {
				t.Errorf("%s: %s is no longer a property, so this case no longer guards anything", id, name)
			}
			if slices.Contains(required, name) {
				t.Errorf("%s: %s is required only under a condition and must not be listed as required", id, name)
			}
		}
	}
}

// AC1, AC2: a Market input schema may name nothing as required at all --
// neither the root object nor the alternatives beside it -- which leaves
// `task run` nothing to check a request against. What those models really
// insist on cannot be read off the page, so it is measured against the live
// API and pinned in internal/catalog/gen/required.go, and the committed
// catalog must hold what was measured.
//
// A model nobody has measured is not an error here. Requiring nothing is the
// ordinary shape of a Market schema, the models turn over quickly, and the
// under-strict side costs a 422 rather than a request kie.ai would have
// taken; the crawl reports those models instead, see #35.
func TestCommittedCatalogAgreesWithTheMeasuredInputRequirements(t *testing.T) {
	// The six that declared nothing when this was written. Naming them keeps
	// the check from passing because the catalog no longer holds any.
	silent := []string{
		"bytedance/seedance-2",
		"bytedance/seedance-2-5",
		"bytedance/seedance-2-fast",
		"bytedance/seedance-2-mini",
		"grok-imagine-video-1-5-preview",
		"grok-imagine/image-to-video",
	}
	models := committed(t).Models
	byID := map[string]catalog.Model{}
	for _, model := range models {
		byID[model.ID] = model
	}
	for _, id := range silent {
		if _, ok := byID[id]; !ok {
			t.Errorf("%s is no longer in the catalog", id)
			continue
		}
		if _, pinned := gen.MeasuredInputRequired(id); !pinned {
			t.Errorf("%s: requires nothing of its own and nobody has measured what it needs", id)
		}
	}

	for _, model := range models {
		want, pinned := gen.MeasuredInputRequired(model.ID)
		if !pinned {
			continue
		}
		want = slices.Compact(slices.Sorted(slices.Values(want)))
		got := requiredNames(model.Input)
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: the catalog requires %v, but a request was measured to need %v", model.ID, got, want)
		}
	}
}
