package cli

import (
	_ "embed"
	"flag"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/template"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
)

// The shells `completion show` writes a script for.
const (
	shellBash = "bash"
	shellZsh  = "zsh"
)

// The axes `completion list` prints the values of. They are the fields of the
// catalog index, which is what lets a shell complete them on a keystroke.
const (
	axisModel    = "model"
	axisCategory = "category"
	axisVendor   = "vendor"
)

// The two scripts. What they hold that changes -- the commands, their verbs
// and their flags -- is filled in from the command table below, so a command
// added there is completed without either file being touched.
var (
	//go:embed completion.bash
	bashScript string
	//go:embed completion.zsh
	zshScript string
)

func runCompletionShow(e *env, args []string) error {
	if len(args) != 1 {
		return usagef("completion show: expected <%s|%s>, got %d arguments", shellBash, shellZsh, len(args))
	}
	var text string
	switch args[0] {
	case shellBash:
		text = bashScript
	case shellZsh:
		text = zshScript
	default:
		return usagef("completion show: no script for %q; this tool writes one for %s and %s", args[0], shellBash, shellZsh)
	}
	rendered, err := template.New(args[0]).Parse(text)
	if err != nil {
		return err
	}
	return rendered.Execute(e.stdout, newCompletionScript(e))
}

func runCompletionList(e *env, args []string) error {
	if len(args) != 1 {
		return usagef("completion list: expected <%s|%s|%s>, got %d arguments",
			axisModel, axisCategory, axisVendor, len(args))
	}
	// The index rather than the catalog: this runs on every TAB, and decoding
	// the catalog to answer with three words of it would be felt.
	entries, err := catalog.LoadIndex(e.paths.Catalog)
	if err != nil {
		return err
	}

	var values []string
	switch args[0] {
	case axisModel:
		// In the order the catalog holds them, which is by id.
		for _, entry := range entries {
			values = append(values, entry.ID)
		}
	case axisCategory:
		values = distinct(entries, func(e catalog.IndexEntry) string { return e.Category })
	case axisVendor:
		values = distinct(entries, func(e catalog.IndexEntry) string { return e.Vendor })
	default:
		return usagef("completion list: unknown axis %q; the axes are %s, %s and %s",
			args[0], axisModel, axisCategory, axisVendor)
	}

	// One value per line: what a shell splits on without a quoting rule of
	// its own, and what keeps a value holding a space from becoming two.
	for _, v := range values {
		if _, err := fmt.Fprintln(e.stdout, v); err != nil {
			return err
		}
	}
	return nil
}

// candidates says where the words for one position on the command line come
// from: a set this binary decides, or one of the axes, which the catalog
// decides and so is asked for at the moment of completion.
type candidates struct {
	fixed []string
	axis  string
}

func fixed(values ...string) candidates { return candidates{fixed: values} }

func from(axis string) candidates { return candidates{axis: axis} }

// words renders the candidates as the word list the generated script hands to
// its helper. A fixed set is written into the script because only a new
// binary can change it; an axis is written as the call that asks this binary,
// because `catalog update` changes it without the script being rewritten.
func (c candidates) words() string {
	if c.axis != "" {
		return fmt.Sprintf(`$("$exe" completion list %s)`, c.axis)
	}
	return strings.Join(c.fixed, " ")
}

// completionScript is what the two templates render: the command table as a
// shell has to see it.
type completionScript struct {
	// Program and Short are the two names the tool is installed under, Func
	// the shell function the script defines and Programs both names as the
	// registration takes them.
	Program  string
	Short    string
	Func     string
	Programs string
	Nouns    string
	Verbs    []nounVerbs
	Commands []commandScript
}

// nounVerbs is one noun and the verbs it takes, which is what the shell
// completes once the noun has been typed.
type nounVerbs struct {
	Noun  string
	Verbs string
}

// commandScript is one noun-verb pair as the script needs it: the flags it
// accepts, where its first argument comes from, and where the value of each
// flag that takes one comes from.
type commandScript struct {
	Name       string
	Flags      string
	Arg        string
	FlagValues []flagCandidates
}

type flagCandidates struct {
	Flag   string
	Values string
}

func newCompletionScript(e *env) completionScript {
	s := completionScript{
		Program:  name,
		Short:    shortName,
		Func:     "_" + strings.ReplaceAll(name, "-", "_"),
		Programs: shortName + " " + name,
	}
	var nouns []string
	verbs := map[string][]string{}
	for _, c := range commands() {
		if _, seen := verbs[c.noun]; !seen {
			nouns = append(nouns, c.noun)
		}
		verbs[c.noun] = append(verbs[c.noun], c.verb)
		s.Commands = append(s.Commands, newCommandScript(e, c))
	}
	s.Nouns = strings.Join(nouns, " ")
	for _, noun := range nouns {
		s.Verbs = append(s.Verbs, nounVerbs{Noun: noun, Verbs: strings.Join(verbs[noun], " ")})
	}
	return s
}

func newCommandScript(e *env, c command) commandScript {
	cs := commandScript{
		Name:  c.noun + " " + c.verb,
		Flags: strings.Join(flagsOf(e, c), " "),
		Arg:   c.arg.words(),
	}
	for _, flagName := range slices.Sorted(maps.Keys(c.flags)) {
		cs.FlagValues = append(cs.FlagValues, flagCandidates{
			Flag: flagName, Values: c.flags[flagName].words(),
		})
	}
	return cs
}

// flagsOf lists the flags a command takes, in the form they are written on the
// command line.
//
// They are read from the same FlagSet the command is parsed with rather than
// from a table beside it, so the script cannot offer a flag the command does
// not have, or miss one it does.
//
// The error binding reports is dropped: task run refuses to bind without the
// model whose input fields it takes a flag for, and what is wanted here is
// exactly what it registered before it looked -- the flags every call of it
// accepts. Its per-model flags are out of reach of a static script anyway.
func flagsOf(e *env, c command) []string {
	fs, _ := newFlagSet(c)
	_, _ = c.bind(e, fs, nil)
	var flags []string
	fs.VisitAll(func(f *flag.Flag) { flags = append(flags, "--"+f.Name) })
	return flags
}
