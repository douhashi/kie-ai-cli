package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douhashi/kie-ai-cli/internal/kie"
)

var stored = kie.Upload{
	FileName:    "photo.png",
	FilePath:    "kie-ai-cli/0011223344556677/photo.png",
	DownloadURL: "https://file.kie.ai/kie-ai-cli/0011223344556677/photo.png",
	FileSize:    5,
	MimeType:    "image/png",
	UploadedAt:  "2026-08-24T03:04:05Z",
}

// The download URL is printed on its own, with no label and nothing beside it,
// because the reason to upload a file is to pass the URL to the next command:
// `kie task run <model> --image "$(kie file upload photo.png)"` only works if
// the whole of stdout is the URL.
func TestWriteUploadPrintsTheURLAlone(t *testing.T) {
	var out bytes.Buffer
	if err := writeUpload(&out, stored, false); err != nil {
		t.Fatalf("writeUpload: %v", err)
	}
	if want := stored.DownloadURL + "\n"; out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

// With --json the whole record is returned, under the names kie.ai gave it.
func TestWriteUploadJSONKeepsEveryField(t *testing.T) {
	var out bytes.Buffer
	if err := writeUpload(&out, stored, true); err != nil {
		t.Fatalf("writeUpload: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON (%v):\n%s", err, out.String())
	}
	want := map[string]any{
		"fileName":    "photo.png",
		"filePath":    "kie-ai-cli/0011223344556677/photo.png",
		"downloadUrl": "https://file.kie.ai/kie-ai-cli/0011223344556677/photo.png",
		"fileSize":    float64(5),
		"mimeType":    "image/png",
		"uploadedAt":  "2026-08-24T03:04:05Z",
	}
	if len(got) != len(want) {
		t.Errorf("JSON has %d fields, want %d:\n%s", len(got), len(want), out.String())
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %v, want %v", name, got[name], value)
		}
	}
}

// The one argument decides which of the two endpoints is used, and that is the
// whole of the decision: an http or https address is fetched by kie.ai, and
// everything else is a path on this machine. A Windows path is the case worth
// naming -- its drive letter parses as a URL scheme of its own.
func TestRemoteURL(t *testing.T) {
	tests := []struct {
		source string
		remote bool
		name   string
	}{
		{source: "https://example.com/a/photo.png", remote: true, name: "photo.png"},
		{source: "http://example.com/photo.png", remote: true, name: "photo.png"},
		{source: "https://example.com/photo.png?v=2", remote: true, name: "photo.png"},
		{source: "https://example.com/my%20photo.png", remote: true, name: "my photo.png"},
		// kie.ai invents a name when it is given none, which is the
		// only sensible answer for an address that names no file.
		{source: "https://example.com/", remote: true},
		{source: "https://example.com", remote: true},
		{source: "photo.png"},
		{source: "./a/photo.png"},
		{source: "/tmp/photo.png"},
		{source: `C:\tmp\photo.png`},
		{source: "-"},
	}
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			u, ok := remoteURL(tt.source)
			if ok != tt.remote {
				t.Fatalf("remoteURL(%q) = %v, want %v", tt.source, ok, tt.remote)
			}
			if !ok {
				return
			}
			if got := urlFileName(u); got != tt.name {
				t.Errorf("urlFileName(%q) = %q, want %q", tt.source, got, tt.name)
			}
		})
	}
}

// A directory opens like a file and reads as nothing or as garbage, and so does
// a device. Sending either would store an empty file and report success, so
// what is not a regular file is refused before anything is sent.
func TestOpenRegularFile(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "photo.png")
	if err := os.WriteFile(regular, []byte("bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	f, err := openRegularFile(regular)
	if err != nil {
		t.Fatalf("openRegularFile: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "a directory", source: dir, want: "regular file"},
		{name: "nothing there", source: filepath.Join(dir, "absent.png"), want: "absent.png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := openRegularFile(tt.source)
			if err == nil {
				_ = f.Close()
				t.Fatalf("openRegularFile(%q) succeeded", tt.source)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}
