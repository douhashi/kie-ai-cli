// Command kie-ai-cli is an unofficial command-line interface for kie.ai.
package main

import (
	"fmt"
	"os"
	"runtime/debug"
)

const usage = `kie-ai-cli - an unofficial command-line interface for kie.ai.

Usage:
  kie-ai-cli --version    Print the version and exit.
  kie-ai-cli --help       Print this message and exit.

No operations are available yet. See https://github.com/douhashi/kie-ai-cli.
`

func main() {
	args := os.Args[1:]

	if len(args) == 1 && args[0] == "--version" {
		fmt.Println(version())
		return
	}
	if len(args) == 0 || (len(args) == 1 && (args[0] == "--help" || args[0] == "-h")) {
		fmt.Print(usage)
		return
	}

	fmt.Fprint(os.Stderr, usage)
	os.Exit(2)
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
