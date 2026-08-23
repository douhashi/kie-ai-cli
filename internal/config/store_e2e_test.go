//go:build e2e

// The unit tests only prove that a key is resolved the way this package says it
// is. They cannot prove that the key kie.ai accepts is the one being resolved,
// so this test authenticates against the real API with it. Run it with
// `infisical run -- go test -tags e2e ./internal/config`.
package config_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/config"
)

// creditURL is the cheapest authenticated endpoint kie.ai has: it reads the
// balance and creates nothing.
const creditURL = "https://api.kie.ai/api/v1/chat/credit"

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

// authenticate calls kie.ai with the resolved key. Only the mask of the key is
// ever logged, so a failing run does not put the credential in the output.
func authenticate(t *testing.T, key config.APIKey) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, creditURL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key.Value)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s with the key from %q: %v", creditURL, key.Source, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		t.Fatalf("reading the response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s with the key %s from %q: status %d, body %s", creditURL, key.Masked(), key.Source, resp.StatusCode, body)
	}
	t.Logf("GET %s with the key %s from %q: %d %s", creditURL, key.Masked(), key.Source, resp.StatusCode, strings.TrimSpace(string(body)))
}
