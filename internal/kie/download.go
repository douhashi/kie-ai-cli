package kie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"
)

// downloadURLPath issues a fresh, directly downloadable link for a file kie.ai
// produced. It takes the URL a task recorded and answers with another one that
// expires twenty minutes later.
const downloadURLPath = "/api/v1/common/download-url"

// downloadTimeout is how long one result is given to arrive. A download is not
// a short exchange like the rest of the API -- what it carries is a video --
// and it is not an upload either: nothing is being sent, so ten minutes is
// generous for a file kie.ai will serve at all.
//
// A variable so that the tests can shrink it. Nothing else assigns to it.
var downloadTimeout = 10 * time.Minute

// Download writes what resultURL serves into w, and reports the media type the
// host declared for it, which is empty when it declared none.
//
// The recorded URL is fetched as it stands. Asking for a fresh link first would
// double the requests on every download and buy nothing: the recorded URL is
// what kie.ai answered the query with, and it serves the file for as long as
// the result exists. A refusal is the one answer a fresh link can change, so
// that is the only case a second attempt is made -- once, because a result that
// has expired is refused however many links are issued for it.
//
// Nothing is written to w unless the file is being served: a refusal is known
// from the status line, before a byte of the body is copied, so the retry
// cannot append a second answer to a half-written first one.
func (c *Client) Download(ctx context.Context, resultURL string, w io.Writer) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	mediaType, err := c.fetch(ctx, resultURL, w)
	var refused *refusal
	if !errors.As(err, &refused) {
		return mediaType, wrapDownload(resultURL, err)
	}

	fresh, issueErr := c.directURL(ctx, resultURL)
	if issueErr != nil {
		// Both halves: the refusal alone reads as a result that has
		// expired, and the refusal to issue a link alone reads as a
		// problem with the account.
		return "", fmt.Errorf("kie.ai: %s: %w; no fresh link could be issued for it either: %v", resultURL, err, issueErr)
	}
	mediaType, err = c.fetch(ctx, fresh, w)
	return mediaType, wrapDownload(resultURL, err)
}

// wrapDownload names the file a failure was about. It is the recorded URL even
// when a fresh link was the one that failed: the fresh one is a credential that
// expires in twenty minutes and has no place in a message that may be logged,
// and it is not an address the caller has ever seen.
func wrapDownload(resultURL string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("kie.ai: %s: %w", resultURL, err)
}

// refusal is a host answering with something other than the file. It carries
// the status alone: what a refusal means here is that this URL did not serve
// the result, and the body of such an answer is a storage service's error
// document rather than anything kie.ai wrote.
type refusal struct{ status int }

func (r *refusal) Error() string { return fmt.Sprintf("HTTP %d", r.status) }

// fetch streams what rawURL serves into w.
//
// This is the one request in this package that does not go through do, because
// it is the one that does not go to kie.ai: a result sits on storage that
// serves it to anyone with the address. Sending the bearer token there would
// hand the account's credential to a third party on every download, for a host
// that never asked for one.
func (c *Client) fetch(ctx context.Context, rawURL string, w io.Writer) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", &refusal{status: resp.StatusCode}
	}
	// No limit on the size: the caller is writing to a file, and what a
	// task produced is as large as it is.
	if _, err := io.Copy(w, resp.Body); err != nil {
		return "", fmt.Errorf("reading the file: %w", err)
	}
	return mediaTypeOf(resp.Header.Get("Content-Type")), nil
}

// mediaTypeOf reads the type out of a Content-Type header, without the
// parameters that may follow it. A header that is not one is no type at all:
// it is used to name a file, and a name is better short than wrong.
func mediaTypeOf(header string) string {
	if header == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil {
		return ""
	}
	return mediaType
}

// downloadURLRequest is the body downloadURLPath takes. kie.ai answers 422 for
// an address it did not produce itself.
type downloadURLRequest struct {
	URL string `json:"url"`
}

// directURL asks kie.ai for a fresh link to a file it produced. The answer is a
// bare string, and anything else is refused rather than passed on as an address
// to fetch.
func (c *Client) directURL(ctx context.Context, resultURL string) (string, error) {
	raw, err := c.postJSON(ctx, c.BaseURL+downloadURLPath, downloadURLRequest{URL: resultURL})
	if err != nil {
		return "", err
	}
	var link string
	if err := json.Unmarshal(raw, &link); err != nil || link == "" {
		return "", fmt.Errorf("kie.ai: %s: the answer is not a link: %s", downloadURLPath, snippet(raw))
	}
	return link, nil
}
