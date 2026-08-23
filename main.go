// Command kie-ai-cli is an unofficial command-line interface for kie.ai.
package main

import (
	"os"

	"github.com/douhashi/kie-ai-cli/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
