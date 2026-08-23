// Package kie talks to the kie.ai HTTP API.
//
// Every endpoint answers with the same envelope, and the envelope is the part
// that has to be read carefully: kie.ai reports an authentication failure as
// HTTP 200 with code 401 in the body. A client that believes the status line
// would take that for a success with a missing result, so a call here counts as
// having worked only when the status and the code agree.
package kie

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// defaultBaseURL is the host every endpoint hangs off.
const defaultBaseURL = "https://api.kie.ai"

// timeout bounds a single call. Without one a stalled connection would hang the
// command for as long as the operating system allows.
const timeout = 30 * time.Second

// Client calls kie.ai on behalf of one API key.
//
// Nothing here retries. Most of what this CLI sends creates work on the other
// side, and a resend that the caller did not ask for would create it twice; a
// retry belongs to the endpoints that are known to be safe to repeat.
type Client struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
}

// New builds a client for the public API.
func New(apiKey string) *Client {
	return &Client{
		APIKey:  apiKey,
		BaseURL: defaultBaseURL,
		HTTP:    &http.Client{Timeout: timeout},
	}
}

// codeOK is the value the envelope carries when the call actually worked.
const codeOK = 200

// maxBodyBytes bounds how much of a response is read. The endpoints here answer
// with a short JSON document; anything larger is a proxy or an error page, and
// reading it whole would let the far side decide how much memory to use.
const maxBodyBytes = 1 << 20

// envelope is the shape kie.ai wraps every answer in.
type envelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// APIError is a call that reached kie.ai and was refused. Status and Code are
// kept apart because they disagree in the ordinary case: an authentication
// failure is HTTP 200 with code 401.
type APIError struct {
	Status int
	Code   int
	Msg    string
}

func (e *APIError) Error() string {
	msg := e.Msg
	if msg == "" {
		msg = "no message"
	}
	return fmt.Sprintf("kie.ai: HTTP %d, code %d: %s", e.Status, e.Code, msg)
}

// get makes an authenticated GET and returns the data field of a successful
// answer. The key travels in a header and appears in nothing this returns.
func (c *Client) get(ctx context.Context, path string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kie.ai: GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("kie.ai: GET %s: reading the response: %w", path, err)
	}
	return payload(resp.StatusCode, body)
}

// payload reads one response. It is separate from the request so that the rule
// it applies -- the status and the code both have to say the call worked -- is
// stated once, for every endpoint.
func payload(status int, body []byte) (json.RawMessage, error) {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("kie.ai: HTTP %d: the response is not what the API returns: %s", status, snippet(body))
	}
	if env.Code != codeOK || status < 200 || status > 299 {
		return nil, &APIError{Status: status, Code: env.Code, Msg: env.Msg}
	}
	return env.Data, nil
}

// snippetRunes is how much of an unrecognised body is quoted back: enough to
// recognise a proxy's error page, far too little to fill the terminal.
const snippetRunes = 160

// snippet renders a body for a one-line error message.
func snippet(body []byte) string {
	// Collapsing the whitespace keeps a wrapped HTML page from spreading the
	// error over the screen.
	s := strings.Join(strings.Fields(string(body)), " ")
	if s == "" {
		return "(empty)"
	}
	if r := []rune(s); len(r) > snippetRunes {
		s = string(r[:snippetRunes]) + "..."
	}
	return strconv.Quote(s)
}
