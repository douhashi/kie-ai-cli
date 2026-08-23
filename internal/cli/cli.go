// Package cli turns command-line arguments into one command invocation and
// reports the exit code the process should end with.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
	"text/tabwriter"

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
	run     func(*env, []string) error
}

// env is what a handler is given: where to write its result, where the state
// lives, and whether the caller asked for JSON.
type env struct {
	stdout io.Writer
	paths  paths.Layout
	json   bool
}

var commands = []command{
	{
		noun: "config", verb: "set", args: "<key> <value>",
		summary: "Set a configuration value.",
		run:     runConfigSet,
	},
	{
		noun: "config", verb: "show",
		summary: "Show the configuration and where the state is kept.",
		run:     runConfigShow,
	},
	{
		noun: "credits", verb: "show",
		summary: "Show the credit balance of the kie.ai account.",
		run:     runCreditsShow,
	},
}

// Run executes one invocation and reports the exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	err := dispatch(args, stdout)
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

func dispatch(args []string, stdout io.Writer) error {
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

	fs := flag.NewFlagSet(cmd.noun+" "+cmd.verb, flag.ContinueOnError)
	// The usage text is printed by Run, once, for every kind of misuse.
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "print the result as JSON")
	positional, err := parseFlags(fs, args[2:])
	if err != nil {
		return usagef("%s %s: %v", cmd.noun, cmd.verb, err)
	}

	layout, err := paths.Resolve()
	if err != nil {
		return err
	}
	return cmd.run(&env{stdout: stdout, paths: layout, json: *asJSON}, positional)
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
