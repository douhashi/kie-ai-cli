package catalog_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
)

// downloadedID names a model that the embedded catalog cannot possibly carry,
// so a listing holding it can only have come from the directory on disk.
const downloadedID = "acme/only-in-the-downloaded-catalog"

// downloadedJSON is a catalog of one model, built rather than written out so
// that it cannot drift from the shape Catalog declares.
func downloadedJSON(t *testing.T, schemaVersion int) []byte {
	t.Helper()
	encoded, err := json.Marshal(catalog.Catalog{
		SchemaVersion: schemaVersion,
		Models: []catalog.Model{{
			ID: downloadedID, Name: "Only Downloaded", Category: "image", Vendor: "acme",
			DocsURL: "https://docs.kie.ai/acme",
			Create:  catalog.Create{Method: "POST", Path: "/api/v1/jobs", Style: catalog.StyleMarket, Model: downloadedID},
			Query:   catalog.Query{Method: "GET", Path: "/api/v1/jobs", Param: "taskId"},
			Input:   map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("marshal the downloaded catalog: %v", err)
	}
	return encoded
}

// writePair puts one catalog and its generation date where Load looks for them.
func writePair(t *testing.T, dir string, catalogJSON []byte, generatedAt string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}
	for name, content := range map[string][]byte{
		catalog.CatalogFile:     catalogJSON,
		catalog.GeneratedAtFile: []byte(generatedAt),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

// AC1: once a catalog has been downloaded it is the one that answers, so a
// model added since this binary was built can be run.
func TestLoadPrefersTheDownloadedCatalog(t *testing.T) {
	dir := t.TempDir()
	writePair(t, dir, downloadedJSON(t, catalog.SchemaVersion), "2026-08-20\n")

	got, err := catalog.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Origin != catalog.OriginDownloaded {
		t.Errorf("origin = %q, want %q", got.Origin, catalog.OriginDownloaded)
	}
	if got.Path != dir {
		t.Errorf("path = %q, want %q", got.Path, dir)
	}
	if len(got.Models) != 1 || got.Models[0].ID != downloadedID {
		t.Fatalf("models = %+v, want only %s", got.Models, downloadedID)
	}
	if want := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC); !got.GeneratedAt.Equal(want) {
		t.Errorf("generated at %s, want %s", got.GeneratedAt, want)
	}
}

// AC2: with nothing downloaded the binary answers from what it carries, and
// says so, which is what makes the CLI work on first run and offline.
func TestLoadFallsBackToTheEmbeddedCatalog(t *testing.T) {
	// A directory that does not exist yet is the state of a fresh install.
	for name, dir := range map[string]string{
		"an empty directory": t.TempDir(),
		"no directory":       filepath.Join(t.TempDir(), "never-created"),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := catalog.Load(dir)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got.Origin != catalog.OriginBuiltIn {
				t.Errorf("origin = %q, want %q", got.Origin, catalog.OriginBuiltIn)
			}
			if got.Path != "" {
				t.Errorf("path = %q, want none: the embedded catalog is not a file", got.Path)
			}
			if len(got.Models) == 0 {
				t.Error("the embedded catalog answered with no models")
			}
		})
	}
}

// A downloaded catalog that cannot be read is not the same as none: falling
// back would report an origin the user cannot act on, and would hide the
// broken files for good. Every case names the directory and both ways out.
func TestLoadRejectsABrokenDownloadedCatalog(t *testing.T) {
	valid := downloadedJSON(t, catalog.SchemaVersion)

	tests := []struct {
		name  string
		place func(t *testing.T, dir string)
	}{
		{
			name: "the catalog file is not JSON",
			place: func(t *testing.T, dir string) {
				writePair(t, dir, []byte("{"), "2026-08-20\n")
			},
		},
		{
			name: "the catalog is of another schema version",
			place: func(t *testing.T, dir string) {
				writePair(t, dir, downloadedJSON(t, catalog.SchemaVersion+1), "2026-08-20\n")
			},
		},
		{
			name: "the date is not a date",
			place: func(t *testing.T, dir string) {
				writePair(t, dir, valid, "yesterday\n")
			},
		},
		{
			// What an update interrupted between its two renames leaves.
			name: "the date is missing",
			place: func(t *testing.T, dir string) {
				writePair(t, dir, valid, "2026-08-20\n")
				if err := os.Remove(filepath.Join(dir, catalog.GeneratedAtFile)); err != nil {
					t.Fatalf("remove: %v", err)
				}
			},
		},
		{
			name: "the catalog is missing",
			place: func(t *testing.T, dir string) {
				writePair(t, dir, valid, "2026-08-20\n")
				if err := os.Remove(filepath.Join(dir, catalog.CatalogFile)); err != nil {
					t.Fatalf("remove: %v", err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.place(t, dir)

			_, err := catalog.Load(dir)
			if err == nil {
				t.Fatal("Load accepted a broken downloaded catalog instead of reporting it")
			}
			// The message has to leave the reader able to fix it without
			// knowing where this CLI keeps its files.
			for _, want := range []string{dir, "delete", "catalog update"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}
