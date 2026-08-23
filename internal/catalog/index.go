package catalog

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// IndexFile names the completion index: the three fields a shell completes a
// model by, and nothing else. It is derived from catalog.json and sits beside
// it in both copies.
//
// It exists because completion happens on a keystroke: the catalog is 700KB of
// schemas and documentation to decode, the index a few kilobytes to read.
const IndexFile = "index.tsv"

// embeddedIndex holds IndexFile. It is committed and embedded rather than
// derived at startup for the same reason: deriving it would mean decoding the
// catalog, which is the cost the index is here to avoid.
//
//go:embed index.tsv
var embeddedIndex []byte

// IndexEntry is one model as the shell sees it.
type IndexEntry struct {
	ID       string
	Category string
	Vendor   string
}

// RenderIndex writes the index of models, in the order they are given: one
// line each, tab-separated, which needs no decoder to read.
//
// A field that is empty or that holds a tab or a newline would read back as a
// different model, so it is refused rather than written. The catalog is
// generated from documentation this project does not own, so what its fields
// hold is checked rather than assumed.
func RenderIndex(models []Model) ([]byte, error) {
	var b bytes.Buffer
	for _, m := range models {
		for _, field := range [][2]string{{"id", m.ID}, {"category", m.Category}, {"vendor", m.Vendor}} {
			if field[1] == "" || strings.ContainsAny(field[1], "\t\n") {
				return nil, fmt.Errorf("model %q has a %s of %q, which cannot be written as a line of %s",
					m.ID, field[0], field[1], IndexFile)
			}
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\n", m.ID, m.Category, m.Vendor)
	}
	return b.Bytes(), nil
}

// LoadIndex returns the index of the catalog in effect: the downloaded one if
// there is one, and the one embedded in this binary otherwise.
//
// A downloaded catalog with no index beside it is a failure rather than an
// absence. Answering from the embedded index would complete the models of a
// catalog that is not the one in effect, and nothing about the answer would
// look wrong.
func LoadIndex(dir string) ([]IndexEntry, error) {
	data, err := os.ReadFile(filepath.Join(dir, IndexFile))
	switch {
	case err == nil:
		entries, parseErr := parseIndex(data)
		if parseErr != nil {
			return nil, unusable(dir, parseErr)
		}
		return entries, nil
	case !errors.Is(err, fs.ErrNotExist):
		return nil, unusable(dir, err)
	}

	if _, err := os.Stat(filepath.Join(dir, CatalogFile)); err == nil {
		return nil, unusable(dir, fmt.Errorf("there is no %s beside it", IndexFile))
	}
	entries, err := parseIndex(embeddedIndex)
	if err != nil {
		return nil, fmt.Errorf("the index built into this binary is unusable: %w", err)
	}
	return entries, nil
}

// parseIndex reads what RenderIndex wrote. An index of no models is refused
// too: it would answer every completion with silence, which reads as a shell
// that has not been set up rather than as a catalog that cannot be read.
func parseIndex(data []byte) ([]IndexEntry, error) {
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil, fmt.Errorf("%s holds no models", IndexFile)
	}
	lines := strings.Split(text, "\n")
	entries := make([]IndexEntry, 0, len(lines))
	for i, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || slices.Contains(fields, "") {
			return nil, fmt.Errorf("line %d of %s is %q, want an id, a category and a vendor separated by tabs",
				i+1, IndexFile, line)
		}
		entries = append(entries, IndexEntry{ID: fields[0], Category: fields[1], Vendor: fields[2]})
	}
	return entries, nil
}
