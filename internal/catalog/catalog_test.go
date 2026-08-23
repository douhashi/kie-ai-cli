package catalog_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
)

// committed reads the generated catalog as it stands in the repository. It is
// what the binary will ship, so it is worth checking on its own rather than
// only checking the generator that produced it.
func committed(t *testing.T) catalog.Catalog {
	t.Helper()
	raw, err := os.ReadFile("catalog.json")
	if err != nil {
		t.Fatalf("read catalog.json: %v", err)
	}
	var read catalog.Catalog
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatalf("decode catalog.json: %v", err)
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

// #1.3 embeds this file into the binary, and #8 filters on these two axes.
func TestCommittedCatalogAxesAreSlugs(t *testing.T) {
	for _, model := range committed(t).Models {
		for field, value := range map[string]string{"category": model.Category, "vendor": model.Vendor} {
			if value != strings.ToLower(value) || strings.ContainsAny(value, " \t") {
				t.Errorf("%s: %s %q is not typable as a filter value", model.ID, field, value)
			}
		}
	}
}
