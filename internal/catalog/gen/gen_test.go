package gen_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
	"github.com/douhashi/kie-ai-cli/internal/catalog/gen"
)

// pages serves fixture files by URL. The fetcher is the generator's only
// external boundary, so it is the only thing these tests stand in for.
type pages map[string]string

func (p pages) Fetch(_ context.Context, urls []string) (map[string]string, error) {
	fetched := make(map[string]string, len(urls))
	for _, url := range urls {
		body, ok := p[url]
		if !ok {
			return nil, os.ErrNotExist
		}
		fetched[url] = body
	}
	return fetched, nil
}

func read(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"testdata"}, parts...)...))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(b)
}

// fixturePages serves every page the fixture llms.txt lists.
func fixturePages(t *testing.T) pages {
	t.Helper()
	served := pages{}
	for _, path := range []string{
		"market/seedream/seedream-v4-text-to-image",
		"market/claude/claude-opus-5",
		"market/common/get-task-detail",
		"suno-api/generate-music",
		"suno-api/get-music-details",
	} {
		served["https://docs.kie.ai/"+path+".md"] = read(t, append([]string{"pages"}, strings.Split(path+".md", "/")...)...)
	}
	return served
}

func build(t *testing.T, llmsText string, served pages) *catalog.Catalog {
	t.Helper()
	built, err := gen.Build(t.Context(), llmsText, served)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return built
}

func TestBuildKeepsOnlyRunnableModels(t *testing.T) {
	built := build(t, read(t, "llms.txt"), fixturePages(t))

	if built.SchemaVersion != catalog.SchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", built.SchemaVersion, catalog.SchemaVersion)
	}
	var ids []string
	for _, model := range built.Models {
		ids = append(ids, model.ID)
	}
	want := []string{"bytedance/seedream-v4-text-to-image", "suno-api/generate-music"}
	if len(ids) != len(want) || ids[0] != want[0] || ids[1] != want[1] {
		t.Fatalf("ids = %v, want %v (chat, and both query endpoints, must drop out)", ids, want)
	}
}

func TestBuildMarketModel(t *testing.T) {
	built := build(t, read(t, "llms.txt"), fixturePages(t))
	model := built.Models[0]

	if model.Name != "Seedream4.0 - Text to Image" {
		t.Errorf("name = %q", model.Name)
	}
	if model.Category != "image" || model.Vendor != "seedream" {
		t.Errorf("taxonomy = (%q, %q), want (image, seedream)", model.Category, model.Vendor)
	}
	if model.DocsURL != "https://docs.kie.ai/market/seedream/seedream-v4-text-to-image.md" {
		t.Errorf("docsUrl = %q", model.DocsURL)
	}
	want := catalog.Create{
		Method: "POST",
		Path:   "/api/v1/jobs/createTask",
		Style:  catalog.StyleMarket,
		Model:  "bytedance/seedream-v4-text-to-image",
	}
	if model.Create != want {
		t.Errorf("create = %+v, want %+v", model.Create, want)
	}
	if (model.Query != catalog.Query{Method: "GET", Path: "/api/v1/jobs/recordInfo", Param: "taskId"}) {
		t.Errorf("query = %+v", model.Query)
	}
	// The Market request body wraps the model's own parameters in "input"; the
	// envelope around it is the CLI's job, not the user's.
	props, _ := model.Input["properties"].(map[string]any)
	if _, ok := props["prompt"]; !ok {
		t.Errorf("input is the envelope, not the model parameters: %v", model.Input)
	}
}

func TestBuildStandardModel(t *testing.T) {
	built := build(t, read(t, "llms.txt"), fixturePages(t))
	model := built.Models[1]

	if model.Category != "music" || model.Vendor != "suno" {
		t.Errorf("taxonomy = (%q, %q), want (music, suno)", model.Category, model.Vendor)
	}
	want := catalog.Create{Method: "POST", Path: "/api/v1/generate", Style: catalog.StyleDirect}
	if model.Create != want {
		t.Errorf("create = %+v, want %+v", model.Create, want)
	}
	if (model.Query != catalog.Query{Method: "GET", Path: "/api/v1/generate/record-info", Param: "taskId"}) {
		t.Errorf("query = %+v", model.Query)
	}
	props, _ := model.Input["properties"].(map[string]any)
	if _, ok := props["customMode"]; !ok {
		t.Errorf("input misses the request body properties: %v", model.Input)
	}
}

