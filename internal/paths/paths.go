// Package paths resolves the on-disk locations kie-ai-cli owns.
//
// Everything the CLI persists lives under one root directory, so a user can
// inspect or delete the whole of it in a single place.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// dirName is the directory kie-ai-cli owns inside the data directory.
const dirName = "kie-ai-cli"

// The names of what lives inside the root. The settings sit in their own
// directory so that later files of the same kind have a home beside them.
const (
	ledgerName = "ledger.db"
	configDir  = "config"
	configName = "config.json"
	catalogDir = "catalog"
)

// Layout is every location kie-ai-cli owns.
type Layout struct {
	Root    string
	Config  string
	Catalog string
	Ledger  string
}

// Root returns the directory holding everything kie-ai-cli persists, creating
// it if it does not exist yet. It is $XDG_DATA_HOME/kie-ai-cli, falling back
// to ~/.local/share/kie-ai-cli when XDG_DATA_HOME is unset. A relative
// XDG_DATA_HOME is ignored, as the XDG Base Directory specification requires.
//
// The directory is created with 0700 because what it holds — the record of
// what the user submitted, and the credentials that go beside it — is the
// user's alone.
func Root() (string, error) {
	root, err := rootPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create data directory %s: %w", root, err)
	}
	return root, nil
}

// Resolve returns every location at once, creating the root. It is the only
// answer to where each piece of state lives: a caller that resolved one of
// them for itself could disagree with the rest about where the root is.
func Resolve() (Layout, error) {
	root, err := Root()
	if err != nil {
		return Layout{}, err
	}
	return Layout{
		Root:    root,
		Config:  filepath.Join(root, configDir, configName),
		Catalog: filepath.Join(root, catalogDir),
		Ledger:  filepath.Join(root, ledgerName),
	}, nil
}

// Ledger returns the path of the SQLite task ledger, creating the root.
func Ledger() (string, error) {
	l, err := Resolve()
	if err != nil {
		return "", err
	}
	return l.Ledger, nil
}

func rootPath() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, dirName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", dirName), nil
}
