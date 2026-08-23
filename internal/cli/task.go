package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
	"github.com/douhashi/kie-ai-cli/internal/ledger"
)

// The flags task run has of its own. They are named here because the input
// fields come from the catalog: a model that declared a field by one of these
// names would otherwise be registered twice, which the flag package answers
// with a panic.
const (
	flagJSON  = "json"
	flagInput = "input"
)

// bindTaskRun reads the model out of the raw arguments and gives the command
// one flag per input field that model takes.
//
// The model has to be the first argument after the verb, because which flags
// exist is decided by it: they cannot be registered before it is known, and
// the flag package stops at the first argument it does not recognise.
func bindTaskRun(e *env, fs *flag.FlagSet, args []string) (binding, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return binding{}, usagef("task run: expected <model-id> as the first argument after the verb")
	}
	c, err := loadCatalog(e)
	if err != nil {
		return binding{}, err
	}
	m, err := findModel(c, args[0])
	if err != nil {
		return binding{}, err
	}

	schema := newInputSchema(m.Input)
	fields := bindFields(fs, schema)
	document := fs.String(flagInput, "", "read the input fields from a JSON document, or from standard input for -")
	return binding{
		run: func(e *env, positional []string) error {
			return runTaskRun(e, m, schema, fields, *document, positional)
		},
		hint: fmt.Sprintf("run `%s model show %s` to see the fields this model takes, or `%s catalog update` if the model is newer than this catalog", name, m.ID, name),
	}, nil
}

func runTaskRun(e *env, m catalog.Model, schema inputSchema, fields []*field, document string, args []string) error {
	if len(args) != 1 {
		return usagef("task run: expected <model-id> and nothing else, got %d arguments", len(args))
	}
	// Resolved before anything is read or opened, so that a missing key is
	// reported as itself rather than behind an unrelated complaint.
	client, err := e.client()
	if err != nil {
		return err
	}

	input, err := readInputDocument(e, document)
	if err != nil {
		return err
	}
	// A flag wins over the document it was given beside. The other way
	// round, adding a flag to a saved input would silently do nothing.
	for _, f := range fields {
		if f.set {
			input[f.name] = f.value
		}
	}
	if err := validateInput(schema, input, m.ID); err != nil {
		return err
	}

	ctx := context.Background()
	// Opened before the task is created: a ledger that cannot be opened is
	// worth finding out about while nothing has been charged for yet.
	l, err := ledger.Open(ctx, e.paths.Ledger)
	if err != nil {
		return err
	}
	defer func() { _ = l.Close() }()

	taskID, err := client.CreateTask(ctx, m.Create.Path, requestBody(m, input))
	if err != nil {
		return err
	}
	if err := l.Add(ctx, taskID, m.ID, ledger.StatusSubmitted, input); err != nil {
		// The task exists and has been paid for. Its id is the only
		// handle on it, so it goes to stdout even now; what failed is
		// reported beside it, on the other stream, and carries the id
		// too -- which is why a failure to write it changes nothing
		// about what there is to say.
		_ = writeTaskID(e, taskID)
		return fmt.Errorf("the task was submitted as %s but the ledger did not record it: %w", taskID, err)
	}
	return writeTaskID(e, taskID)
}

// marketRequest is the body the Market endpoint takes: one path serves every
// model, so the model is named beside the input it is given.
type marketRequest struct {
	Model string         `json:"model"`
	Input map[string]any `json:"input"`
}

// requestBody wraps the input the way the endpoint this model is submitted to
// expects it.
//
// Neither form carries a callBackUrl. Nothing here is listening on one, and an
// address that answers nothing would have kie.ai retrying against it; the state
// of a task is asked for instead.
func requestBody(m catalog.Model, input map[string]any) any {
	if m.Create.Style == catalog.StyleMarket {
		return marketRequest{Model: m.Create.Model, Input: input}
	}
	return input
}

