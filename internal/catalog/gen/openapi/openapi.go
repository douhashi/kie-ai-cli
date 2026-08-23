// Package openapi reads the OpenAPI document embedded in a docs.kie.ai page
// and reduces it to the one operation the page documents.
//
// References are resolved and apidog's vendor extensions are applied and then
// dropped here, so callers see a plain JSON Schema and never have to know how
// the docs site stores it.
package openapi

import (
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// specPattern matches the fenced yaml block that follows the OpenAPI heading.
// Every endpoint reference page on docs.kie.ai carries exactly one.
var specPattern = regexp.MustCompile("(?s)## OpenAPI Specification\\s*\n+```yaml\n(.*?)\n```")

// methods are the HTTP verbs a path item may hold.
var methods = []string{"get", "post", "put", "patch", "delete", "head", "options"}

// Operation is the single endpoint a reference page documents.
type Operation struct {
	Method  string
	Path    string
	Summary string
	// Description is the first prose paragraph of the operation description,
	// collapsed to one line. Headings, admonitions and MDX cards are skipped:
	// many pages open with one, and it says nothing about the model.
	Description string
	// RequestSchema is the resolved application/json request body schema, or
	// nil when the operation takes no body.
	RequestSchema map[string]any
	// QueryParams are the names of the query-string parameters, in document
	// order.
	QueryParams []string
	// ReturnsTaskID reports whether the 200 response carries data.taskId, which
	// is what makes an endpoint asynchronous and therefore trackable.
	ReturnsTaskID bool
}

// Parse extracts the operation from a docs.kie.ai page in Markdown form.
func Parse(page string) (*Operation, error) {
	m := specPattern.FindStringSubmatch(page)
	if m == nil {
		return nil, fmt.Errorf("no OpenAPI Specification block")
	}

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(m[1]), &doc); err != nil {
		return nil, fmt.Errorf("decode OpenAPI yaml: %w", err)
	}

	path, item, err := singlePath(doc)
	if err != nil {
		return nil, err
	}
	method, operation, err := singleMethod(item)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	op := &Operation{
		Method:      strings.ToUpper(method),
		Path:        path,
		Summary:     stringField(operation, "summary"),
		Description: ProseLine(stringField(operation, "description")),
	}
	if op.QueryParams, err = queryParams(operation); err != nil {
		return nil, fmt.Errorf("%s %s: %w", op.Method, path, err)
	}
	if op.RequestSchema, err = requestSchema(operation, doc); err != nil {
		return nil, fmt.Errorf("%s %s: request body: %w", op.Method, path, err)
	}
	if op.ReturnsTaskID, err = returnsTaskID(operation, doc); err != nil {
		return nil, fmt.Errorf("%s %s: 200 response: %w", op.Method, path, err)
	}
	return op, nil
}

// RequestProperty returns one top-level property of the request body schema.
func (o *Operation) RequestProperty(name string) (map[string]any, error) {
	properties, _ := o.RequestSchema["properties"].(map[string]any)
	property, ok := properties[name].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("request body has no %q property", name)
	}
	return property, nil
}

// SingleEnumString returns the only value a schema's enum permits. More than
// one value means the page documents several models at once, which the catalog
// has no way to name.
func SingleEnumString(schema map[string]any) (string, error) {
	values, ok := schema["enum"].([]any)
	if !ok {
		return "", fmt.Errorf("schema has no enum")
	}
	if len(values) != 1 {
		return "", fmt.Errorf("enum has %d values, want exactly 1: %v", len(values), values)
	}
	value, ok := values[0].(string)
	if !ok {
		return "", fmt.Errorf("enum value %v is not a string", values[0])
	}
	return value, nil
}

func singlePath(doc map[string]any) (string, map[string]any, error) {
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		return "", nil, fmt.Errorf("document has no paths")
	}
	if len(paths) != 1 {
		return "", nil, fmt.Errorf("document has %d paths, want exactly 1: %v",
			len(paths), slices.Sorted(maps.Keys(paths)))
	}
	for path, item := range paths {
		pathItem, ok := item.(map[string]any)
		if !ok {
			return "", nil, fmt.Errorf("path %s is not an object", path)
		}
		return path, pathItem, nil
	}
	panic("unreachable: paths holds exactly one entry")
}

