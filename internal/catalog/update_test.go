// This test is in the package rather than beside it because it points the
// downloader at a server of its own: the address of the published assets and
// the size it will accept are deliberately not part of the API.
package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// theirID is a model no build of this binary carries, so finding it proves the
// answer came from the server rather than from the embedded copy.
const theirID = "acme/published-since-this-build"

// published is a catalog of one model, as the release serves it.
func published(t *testing.T, schemaVersion int, models []Model) []byte {
	t.Helper()
	encoded, err := json.Marshal(Catalog{SchemaVersion: schemaVersion, Models: models})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return encoded
}

func oneModel() []Model {
	return []Model{{
		ID: theirID, Name: "Published Since", Category: "image", Vendor: "acme",
		DocsURL: "https://docs.kie.ai/acme",
		Create:  Create{Method: "POST", Path: "/api/v1/jobs", Style: StyleMarket, Model: theirID},
		Query:   Query{Method: "GET", Path: "/api/v1/jobs", Param: "taskId"},
		Input:   map[string]any{"type": "object"},
	}}
}

// serve stands in for the release, handing out whatever the test names each
// asset, and points Update at itself for the duration of the test.
func serve(t *testing.T, assets map[string][]byte) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := assets[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			// What the release answers for an asset it does not hold.
			http.NotFound(w, r)
			return
		}
		// A truncated download: the length promised is not the length sent,
		// so the reader is cut off mid-way as a dropped connection would.
		if rest, cut := strings.CutPrefix(string(body), "truncate:"); cut {
			w.Header().Set("Content-Length", "1048576")
			_, _ = w.Write([]byte(rest))
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	previous := baseURL
	baseURL = server.URL + "/"
	t.Cleanup(func() { baseURL = previous })
}

// AC1: the published catalog replaces what the binary carries, and the files
// left behind are the bytes that were served, so the next run reads the same
// catalog this one downloaded.
func TestUpdateWritesThePublishedPair(t *testing.T) {
	catalogJSON := published(t, SchemaVersion, oneModel())
	generatedAt := []byte("2026-08-24\n")
	serve(t, map[string][]byte{CatalogFile: catalogJSON, GeneratedAtFile: generatedAt})
	dir := filepath.Join(t.TempDir(), "catalog")

	got, err := Update(context.Background(), dir)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Origin != OriginDownloaded || got.Path != dir {
		t.Errorf("origin %q at %q, want %q at %q", got.Origin, got.Path, OriginDownloaded, dir)
	}
	if len(got.Models) != 1 || got.Models[0].ID != theirID {
		t.Fatalf("models = %+v, want only %s", got.Models, theirID)
	}
	if want := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC); !got.GeneratedAt.Equal(want) {
		t.Errorf("generated at %s, want %s", got.GeneratedAt, want)
	}

	for name, want := range map[string][]byte{CatalogFile: catalogJSON, GeneratedAtFile: generatedAt} {
		if read, err := os.ReadFile(filepath.Join(dir, name)); err != nil {
			t.Errorf("read %s: %v", name, err)
		} else if string(read) != string(want) {
			t.Errorf("%s on disk is %q, want the bytes that were served", name, read)
		}
	}

	// What was written is what the next run reads.
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load after Update: %v", err)
	}
	if len(reloaded.Models) != 1 || reloaded.Models[0].ID != theirID {
		t.Errorf("the reloaded catalog holds %+v, want only %s", reloaded.Models, theirID)
	}
}

