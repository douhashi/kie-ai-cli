// Package source fetches documentation pages from docs.kie.ai.
//
// It is the generator's external boundary, and the only place that knows how
// the site misbehaves: it answers with its HTML shell and a 200 both for a path
// that does not exist and, once a crawl gets brisk, for one that does.
package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// A full crawl is 231 pages. Asking for them as fast as the network allows
	// gets the run throttled partway through — the site starts answering with
	// its HTML shell — so requests are both bounded and spaced. Together these
	// hold the crawl near four requests a second, which it serves happily.
	defaultConcurrency = 2
	defaultInterval    = 250 * time.Millisecond
	// A throttled site does not recover in the seven seconds four tries a
	// second apart give it: a real crawl spent them all and the run died
	// with 230 of its 231 pages in hand. Six waits, doubling and capped,
	// hand it a minute of quiet instead -- and cost nothing on a run where
	// nothing goes wrong, because a page that answers is not retried.
	defaultAttempts = 7
	defaultBackoff  = time.Second
	maxBackoff      = 30 * time.Second
	// maxPageBytes caps what a page may weigh. The largest real page is a few
	// hundred kilobytes; anything far past that is not the page we asked for.
	maxPageBytes = 8 << 20
)

// Client fetches pages, optionally through an on-disk cache.
//
// The zero value works. A Client keeps the schedule of its own requests, so
// one Client should serve a whole crawl.
type Client struct {
	// HTTP is the client to fetch with. nil uses a client with a timeout.
	HTTP *http.Client
	// Dir caches pages under their URL path. Empty disables the cache.
	// A full crawl is over two hundred requests, so re-runs should hit disk.
	Dir string
	// Concurrency bounds the requests in flight, to stay a polite visitor.
	Concurrency int
	// Interval is the least time between two request starts.
	Interval time.Duration
	// Attempts is the number of tries per page, retrying only failures that
	// may pass on their own.
	Attempts int
	// Backoff is the pause before a retry, doubling each time and capped.
	// It holds back the whole crawl rather than the one page that failed.
	Backoff time.Duration

	// scheduleMu guards nextRequest, the time the next request may start.
	scheduleMu  sync.Mutex
	nextRequest time.Time
}

// schedule blocks until this client's turn to make a request comes round.
//
// A slot is taken at the moment it falls due rather than reserved in advance,
// and the wait is re-checked when it expires. That is what lets pause hold back
// requests that are already waiting their turn: a slot handed out ahead of time
// would be spent knocking on a site that has just asked to be left alone.
func (c *Client) schedule(ctx context.Context) error {
	interval := c.Interval
	if interval <= 0 {
		interval = defaultInterval
	}

	for {
		c.scheduleMu.Lock()
		now := time.Now()
		at := c.nextRequest
		if !at.After(now) {
			c.nextRequest = now.Add(interval)
			c.scheduleMu.Unlock()
			return nil
		}
		c.scheduleMu.Unlock()

		timer := time.NewTimer(time.Until(at))
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

// pause holds the whole crawl back for at least d.
//
// The site does not throttle one page at a time: when it answers with its
// shell, it is answering every request that way, and the pages queued behind
// the one that noticed are about to be told the same thing. So the wait goes on
// the crawl's clock rather than into the goroutine that found it -- retrying
// alone, while the rest of the crawl keeps knocking, spends the tries without
// the site ever being given a quiet moment.
//
// It is a deadline rather than an addition, so several pages noticing at once
// ask for one pause between them instead of stacking one each.
func (c *Client) pause(d time.Duration) {
	until := time.Now().Add(d)
	c.scheduleMu.Lock()
	defer c.scheduleMu.Unlock()
	if c.nextRequest.Before(until) {
		c.nextRequest = until
	}
}

// Fetch returns the body of every URL, keyed by URL.
//
// A crawl is expensive, so it does not stop at the first failure: every page
// that could not be read is reported together.
func (c *Client) Fetch(ctx context.Context, urls []string) (map[string]string, error) {
	pages := make(map[string]string, len(urls))
	problems := make([]error, len(urls))

	concurrency := c.Concurrency
	if concurrency < 1 {
		concurrency = defaultConcurrency
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, concurrency)

	for i, pageURL := range urls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				problems[i] = ctx.Err()
				return
			}

			body, err := c.page(ctx, pageURL)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				problems[i] = fmt.Errorf("%s: %w", pageURL, err)
				return
			}
			pages[pageURL] = body
		}()
	}
	wg.Wait()

	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	return pages, nil
}

// page returns a cached body when there is one, and otherwise downloads and
// caches it.
func (c *Client) page(ctx context.Context, pageURL string) (string, error) {
	cachePath, err := c.cachePath(pageURL)
	if err != nil {
		return "", err
	}
	if cachePath != "" {
		if body, err := os.ReadFile(cachePath); err == nil {
			return string(body), nil
		}
	}

	body, err := c.download(ctx, pageURL)
	if err != nil {
		return "", err
	}
	if cachePath != "" {
		if err := writeFile(cachePath, body); err != nil {
			return "", err
		}
	}
	return body, nil
}

func (c *Client) download(ctx context.Context, pageURL string) (string, error) {
	attempts := c.Attempts
	if attempts < 1 {
		attempts = defaultAttempts
	}
	backoff := c.Backoff
	if backoff <= 0 {
		backoff = defaultBackoff
	}

	var lastErr error
	wait := backoff
	for attempt := range attempts {
		body, retryable, err := c.attempt(ctx, pageURL)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retryable {
			return "", err
		}
		if attempt < attempts-1 {
			c.pause(wait)
			wait = min(wait*2, maxBackoff)
		}
	}
	return "", fmt.Errorf("gave up after %d attempts: %w", attempts, lastErr)
}

func (c *Client) attempt(ctx context.Context, pageURL string) (body string, retryable bool, err error) {
	if err := c.schedule(ctx); err != nil {
		return "", false, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return "", false, err
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	response, err := client.Do(request)
	if err != nil {
		return "", true, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		// Only a server-side or rate-limit answer can change by itself.
		retryable := response.StatusCode >= 500 || response.StatusCode == http.StatusTooManyRequests
		return "", retryable, fmt.Errorf("status %d", response.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(response.Body, maxPageBytes))
	if err != nil {
		return "", true, err
	}
	// The site answers a page with text/markdown and its single-page shell with
	// text/html — for a path that does not exist, and, as a real crawl showed,
	// now and then for one that does. So the shell is retried; a path that is
	// genuinely wrong answers the same way every time and fails once the tries
	// run out.
	if strings.Contains(response.Header.Get("Content-Type"), "html") ||
		strings.HasPrefix(strings.TrimSpace(string(raw)), "<") {
		return "", true, fmt.Errorf("body is HTML, not the Markdown page")
	}
	return string(raw), false, nil
}

// cachePath maps a URL onto a file below Dir, mirroring its path. It returns an
// empty path when caching is off.
func (c *Client) cachePath(pageURL string) (string, error) {
	if c.Dir == "" {
		return "", nil
	}
	parsed, err := url.Parse(pageURL)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(parsed.Path, "/")))
	if clean == "." || clean == string(filepath.Separator) {
		return "", fmt.Errorf("%s has no path to cache under", pageURL)
	}
	if slices.Contains(strings.Split(clean, string(filepath.Separator)), "..") {
		return "", fmt.Errorf("%s escapes the cache directory", pageURL)
	}
	return filepath.Join(c.Dir, clean), nil
}

// writeFile writes through a temporary file so an interrupted run leaves no
// half-written page behind to be read as a cache hit.
func writeFile(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())

	if _, err := temp.WriteString(body); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
