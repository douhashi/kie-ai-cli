package openapi_test

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	id, err := openapi.FixedString(modelProp)
	if err != nil {
		t.Fatalf("FixedString: %v", err)
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

// Some kie.ai pages spell a property with a trailing blank. kie.ai reads the
// trimmed name and ignores the spelled one (#60), so the blank is dropped from
// both the property and the required list.
func TestParseTrimsBlanksAroundPropertyNames(t *testing.T) {
	op := parseFixture(t, "quirks", "spaced-names.md")

	input, err := op.RequestProperty("input")
	if err != nil {
		t.Fatalf("RequestProperty(input): %v", err)
	}
	props := input["properties"].(map[string]any)
	if got := slices.Sorted(maps.Keys(props)); !slices.Equal(got, []string{"image_urls", "prompt"}) {
		t.Errorf("properties = %q, want [image_urls prompt]", got)
	}
	if got := input["required"]; !reflect.DeepEqual(got, []any{"image_urls"}) {
		t.Errorf("required = %q, want [image_urls]", got)
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
		// Keeping either would silently drop the other's schema.
		"names equal once trimmed": {"colliding-names.md", "image_urls"},
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

func TestFixedStringRejectsAmbiguousEnum(t *testing.T) {
	op := parseFixture(t, "broken", "two-models.md")

	modelProp, err := op.RequestProperty("model")
	if err != nil {
		t.Fatalf("RequestProperty(model): %v", err)
	}
	if _, err := openapi.FixedString(modelProp); err == nil {
		t.Fatal("want error when the model enum holds more than one value")
	}
}

// kie.ai states a model's fixed value in whichever of enum, default and
// examples the page's author reached for, so each shape it takes counts.
func TestFixedStringReadsEveryShapeAPageStatesItIn(t *testing.T) {
	tests := map[string]map[string]any{
		"enum":                {"enum": []any{"vendor/model"}, "default": "vendor/model"},
		"default and example": {"default": "vendor/model", "examples": []any{"vendor/model"}},
		"examples only":       {"examples": []any{"vendor/model"}},
		"default only":        {"default": "vendor/model"},
	}
	for name, schema := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := openapi.FixedString(schema)
			if err != nil {
				t.Fatalf("FixedString: %v", err)
			}
			if got != "vendor/model" {
				t.Errorf("FixedString = %q, want vendor/model", got)
			}
		})
	}
}

// A page that names two values has not fixed one, and picking either would
// send some model's requests to another.
func TestFixedStringRejectsWhatDoesNotFixOneValue(t *testing.T) {
	tests := map[string]struct {
		schema map[string]any
		want   string
	}{
		"several enum values": {
			schema: map[string]any{"enum": []any{"vendor/a", "vendor/b"}},
			want:   "enum has 2 values",
		},
		"default and example disagree": {
			schema: map[string]any{"default": "vendor/a", "examples": []any{"vendor/b"}},
			want:   "disagree",
		},
		"examples disagree": {
			schema: map[string]any{"examples": []any{"vendor/a", "vendor/b"}},
			want:   "disagree",
		},
		"nothing stated": {
			schema: map[string]any{"type": "string"},
			want:   "no enum, default or examples",
		},
		"not a string": {
			schema: map[string]any{"default": 3},
			want:   "not a string",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := openapi.FixedString(tt.schema)
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestRequestPropertyRejectsMissingProperty(t *testing.T) {
	op := parseFixture(t, "pages", "market", "seedream", "seedream-v4-text-to-image.md")

	if _, err := op.RequestProperty("no_such_property"); err == nil {
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
