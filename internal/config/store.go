// Package config reads and writes the settings kie-ai-cli keeps on disk.
//
// Where that file lives is not this package's business: every function here
// takes its path, and internal/paths is the one place that answers where it is.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// APIKeyEnv is the environment variable that overrides the configuration file.
const APIKeyEnv = "KIE_AI_API_KEY"

const (
	dirPerm  fs.FileMode = 0o700
	filePerm fs.FileMode = 0o600
)

// Settings is the content of the configuration file.
type Settings struct {
	APIKey string `json:"api_key,omitempty"`
}

// Load reads the configuration file. A missing file is not an error; it is an
// empty configuration.
func Load(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Save writes the configuration file, creating its directory.
//
// The file holds a credential, so it is written through a temporary file in the
// same directory that no one but the owner can read, and then renamed over the
// target. The mode of an existing file is therefore replaced rather than
// inherited, and a write that fails halfway leaves the previous file intact.
func Save(path string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return err
	}
	tmp := f.Name()
	// Once the rename below succeeds there is nothing left to remove; until
	// then this keeps a failed write from leaving the key in a stray file.
	defer func() { _ = os.Remove(tmp) }()

	if err := writeAndClose(f, data); err != nil {
		return fmt.Errorf("%s: %w", tmp, err)
	}
	return os.Rename(tmp, path)
}

// writeAndClose fills f and closes it, leaving it readable and writable by its
// owner alone. CreateTemp already uses that mode; Chmod makes it hold whatever
// the umask is, and Sync makes sure the content reaches disk before the rename
// publishes the file under its final name.
func writeAndClose(f *os.File, data []byte) error {
	err := func() error {
		if err := f.Chmod(filePerm); err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			return err
		}
		return f.Sync()
	}()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// KeySource says where a resolved API key was found.
type KeySource string

const (
	// KeyUnset means no key is configured anywhere.
	KeyUnset KeySource = ""
	// KeyFromEnv means the key came from the environment variable.
	KeyFromEnv KeySource = "env"
	// KeyFromFile means the key came from the configuration file.
	KeyFromFile KeySource = "file"
)

// APIKey is a resolved key together with where it was found.
type APIKey struct {
	Value  string
	Source KeySource
}

// ResolveAPIKey reports the key that is in effect. The environment variable
// wins over the configuration file, so that a key can be supplied for a single
// invocation without touching the file. An exported but empty variable is
// treated as absent, because that is how a shell unsets one.
func ResolveAPIKey(configPath string) (APIKey, error) {
	if v := os.Getenv(APIKeyEnv); v != "" {
		return APIKey{Value: v, Source: KeyFromEnv}, nil
	}
	s, err := Load(configPath)
	if err != nil {
		return APIKey{}, err
	}
	if s.APIKey != "" {
		return APIKey{Value: s.APIKey, Source: KeyFromFile}, nil
	}
	return APIKey{Source: KeyUnset}, nil
}

// IsSet reports whether a key was found at all.
func (k APIKey) IsSet() bool { return k.Source != KeyUnset }

// maskPrefix stands for everything that is withheld. Its width is fixed, so a
// masked key does not disclose how long the key is.
const maskPrefix = "****"

// maskTail is how many trailing characters a masked key keeps: enough to tell
// two keys apart, far too few to reconstruct one.
const maskTail = 4

// Masked renders the key for display. It keeps the last few characters so the
// reader can tell which of several keys is in effect. A key shorter than twice
// the tail is withheld entirely, since its tail would be most of it.
func (k APIKey) Masked() string {
	if !k.IsSet() {
		return ""
	}
	r := []rune(k.Value)
	if len(r) < 2*maskTail {
		return maskPrefix
	}
	return maskPrefix + string(r[len(r)-maskTail:])
}
