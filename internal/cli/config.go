package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/douhashi/kie-ai-cli/internal/config"
	"github.com/douhashi/kie-ai-cli/internal/paths"
)

// keyAPIKey is the only key config set accepts. Rejecting anything else keeps a
// typo from being written to the file and silently ignored ever after.
const keyAPIKey = "api_key"

func runConfigSet(e *env, args []string) error {
	if len(args) != 2 {
		return usagef("config set: expected <key> <value>")
	}
	key, value := args[0], args[1]
	if key != keyAPIKey {
		return usagef("config set: unknown key %q, expected %q", key, keyAPIKey)
	}
	if value == "" {
		return usagef("config set: %s must not be empty", key)
	}

	settings, err := config.Load(e.paths.Config)
	if err != nil {
		return err
	}
	settings.APIKey = value
	if err := config.Save(e.paths.Config, settings); err != nil {
		return err
	}
	if !e.json {
		return nil
	}
	// The command has no line of its own to print, so with --json it answers
	// with the state it produced.
	return writeStateJSON(e)
}

func runConfigShow(e *env, args []string) error {
	if len(args) != 0 {
		return usagef("config show: expected no arguments, got %d", len(args))
	}
	if e.json {
		return writeStateJSON(e)
	}
	return writeStateText(e)
}

// The two states an API key can be in, as reported to the caller.
const (
	keyStateSet   = "set"
	keyStateUnset = "unset"
)

// state is what config reports: which API key is in effect, where it came from,
// and where each piece of state lives.
//
// The mask deliberately does not sit in a field named api_key. A consumer that
// reads a field by that name expects a usable credential and would send the
// mask to the API; separate fields make that mistake impossible to write.
type state struct {
	APIKeyState  string `json:"api_key_state"`
	APIKeySource string `json:"api_key_source"`
	APIKeyMasked string `json:"api_key_masked"`
	Root         string `json:"root"`
	ConfigFile   string `json:"config_file"`
	CatalogDir   string `json:"catalog_dir"`
	LedgerFile   string `json:"ledger_file"`
}

func newState(p paths.Layout) (state, error) {
	key, err := config.ResolveAPIKey(p.Config)
	if err != nil {
		return state{}, err
	}
	keyState := keyStateUnset
	if key.IsSet() {
		keyState = keyStateSet
	}
	return state{
		APIKeyState:  keyState,
		APIKeySource: string(key.Source),
		APIKeyMasked: key.Masked(),
		Root:         p.Root,
		ConfigFile:   p.Config,
		CatalogDir:   p.Catalog,
		LedgerFile:   p.Ledger,
	}, nil
}

// apiKeyLine describes the key in one line, showing enough of it to tell which
// key is in effect when the environment and the file disagree.
func (s state) apiKeyLine() string {
	if s.APIKeyState != keyStateSet {
		return s.APIKeyState
	}
	return fmt.Sprintf("%s (%s) %s", s.APIKeyState, s.APIKeySource, s.APIKeyMasked)
}

func writeStateText(e *env) error {
	s, err := newState(e.paths)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(e.stdout, 0, 0, 2, ' ', 0)
	for _, row := range [][2]string{
		{"api_key", s.apiKeyLine()},
		{"root", s.Root},
		{"config", s.ConfigFile},
		{"catalog", s.CatalogDir},
		{"ledger", s.LedgerFile},
	} {
		fmt.Fprintf(w, "%s\t%s\n", row[0], row[1])
	}
	return w.Flush()
}

func writeStateJSON(e *env) error {
	s, err := newState(e.paths)
	if err != nil {
		return err
	}
	return writeJSON(e.stdout, s)
}
