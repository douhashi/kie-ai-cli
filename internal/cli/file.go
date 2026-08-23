package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"

	"github.com/douhashi/kie-ai-cli/internal/kie"
)

func runFileUpload(e *env, args []string) error {
	if len(args) != 1 {
		return usagef("file upload: expected one <path|url>, got %d arguments", len(args))
	}
	// Resolved before the file is opened, so that a missing key is reported
	// as itself rather than after an unrelated complaint about the path.
	client, err := e.client()
	if err != nil {
		return err
	}

	source, ctx := args[0], context.Background()
	var up kie.Upload
	if u, ok := remoteURL(source); ok {
		up, err = client.UploadURL(ctx, source, urlFileName(u))
	} else {
		up, err = uploadLocalFile(ctx, client, source)
	}
	if err != nil {
		return err
	}
	return writeUpload(e.stdout, up, e.json)
}

// remoteURL decides which of the two upload endpoints the argument is for. An
// http or https address is one kie.ai can fetch for itself; everything else is
// a path on this machine, including a Windows path, whose drive letter parses
// as a URL scheme of its own.
func remoteURL(source string) (*url.URL, bool) {
	u, err := url.Parse(source)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, false
	}
	return u, true
}

// urlFileName is the last segment of the address, which is the closest thing a
// URL has to a file name. An address that names no file gets none: kie.ai
// invents one when it is not given one.
func urlFileName(u *url.URL) string {
	base := path.Base(u.Path)
	if base == "." || base == "/" {
		return ""
	}
	return base
}

func uploadLocalFile(ctx context.Context, client *kie.Client, name string) (kie.Upload, error) {
	f, err := openRegularFile(name)
	if err != nil {
		return kie.Upload{}, err
	}
	defer func() { _ = f.Close() }()
	return client.UploadStream(ctx, filepath.Base(name), f)
}

// openRegularFile opens name for reading and refuses anything else.
//
// A directory opens like a file and reads as nothing or as garbage, and so does
// a device. Either would be uploaded without complaint and stored as an empty
// file, so the kind is checked before a single byte is sent.
func openRegularFile(name string) (*os.File, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("%s: not a regular file", name)
	}
	return f, nil
}

func writeUpload(w io.Writer, up kie.Upload, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(up)
	}
	// The URL alone, with no label and nothing beside it: the reason to
	// upload a file is to hand the URL to the next command, and
	// `--image "$(kie file upload photo.png)"` has to be all it takes.
	_, err := fmt.Fprintln(w, up.DownloadURL)
	return err
}
