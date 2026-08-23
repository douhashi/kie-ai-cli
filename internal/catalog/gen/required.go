package gen

import (
	"fmt"
	"regexp"
	"slices"
)

// kie.ai says in prose that some properties must be sent on every request
// while leaving them out of the request body's required list. The two
// statements disagree, and only the API can settle which one is right: asked
// without the property, the suno endpoints below refuse the request and the
// runway ones carry it out.
//
// The prose is therefore read only to *detect* a disagreement. What to do
// about each one is pinned in measured, with the request that decided it, and
// a disagreement nobody has measured fails the generation instead of being
// guessed at. That is the rule the category alias table in gen/llms already
// follows, for the same reason: a table nothing forces anyone to update goes
// stale in silence.
//
// Guessing is refused because the two ways of being wrong are not symmetric.
// A required property left optional surfaces as kie.ai's own 422, which costs
// nothing and names the field. An optional one made required has the CLI
// refuse a request kie.ai would have taken, and nothing tells the user that
// the model does not actually want it.

// unconditionalRequired matches a description that says the property is needed
// on every request, with no condition attached. It is deliberately narrower
// than the word "required": across the catalogue, 28 further properties say
// they are required only in a mode ("Required when `defaultParamFlag` is
// `true`", "Required in Custom Mode") or sit in a Market schema that lists
// nothing as required at all (#35).
var unconditionalRequired = regexp.MustCompile(`(?i)required for all\b`)

// UnconditionallyRequired reports whether a property description states,
// without condition, that the property must be supplied. It is exported so
// that the catalog's own invariant test reads the descriptions by the same
// rule the generator does.
func UnconditionallyRequired(description string) bool {
	return unconditionalRequired.MatchString(description)
}

// modelProperty names one property of one model, which is the scope a
// measurement has: the same field name means different things elsewhere.
type modelProperty struct{ model, property string }

// measured records what kie.ai did with a request that left the property out:
// true where it refused it, false where it carried it out. Every entry is a
// request that was really made, on the date given, because kie.ai may change
// its answer -- and then this table is wrong rather than merely old.
var measured = map[modelProperty]bool{
	// HTTP 200 with code 422 "Please enter callBackUrl", no task created.
	// #9 for generate-lyrics; #33, 2026-08-23, for both.
	{"suno-api/generate-lyrics", "callBackUrl"}: true,
	{"suno-api/cover-suno", "callBackUrl"}:      true,
	// Accepted: both answered a request without callBackUrl with a taskId.
	// extend-ai-video created a task that failed on its (deliberately
	// invalid) parent id, and generate-ai-video produced a video and spent
	// 12 credits, so neither endpoint enforces what its page states.
	// #33, 2026-08-23.
	{"runway-api/extend-ai-video", "callBackUrl"}:   false,
	{"runway-api/generate-ai-video", "callBackUrl"}: false,
}

// MeasuredRequired reports what was measured for one property of one model:
// whether kie.ai refused a request without it, and whether it was measured at
// all.
func MeasuredRequired(model, property string) (required, pinned bool) {
	required, pinned = measured[modelProperty{model, property}]
	return required, pinned
}

// correctRequired brings the input schema's required lists into line with the
// measurements, and reports every disagreement that has none.
func correctRequired(model string, input map[string]any) error {
	var unmeasured []string
	correctObject(model, input, &unmeasured)
	if len(unmeasured) == 0 {
		return nil
	}
	slices.Sort(unmeasured)
	return fmt.Errorf(
		"%s: described as required for every request but left out of required, and not measured: %v; send the request without it, and add what kie.ai answers to measured in internal/catalog/gen/required.go",
		model, unmeasured)
}

// correctObject walks one object schema and the schemas below it, which is
// where a property nested in an array item or a oneOf branch is reached.
func correctObject(model string, schema map[string]any, unmeasured *[]string) {
	properties, _ := schema["properties"].(map[string]any)
	listed := requiredNames(schema)
	var add []string
	for name, raw := range properties {
		property, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		correctObject(model, property, unmeasured)
		description, _ := property["description"].(string)
		if !UnconditionallyRequired(description) || slices.Contains(listed, name) {
			continue
		}
		switch required, pinned := MeasuredRequired(model, name); {
		case !pinned:
			*unmeasured = append(*unmeasured, name)
		case required:
			add = append(add, name)
		}
	}
	if len(add) > 0 {
		schema["required"] = sortedRequired(append(listed, add...))
	}
	for _, keyword := range []string{"oneOf", "anyOf", "allOf"} {
		members, _ := schema[keyword].([]any)
		for _, member := range members {
			if object, ok := member.(map[string]any); ok {
				correctObject(model, object, unmeasured)
			}
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		correctObject(model, items, unmeasured)
	}
}

func requiredNames(schema map[string]any) []string {
	raw, _ := schema["required"].([]any)
	names := make([]string, 0, len(raw))
	for _, value := range raw {
		if name, ok := value.(string); ok {
			names = append(names, name)
		}
	}
	return names
}

// sortedRequired renders the list the way the resolved schema already holds
// it: sorted and without repetition, so that the generated catalog stays
// byte-identical between runs.
func sortedRequired(names []string) []any {
	slices.Sort(names)
	names = slices.Compact(names)
	out := make([]any, len(names))
	for i, name := range names {
		out[i] = name
	}
	return out
}
