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

// pinMeasured hands one test a measurement table of its own, so that each
// outcome is reachable whatever the real table holds.
func pinMeasured(t *testing.T, table map[modelProperty]bool) {
	t.Helper()
	original := measured
	measured = table
	t.Cleanup(func() { measured = original })
}

func TestCorrectRequiredAddsWhatTheAPIRefusesWithout(t *testing.T) {
	pinMeasured(t, map[modelProperty]bool{{"vendor/model", "callBackUrl"}: true})
	schema := describedSchema()
	if err := correctRequired("vendor/model", schema); err != nil {
		t.Fatalf("correctRequired: %v", err)
	}
	// callBackUrl is added because kie.ai refuses the request without it,
	// prompt is not repeated, and neither conditional property is touched.
	if got, want := schema["required"], []any{"callBackUrl", "prompt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}
}

// A page may say the same thing of a model whose API takes the request anyway.
// Requiring it there would have the CLI refuse what kie.ai accepts.
func TestCorrectRequiredLeavesWhatTheAPIAccepts(t *testing.T) {
	pinMeasured(t, map[modelProperty]bool{{"vendor/model", "callBackUrl"}: false})
	schema := describedSchema()
	if err := correctRequired("vendor/model", schema); err != nil {
		t.Fatalf("correctRequired: %v", err)
	}
	if got, want := schema["required"], []any{"prompt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}
}

// Upstream may also list a property as required that kie.ai does without. The
// measurement is the authority in that direction too, whatever the description
// says, and the name is taken off the list wherever the walk reaches it (#69).
func TestCorrectRequiredRemovesWhatTheAPIDoesWithout(t *testing.T) {
	pinMeasured(t, map[modelProperty]bool{{"vendor/model", "stem_name"}: false})
	schema := map[string]any{
		"required": []any{"stem_name", "type"},
		"properties": map[string]any{
			"stem_name": map[string]any{"description": "Only used when `type` is `split_stem_advanced`."},
			"type":      map[string]any{"description": "Separation type."},
		},
	}
	if err := correctRequired("vendor/model", schema); err != nil {
		t.Fatalf("correctRequired: %v", err)
	}
	if got, want := schema["required"], []any{"type"}; !reflect.DeepEqual(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}
}

// Upstream may also leave a property off the list without its description
// saying anything about it. A refusal measured without it adds it all the same
// (#70).
func TestCorrectRequiredAddsWhatTheAPIRefusesWithoutWhateverTheDescriptionSays(t *testing.T) {
	pinMeasured(t, map[modelProperty]bool{{"vendor/model", "voice"}: true})
	schema := map[string]any{
		"required": []any{"text"},
		"properties": map[string]any{
			"text":  map[string]any{"description": "The text to convert to speech."},
			"voice": map[string]any{"description": "The voice to use for speech generation."},
		},
	}
	if err := correctRequired("vendor/model", schema); err != nil {
		t.Fatalf("correctRequired: %v", err)
	}
	if got, want := schema["required"], []any{"text", "voice"}; !reflect.DeepEqual(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}
}

// Taking the only required name off must drop the list rather than leave an
// empty one: the two mean the same, and an empty list would read as a schema
// that states its requirements.
func TestCorrectRequiredDropsARequiredListLeftEmpty(t *testing.T) {
	pinMeasured(t, map[modelProperty]bool{{"vendor/model", "stem_name"}: false})
	schema := map[string]any{
		"required":   []any{"stem_name"},
		"properties": map[string]any{"stem_name": map[string]any{}},
	}
	if err := correctRequired("vendor/model", schema); err != nil {
		t.Fatalf("correctRequired: %v", err)
	}
	if _, ok := schema["required"]; ok {
		t.Errorf("required = %v, want the key to be absent", schema["required"])
	}
}

// A disagreement nobody has measured is the one case where guessing either way
// would be a decision the table is supposed to hold, so the crawl fails and
// says what to measure.
func TestCorrectRequiredFailsOnAnUnmeasuredDisagreement(t *testing.T) {
	pinMeasured(t, map[modelProperty]bool{{"other/model", "callBackUrl"}: true})
	schema := describedSchema()
	err := correctRequired("vendor/model", schema)
	if err == nil {
		t.Fatal("want an error for a model with no measurement")
	}
	for _, want := range []string{"vendor/model", "callBackUrl", "required.go"} {
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
	pinMeasured(t, map[modelProperty]bool{{"vendor/model", "callBackUrl"}: true})
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
	if err := correctRequired("vendor/model", nested); err != nil {
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

// pinInputRequired hands one test a measurement table of its own, so that each
// shape a measurement can take is reachable whatever the real table holds.
func pinInputRequired(t *testing.T, table map[string][][]string) {
	t.Helper()
	original := measuredInputRequired
	measuredInputRequired = table
	t.Cleanup(func() { measuredInputRequired = original })
}

func TestCorrectRequiredAppliesWhatASilentSchemaWasMeasuredToNeed(t *testing.T) {
	pinInputRequired(t, map[string][][]string{"vendor/model": {{"prompt", "aspect_ratio"}}})
	schema := silentSchema()
	if err := correctRequired("vendor/model", schema); err != nil {
		t.Fatalf("correctRequired: %v", err)
	}
	if got, want := schema["required"], []any{"aspect_ratio", "prompt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}
}

// The measured answer for some models: the endpoint takes a request that
// carries nothing. An empty required list would say the same as
// no required list while moving a line of the catalog, so none is written.
func TestCorrectRequiredWritesNoRequiredWhereNothingIsNeeded(t *testing.T) {
	pinInputRequired(t, map[string][][]string{"vendor/model": {}})
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
	pinInputRequired(t, map[string][][]string{})
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
	pinInputRequired(t, map[string][][]string{"vendor/model": {{"prompt"}}})
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

// An endpoint that wants any one of several fields is measured as that many
// alternatives. A flat list would demand all of them, so they are written as
// anyOf branches that do nothing but require, in the order the table gives
// them, which is the order `model show` numbers them in (#43).
func TestCorrectRequiredWritesAlternativesAsAnyOf(t *testing.T) {
	pinInputRequired(t, map[string][][]string{"vendor/model": {{"prompt"}, {"aspect_ratio"}}})
	schema := silentSchema()
	if err := correctRequired("vendor/model", schema); err != nil {
		t.Fatalf("correctRequired: %v", err)
	}
	if _, ok := schema["required"]; ok {
		t.Errorf("root required = %v, want none: no one field is needed on its own", schema["required"])
	}
	want := []any{
		map[string]any{"required": []any{"prompt"}},
		map[string]any{"required": []any{"aspect_ratio"}},
	}
	if got := schema["anyOf"]; !reflect.DeepEqual(got, want) {
		t.Errorf("anyOf = %v, want %v", got, want)
	}
	if RequiresNothing(schema) {
		t.Error("a schema that requires through the alternatives it was given is not silent")
	}
}

// A name the schema does not declare would make an alternative no request can
// complete -- the CLI refuses the field before it is sent -- so a table that
// has drifted from the page fails the crawl rather than locking the model out.
func TestCorrectRequiredFailsOnAMeasuredNameTheSchemaDoesNotDeclare(t *testing.T) {
	for name, alternatives := range map[string][][]string{
		"one alternative": {{"prompt", "promt"}},
		"alternatives":    {{"prompt"}, {"promt"}},
		"trailing blank":  {{"prompt "}, {"aspect_ratio"}},
	} {
		t.Run(name, func(t *testing.T) {
			pinInputRequired(t, map[string][][]string{"vendor/model": alternatives})
			schema := silentSchema()
			err := correctRequired("vendor/model", schema)
			if err == nil {
				t.Fatal("want an error for a name the schema does not declare")
			}
			for _, want := range []string{"vendor/model", "required.go"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want it to mention %q", err, want)
				}
			}
			if !reflect.DeepEqual(schema, silentSchema()) {
				t.Errorf("schema = %v, want it left as it was", schema)
			}
		})
	}
}

// Alternatives the schema already offers, requiring nothing, would be merged
// with the measured ones into a single choice by every reader of the catalog,
// which says something neither of them does. Nothing in the catalog takes that
// shape, so meeting one fails the crawl instead of being guessed at.
func TestCorrectRequiredFailsWhereTheSchemaAlreadyOffersAlternatives(t *testing.T) {
	for _, keyword := range []string{"oneOf", "anyOf"} {
		t.Run(keyword, func(t *testing.T) {
			pinInputRequired(t, map[string][][]string{"vendor/model": {{"prompt"}, {"aspect_ratio"}}})
			schema := silentSchema()
			schema[keyword] = []any{map[string]any{"title": "Silent"}}
			err := correctRequired("vendor/model", schema)
			if err == nil {
				t.Fatal("want an error for a schema that already offers alternatives")
			}
			if !strings.Contains(err.Error(), keyword) {
				t.Errorf("error = %v, want it to mention %q", err, keyword)
			}
			if _, ok := schema["required"]; ok {
				t.Errorf("required = %v, want the schema left as it was", schema["required"])
			}
		})
	}
}