// readInputDocument reads the JSON object --input names, and returns an empty
// one when it names nothing.
func readInputDocument(e *env, path string) (map[string]any, error) {
	if path == "" {
		return map[string]any{}, nil
	}
	source := e.stdin
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		source = f
	}

	dec := json.NewDecoder(source)
	// UseNumber so that a number survives as it was written: read through
	// float64, a seed or an id would be sent back in exponent notation.
	dec.UseNumber()
	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("--%s %s: the document is empty", flagInput, path)
		}
		return nil, fmt.Errorf("--%s %s: %w", flagInput, path, err)
	}
	input, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("--%s %s: the document is %s; the input is a JSON object of fields", flagInput, path, describe(decoded))
	}
	if dec.More() {
		return nil, fmt.Errorf("--%s %s: the document holds more than one JSON value", flagInput, path)
	}
	return input, nil
}

// taskResult is the JSON contract of task run: the id, which is what every
// later command about this task is given.
type taskResult struct {
	TaskID string `json:"taskId"`
}

func writeTaskID(e *env, taskID string) error {
	if e.json {
		return writeJSON(e.stdout, taskResult{TaskID: taskID})
	}
	// Tab-separated like the other one-value answers, so that cut(1)
	// reaches the id without the caller having to ask for JSON.
	_, err := fmt.Fprintf(e.stdout, "taskId\t%s\n", taskID)
	return err
}

// field is one input field bound to a flag, holding what the command line gave
// it in the shape it will be sent.
type field struct {
	property
	value any
	set   bool
}

// bindFields registers one flag per input field the model declares.
//
// A name that cannot be a flag is skipped rather than spelled differently:
// --input reaches every field, and a spelling the documentation does not use
// would leave the caller nothing to look the field up by.
func bindFields(fs *flag.FlagSet, schema inputSchema) []*field {
	var fields []*field
	for _, p := range schema.properties() {
		if !flaggable(p.name) {
			continue
		}
		f := &field{property: p}
		fs.Var(f, p.name, strings.Join(p.types, " or "))
		fields = append(fields, f)
	}
	return fields
}

// flaggable reports whether a field name can be written as a flag.
//
// Three of the catalog's fields are declared with a trailing space, which no
// shell passes through; a name that starts with a dash or holds an equals sign
// is one the flag package refuses; and a name this tool already uses for a flag
// of its own would be registered twice, which is a panic. The catalog is
// generated from documentation this project does not own, so all three are
// possible without anyone having read the name first.
func flaggable(name string) bool {
	if name == "" || name == flagJSON || name == flagInput {
		return false
	}
	if strings.HasPrefix(name, "-") || strings.ContainsRune(name, '=') {
		return false
	}
	return strings.IndexFunc(name, unicode.IsSpace) < 0
}

// String is what the flag package prints for the value in hand. It is empty
// because a field holds nothing until it is given something: the defaults
// belong to kie.ai, and printing one here would suggest this tool sends it.
func (f *field) String() string { return "" }

// IsBoolFlag lets a boolean field be written on its own -- --f, and --f=false
// for the other way round -- which is what the true/false the documentation
// describes reads as on a command line.
func (f *field) IsBoolFlag() bool { return f.only("boolean") }

// Set records one occurrence of the flag. An array collects one element per
// occurrence, so that a value holding a space or a comma needs no quoting rule
// of its own.
func (f *field) Set(raw string) error {
	value, err := f.read(raw)
	if err != nil {
		return err
	}
	if f.only("array") {
		elements, _ := f.value.([]any)
		value = append(elements, value)
	}
	f.value = value
	f.set = true
	return nil
}

// only reports whether typ is the one type the schema declares for the field.
func (f *field) only(typ string) bool {
	return len(f.types) == 1 && f.types[0] == typ
}

func (f *field) read(raw string) (any, error) {
	if f.only("array") {
		return readValue(stringOf(f.items["type"]), raw)
	}
	if len(f.types) == 1 && f.types[0] != "" {
		return readValue(f.types[0], raw)
	}
	// Either the branches declare the field as more than one type -- one
	// model in the catalog does -- or they declare none at all.
	return readEither(f.types, raw), nil
}

