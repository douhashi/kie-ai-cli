// Command catalog-gen regenerates internal/catalog/catalog.json from
// docs.kie.ai.
//
// It is a development tool, not part of the shipped binary: the CLI reads the
// committed catalog, so a build never depends on the docs site being up.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/douhashi/kie-ai-cli/internal/catalog"
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
		now:         time.Now().UTC(),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "catalog-gen: %v\n", err)
		os.Exit(1)
	}
}

// options is what a run needs. indexURL, interval and now are fields so a test
// can point the crawl at a server of its own, not pace itself for its sake, and
// decide the date a run stamps.
type options struct {
	indexURL    string
	out         string
	pagesDir    string
	concurrency int
	interval    time.Duration
	now         time.Time
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

	// Nothing is written until here: a catalog missing whatever failed would be
	// worse than the one already on disk.
	rendered, err := render(built)
	if err != nil {
		return err
	}
	// The index the shell completes from is derived from the same crawl, so
	// that the two are committed together and cannot disagree.
	modelIndex, err := catalog.RenderIndex(built.Models)
	if err != nil {
		return err
	}
	dir := filepath.Dir(opts.out)
	indexPath := filepath.Join(dir, catalog.IndexFile)
	stampPath := filepath.Join(dir, catalog.GeneratedAtFile)

	catalogChanged, err := differs(opts.out, rendered)
	if err != nil {
		return err
	}
	// The index follows the catalog, but it can still differ on its own: a
	// file lost or hand-edited, or a change to how it is rendered.
	indexChanged, err := differs(indexPath, modelIndex)
	if err != nil {
		return err
	}

	// A crawl that turns up nothing new leaves all three files exactly as they
	// were. Stamping today's date on an identical catalog would make every
	// scheduled run in #5 a pull request that moves one line and says nothing.
	if !catalogChanged && !indexChanged {
		fmt.Fprintf(os.Stderr, "catalog-gen: %s is unchanged (%d models); left it, %s and %s as they were\n",
			opts.out, len(built.Models), indexPath, stampPath)
		return nil
	}
	if catalogChanged {
		if err := writeAtomically(opts.out, rendered); err != nil {
			return err
		}
	}
	if indexChanged {
		if err := writeAtomically(indexPath, modelIndex); err != nil {
			return err
		}
	}
	// The date describes the catalog, so an index rewritten on its own does
	// not move it: the catalog is as old as it was.
	if !catalogChanged {
		fmt.Fprintf(os.Stderr, "catalog-gen: %s is unchanged (%d models); rewrote %s and left %s as it was\n",
			opts.out, len(built.Models), indexPath, stampPath)
		return nil
	}
	// After the catalog, so that a failure here leaves a date older than the
	// catalog it describes rather than one that overstates its freshness.
	stamp := []byte(opts.now.UTC().Format(time.DateOnly) + "\n")
	if err := writeAtomically(stampPath, stamp); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "catalog-gen: wrote %d models to %s and %s, generated %s\n",
		len(built.Models), opts.out, indexPath, bytes.TrimSpace(stamp))
	return nil
}

// differs reports whether path holds something other than content. A file that
// is not there differs from everything, which is what makes a lost one get
// written again.
func differs(path string, content []byte) (bool, error) {
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	return !bytes.Equal(current, content), nil
}

// render encodes the catalog exactly as it is committed, so the bytes can be
// compared with what is already on disk before anything is written.
func render(value *catalog.Catalog) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	// Indent so a regeneration shows as a readable diff, and leave the docs
	// text as written instead of escaping every < and & in it.
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// writeAtomically swaps the file in by rename, so a failed run never leaves a
// truncated one behind.
func writeAtomically(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())

	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	// CreateTemp makes the file readable by its owner alone; both files are
	// committed and read by everyone who builds.
	if err := os.Chmod(temp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
