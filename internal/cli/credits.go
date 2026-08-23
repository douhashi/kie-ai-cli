package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/douhashi/kie-ai-cli/internal/config"
	"github.com/douhashi/kie-ai-cli/internal/kie"
)

func runCreditsShow(e *env, args []string) error {
	if len(args) != 0 {
		return usagef("credits show: expected no arguments, got %d", len(args))
	}
	client, err := e.client()
	if err != nil {
		return err
	}
	balance, err := client.Credits(context.Background())
	if err != nil {
		return err
	}
	return writeCredits(e.stdout, balance, e.json)
}

// newClient builds the client a command talks to kie.ai with. It is a variable
// so that a test can point every command at a server of its own; nothing in a
// shipped build assigns to it, which is why there is no flag for it.
var newClient = kie.New

// client resolves the API key and builds a client for kie.ai. A key that is
// nowhere to be found is reported here, once, in terms of the two places it
// can be put -- the caller would otherwise learn about it from an
// authentication failure that names neither.
func (e *env) client() (*kie.Client, error) {
	key, err := config.ResolveAPIKey(e.paths.Config)
	if err != nil {
		return nil, err
	}
	if !key.IsSet() {
		return nil, fmt.Errorf("no API key is configured: set %s, or run `%s config set %s <value>`",
			config.APIKeyEnv, name, keyAPIKey)
	}
	return newClient(key.Value), nil
}

// creditsResult is the JSON contract of credits show. The balance is a
// json.Number so that it is written back exactly as kie.ai sent it.
type creditsResult struct {
	Credits json.Number `json:"credits"`
}

func writeCredits(w io.Writer, balance json.Number, asJSON bool) error {
	if asJSON {
		return writeJSON(w, creditsResult{Credits: balance})
	}
	// One row, tab-separated like config show, so that cut(1) reaches the
	// value without the caller having to ask for JSON.
	_, err := fmt.Fprintf(w, "credits\t%s\n", balance)
	return err
}