func singleMethod(item map[string]any) (string, map[string]any, error) {
	var found []string
	for _, method := range methods {
		if _, ok := item[method]; ok {
			found = append(found, method)
		}
	}
	if len(found) != 1 {
		return "", nil, fmt.Errorf("path has %d methods, want exactly 1: %v", len(found), found)
	}
	operation, ok := item[found[0]].(map[string]any)
	if !ok {
		return "", nil, fmt.Errorf("%s operation is not an object", found[0])
	}
	return found[0], operation, nil
}

func queryParams(operation map[string]any) ([]string, error) {
	raw, ok := operation["parameters"].([]any)
	if !ok {
		return nil, nil
	}
	var names []string
	for _, entry := range raw {
		parameter, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("parameter %v is not an object", entry)
		}
		if parameter["in"] != "query" {
			continue
		}
		name, ok := parameter["name"].(string)
		if !ok {
			return nil, fmt.Errorf("query parameter %v has no name", parameter)
		}
		names = append(names, name)
	}
	return names, nil
}

// jsonContent digs out the application/json schema of a request body or
// response, returning nil when there is none.
func jsonContent(holder map[string]any) map[string]any {
	content, ok := holder["content"].(map[string]any)
	if !ok {
		return nil
	}
	media, ok := content["application/json"].(map[string]any)
	if !ok {
		return nil
	}
	schema, _ := media["schema"].(map[string]any)
	return schema
}

func requestSchema(operation, doc map[string]any) (map[string]any, error) {
	body, ok := operation["requestBody"].(map[string]any)
	if !ok {
		return nil, nil
	}
	schema := jsonContent(body)
	if schema == nil {
		return nil, nil
	}
	resolved, err := resolve(schema, doc, nil)
	if err != nil {
		return nil, err
	}
	object, ok := resolved.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schema is not an object")
	}
	return object, nil
}

// returnsTaskID resolves the 200 response schema and looks for data.taskId.
func returnsTaskID(operation, doc map[string]any) (bool, error) {
	responses, ok := operation["responses"].(map[string]any)
	if !ok {
		return false, fmt.Errorf("operation has no responses")
	}
	success, ok := responses["200"].(map[string]any)
	if !ok {
		return false, fmt.Errorf("operation has no 200 response")
	}
	schema := jsonContent(success)
	if schema == nil {
		return false, nil
	}
	resolved, err := resolve(schema, doc, nil)
	if err != nil {
		return false, err
	}
	object, ok := resolved.(map[string]any)
	if !ok {
		return false, nil
	}
	properties, _ := object["properties"].(map[string]any)
	data, _ := properties["data"].(map[string]any)
	dataProperties, _ := data["properties"].(map[string]any)
	_, found := dataProperties["taskId"]
	return found, nil
}

// resolve rewrites a schema into plain JSON Schema: $ref and allOf are folded
// in, apidog's x-apidog-refs properties are spliced into place, and every
// x-apidog extension is dropped. stack holds the references currently being
// followed so a cycle fails instead of hanging.
func resolve(node any, doc map[string]any, stack []string) (any, error) {
	switch value := node.(type) {
	case map[string]any:
		return resolveObject(value, doc, stack)
	case []any:
		resolved := make([]any, len(value))
		for i, item := range value {
			item, err := resolve(item, doc, stack)
			if err != nil {
				return nil, err
			}
			resolved[i] = item
		}
		return resolved, nil
	default:
		return node, nil
	}
}

func resolveObject(node map[string]any, doc map[string]any, stack []string) (any, error) {
	out := map[string]any{}

	if ref, ok := node["$ref"].(string); ok {
		target, err := lookupRef(doc, ref, stack)
		if err != nil {
			return nil, err
		}
		resolved, err := resolve(target, doc, append(stack, ref))
		if err != nil {
			return nil, err
		}
		object, ok := resolved.(map[string]any)
		if !ok {
			// A $ref to a non-object cannot be merged with siblings, so the
			// target replaces the node outright.
			return resolved, nil
		}
		merge(out, object)
	}

	if members, ok := node["allOf"].([]any); ok {
		for _, member := range members {
			resolved, err := resolve(member, doc, stack)
			if err != nil {
				return nil, err
			}
			object, ok := resolved.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("allOf member is not an object")
			}
			merge(out, object)
		}
	}

	// x-apidog-refs pulls shared properties in by id, and is therefore the one
	// vendor extension read before being dropped. The ids also appear in
	// x-apidog-orders, which is why an unexpanded ref loses properties without
	// any complaint from the docs site.
	if refs, ok := node["x-apidog-refs"].(map[string]any); ok {
		for id, member := range refs {
			resolved, err := resolve(member, doc, stack)
			if err != nil {
				return nil, fmt.Errorf("x-apidog-refs[%s]: %w", id, err)
			}
			object, ok := resolved.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("x-apidog-refs[%s] is not an object", id)
			}
			merge(out, object)
		}
	}

	for key, value := range node {
		// Every x- key is a vendor extension. apidog writes them under several
		// spellings (x-apidog-orders, and x--orders where the vendor name came
		// out empty), and none of them mean anything to a JSON Schema reader.
		if key == "$ref" || key == "allOf" || strings.HasPrefix(key, "x-") {
			continue
		}
		resolved, err := resolve(value, doc, stack)
		if err != nil {
			return nil, err
		}
		mergeKey(out, key, resolved)
	}

	// required is a set, and it can be assembled from several sources, so it is
	// sorted to keep the generated catalog byte-identical between runs.
	if required, ok := out["required"].([]any); ok {
		out["required"] = sortedStrings(required)
	}
	return out, nil
}

