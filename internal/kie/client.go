// Package kie talks to the kie.ai HTTP API.
//
// Every endpoint answers with the same envelope, and the envelope is the part
// that has to be read carefully: kie.ai reports an authentication failure as
// HTTP 200 with code 401 in the body. A client that believes the status line
// would take that for a success with a missing result, so a call here counts as
// having worked only when the status and the code agree.
//
// The endpoints are also split across two hosts, which the documentation does
// not say: see defaultUploadBaseURL.
package kie

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// defaultBaseURL is the host the API hangs off.
const defaultBaseURL = "https://api.kie.ai"

// defaultUploadBaseURL is the host the file-upload endpoints hang off instead.
//
// This is not a typo for defaultBaseURL. Asked for any of the three upload
// paths, api.kie.ai answers 404; asked for anything else, the host below
// answers with a 404 that lists those three paths as everything it serves. The
// OpenAPI documents on docs.kie.ai declare api.kie.ai for the upload endpoints
// and the curl examples printed beside them use this host: only the examples
// are right. Established against the live API, see issue #16.
const defaultUploadBaseURL = "https://kieai.redpandaai.co"

// How long one call is given before it is abandoned. Without a limit a stalled
// connection would hang the command for as long as the operating system allows,
// and a single limit for everything cannot fit both kinds of call: reading a
// balance and submitting a task are short exchanges, while an upload carries
// the file, and 100MB -- the largest kie.ai accepts -- does not cross a
// domestic uplink in thirty seconds.
//
// These are variables so that the tests can shrink them. Nothing else assigns
// to them.
var (
	callTimeout   = 30 * time.Second
	uploadTimeout = 10 * time.Minute
)

// Client calls kie.ai on behalf of one API key.
//
// Nothing here retries. Most of what this CLI sends creates work on the other
// side, and a resend that the caller did not ask for would create it twice; a
// retry belongs to the endpoints that are known to be safe to repeat.
type Client struct {
	APIKey string
	// BaseURL serves the API. UploadBaseURL serves the upload endpoints,
	// and is a different host.
	BaseURL       string
	UploadBaseURL string
	HTTP          *http.Client
}

// New builds a client for the public API.
func New(apiKey string) *Client {
	return &Client{
		APIKey:        apiKey,
		BaseURL:       defaultBaseURL,
		UploadBaseURL: defaultUploadBaseURL,
		// No Timeout: it would apply to the whole exchange, upload
		// included, and each call sets its own deadline instead.
		HTTP: &http.Client{},
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

// get makes an authenticated GET against the API host and returns the data
// field of a successful answer. It bounds itself, so a caller that has no
// deadline of its own still gets one.
func (c *Client) get(ctx context.Context, path string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req, "GET "+path)
}

// post makes an authenticated POST of a JSON body against the API host, under
// the same deadline get takes: both are short exchanges with api.kie.ai, and
// only the uploads, which go to the other host, need longer.
func (c *Client) post(ctx context.Context, path string, body any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return c.postJSON(ctx, c.BaseURL+path, body)
}

// postJSON sends one JSON document to url. Unlike get and post it sets no
// deadline of its own: how long an upload is allowed to take depends on what is
// being sent, so that choice is left to the caller.
func (c *Client) postJSON(ctx context.Context, url string, body any) (json.RawMessage, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, "POST "+url)
}

// fileField is the name kie.ai reads the uploaded bytes from.
const fileField = "file"

// postMultipart sends fields and one file as a multipart form.
//
// The body is written onto the connection as it is read, through a pipe, rather
// than assembled first: a file large enough to be worth uploading is large
// enough not to want a second copy of in memory. The length is therefore
// unknown when the request starts and the body is sent in chunks, which kie.ai
// accepts.
func (c *Client) postMultipart(ctx context.Context, url, fileName string, file io.Reader, fields map[string]string) (json.RawMessage, error) {
	pr, pw := io.Pipe()
	form := multipart.NewWriter(pw)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, pr)
	if err != nil {
		_ = pr.Close()
		return nil, err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())

	// Handing the failure to the read end stops the request with that error
	// instead of sending a body that was cut short. Whatever happens to the
	// request, the transport closes pr, which releases this goroutine.
	go func() { _ = pw.CloseWithError(writeForm(form, fileName, file, fields)) }()

	return c.do(req, "POST "+url)
}

// writeForm lays out the multipart body: the text fields first, so that a
// server which reads the parts in order has the file's destination before the
// file itself arrives.
func writeForm(form *multipart.Writer, fileName string, file io.Reader, fields map[string]string) error {
	for name, value := range fields {
		if err := form.WriteField(name, value); err != nil {
			return err
		}
	}
	part, err := form.CreateFormFile(fileField, fileName)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file); err != nil {
		return err
	}
	return form.Close()
}

// do sends a prepared request and returns the data field of a successful
// answer. The key is added here, so that no endpoint can forget it; it travels
// in a header and appears in nothing this returns.
func (c *Client) do(req *http.Request, what string) (json.RawMessage, error) {
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kie.ai: %s: %w", what, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("kie.ai: %s: reading the response: %w", what, err)
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
