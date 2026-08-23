// Package cli turns command-line arguments into one command invocation and
// reports the exit code the process should end with.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/paths"
)

// name is how the tool refers to itself in the usage text and in messages.
const name = "kie-ai-cli"

const (
	exitOK = 0
	// exitError reports a command that was called correctly but failed.
	exitError = 1
	// exitUsage reports a mistake in how the command was called.
	exitUsage = 2
)

// command is one noun-verb pair. Every command the tool has is a row in
// commands below: the dispatch table and the usage text are the same list, so
// a command cannot exist without being documented.
type command struct {
	noun    string
	verb    string
	args    string
	summary string
	// bind registers the flags this command takes of its own and returns the
	// handler that reads them once they are parsed. A command that takes no
	// flags of its own is wrapped in noFlags.
	//
	// It is given the raw arguments after the verb as well, because one
	// command -- task run -- takes a flag per input field of the model
	// named in the first of them, and so cannot know its own flags until it
	// has read that far.
	bind func(*env, *flag.FlagSet, []string) (binding, error)
}

// binding is what registering a command's flags produced: the handler to run
// once they are parsed, and what to add to the message when one of them is
// rejected. The hint is for task run, whose flags come from the catalog rather
// than from the usage text and so cannot be listed there.
type binding struct {
	run  handler
	hint string
}

// handler runs one command against its environment and its positional
// arguments.
type handler func(*env, []string) error

// noFlags adapts a handler that takes no flags beyond the ones every command
// has.
func noFlags(run handler) func(*env, *flag.FlagSet, []string) (binding, error) {
	return func(*env, *flag.FlagSet, []string) (binding, error) { return binding{run: run}, nil }
}

// env is what a handler is given: the three streams, where the state lives,
// whether the caller asked for JSON, and what time it is -- which is a value
// rather than a call so that the age of the catalog in effect can be tested
// without waiting for it.
type env struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	paths  paths.Layout
	json   bool
	now    time.Time
}

var commands = []command{
	{
		noun: "catalog", verb: "update",
		summary: "Download the published model catalog and read it from now on.",
		bind:    noFlags(runCatalogUpdate),
	},
	{
		noun: "catalog", verb: "show",
		summary: "Show which model catalog is in effect and when it was generated.",
		bind:    noFlags(runCatalogShow),
	},
	{
		noun: "config", verb: "set", args: "<key> <value>",
		summary: "Set a configuration value.",
		bind:    noFlags(runConfigSet),
	},
	{
		noun: "config", verb: "show",
		summary: "Show the configuration and where the state is kept.",
		bind:    noFlags(runConfigShow),
	},
	{
		noun: "credits", verb: "show",
		summary: "Show the credit balance of the kie.ai account.",
		bind:    noFlags(runCreditsShow),
	},
	{
		noun: "model", verb: "list", args: "[--category <name>] [--vendor <name>]",
		summary: "List the models in the catalog in effect.",
		bind:    bindModelList,
	},
	{
		noun: "model", verb: "show", args: "<model-id>",
		summary: "Show one model, its documentation and its input fields.",
		bind:    noFlags(runModelShow),
	},
	{
		noun: "file", verb: "upload", args: "<path|url>",
		summary: "Upload a local file or a URL and print its download URL.",
		bind:    noFlags(runFileUpload),
	},
	{
		noun: "task", verb: "run", args: "<model-id> [--<field> <value>...] [--input <file|->]",
		summary: "Submit a task to one model and print the task ID.",
		bind:    bindTaskRun,
	},
}

// Run executes one invocation and reports the exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	err := dispatch(args, stdin, stdout, stderr)
	var ue usageError
	switch {
	case err == nil:
		return exitOK
	case errors.As(err, &ue):
		fmt.Fprintf(stderr, "%s: %v\n\n%s", name, err, usage())
		return exitUsage
	default:
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return exitError
	}
}

func dispatch(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage())
		return nil
	}
	if len(args) == 1 {
		switch args[0] {
		case "--version":
			fmt.Fprintln(stdout, version())
			return nil
		case "--help", "-h":
			fmt.Fprint(stdout, usage())
			return nil
		}
	}
	if len(args) < 2 {
		return usagef("expected a noun and a verb, got %q", args[0])
	}
	cmd := lookup(args[0], args[1])
	if cmd == nil {
		return usagef("unknown command: %s %s", args[0], args[1])
	}

	layout, err := paths.Resolve()
	if err != nil {
		return err
	}
	// Built before the flags are registered, because which flags a command
	// has can depend on what is on disk. Only json is still unknown here,
	// and nothing reads it until the handler runs.
	e := &env{stdin: stdin, stdout: stdout, stderr: stderr, paths: layout, now: time.Now()}

	fs := flag.NewFlagSet(cmd.noun+" "+cmd.verb, flag.ContinueOnError)
	// The usage text is printed by Run, once, for every kind of misuse.
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool(flagJSON, false, "print the result as JSON")
	bound, err := cmd.bind(e, fs, args[2:])
	if err != nil {
		return err
	}
	positional, err := parseFlags(fs, args[2:])
	if err != nil {
		message := fmt.Sprintf("%s %s: %v", cmd.noun, cmd.verb, err)
		if bound.hint != "" {
			message += "; " + bound.hint
		}
		return usageError{msg: message}
	}

	e.json = *asJSON
	return bound.run(e, positional)
}

func lookup(noun, verb string) *command {
	for i, c := range commands {
		if c.noun == noun && c.verb == verb {
			return &commands[i]
		}
	}
	return nil
}

// parseFlags collects the positional arguments, accepting flags before, between
// and after them. The flag package stops at the first argument that is not a
// flag, so parsing resumes after each one it hands back; "--" ends the flags
// for good, and everything past it is a value even if it looks like a flag.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		if consumed := len(args) - len(rest); consumed > 0 && args[consumed-1] == "--" {
			return append(positional, rest...), nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// writeJSON is how every command answers --json: one indented document, so
// that the answers are the same shape to read and to diff.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// usageError is a mistake in how the command was called. It is shown with the
// usage text and ends the process with exitUsage.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error {
	return usageError{msg: fmt.Sprintf(format, a...)}
}

func usage() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s - an unofficial command-line interface for kie.ai.\n\n", name)
	fmt.Fprintf(&b, "Usage:\n  %s <noun> <verb> [arguments] [flags]\n\n", name)

	w := tabwriter.NewWriter(&b, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "Commands:")
	for _, c := range commands {
		fmt.Fprintf(w, "  %s\t%s\n", strings.TrimRight(c.noun+" "+c.verb+" "+c.args, " "), c.summary)
	}
	fmt.Fprintln(w, "\nFlags:")
	fmt.Fprintf(w, "  --json\tPrint the result as JSON.\n")
	fmt.Fprintf(w, "  --version\tPrint the version and exit.\n")
	fmt.Fprintf(w, "  --help\tPrint this message and exit.\n")
	_ = w.Flush()

	fmt.Fprintf(&b, "\nSee https://github.com/douhashi/kie-ai-cli.\n")
	return b.String()
}

// version reports the version stamped into the binary at build time.
// The Go toolchain derives it from the module version or the VCS state, so
// there is no version constant to keep in sync with the release tags.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "unknown"
	}
	return info.Main.Version
}
