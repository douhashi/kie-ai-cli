package gen

import (
	"reflect"
	"strings"
	"testing"
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