// merge folds src into dst the way JSON Schema composition expects: property
// maps are unioned, everything else is taken from whichever source has it.
func merge(dst, src map[string]any) {
	for key, value := range src {
		mergeKey(dst, key, value)
	}
}

func mergeKey(dst map[string]any, key string, value any) {
	existing, ok := dst[key]
	if !ok {
		dst[key] = value
		return
	}
	if key == "properties" {
		existingProps, okA := existing.(map[string]any)
		newProps, okB := value.(map[string]any)
		if okA && okB {
			for name, schema := range newProps {
				existingProps[name] = schema
			}
			return
		}
	}
	if key == "required" {
		existingList, okA := existing.([]any)
		newList, okB := value.([]any)
		if okA && okB {
			dst[key] = append(existingList, newList...)
			return
		}
	}
	// Keep the first definition: a $ref target is the base and the node that
	// referenced it has already contributed its own keys.
}

func sortedStrings(values []any) []any {
	names := make([]string, 0, len(values))
	for _, value := range values {
		name, ok := value.(string)
		if !ok {
			return values
		}
		names = append(names, name)
	}
	slices.Sort(names)
	names = slices.Compact(names)
	out := make([]any, len(names))
	for i, name := range names {
		out[i] = name
	}
	return out
}

// lookupRef follows a local JSON pointer such as #/components/schemas/X.
func lookupRef(doc map[string]any, ref string, stack []string) (any, error) {
	if slices.Contains(stack, ref) {
		return nil, fmt.Errorf("circular $ref %s", ref)
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("unsupported $ref %s: only local references are resolvable", ref)
	}
	var node any = doc
	for _, token := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		// The pointer travels in a URI fragment, so it is percent-encoded
		// first and JSON-pointer-escaped underneath. kie.ai has component
		// names with spaces, which arrive as %20.
		decoded, err := url.PathUnescape(token)
		if err != nil {
			return nil, fmt.Errorf("unresolvable $ref %s: %w", ref, err)
		}
		token = strings.ReplaceAll(decoded, "~1", "/")
		token = strings.ReplaceAll(token, "~0", "~")
		object, ok := node.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unresolvable $ref %s: %s is not an object", ref, token)
		}
		node, ok = object[token]
		if !ok {
			return nil, fmt.Errorf("unresolvable $ref %s: %s not found", ref, token)
		}
	}
	return node, nil
}

func stringField(object map[string]any, key string) string {
	value, _ := object[key].(string)
	return value
}

// decorationPrefixes open a paragraph that is layout rather than prose.
var decorationPrefixes = []string{"#", ":::", "<", ">", "-", "*", "|", "`", "!["}

// listItem matches the start of an ordered list, whose items run long and read
// as instructions rather than as a description of the model.
var listItem = regexp.MustCompile(`^\d+[.)]\s`)

// markup matches an HTML or MDX tag, which the docs use for cards and callouts.
var markup = regexp.MustCompile(`<[A-Za-z/]`)

// ProseLine returns the first paragraph of a Markdown blob that reads as a
// sentence about the model, collapsed onto one line. Pages routinely open with
// a heading, an admonition or a card group, none of which describe anything.
// An empty string means the text held no prose at all.
func ProseLine(text string) string {
	for _, paragraph := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		paragraph = strings.Join(strings.Fields(paragraph), " ")
		if paragraph == "" || hasAnyPrefix(paragraph, decorationPrefixes) {
			continue
		}
		if listItem.MatchString(paragraph) || markup.MatchString(paragraph) {
			continue
		}
		return paragraph
	}
	return ""
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
