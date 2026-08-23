package gen

import (
	"reflect"
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
)

// describedSchema is a request schema holding one property of each kind the
// correction has to tell apart.
func describedSchema() map[string]any {
	return map[string]any{
		"required": []any{"prompt"},
		"properties": map[string]any{
			"prompt": map[string]any{
				"description": "The prompt. Required for all generation requests.",
			},
			"callBackUrl": map[string]any{
				"description": "The URL to receive updates. Required for all generation requests.",
			},
			"style": map[string]any{
				"description": "Music style. Required in Custom Mode (customMode: true).",
			},
			"continueAt": map[string]any{
				"description": "Required when defaultParamFlag is true.",
			},
		},
	}
}

func TestCorrectRequiredAddsWhatTheAPIRefusesWithout(t *testing.T) {
	schema := describedSchema()
	if err := correctRequired("suno-api/generate-lyrics", schema); err != nil {
		t.Fatalf("correctRequired: %v", err)
	}
	// callBackUrl is added because kie.ai refuses the request without it,
	// prompt is not repeated, and neither conditional property is touched.
	if got, want := schema["required"], []any{"callBackUrl", "prompt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}
}

// The page says the same thing for runway, and the API takes the request
// anyway. Requiring it there would have the CLI refuse what kie.ai accepts.
func TestCorrectRequiredLeavesWhatTheAPIAccepts(t *testing.T) {
	schema := describedSchema()
	if err := correctRequired("runway-api/generate-ai-video", schema); err != nil {
		t.Fatalf("correctRequired: %v", err)
	}
	if got, want := schema["required"], []any{"prompt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}
}

// A disagreement nobody has measured is the one case where guessing either way
// would be a decision the table is supposed to hold, so the crawl fails and
// says what to measure.
func TestCorrectRequiredFailsOnAnUnmeasuredDisagreement(t *testing.T) {
	schema := describedSchema()
	err := correctRequired("suno-api/generate-music", schema)
	if err == nil {
		t.Fatal("want an error for a model with no measurement")
	}
	for _, want := range []string{"suno-api/generate-music", "callBackUrl", "required.go"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
	if got, want := schema["required"], []any{"prompt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("required = %v, want %v: a failed crawl must not half-correct the schema", got, want)
	}
}

// oneOf branches and array items hold properties of their own, and a page that
// describes one of those as required disagrees just as loudly.
func TestCorrectRequiredReachesNestedSchemas(t *testing.T) {
	nested := map[string]any{
		"properties": map[string]any{
			"shots": map[string]any{
				"items": map[string]any{
					"properties": map[string]any{
						"callBackUrl": map[string]any{
							"description": "Required for all shots.",
						},
					},
				},
			},
		},
	}
	if err := correctRequired("suno-api/cover-suno", nested); err != nil {
		t.Fatalf("correctRequired: %v", err)
	}
	shots := nested["properties"].(map[string]any)["shots"].(map[string]any)
	items := shots["items"].(map[string]any)
	if got, want := items["required"], []any{"callBackUrl"}; !reflect.DeepEqual(got, want) {
		t.Errorf("items required = %v, want %v", got, want)
	}
}

func TestUnconditionallyRequiredIgnoresConditionalStatements(t *testing.T) {
	for description, want := range map[string]bool{
		"Required for all lyrics generation requests.":  true,
		"This parameter is required for all requests.":  true,
		"Required when `defaultParamFlag` is `true`.":   false,
		"Required in Custom Mode (`customMode: true`).": false,
		"Required field.":              false,
		"Exactly 1 video is required.": false,
		"":                             false,
	} {
		if got := UnconditionallyRequired(description); got != want {
			t.Errorf("UnconditionallyRequired(%q) = %v, want %v", description, got, want)
		}
	}
}

// silentSchema is a Market input schema of the shape the second correction
// looks for: properties, and not one word about which of them a request needs.
func silentSchema() map[string]any {
	return map[string]any{
		"properties": map[string]any{
			"prompt":       map[string]any{"description": "The text prompt used to generate the video. Required field."},
			"aspect_ratio": map[string]any{"description": "Video aspect ratio configuration. Required field."},
		},
	}
}

// pinInputRequired hands one test a measurement table of its own. Every model
// in the real one was measured to take a request that carries nothing, so a
// list that has to be applied is only reachable this way.
func pinInputRequired(t *testing.T, table map[string][]string) {
	t.Helper()
	original := measuredInputRequired
	measuredInputRequired = table
	t.Cleanup(func() { measuredInputRequired = original })
}

func TestCorrectRequiredAppliesWhatASilentSchemaWasMeasuredToNeed(t *testing.T) {
	pinInputRequired(t, map[string][]string{"vendor/model": {"prompt", "aspect_ratio"}})
	schema := silentSchema()
	if err := correctRequired("vendor/model", schema); err != nil {
		t.Fatalf("correctRequired: %v", err)
	}
	if got, want := schema["required"], []any{"aspect_ratio", "prompt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}
}

// The measured answer for every model in the table so far: the endpoint takes
// a request that carries nothing. An empty required list would say the same as
// no required list while moving a line of the catalog, so none is written.
func TestCorrectRequiredWritesNoRequiredWhereNothingIsNeeded(t *testing.T) {
	pinInputRequired(t, map[string][]string{"vendor/model": {}})
	schema := silentSchema()
	if err := correctRequired("vendor/model", schema); err != nil {
		t.Fatalf("correctRequired: %v", err)
	}
	if _, ok := schema["required"]; ok {
		t.Errorf("required = %v, want the key to be absent", schema["required"])
	}
}

// A silent schema nobody has measured is the ordinary shape of a Market model,
// not a contradiction, so the crawl carries on and the model is reported
// instead of guessed at (#35).
func TestCorrectRequiredCarriesOnPastAnUnmeasuredSilentSchema(t *testing.T) {
	pinInputRequired(t, map[string][]string{})
	schema := silentSchema()
	if err := correctRequired("vendor/model", schema); err != nil {
		t.Fatalf("correctRequired: %v", err)
	}
	if _, ok := schema["required"]; ok {
		t.Errorf("required = %v, want the key to be absent", schema["required"])
	}
	models := []catalog.Model{{ID: "vendor/model", Input: schema}}
	if got, want := UnmeasuredInputRequired(models), []string{"vendor/model"}; !reflect.DeepEqual(got, want) {
		t.Errorf("UnmeasuredInputRequired = %v, want %v", got, want)
	}
}

// Five of the eleven schemas that say nothing at the root state their
// requirements in the alternatives they offer instead. Those are not silent,
// and a measurement must not overwrite what they already declare.
func TestCorrectRequiredLeavesASchemaThatRequiresThroughItsBranches(t *testing.T) {
	pinInputRequired(t, map[string][]string{"vendor/model": {"prompt"}})
	schema := map[string]any{
		"properties": map[string]any{"image_url": map[string]any{}, "prompt": map[string]any{}},
		"oneOf": []any{
			map[string]any{"title": "Text to video", "required": []any{"prompt"}},
			map[string]any{"title": "Image to video", "required": []any{"image_url"}},
		},
	}
	if RequiresNothing(schema) {
		t.Fatal("a schema whose branches require something is not silent")
	}
	if err := correctRequired("vendor/model", schema); err != nil {
		t.Fatalf("correctRequired: %v", err)
	}
	if _, ok := schema["required"]; ok {
		t.Errorf("root required = %v, want the branches left to do the requiring", schema["required"])
	}
	if got := UnmeasuredInputRequired([]catalog.Model{{ID: "other/model", Input: schema}}); got != nil {
		t.Errorf("UnmeasuredInputRequired = %v, want nothing to report", got)
	}
}