// AC3: after an update the shell completes the models that were downloaded.
// The index is derived from the catalog rather than published beside it, so
// there is no third asset that can be missing or disagree with the other two.
func TestUpdateDerivesTheIndexFromTheCatalogItDownloaded(t *testing.T) {
	serve(t, map[string][]byte{
		CatalogFile:     published(t, SchemaVersion, oneModel()),
		GeneratedAtFile: []byte("2026-08-24\n"),
	})
	dir := t.TempDir()

	if _, err := Update(context.Background(), dir); err != nil {
		t.Fatalf("Update: %v", err)
	}
	want, err := RenderIndex(oneModel())
	if err != nil {
		t.Fatalf("RenderIndex: %v", err)
	}
	read, err := os.ReadFile(filepath.Join(dir, IndexFile))
	if err != nil {
		t.Fatalf("read %s: %v", IndexFile, err)
	}
	if string(read) != string(want) {
		t.Errorf("%s is %q, want the index of the catalog that was downloaded", IndexFile, read)
	}

	entries, err := LoadIndex(dir)
	if err != nil {
		t.Fatalf("LoadIndex after Update: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != theirID {
		t.Errorf("the index holds %+v, want only %s", entries, theirID)
	}
}

// A failed update leaves the catalog that was already there: a user whose
// download fails keeps a working CLI, and one who has never downloaded keeps
// the embedded catalog rather than a directory of rubble.
func TestUpdateFailuresLeaveTheCatalogAlone(t *testing.T) {
	good := published(t, SchemaVersion, oneModel())

	tests := []struct {
		name   string
		assets map[string][]byte
		// want is a phrase the message has to carry, so that the reason is
		// distinguishable from every other reason an update can fail.
		want string
	}{
		{
			name:   "the catalog is not published",
			assets: map[string][]byte{GeneratedAtFile: []byte("2026-08-24\n")},
			want:   "404",
		},
		{
			// The pair is uploaded one asset at a time, so this is what a
			// download landing between the two uploads can meet.
			name:   "the date is not published",
			assets: map[string][]byte{CatalogFile: good},
			want:   "404",
		},
		{
			name:   "the catalog is not JSON",
			assets: map[string][]byte{CatalogFile: []byte("<html>rate limited</html>"), GeneratedAtFile: []byte("2026-08-24\n")},
			want:   "decode " + CatalogFile,
		},
		{
			name:   "the catalog is of another schema version",
			assets: map[string][]byte{CatalogFile: published(t, SchemaVersion+1, oneModel()), GeneratedAtFile: []byte("2026-08-24\n")},
			want:   "schema version",
		},
		{
			name:   "the catalog holds no models",
			assets: map[string][]byte{CatalogFile: published(t, SchemaVersion, nil), GeneratedAtFile: []byte("2026-08-24\n")},
			want:   "no models",
		},
		{
			name:   "the date is not a date",
			assets: map[string][]byte{CatalogFile: good, GeneratedAtFile: []byte("2026-08\n")},
			want:   "read " + GeneratedAtFile,
		},
		{
			// The index is derived rather than downloaded, so a catalog it
			// cannot be derived from is caught here rather than by the shell.
			name: "a model the index cannot hold",
			assets: map[string][]byte{
				CatalogFile:     published(t, SchemaVersion, []Model{{ID: "acme/one\ttwo", Category: "image", Vendor: "acme"}}),
				GeneratedAtFile: []byte("2026-08-24\n"),
			},
			want: IndexFile,
		},
		{
			name:   "the download is cut short",
			assets: map[string][]byte{CatalogFile: []byte("truncate:" + string(good)), GeneratedAtFile: []byte("2026-08-24\n")},
			want:   "unexpected EOF",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serve(t, tt.assets)
			dir := t.TempDir()
			// The state an update has to protect: a catalog already
			// downloaded, which a half-finished write would destroy.
			before := map[string][]byte{
				CatalogFile:     published(t, SchemaVersion, []Model{{ID: "acme/downloaded-earlier"}}),
				GeneratedAtFile: []byte("2026-08-01\n"),
			}
			for name, content := range before {
				if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
			}

			_, err := Update(context.Background(), dir)
			if err == nil {
				t.Fatal("Update reported success on an unusable download")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not say %q", err, tt.want)
			}

			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("read %s: %v", dir, err)
			}
			if len(entries) != len(before) {
				t.Errorf("%s holds %d files, want the %d that were there", dir, len(entries), len(before))
			}
			for name, want := range before {
				read, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Errorf("read %s: %v", name, err)
					continue
				}
				if string(read) != string(want) {
					t.Errorf("%s changed to %q, want the bytes from before the failed update", name, read)
				}
			}
		})
	}
}

// An asset larger than the catalog could plausibly be is refused before it is
// read, so that the far side cannot decide how much memory this process uses.
func TestUpdateRefusesAnOversizedAsset(t *testing.T) {
	previous := maxAssetBytes
	maxAssetBytes = 64
	t.Cleanup(func() { maxAssetBytes = previous })

	serve(t, map[string][]byte{
		CatalogFile:     published(t, SchemaVersion, oneModel()),
		GeneratedAtFile: []byte("2026-08-24\n"),
	})
	dir := t.TempDir()

	_, err := Update(context.Background(), dir)
	if err == nil {
		t.Fatal("Update accepted an asset past the size it will read")
	}
	if !strings.Contains(err.Error(), "64") {
		t.Errorf("error %q does not say what the limit is", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("%s holds %d files, want nothing written", dir, len(entries))
	}
}
