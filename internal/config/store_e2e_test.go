//go:build e2e

// The unit tests only prove that a key is resolved the way this package says it
// is. They cannot prove that the key kie.ai accepts is the one being resolved,
// so this test authenticates against the real API with it. Run it with
// `infisical run -- go test -tags e2e ./internal/config`.
package config_test

import (
	"os"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/config"
	"github.com/douhashi/kie-ai-cli/internal/kie"
)

// V3: a key reaches kie.ai whichever of the two places it is configured in.
func TestResolvedKeyAuthenticates(t *testing.T) {
	key := os.Getenv(config.APIKeyEnv)
	if key == "" {
		t.Skipf("%s is not set; run this test through `infisical run --`", config.APIKeyEnv)
	}

	t.Run("from the environment", func(t *testing.T) {
		path := configPath(t)
		t.Setenv(config.APIKeyEnv, key)

		resolved := resolve(t, path)
		if resolved.Source != config.KeyFromEnv {
			t.Fatalf("source = %q, want %q", resolved.Source, config.KeyFromEnv)
		}
		authenticate(t, resolved)
	})

	t.Run("from the configuration file", func(t *testing.T) {
		path := configPath(t)
		t.Setenv(config.APIKeyEnv, "")
		if err := config.Save(path, config.Settings{APIKey: key}); err != nil {
			t.Fatalf("Save: %v", err)
		}

		resolved := resolve(t, path)
		if resolved.Source != config.KeyFromFile {
			t.Fatalf("source = %q, want %q", resolved.Source, config.KeyFromFile)
		}
		authenticate(t, resolved)
	})
}

func resolve(t *testing.T, path string) config.APIKey {
	t.Helper()
	key, err := config.ResolveAPIKey(path)
	if err != nil {
		t.Fatalf("ResolveAPIKey: %v", err)
	}
	return key
}

// authenticate calls kie.ai with the resolved key. Reading the balance is the
// cheapest authenticated call there is: it creates nothing. Which endpoint that
// is, and how its answer is read, belongs to internal/kie; this test only cares
// that the key it resolved is one kie.ai accepts. Only the mask of the key is
// ever logged, so a failing run does not put the credential in the output.
func authenticate(t *testing.T, key config.APIKey) {
	t.Helper()
	balance, err := kie.New(key.Value).Credits(t.Context())
	if err != nil {
		t.Fatalf("reading the balance with the key %s from %q: %v", key.Masked(), key.Source, err)
	}
	t.Logf("the key %s from %q reads a balance of %s", key.Masked(), key.Source, balance)
}
