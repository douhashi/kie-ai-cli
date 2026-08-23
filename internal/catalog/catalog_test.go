package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
)

// committed returns the generated catalog as the binary ships it: the copy
// embedded from catalog.json at build time. Checking it is a separate matter
// from checking the generator that produced it.
func committed(t *testing.T) catalog.Catalog {
	t.Helper()
	read, err := catalog.Load()
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
	subject := catalog.Catalog{GeneratedAt: generated}

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
