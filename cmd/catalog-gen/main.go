// Command catalog-gen regenerates internal/catalog/catalog.json from
// docs.kie.ai.
//
// It is a development tool, not part of the shipped binary: the CLI reads the
// committed catalog, so a build never depends on the docs site being up.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/catalog/gen"
	"github.com/douhashi/kie-ai-cli/internal/catalog/gen/source"
)

// llmsURL indexes every documentation page and is the crawl's only entry point.
const llmsURL = "https://docs.kie.ai/llms.txt"

func main() {
	out := flag.String("out", filepath.Join("internal", "catalog", "catalog.json"),
		"path of the catalog to write")
	pagesDir := flag.String("pages-dir", "",
		"directory to cache downloaded pages in, and to re-read them from")
	concurrency := flag.Int("concurrency", 4,
		"how many pages to download at once")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, options{
		indexURL:    llmsURL,
		out:         *out,
		pagesDir:    *pagesDir,
		concurrency: *concurrency,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "catalog-gen: %v\n", err)
		os.Exit(1)
	}
}

// options is what a run needs. indexURL and interval are fields so a test can
// point the crawl at a server of its own and not pace itself for its sake.
type options struct {
	indexURL    string
	out         string
	pagesDir    string
	concurrency int
	interval    time.Duration
}

func run(ctx context.Context, opts options) error {
	client := &source.Client{
		Dir:         opts.pagesDir,
		Concurrency: opts.concurrency,
		Interval:    opts.interval,
	}

	index, err := client.Fetch(ctx, []string{opts.indexURL})
	if err != nil {
		return err
	}
	built, err := gen.Build(ctx, index[opts.indexURL], client)
	if err != nil {
		return err
	}

	// Written only now: a catalog missing whatever failed would be worse than
	// the one already on disk.
	if err := writeCatalog(opts.out, built); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "catalog-gen: wrote %d models to %s\n", len(built.Models), opts.out)
	return nil
}

// writeCatalog renders the catalog and swaps it in atomically, so a failed run
// never leaves a truncated catalog behind.
func writeCatalog(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())

	encoder := json.NewEncoder(temp)
	// Indent so a regeneration shows as a readable diff, and leave the docs
	// text as written instead of escaping every < and & in it.
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	// CreateTemp makes the file readable by its owner alone; the catalog is
	// committed and read by everyone who builds.
	if err := os.Chmod(temp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
