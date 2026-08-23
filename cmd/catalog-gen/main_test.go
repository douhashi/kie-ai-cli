package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// site serves an llms.txt and the pages it lists, standing in for docs.kie.ai
// over real HTTP so the whole command is exercised, network layer included.
func site(t *testing.T, files map[string]string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func fixture(t *testing.T, parts ...string) string {
	t.Helper()
	root := filepath.Join("..", "..", "internal", "catalog", "gen", "testdata")
	body, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(body)
}

// index writes an llms.txt whose entries point at the given server.
func index(base string, entries map[string]string) string {
	var b strings.Builder
	b.WriteString("# docs\n\n## API Docs\n")
	for path, breadcrumb := range entries {
		fmt.Fprintf(&b, "- %s [Page](%s/%s.md): a page\n", breadcrumb, base, path)
	}
	return b.String()
}

func TestRunWritesTheCatalog(t *testing.T) {
	files := map[string]string{
		"/market/seedream/seedream-v4-text-to-image.md": fixture(t, "pages", "market", "seedream", "seedream-v4-text-to-image.md"),
		"/market/common/get-task-detail.md":             fixture(t, "pages", "market", "common", "get-task-detail.md"),
	}
	base := site(t, files)
	files["/llms.txt"] = index(base, map[string]string{
		"market/seedream/seedream-v4-text-to-image": "Image Models > Seedream",
		"market/common/get-task-detail":             "",
	})

	out := filepath.Join(t.TempDir(), "catalog.json")
	err := run(t.Context(), options{indexURL: base + "/llms.txt", out: out, concurrency: 2, interval: time.Microsecond})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	if !strings.Contains(string(written), "bytedance/seedream-v4-text-to-image") {
		t.Errorf("catalog does not hold the model: %s", written)
	}
}

// AC3: a page the generator cannot read stops the run, and whatever catalog is
// already committed stays untouched rather than losing the models that failed.
func TestRunLeavesTheCatalogAloneWhenAPageIsUnexpected(t *testing.T) {
	const existing = `{"schemaVersion":1,"models":[]}`

	for name, broken := range map[string]string{
		"no OpenAPI block":  fixture(t, "broken", "no-yaml.md"),
		"two paths":         fixture(t, "broken", "two-paths.md"),
		"ambiguous model":   fixture(t, "broken", "two-models.md"),
		"unresolvable $ref": fixture(t, "broken", "dangling-ref.md"),
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{
				"/market/seedream/broken.md":        broken,
				"/market/common/get-task-detail.md": fixture(t, "pages", "market", "common", "get-task-detail.md"),
			}
			base := site(t, files)
			files["/llms.txt"] = index(base, map[string]string{
				"market/seedream/broken":        "Image Models > Seedream",
				"market/common/get-task-detail": "",
			})

			out := filepath.Join(t.TempDir(), "catalog.json")
			if err := os.WriteFile(out, []byte(existing), 0o644); err != nil {
				t.Fatalf("seed catalog: %v", err)
			}

			err := run(t.Context(), options{indexURL: base + "/llms.txt", out: out, concurrency: 2, interval: time.Microsecond})
			if err == nil {
				t.Fatal("want an error")
			}
			after, readErr := os.ReadFile(out)
			if readErr != nil {
				t.Fatalf("read catalog: %v", readErr)
			}
			if string(after) != existing {
				t.Errorf("catalog was rewritten by a failed run: %s", after)
			}
		})
	}
}

// AC2: two runs over the same pages must agree byte for byte, or the scheduled
// regeneration in #5 would open a pull request every time it runs.
func TestRunIsReproducible(t *testing.T) {
	files := map[string]string{
		"/market/seedream/seedream-v4-text-to-image.md": fixture(t, "pages", "market", "seedream", "seedream-v4-text-to-image.md"),
		"/suno-api/generate-music.md":                   fixture(t, "pages", "suno-api", "generate-music.md"),
		"/suno-api/get-music-details.md":                fixture(t, "pages", "suno-api", "get-music-details.md"),
		"/market/common/get-task-detail.md":             fixture(t, "pages", "market", "common", "get-task-detail.md"),
	}
	base := site(t, files)
	files["/llms.txt"] = index(base, map[string]string{
		"market/seedream/seedream-v4-text-to-image": "Image Models > Seedream",
		"suno-api/generate-music":                   "Suno API > Music Generation",
		"suno-api/get-music-details":                "Suno API > Music Generation",
		"market/common/get-task-detail":             "",
	})

	dir := t.TempDir()
	var first string
	for attempt := range 3 {
		out := filepath.Join(dir, fmt.Sprintf("catalog-%d.json", attempt))
		if err := run(t.Context(), options{indexURL: base + "/llms.txt", out: out, pagesDir: filepath.Join(dir, "pages"), concurrency: 2, interval: time.Microsecond}); err != nil {
			t.Fatalf("run: %v", err)
		}
		written, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("read catalog: %v", err)
		}
		if attempt == 0 {
			first = string(written)
			continue
		}
		if string(written) != first {
			t.Fatal("two runs over the same pages produced different bytes")
		}
	}
}
