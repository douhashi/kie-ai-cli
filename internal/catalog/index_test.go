package catalog_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
)

// AC3/V4: the index is derived from the catalog, so a committed index that
// says anything else is a lie the shell would repeat on every TAB. Both are
// generated together; this is what keeps a hand-edited one from surviving.
func TestCommittedIndexIsDerivedFromTheCommittedCatalog(t *testing.T) {
	models := committed(t).Models
	want, err := catalog.RenderIndex(models)
	if err != nil {
		t.Fatalf("RenderIndex: %v", err)
	}
	got, err := os.ReadFile(catalog.IndexFile)
	if err != nil {
		t.Fatalf("read the committed index: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("%s is not what the committed catalog renders to; run `mise run catalog`", catalog.IndexFile)
	}

	// The file the test read and the bytes the binary carries are the same
	// file only if the embed directive names it, which nothing else checks.
	entries, err := catalog.LoadIndex(t.TempDir())
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if len(entries) != len(models) {
		t.Fatalf("the embedded index holds %d models, the embedded catalog %d", len(entries), len(models))
	}
	for i, e := range entries {
		if e != (catalog.IndexEntry{ID: models[i].ID, Category: models[i].Category, Vendor: models[i].Vendor}) {
			t.Errorf("entry %d is %+v, want the model %+v", i, e, models[i])
		}
	}
}

// A field that cannot be written as one line would read back as another model
// altogether. The catalog is generated from documentation this project does
// not own, so the values are checked rather than trusted.
func TestRenderIndexRefusesAFieldItCannotWrite(t *testing.T) {
	for name, model := range map[string]catalog.Model{
		"a tab in the vendor":      {ID: "acme/one", Category: "image", Vendor: "ac\tme"},
		"a newline in the id":      {ID: "acme/\none", Category: "image", Vendor: "acme"},
		"a category that is empty": {ID: "acme/one", Vendor: "acme"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := catalog.RenderIndex([]catalog.Model{model}); err == nil {
				t.Fatal("RenderIndex wrote a model it cannot read back")
			}
		})
	}
}

// AC3: once a catalog has been downloaded, the shell completes the models it
// holds rather than the ones this binary was built with.
func TestLoadIndexPrefersTheDownloadedIndex(t *testing.T) {
	dir := t.TempDir()
	writeDownloaded(t, dir, downloadedJSON(t, catalog.SchemaVersion), "2026-08-20\n")

	entries, err := catalog.LoadIndex(dir)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != downloadedID {
		t.Fatalf("entries = %+v, want only %s", entries, downloadedID)
	}
}

// Deleting the directory is what returns the shell to the models the binary
// carries, the same way it returns the catalog.
func TestLoadIndexFallsBackToTheEmbeddedIndex(t *testing.T) {
	entries, err := catalog.LoadIndex(t.TempDir())
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("the embedded index holds no models")
	}
	for _, e := range entries {
		if e.ID == downloadedID {
			t.Errorf("the embedded index holds %s, which only a downloaded catalog can", e.ID)
		}
	}
}

// A downloaded catalog with no index beside it is not an absence: completing
// from the embedded index would offer the models of a catalog that is not the
// one in effect, and nothing about the answer would look wrong.
func TestLoadIndexRejectsADownloadedCatalogWithNoIndex(t *testing.T) {
	dir := t.TempDir()
	writeDownloaded(t, dir, downloadedJSON(t, catalog.SchemaVersion), "2026-08-20\n")
	if err := os.Remove(filepath.Join(dir, catalog.IndexFile)); err != nil {
		t.Fatalf("remove: %v", err)
	}

	_, err := catalog.LoadIndex(dir)
	if err == nil {
		t.Fatal("LoadIndex answered from the embedded index while a downloaded catalog sat beside it")
	}
	for _, want := range []string{dir, "delete", "catalog update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestLoadIndexRejectsABrokenIndex(t *testing.T) {
	for name, content := range map[string]string{
		"a line with no vendor":          "acme/one\timage\n",
		"a line with a field left empty": "acme/one\t\tacme\n",
		"nothing at all":                 "",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeDownloaded(t, dir, downloadedJSON(t, catalog.SchemaVersion), "2026-08-20\n")
			if err := os.WriteFile(filepath.Join(dir, catalog.IndexFile), []byte(content), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}

			if _, err := catalog.LoadIndex(dir); err == nil {
				t.Fatal("LoadIndex accepted an index it cannot read")
			}
		})
	}
}
