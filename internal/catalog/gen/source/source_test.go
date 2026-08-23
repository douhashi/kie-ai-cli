package source_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/catalog/gen/source"
)

// serve starts a real server: the point of this package is the HTTP boundary,
// so there is nothing useful left to test once it is stubbed out.
func serve(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func TestFetchReturnsEveryPage(t *testing.T) {
	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "body of %s", r.URL.Path)
	})
	client := &source.Client{Concurrency: 4, Interval: time.Microsecond}

	urls := []string{server.URL + "/a.md", server.URL + "/b/c.md"}
	pages, err := client.Fetch(t.Context(), urls)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	for _, url := range urls {
		if pages[url] == "" {
			t.Errorf("%s not fetched", url)
		}
	}
	if pages[urls[1]] != "body of /b/c.md" {
		t.Errorf("body = %q", pages[urls[1]])
	}
}

func TestFetchStaysWithinConcurrencyLimit(t *testing.T) {
	var inFlight, peak atomic.Int64
	server := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		current := inFlight.Add(1)
		for {
			was := peak.Load()
			if current <= was || peak.CompareAndSwap(was, current) {
				break
			}
		}
		defer inFlight.Add(-1)
		fmt.Fprint(w, "page")
	})

	var urls []string
	for i := range 40 {
		urls = append(urls, fmt.Sprintf("%s/page-%d.md", server.URL, i))
	}
	client := &source.Client{Concurrency: 3, Interval: time.Microsecond}
	if _, err := client.Fetch(t.Context(), urls); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if peak.Load() > 3 {
		t.Errorf("peak concurrency = %d, want at most 3", peak.Load())
	}
}

// 231 pages is enough traffic that a re-run should not repeat it.
func TestFetchReusesTheCacheDirectory(t *testing.T) {
	var requests atomic.Int64
	server := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, "cached page")
	})
	dir := t.TempDir()
	url := server.URL + "/market/seedream/page.md"

	for range 2 {
		client := &source.Client{Dir: dir, Concurrency: 2, Interval: time.Microsecond}
		pages, err := client.Fetch(t.Context(), []string{url})
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if pages[url] != "cached page" {
			t.Fatalf("body = %q", pages[url])
		}
	}
	if requests.Load() != 1 {
		t.Errorf("server saw %d requests, want 1", requests.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, "market", "seedream", "page.md")); err != nil {
		t.Errorf("cache file not written: %v", err)
	}
}

func TestFetchRetriesServerErrors(t *testing.T) {
	var attempts atomic.Int64
	server := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, "page")
	})
	client := &source.Client{Concurrency: 1, Attempts: 3, Backoff: time.Millisecond, Interval: time.Microsecond}

	url := server.URL + "/flaky.md"
	pages, err := client.Fetch(t.Context(), []string{url})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if pages[url] != "page" {
		t.Errorf("body = %q", pages[url])
	}
}

func TestFetchReportsExhaustedRetries(t *testing.T) {
	server := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	client := &source.Client{Concurrency: 1, Attempts: 2, Backoff: time.Millisecond, Interval: time.Microsecond}

	_, err := client.Fetch(t.Context(), []string{server.URL + "/down.md"})
	if err == nil {
		t.Fatal("want an error when every attempt fails")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %v, want it to report the status", err)
	}
}

// docs.kie.ai answers with its HTML shell and a 200 both for a path that does
// not exist and, now and then, for one that does, so an HTML body is retried
// and only then treated as a failure. Caching it would poison the crawl.
func TestFetchRetriesAndThenRejectsHTMLResponses(t *testing.T) {
	var requests atomic.Int64
	server := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, "<!DOCTYPE html><html><body>not found</body></html>")
	})
	dir := t.TempDir()
	client := &source.Client{Dir: dir, Concurrency: 1, Attempts: 2, Backoff: time.Millisecond, Interval: time.Microsecond}

	_, err := client.Fetch(t.Context(), []string{server.URL + "/missing.md"})
	if err == nil {
		t.Fatal("want an error for an HTML body")
	}
	if !strings.Contains(err.Error(), "HTML") {
		t.Errorf("error = %v, want it to say the body was HTML", err)
	}
	if requests.Load() != 2 {
		t.Errorf("server saw %d requests, want 2 attempts", requests.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, "missing.md")); err == nil {
		t.Error("an HTML body was cached")
	}
}

func TestFetchRecoversFromATransientHTMLResponse(t *testing.T) {
	var requests atomic.Int64
	server := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			fmt.Fprint(w, "<!DOCTYPE html><html></html>")
			return
		}
		fmt.Fprint(w, "# a real page")
	})
	client := &source.Client{Concurrency: 1, Attempts: 3, Backoff: time.Millisecond, Interval: time.Microsecond}

	url := server.URL + "/flaky.md"
	pages, err := client.Fetch(t.Context(), []string{url})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if pages[url] != "# a real page" {
		t.Errorf("body = %q", pages[url])
	}
}

func TestFetchReportsEveryFailure(t *testing.T) {
	var mu sync.Mutex
	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/bad") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, "page")
	})
	client := &source.Client{Concurrency: 2, Attempts: 1, Interval: time.Microsecond}

	_, err := client.Fetch(t.Context(), []string{
		server.URL + "/bad-1.md", server.URL + "/good.md", server.URL + "/bad-2.md",
	})
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"bad-1.md", "bad-2.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %s", err, want)
		}
	}
}

// Asking for 231 pages as fast as the network allows gets the crawl throttled,
// so requests are spaced whatever the concurrency is.
func TestFetchSpacesItsRequests(t *testing.T) {
	var starts []time.Time
	var mu sync.Mutex
	server := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		fmt.Fprint(w, "page")
	})

	const interval = 20 * time.Millisecond
	client := &source.Client{Concurrency: 4, Interval: interval}
	var urls []string
	for i := range 5 {
		urls = append(urls, fmt.Sprintf("%s/page-%d.md", server.URL, i))
	}
	if _, err := client.Fetch(t.Context(), urls); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	slices.SortFunc(starts, func(a, b time.Time) int { return a.Compare(b) })
	spread := starts[len(starts)-1].Sub(starts[0])
	// The limiter releases the first request at once and spaces the rest, so a
	// perfect run is spread over exactly (n-1) intervals -- the most the server
	// can ever observe. Every source of jitter (goroutine scheduling, the clock's
	// resolution, the handler's own lock) can only shorten what is measured here,
	// so asserting the ideal makes the test a coin flip. One interval of slack
	// keeps it a real check: a client that does not space at all lands near zero.
	const slack = interval
	if want := time.Duration(len(urls)-1)*interval - slack; spread < want {
		t.Errorf("5 requests spread over %v, want at least %v", spread, want)
	}
}
