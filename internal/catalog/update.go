package catalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// releaseBaseURL is where CI publishes the catalog: one fixed tag, so the
// address never changes and no Release API call is needed to find it. The two
// assets there are byte-identical to the two files this package embeds.
//
// It is a variable so that the test can point it at a server of its own.
// Nothing else assigns to it.
var baseURL = "https://github.com/douhashi/kie-ai-cli/releases/download/catalog/"

// downloadTimeout bounds the whole update rather than each asset: what the
// caller is waiting for is the pair, and two limits would let the command take
// twice as long as either of them says.
const downloadTimeout = 60 * time.Second

// maxAssetBytes is how much of an asset is read. The catalog is under a
// megabyte and grows with the number of models, so the limit is generous; its
// job is to keep whatever answers the address -- a proxy, a captive portal --
// from deciding how much memory this process uses.
//
// A variable so that the test can shrink it. Nothing else assigns to it.
var maxAssetBytes int64 = 8 << 20

// Update downloads the published catalog into dir and returns what it wrote.
//
// Nothing is written unless both assets arrive and this binary can read them:
// a failed update leaves the catalog that was already there, so the CLI still
// answers afterwards.
func Update(ctx context.Context, dir string) (Catalog, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	catalogJSON, err := download(ctx, CatalogFile)
	if err != nil {
		return Catalog{}, err
	}
	generatedAt, err := download(ctx, GeneratedAtFile)
	if err != nil {
		return Catalog{}, err
	}

	downloaded, err := parse(catalogJSON, string(generatedAt))
	if err != nil {
		return Catalog{}, fmt.Errorf("the published catalog is unusable: %w", err)
	}
	// An empty catalog parses, and would replace a working one with a CLI that
	// knows no models at all.
	if len(downloaded.Models) == 0 {
		return Catalog{}, errors.New("the published catalog holds no models")
	}

	if err := writePair(dir, catalogJSON, generatedAt); err != nil {
		return Catalog{}, err
	}
	downloaded.Origin = OriginDownloaded
	downloaded.Path = dir
	return downloaded, nil
}

// download fetches one published asset. It sends no credentials: the release
// is public, and asking a user of an unofficial CLI for a second account would
// cost more than the catalog is worth.
func download(ctx context.Context, name string) ([]byte, error) {
	url := baseURL + name
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	// One byte past the limit is read so that an asset of exactly the limit is
	// still accepted, and a larger one is told apart from a full read.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAssetBytes+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	if int64(len(body)) > maxAssetBytes {
		return nil, fmt.Errorf("download %s: larger than the %d bytes a catalog is allowed to be", url, maxAssetBytes)
	}
	return body, nil
}

// writePair puts both files in place, or neither.
//
// Both are written under temporary names first, so that a disk that fills up
// half way through has not yet touched what was there. The renames that follow
// are atomic one by one but not together, which is why the order matters.
func writePair(dir string, catalogJSON, generatedAt []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create the catalog directory %s: %w", dir, err)
	}
	// The catalog comes first in both passes. A reader arriving between the
	// two renames then sees the new models under the old date, which
	// understates how fresh the catalog is; the other order would have it
	// claim a freshness it does not have.
	files := []struct {
		name    string
		content []byte
	}{
		{CatalogFile, catalogJSON},
		{GeneratedAtFile, generatedAt},
	}
	staged := make([]string, 0, len(files))
	for _, f := range files {
		temp, err := writeTemp(dir, f.name, f.content)
		// Whichever renames succeed, removing what is left is harmless: a
		// renamed file is no longer under its temporary name.
		defer func() { _ = os.Remove(temp) }()
		if err != nil {
			return err
		}
		staged = append(staged, temp)
	}
	for i, f := range files {
		if err := os.Rename(staged[i], filepath.Join(dir, f.name)); err != nil {
			return fmt.Errorf("put %s in place: %w", f.name, err)
		}
	}
	return nil
}

// writeTemp writes content beside its destination -- the same directory, so
// that the rename that follows stays within one filesystem and is atomic.
func writeTemp(dir, name string, content []byte) (string, error) {
	f, err := os.CreateTemp(dir, name+".*")
	if err != nil {
		return "", fmt.Errorf("write %s: %w", name, err)
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return f.Name(), fmt.Errorf("write %s: %w", name, err)
	}
	// Closed rather than deferred: the error a full disk reports arrives here,
	// and it has to be seen before the file is renamed into place.
	if err := f.Close(); err != nil {
		return f.Name(), fmt.Errorf("write %s: %w", name, err)
	}
	return f.Name(), nil
}
