package llms_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/catalog/gen/llms"
)

func readFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", "llms.txt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(b)
}

func TestParseAPIDocsKeepsOnlyEnglishAPIDocs(t *testing.T) {
	entries, err := llms.ParseAPIDocs(readFixture(t))
	if err != nil {
		t.Fatalf("ParseAPIDocs: %v", err)
	}

	var paths []string
	for _, e := range entries {
		paths = append(paths, e.DocsPath())
	}
	want := []string{
		"market/seedream/seedream-v4-text-to-image",
		"market/claude/claude-opus-5",
		"suno-api/generate-music",
		"suno-api/get-music-details",
		"market/common/get-task-detail",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("docs paths = %v, want %v", paths, want)
	}
}

func TestParseAPIDocsReadsEveryField(t *testing.T) {
	entries, err := llms.ParseAPIDocs(readFixture(t))
	if err != nil {
		t.Fatalf("ParseAPIDocs: %v", err)
	}

	got := entries[0]
	want := llms.Entry{
		Breadcrumb:  []string{"Image Models", "Seedream"},
		URL:         "https://docs.kie.ai/market/seedream/seedream-v4-text-to-image.md",
		Description: "High-quality photorealistic image generation powered by Seedream4.0's advanced AI model",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entry = %+v, want %+v", got, want)
	}
}

func TestParseAPIDocsAcceptsEntryWithoutBreadcrumb(t *testing.T) {
	entries, err := llms.ParseAPIDocs(readFixture(t))
	if err != nil {
		t.Fatalf("ParseAPIDocs: %v", err)
	}

	last := entries[len(entries)-1]
	if len(last.Breadcrumb) != 0 {
		t.Errorf("breadcrumb = %v, want empty", last.Breadcrumb)
	}
	if last.DocsPath() != "market/common/get-task-detail" {
		t.Errorf("docs path = %q", last.DocsPath())
	}
}

func TestParseAPIDocsRejectsUnparsableLine(t *testing.T) {
	_, err := llms.ParseAPIDocs("## API Docs\n- Image Models > Seedream no link here\n")
	if err == nil {
		t.Fatal("want error for a line that is not an entry")
	}
	if !strings.Contains(err.Error(), "no link here") {
		t.Errorf("error should quote the offending line, got %v", err)
	}
}

func TestParseAPIDocsRejectsMissingSection(t *testing.T) {
	if _, err := llms.ParseAPIDocs("# docs.kie.ai\n\n## Docs\n- [Market](https://docs.kie.ai/x.md): \n"); err == nil {
		t.Fatal("want error when the API Docs section is absent")
	}
}

func TestTaxonomy(t *testing.T) {
	tests := []struct {
		name         string
		breadcrumb   []string
		wantCategory string
		wantVendor   string
	}{
		{"models suffix", []string{"Image Models", "Seedream"}, "image", "seedream"},
		{"multi word vendor", []string{"Image Models", "Grok Imagine"}, "image", "grok-imagine"},
		{"vendor api suffix", []string{"Image Models", "4o Image API"}, "image", "4o-image"},
		{"third level ignored", []string{"Video Models", "Runway API", "Aleph"}, "video", "runway"},
		{"suno alias", []string{"Suno API", "WAV Conversion"}, "music", "suno"},
		{"veo alias", []string{"Veo3.1 API"}, "video", "veo3.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			category, vendor, err := llms.Taxonomy(tt.breadcrumb)
			if err != nil {
				t.Fatalf("Taxonomy: %v", err)
			}
			if category != tt.wantCategory || vendor != tt.wantVendor {
				t.Errorf("got (%q, %q), want (%q, %q)", category, vendor, tt.wantCategory, tt.wantVendor)
			}
		})
	}
}

func TestTaxonomyRejectsUnknownBreadcrumb(t *testing.T) {
	tests := map[string][]string{
		"unknown api suffix": {"Kling API", "Kling"},
		"no suffix":          {"Something", "Else"},
		"missing vendor":     {"Image Models"},
		"empty":              nil,
	}
	for name, breadcrumb := range tests {
		t.Run(name, func(t *testing.T) {
			if _, _, err := llms.Taxonomy(breadcrumb); err == nil {
				t.Fatalf("want error for %v", breadcrumb)
			}
		})
	}
}

func TestParseAPIDocsRejectsForeignHost(t *testing.T) {
	text := "## API Docs\n" +
		"- Image Models > Seedream [A](https://docs.kie.ai/market/a.md): x\n" +
		"- Image Models > Seedream [B](https://example.com/market/b.md): x\n"
	if _, err := llms.ParseAPIDocs(text); err == nil {
		t.Fatal("want an error when an entry links off the documentation site")
	}
}
