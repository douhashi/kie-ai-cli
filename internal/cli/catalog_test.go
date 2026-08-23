package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
	"github.com/douhashi/kie-ai-cli/internal/paths"
)

// catalogState mirrors the JSON contract of catalog show, which is how a
// script asks which catalog it is about to run against.
type catalogState struct {
	Origin      string `json:"origin"`
	Path        string `json:"path"`
	GeneratedAt string `json:"generatedAt"`
	AgeDays     int    `json:"ageDays"`
	Models      int    `json:"models"`
}

// newModelID names a model that cannot be in the embedded catalog, so a
// command that answers with it can only be reading the downloaded one.
const newModelID = "acme/published-after-this-build"

// download puts a catalog of one model where the CLI keeps the downloaded one,
// which is the state `catalog update` leaves behind: the two published files
// and the index derived from them.
func download(t *testing.T, layout paths.Layout, generatedAt string) {
	t.Helper()
	models := []catalog.Model{{
		ID: newModelID, Name: "Published After", Category: "image", Vendor: "acme",
		DocsURL: "https://docs.kie.ai/acme",
		Create:  catalog.Create{Method: "POST", Path: "/api/v1/jobs", Style: catalog.StyleMarket, Model: newModelID},
		Query:   catalog.Query{Method: "GET", Path: "/api/v1/jobs", Param: "taskId"},
		Input:   map[string]any{"type": "object"},
	}}
	encoded, err := json.Marshal(catalog.Catalog{SchemaVersion: catalog.SchemaVersion, Models: models})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	index, err := catalog.RenderIndex(models)
	if err != nil {
		t.Fatalf("RenderIndex: %v", err)
	}
	if err := os.MkdirAll(layout.Catalog, 0o700); err != nil {
		t.Fatalf("create %s: %v", layout.Catalog, err)
	}
	for name, content := range map[string][]byte{
		catalog.CatalogFile:     encoded,
		catalog.IndexFile:       index,
		catalog.GeneratedAtFile: []byte(generatedAt + "\n"),
	} {
		if err := os.WriteFile(filepath.Join(layout.Catalog, name), content, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func showCatalog(t *testing.T) catalogState {
	t.Helper()
	got := run(t, "catalog", "show", "--json")
	if got.code != 0 {
		t.Fatalf("catalog show --json: code %d, stderr %q", got.code, got.stderr)
	}
	var s catalogState
	if err := json.Unmarshal([]byte(got.stdout), &s); err != nil {
		t.Fatalf("output is not JSON (%v):\n%s", err, got.stdout)
	}
	return s
}

// AC2: with nothing downloaded, what answers is the catalog in the binary, and
// catalog show says so rather than leaving the user to guess.
func TestCatalogShowReportsTheBuiltInCatalog(t *testing.T) {
	isolate(t)
	c := embedded(t)

	s := showCatalog(t)
	if s.Origin != string(catalog.OriginBuiltIn) {
		t.Errorf("origin = %q, want %q", s.Origin, catalog.OriginBuiltIn)
	}
	if s.Path != "" {
		t.Errorf("path = %q, want none: the embedded catalog is not a file on disk", s.Path)
	}
	if want := c.GeneratedAt.Format(time.DateOnly); s.GeneratedAt != want {
		t.Errorf("generatedAt = %q, want %q", s.GeneratedAt, want)
	}
	if s.Models != len(c.Models) {
		t.Errorf("models = %d, want %d", s.Models, len(c.Models))
	}

	text := run(t, "catalog", "show")
	if text.code != 0 {
		t.Fatalf("catalog show: code %d, stderr %q", text.code, text.stderr)
	}
	for _, want := range []string{string(catalog.OriginBuiltIn), s.GeneratedAt} {
		if !strings.Contains(text.stdout, want) {
			t.Errorf("catalog show lacks %q:\n%s", want, text.stdout)
		}
	}
}

// AC1 and AC2: once a catalog has been downloaded it is the one every command
// reads, and catalog show names it and says where it is -- which is the whole
// answer to going back, since deleting that directory is what does it.
func TestCatalogShowReportsTheDownloadedCatalog(t *testing.T) {
	layout := isolate(t)
	download(t, layout, "2026-08-20")

	s := showCatalog(t)
	want := catalogState{
		Origin:      string(catalog.OriginDownloaded),
		Path:        layout.Catalog,
		GeneratedAt: "2026-08-20",
		AgeDays:     s.AgeDays,
		Models:      1,
	}
	if s != want {
		t.Errorf("state = %+v, want %+v", s, want)
	}

	text := run(t, "catalog", "show")
	if text.code != 0 {
		t.Fatalf("catalog show: code %d, stderr %q", text.code, text.stderr)
	}
	for _, want := range []string{string(catalog.OriginDownloaded), layout.Catalog, "2026-08-20"} {
		if !strings.Contains(text.stdout, want) {
			t.Errorf("catalog show lacks %q:\n%s", want, text.stdout)
		}
	}

	// AC1: the point of downloading is that the models it brought can be run,
	// so the listing is the downloaded one rather than the embedded one.
	list := run(t, "model", "list")
	if list.code != 0 {
		t.Fatalf("model list: code %d, stderr %q", list.code, list.stderr)
	}
	if rows := lines(list.stdout); len(rows) != 1 || !strings.HasPrefix(rows[0], newModelID+" ") {
		t.Errorf("model list = %q, want only the downloaded model %s", rows, newModelID)
	}
	if show := run(t, "model", "show", newModelID); show.code != 0 {
		t.Errorf("model show %s: code %d, stderr %q", newModelID, show.code, show.stderr)
	}
}

// A downloaded catalog that cannot be read stops the command instead of
// quietly answering from the embedded one, which would report an origin the
// files on disk contradict. Every command that reads the catalog does so.
func TestABrokenDownloadedCatalogIsReported(t *testing.T) {
	for _, args := range [][]string{
		{"catalog", "show"},
		{"model", "list"},
		{"model", "show", newModelID},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			layout := isolate(t)
			download(t, layout, "2026-08-20")
			broken := filepath.Join(layout.Catalog, catalog.CatalogFile)
			if err := os.WriteFile(broken, []byte("{"), 0o600); err != nil {
				t.Fatalf("write %s: %v", broken, err)
			}

			got := run(t, args...)
			if got.code != 1 {
				t.Fatalf("code = %d, want 1 (stderr %q)", got.code, got.stderr)
			}
			if got.stdout != "" {
				t.Errorf("stdout = %q, want nothing on the success stream", got.stdout)
			}
			if strings.Contains(got.stderr, "Usage:") {
				t.Errorf("a broken downloaded catalog is not a usage mistake:\n%s", got.stderr)
			}
			for _, want := range []string{layout.Catalog, "delete", "catalog update"} {
				if !strings.Contains(got.stderr, want) {
					t.Errorf("stderr does not mention %q:\n%s", want, got.stderr)
				}
			}
		})
	}
}

// AC2: a catalog old enough to be misleading says so whichever copy it is,
// and says which one, because the two are refreshed differently.
func TestAnOldDownloadedCatalogWarns(t *testing.T) {
	layout := isolate(t)
	old := time.Now().UTC().Add(-catalog.MaxAge - 24*time.Hour)
	download(t, layout, old.Format(time.DateOnly))

	got := run(t, "catalog", "show", "--json")
	if got.code != 0 {
		t.Fatalf("catalog show: code %d, stderr %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, string(catalog.OriginDownloaded)) {
		t.Errorf("stderr does not warn about the downloaded catalog's age:\n%s", got.stderr)
	}
	// The warning stays off stdout so that --json remains one document.
	var s catalogState
	if err := json.Unmarshal([]byte(got.stdout), &s); err != nil {
		t.Fatalf("output is not JSON (%v):\n%s", err, got.stdout)
	}
}
