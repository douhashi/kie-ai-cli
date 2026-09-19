package cli

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/douhashi/kie-ai-cli/internal/config"
	"github.com/douhashi/kie-ai-cli/internal/paths"
)

// The keys config set accepts. Rejecting anything else keeps a typo from being
// written to the file and silently ignored ever after.
const (
	keyAPIKey       = "api_key"
	keyUSDPerCredit = "usd_per_credit"
)

// setting is one key config set accepts, and how a value given for it is
// written into the settings. A value it refuses is a usage error, and leaves
// the file as it was.
type setting struct {
	key   string
	apply func(s *config.Settings, value string) error
}

var settings = []setting{
	{key: keyAPIKey, apply: func(s *config.Settings, value string) error {
		s.APIKey = value
		return nil
	}},
	{key: keyUSDPerCredit, apply: func(s *config.Settings, value string) error {
		rate, err := config.ParseUSDPerCredit(value)
		if err != nil {
			return usagef("config set: %v", err)
		}
		s.USDPerCredit = &rate
		return nil
	}},
}

// settingKeys lists the keys config set accepts, in the order they are shown.
func settingKeys() []string {
	keys := make([]string, 0, len(settings))
	for _, s := range settings {
		keys = append(keys, s.key)
	}
	return keys
}

func runConfigSet(e *env, args []string) error {
	if len(args) != 2 {
		return usagef("config set: expected <key> <value>")
	}
	key, value := args[0], args[1]
	i := slices.IndexFunc(settings, func(s setting) bool { return s.key == key })
	if i < 0 {
		return usagef("config set: unknown key %q, expected one of %s", key, strings.Join(settingKeys(), ", "))
	}
	if value == "" {
		return usagef("config set: %s must not be empty", key)
	}

	stored, err := config.Load(e.paths.Config)
	if err != nil {
		return err
	}
	if err := settings[i].apply(&stored, value); err != nil {
		return err
	}
	if err := config.Save(e.paths.Config, stored); err != nil {
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
// the rate credits are converted to US dollars at and where that came from, and
// where each piece of state lives.
//
// The mask deliberately does not sit in a field named api_key. A consumer that
// reads a field by that name expects a usable credential and would send the
// mask to the API; separate fields make that mistake impossible to write.
type state struct {
	APIKeyState  string `json:"api_key_state"`
	APIKeySource string `json:"api_key_source"`
	APIKeyMasked string `json:"api_key_masked"`
	// USDPerCreditSource is "default" when the file says nothing, so that a
	// consumer can tell the built-in rate from one that was chosen.
	USDPerCredit       float64 `json:"usd_per_credit"`
	USDPerCreditSource string  `json:"usd_per_credit_source"`
	Root               string  `json:"root"`
	ConfigFile         string  `json:"config_file"`
	CatalogDir         string  `json:"catalog_dir"`
	LedgerFile         string  `json:"ledger_file"`
}

func newState(p paths.Layout) (state, error) {
	key, err := config.ResolveAPIKey(p.Config)
	if err != nil {
		return state{}, err
	}
	rate, err := config.ResolveUSDPerCredit(p.Config)
	if err != nil {
		return state{}, err
	}
	keyState := keyStateUnset
	if key.IsSet() {
		keyState = keyStateSet
	}
	return state{
		APIKeyState:        keyState,
		APIKeySource:       string(key.Source),
		APIKeyMasked:       key.Masked(),
		USDPerCredit:       rate.Value,
		USDPerCreditSource: string(rate.Source),
		Root:               p.Root,
		ConfigFile:         p.Config,
		CatalogDir:         p.Catalog,
		LedgerFile:         p.Ledger,
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

// usdPerCreditLine describes the rate and whether it was chosen or is the
// built-in one.
func (s state) usdPerCreditLine() string {
	return fmt.Sprintf("%s (%s)", strconv.FormatFloat(s.USDPerCredit, 'f', -1, 64), s.USDPerCreditSource)
}

func writeStateText(e *env) error {
	s, err := newState(e.paths)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(e.stdout, 0, 0, 2, ' ', 0)
	for _, row := range [][2]string{
		{keyAPIKey, s.apiKeyLine()},
		{keyUSDPerCredit, s.usdPerCreditLine()},
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