// AC1: nothing reaches the catalog without the three things a run needs.
func TestBuildFillsEveryRequiredField(t *testing.T) {
	built := build(t, read(t, "llms.txt"), fixturePages(t))

	for _, model := range built.Models {
		switch {
		case model.ID == "":
			t.Error("model with an empty id")
		case model.Name == "", model.Category == "", model.Vendor == "", model.DocsURL == "":
			t.Errorf("%s: empty descriptive field: %+v", model.ID, model)
		case model.Create.Method == "", model.Create.Path == "", model.Create.Style == "":
			t.Errorf("%s: incomplete create endpoint: %+v", model.ID, model.Create)
		case model.Query.Method == "", model.Query.Path == "", model.Query.Param == "":
			t.Errorf("%s: incomplete query endpoint: %+v", model.ID, model.Query)
		case len(model.Input) == 0:
			t.Errorf("%s: empty input schema", model.ID)
		case strings.HasPrefix(model.Description, "#"):
			t.Errorf("%s: description is a heading, not prose: %q", model.ID, model.Description)
		}
	}
}

// AC2: the same pages must produce the same bytes, or the scheduled run in #5
// would raise a diff on every execution.
func TestBuildIsDeterministic(t *testing.T) {
	llmsText, served := read(t, "llms.txt"), fixturePages(t)

	first, err := json.Marshal(build(t, llmsText, served))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for range 5 {
		next, err := json.Marshal(build(t, llmsText, served))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(next) != string(first) {
			t.Fatal("two builds of the same pages differ")
		}
	}
}

// AC3: anything the generator does not understand stops the whole run.
func TestBuildRejectsUnexpectedInput(t *testing.T) {
	seedream := "https://docs.kie.ai/market/seedream/seedream-v4-text-to-image.md"
	suno := "https://docs.kie.ai/suno-api/generate-music.md"

	tests := map[string]struct {
		llms   string
		served pages
		want   string
	}{
		"unknown breadcrumb": {
			llms:   "## API Docs\n- Kling API > Kling [Kling](" + seedream + "): Video generation\n",
			served: fixturePages(t),
			want:   "Kling API",
		},
		"standard page missing from the pair table": {
			llms:   "## API Docs\n- Suno API > Music Generation [Future](https://docs.kie.ai/suno-api/not-in-the-table.md): x\n",
			served: pages{"https://docs.kie.ai/suno-api/not-in-the-table.md": read(t, "pages", "suno-api", "generate-music.md")},
			want:   "not-in-the-table",
		},
		"market page without its query endpoint": {
			llms:   "## API Docs\n- Image Models > Seedream [Seedream](" + seedream + "): x\n",
			served: fixturePages(t),
			want:   "market/common/get-task-detail",
		},
		"unparsable page": {
			llms:   "## API Docs\n- Suno API > Music Generation [Generate Music](" + suno + "): x\n",
			served: pages{suno: read(t, "broken", "no-yaml.md"), "https://docs.kie.ai/" + "market/common/get-task-detail.md": read(t, "pages", "market", "common", "get-task-detail.md")},
			want:   "OpenAPI",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := gen.Build(t.Context(), tt.llms, tt.served)
			if err == nil {
				t.Fatal("want an error, got a catalog")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestBuildRejectsDuplicateIDs(t *testing.T) {
	seedream := "https://docs.kie.ai/market/seedream/seedream-v4-text-to-image.md"
	twin := "https://docs.kie.ai/market/seedream/seedream-v4-text-to-image-copy.md"
	served := fixturePages(t)
	served[twin] = served[seedream]

	llmsText := "## API Docs\n" +
		"- Image Models > Seedream [Seedream](" + seedream + "): x\n" +
		"- Image Models > Seedream [Seedream copy](" + twin + "): x\n" +
		"- [Get Task Details](https://docs.kie.ai/market/common/get-task-detail.md): x\n"

	_, err := gen.Build(t.Context(), llmsText, served)
	if err == nil {
		t.Fatal("want an error for two pages claiming the same model id")
	}
	if !strings.Contains(err.Error(), "bytedance/seedream-v4-text-to-image") {
		t.Errorf("error = %v, want it to name the duplicated id", err)
	}
}
