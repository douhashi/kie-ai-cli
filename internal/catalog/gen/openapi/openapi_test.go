package openapi_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/catalog/gen/openapi"
)

func parseFixture(t *testing.T, parts ...string) *openapi.Operation {
	t.Helper()
	op, err := openapi.Parse(readFixture(t, parts...))
	if err != nil {
		t.Fatalf("Parse(%v): %v", parts, err)
	}
	return op
}

func readFixture(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"..", "testdata"}, parts...)...))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(b)
}

func TestParseMarketCreatePage(t *testing.T) {
	op := parseFixture(t, "pages", "market", "seedream", "seedream-v4-text-to-image.md")

	if op.Method != "POST" {
		t.Errorf("method = %q, want POST", op.Method)
	}
	if op.Path != "/api/v1/jobs/createTask" {
		t.Errorf("path = %q", op.Path)
	}
	if op.Summary != "Seedream4.0 - Text to Image" {
		t.Errorf("summary = %q", op.Summary)
	}
	if !op.ReturnsTaskID {
		t.Error("ReturnsTaskID = false, want true")
	}
	if len(op.QueryParams) != 0 {
		t.Errorf("query params = %v, want none", op.QueryParams)
	}
}

// The llms.txt description of this page is its own first heading, so the usable
// one-liner has to come from the OpenAPI document's first prose paragraph.
func TestParseSkipsMarkdownDecorationInDescription(t *testing.T) {
	op := parseFixture(t, "pages", "market", "seedream", "seedream-v4-text-to-image.md")

	want := "High-quality photorealistic image generation powered by Seedream4.0's advanced AI model"
	if op.Description != want {
		t.Errorf("description = %q, want %q", op.Description, want)
	}
}

func TestParseResolvesMarketModelAndInput(t *testing.T) {
	op := parseFixture(t, "pages", "market", "seedream", "seedream-v4-text-to-image.md")

	modelProp, err := op.RequestProperty("model")
	if err != nil {
		t.Fatalf("RequestProperty(model): %v", err)
	}
	id, err := openapi.SingleEnumString(modelProp)
	if err != nil {
		t.Fatalf("SingleEnumString: %v", err)
	}
	if id != "bytedance/seedream-v4-text-to-image" {
		t.Errorf("model = %q", id)
	}

	input, err := op.RequestProperty("input")
	if err != nil {
		t.Fatalf("RequestProperty(input): %v", err)
	}
	props, ok := input["properties"].(map[string]any)
	if !ok {
		t.Fatalf("input has no properties: %v", input)
	}
	for _, name := range []string{"prompt", "image_size", "seed"} {
		if _, ok := props[name]; !ok {
			t.Errorf("input property %q missing", name)
		}
	}
}

// nsfw_checker reaches the schema only through x-apidog-refs. Leaving that
// vendor extension unresolved drops the property without any error.
func TestParseExpandsApidogRefs(t *testing.T) {
	op := parseFixture(t, "pages", "market", "seedream", "seedream-v4-text-to-image.md")

	input, err := op.RequestProperty("input")
	if err != nil {
		t.Fatalf("RequestProperty(input): %v", err)
	}
	props := input["properties"].(map[string]any)
	nsfw, ok := props["nsfw_checker"].(map[string]any)
	if !ok {
		t.Fatalf("nsfw_checker missing from %v", props)
	}
	if nsfw["type"] != "boolean" {
		t.Errorf("nsfw_checker type = %v, want boolean", nsfw["type"])
	}
}

func TestParseDropsVendorExtensions(t *testing.T) {
	op := parseFixture(t, "pages", "market", "seedream", "seedream-v4-text-to-image.md")

	encoded, err := json.Marshal(op.RequestSchema)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Not just x-apidog-*: the docs also carry x--orders, the same extension
	// with an empty vendor name.
	if strings.Contains(string(encoded), `"x-`) {
		t.Errorf("resolved schema still carries a vendor extension: %s", encoded)
	}
}

func TestParseMarketQueryPage(t *testing.T) {
	op := parseFixture(t, "pages", "market", "common", "get-task-detail.md")

	if op.Method != "GET" {
		t.Errorf("method = %q, want GET", op.Method)
	}
	if op.Path != "/api/v1/jobs/recordInfo" {
		t.Errorf("path = %q", op.Path)
	}
	if !reflect.DeepEqual(op.QueryParams, []string{"taskId"}) {
		t.Errorf("query params = %v, want [taskId]", op.QueryParams)
	}
}

func TestParseStandardCreatePage(t *testing.T) {
	op := parseFixture(t, "pages", "suno-api", "generate-music.md")

	if op.Method != "POST" || op.Path != "/api/v1/generate" {
		t.Errorf("got %s %s", op.Method, op.Path)
	}
	if !op.ReturnsTaskID {
		t.Error("ReturnsTaskID = false, want true")
	}
	props, ok := op.RequestSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("request schema has no properties: %v", op.RequestSchema)
	}
	if _, ok := props["customMode"]; !ok {
		t.Errorf("customMode missing from %v", props)
	}
}

// Chat models answer synchronously, so nothing about them can be tracked in the
// task ledger. The test asserts the shape is what excludes them, not the name.
func TestParseDetectsSynchronousEndpoint(t *testing.T) {
	op := parseFixture(t, "pages", "market", "claude", "claude-opus-5.md")

	if op.ReturnsTaskID {
		t.Error("ReturnsTaskID = true, want false for a synchronous endpoint")
	}
}

func TestParseRejectsUnexpectedPages(t *testing.T) {
	tests := map[string]struct {
		fixture string
		want    string
	}{
		"no OpenAPI block": {"no-yaml.md", "OpenAPI"},
		"two paths":        {"two-paths.md", "paths"},
		"dangling $ref":    {"dangling-ref.md", "nsfw_checker"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := openapi.Parse(readFixture(t, "broken", tt.fixture))
			if err == nil {
				t.Fatalf("want error for %s", tt.fixture)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestSingleEnumStringRejectsAmbiguousEnum(t *testing.T) {
	op := parseFixture(t, "broken", "two-models.md")

	modelProp, err := op.RequestProperty("model")
	if err != nil {
		t.Fatalf("RequestProperty(model): %v", err)
	}
	if _, err := openapi.SingleEnumString(modelProp); err == nil {
		t.Fatal("want error when the model enum holds more than one value")
	}
}

func TestRequestPropertyRejectsMissingProperty(t *testing.T) {
	op := parseFixture(t, "pages", "suno-api", "generate-music.md")

	if _, err := op.RequestProperty("input"); err == nil {
		t.Fatal("want error for a property the request body does not have")
	}
}

// kie.ai names some components with a space, which arrives percent-encoded in
// the $ref. Failing to decode it loses the whole request schema.
func TestParseResolvesPercentEncodedRef(t *testing.T) {
	op := parseFixture(t, "pages", "market", "grok", "grok-4-6.md")

	if op.ReturnsTaskID {
		t.Error("ReturnsTaskID = true, want false for a chat completion endpoint")
	}
	props, ok := op.RequestSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("request schema has no properties: %v", op.RequestSchema)
	}
	// "test" is kie.ai's own typo for the structured-output setting; it is the
	// property whose $ref carries the encoded name.
	structured, ok := props["test"].(map[string]any)
	if !ok {
		t.Fatalf("structured output property missing from %v", props)
	}
	if _, ok := structured["properties"]; !ok {
		t.Errorf("structured output property was not resolved: %v", structured)
	}
}