// readValue reads one word of the command line as the type the schema declares.
// Anything that is not a scalar is written as JSON, which is the only way an
// object fits in a single argument.
func readValue(typ, raw string) (any, error) {
	switch typ {
	case "string":
		return raw, nil
	case "boolean":
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("%q is not a boolean; write true or false", raw)
		}
		return value, nil
	case "integer":
		if _, err := strconv.ParseInt(raw, 10, 64); err != nil {
			return nil, fmt.Errorf("%q is not an integer", raw)
		}
		// Carried as the digits that were typed: read through float64,
		// a large seed would be sent back rounded.
		return json.Number(raw), nil
	case "number":
		if _, err := strconv.ParseFloat(raw, 64); err != nil {
			return nil, fmt.Errorf("%q is not a number", raw)
		}
		return json.Number(raw), nil
	case "":
		return readEither(nil, raw), nil
	default:
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, fmt.Errorf("%q is not the JSON %s this field takes", raw, typ)
		}
		return value, nil
	}
}

// readEither reads a word for a field whose type the schema leaves open: one
// declared differently by different branches, or not declared at all.
//
// The JSON reading comes first, so that the array form of a field that also
// takes a string can be written as an array. A word that is not JSON, or that
// is JSON of a type the field does not accept, is the string it looks like.
func readEither(types []string, raw string) any {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	if len(types) == 0 || slices.ContainsFunc(types, func(typ string) bool { return matchesType(typ, value) }) {
		return value
	}
	return raw
}

// validateInput reports everything the catalog can tell is wrong with the
// input, all at once.
//
// A submission cannot be taken back and has been paid for by the time it is
// answered, so this is the last point at which a typo costs nothing -- and a
// caller who had to submit again to learn about the next mistake would pay for
// the discovery every time.
func validateInput(schema inputSchema, input map[string]any, modelID string) error {
	declared := make(map[string]property)
	for _, p := range schema.properties() {
		declared[p.name] = p
	}

	var problems []string
	for _, name := range slices.Sorted(maps.Keys(input)) {
		p, ok := declared[name]
		if !ok {
			problems = append(problems, fmt.Sprintf("%q is not a field this model takes", name))
			continue
		}
		value := input[name]
		if !slices.ContainsFunc(p.types, func(typ string) bool { return matchesType(typ, value) }) {
			problems = append(problems, fmt.Sprintf("%q must be %s, not %s",
				name, article(strings.Join(p.types, " or ")), describe(value)))
		}
	}

	// The root's own required fields are always asked for; the branches are
	// alternatives, so one complete branch is all that is needed.
	for _, name := range missingFrom(schema.root, input) {
		problems = append(problems, fmt.Sprintf("the required field %q is missing", name))
	}
	if incomplete := incompleteAlternatives(schema, input); incomplete != "" {
		problems = append(problems, incomplete)
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%s: %s; run `%s model show %s` to see the fields this model takes, or `%s catalog update` if the model is newer than this catalog",
		modelID, strings.Join(problems, "; "), name, modelID, name)
}

// incompleteAlternatives describes what each alternative is still missing, and
// is empty as soon as one of them is complete.
func incompleteAlternatives(schema inputSchema, input map[string]any) string {
	if len(schema.branches) == 0 {
		return ""
	}
	var short []string
	for i, b := range schema.branches {
		missing := missingFrom(b.schema, input)
		if len(missing) == 0 {
			return ""
		}
		short = append(short, fmt.Sprintf("%s needs %s", b.label(i), quoteAll(missing)))
	}
	return "none of the alternatives is complete: " + strings.Join(short, "; ")
}

// missingFrom lists the fields one object schema insists on and the input does
// not carry.
func missingFrom(schema map[string]any, input map[string]any) []string {
	var missing []string
	for _, name := range requiredOf(schema) {
		if _, ok := input[name]; !ok {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	return missing
}

func quoteAll(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, strconv.Quote(n))
	}
	return strings.Join(quoted, ", ")
}

// describe names the JSON type of a value for a message, so that a mismatch
// says what was written as well as what was wanted.
func describe(v any) string {
	if typ := jsonTypeOf(v); typ != "" {
		return article(typ)
	}
	return "something this tool cannot read"
}

// article prefixes a type name the way the message reads it aloud.
func article(typ string) string {
	if typ == "" {
		return typ
	}
	if strings.ContainsRune("aeiou", rune(typ[0])) {
		return "an " + typ
	}
	return "a " + typ
}
