package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
)

// inputSchema is one model's input schema, read the way both `model show` and
// `task run` need it: the root object together with the alternatives a oneOf or
// an anyOf offers beside it.
//
// Nine of the catalog's schemas carry such alternatives, and four of those
// declare no properties of their own. A reader that looked only at the root
// would describe those models as taking no input at all, and would refuse
// every field a caller gave them, so nothing reads the raw map directly.
type inputSchema struct {
	root     map[string]any
	branches []branch
}

// branch is one alternative: the object schema and the title the documentation
// gives it, which is how a caller is told which of them to complete.
type branch struct {
	title  string
	schema map[string]any
}

// label names one alternative for a message: the title the documentation gives
// it, or its position among the others when it has none.
func (b branch) label(i int) string {
	if b.title != "" {
		return strconv.Quote(b.title)
	}
	return fmt.Sprintf("alternative %d", i+1)
}

func newInputSchema(input map[string]any) inputSchema {
	s := inputSchema{root: input}
	// oneOf and anyOf differ in whether more than one may match, which
	// changes nothing about what a caller has to supply: either way one
	// complete alternative is enough.
	for _, key := range []string{"oneOf", "anyOf"} {
		raw, _ := input[key].([]any)
		for _, b := range raw {
			if object, ok := b.(map[string]any); ok {
				s.branches = append(s.branches, branch{title: stringOf(object["title"]), schema: object})
			}
		}
	}
	return s
}

// property is one input field as the whole schema declares it.
type property struct {
	name string
	// types are the types declared for the name, in the order they were
	// found and without repetition. There is more than one when the
	// branches disagree, which one model in the catalog does.
	types []string
	// items is the element schema of an array field, and is nil for any
	// other kind.
	items map[string]any
}

// properties lists every field the model accepts, by name, gathering the root
// and every alternative into one list: which of them a field came from decides
// whether it is required, never whether it may be sent.
func (s inputSchema) properties() []property {
	found := map[string]*property{}
	var names []string
	for _, object := range s.objects() {
		for name, raw := range propertiesOf(object) {
			declared, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			p := found[name]
			if p == nil {
				p = &property{name: name}
				found[name] = p
				names = append(names, name)
			}
			if typ := stringOf(declared["type"]); !slices.Contains(p.types, typ) {
				p.types = append(p.types, typ)
			}
			if items, ok := declared["items"].(map[string]any); ok && p.items == nil {
				p.items = items
			}
		}
	}

	slices.Sort(names)
	out := make([]property, 0, len(names))
	for _, name := range names {
		out = append(out, *found[name])
	}
	return out
}

// objects lists the root and every alternative, which together hold every
// property the model accepts.
func (s inputSchema) objects() []map[string]any {
	objects := make([]map[string]any, 0, len(s.branches)+1)
	objects = append(objects, s.root)
	for _, b := range s.branches {
		objects = append(objects, b.schema)
	}
	return objects
}

func propertiesOf(schema map[string]any) map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	return properties
}

// requiredOf reads the names one object schema insists on, in the order it
// lists them.
func requiredOf(schema map[string]any) []string {
	raw, _ := schema["required"].([]any)
	names := make([]string, 0, len(raw))
	for _, n := range raw {
		if name, ok := n.(string); ok {
			names = append(names, name)
		}
	}
	return names
}

// jsonTypeOf names the JSON type a decoded value would be written as. A whole
// number is reported as an integer, which matchesType then also accepts where a
// number is asked for: JSON has one numeric type and JSON Schema has two.
func jsonTypeOf(v any) string {
	switch value := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	case json.Number:
		if _, err := value.Int64(); err == nil {
			return "integer"
		}
		return "number"
	case float64:
		if value == float64(int64(value)) {
			return "integer"
		}
		return "number"
	default:
		return ""
	}
}

// matchesType reports whether value could have been written for a field
// declared as typ.
//
// A field the catalog declares no type for accepts anything: refusing it would
// make the field unusable rather than the call safe.
func matchesType(typ string, value any) bool {
	if typ == "" {
		return true
	}
	actual := jsonTypeOf(value)
	return typ == actual || (typ == "number" && actual == "integer")
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}
