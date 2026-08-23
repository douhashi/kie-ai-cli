package cli

import (
	"net/http"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/kie"
)

// PointAtServer makes every command run during one test talk to baseURL
// instead of to kie.ai.
//
// It lives in a test file because the hook it sets is unexported: there is no
// flag and no environment variable that sends this tool's requests -- and the
// API key with them -- anywhere else, and a build that ships cannot be made to.
func PointAtServer(t *testing.T, baseURL string, httpClient *http.Client) {
	t.Helper()
	previous := newClient
	t.Cleanup(func() { newClient = previous })
	newClient = func(apiKey string) *kie.Client {
		c := previous(apiKey)
		c.BaseURL = baseURL
		c.UploadBaseURL = baseURL
		c.HTTP = httpClient
		return c
	}
}
