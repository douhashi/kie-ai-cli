package kie

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// The two ways a file can be given to kie.ai: by sending it, or by naming an
// address for kie.ai to fetch it from.
//
// There is a third, /api/file-base64-upload, which this package does not use.
// It can do nothing the stream endpoint cannot, carries a third more bytes for
// the same file, and is documented for files an order of magnitude smaller.
const (
	urlUploadPath    = "/api/file-url-upload"
	streamUploadPath = "/api/file-stream-upload"
)

// uploadRoot is the directory this tool keeps its uploads under, so that an
// account's storage says which files came from here.
const uploadRoot = "kie-ai-cli"

// uploadPathBytes is how much randomness names the directory of one upload.
// Sixty-four bits is far more than enough to never collide, and short enough to
// read back in the download URL.
const uploadPathBytes = 8

// Upload is what kie.ai reports about a file it has stored.
//
// UploadedAt is kept as the API wrote it. Nothing here needs it as an instant,
// and parsing it would only introduce a second opinion about what it means.
type Upload struct {
	FileName    string `json:"fileName"`
	FilePath    string `json:"filePath"`
	DownloadURL string `json:"downloadUrl"`
	FileSize    int64  `json:"fileSize"`
	MimeType    string `json:"mimeType"`
	UploadedAt  string `json:"uploadedAt"`
}

// urlUploadRequest is the body of the URL endpoint. An absent fileName is what
// lets kie.ai invent one, so an empty one must not be sent.
type urlUploadRequest struct {
	FileURL    string `json:"fileUrl"`
	UploadPath string `json:"uploadPath"`
	FileName   string `json:"fileName,omitempty"`
}

// UploadURL has kie.ai fetch fileURL itself and store what it finds. fileName
// may be empty, in which case kie.ai chooses one.
func (c *Client) UploadURL(ctx context.Context, fileURL, fileName string) (Upload, error) {
	ctx, cancel := context.WithTimeout(ctx, uploadTimeout)
	defer cancel()

	raw, err := c.postJSON(ctx, c.UploadBaseURL+urlUploadPath, urlUploadRequest{
		FileURL:    fileURL,
		UploadPath: newUploadPath(),
		FileName:   fileName,
	})
	if err != nil {
		return Upload{}, err
	}
	return decodeUpload(urlUploadPath, raw)
}

// UploadStream sends the bytes of file to kie.ai under fileName, which the
// stored file is addressed by and so cannot be empty.
func (c *Client) UploadStream(ctx context.Context, fileName string, file io.Reader) (Upload, error) {
	if fileName == "" {
		return Upload{}, errors.New("kie.ai: an upload needs a file name")
	}
	ctx, cancel := context.WithTimeout(ctx, uploadTimeout)
	defer cancel()

	raw, err := c.postMultipart(ctx, c.UploadBaseURL+streamUploadPath, fileName, file, map[string]string{
		"uploadPath": newUploadPath(),
		"fileName":   fileName,
	})
	if err != nil {
		return Upload{}, err
	}
	return decodeUpload(streamUploadPath, raw)
}

// newUploadPath invents the directory one upload goes into.
//
// It is new for every call on purpose. kie.ai addresses a stored file by its
// uploadPath and fileName together and has no endpoint that deletes one, so a
// fixed directory would let a second upload of photo.png take the place of the
// first without either the caller or the API saying so.
func newUploadPath() string {
	var b [uploadPathBytes]byte
	// Documented never to fail: it panics if the system source is broken.
	_, _ = rand.Read(b[:])
	return uploadRoot + "/" + hex.EncodeToString(b[:])
}

// decodeUpload reads the stored file out of a successful answer. A record
// without a download URL is refused rather than returned empty: the URL is the
// whole point of the call, and an empty one would travel on as a task input.
func decodeUpload(path string, raw json.RawMessage) (Upload, error) {
	var up Upload
	if err := json.Unmarshal(raw, &up); err != nil {
		return Upload{}, fmt.Errorf("kie.ai: %s: the answer is not an upload: %s", path, snippet(raw))
	}
	if up.DownloadURL == "" {
		return Upload{}, fmt.Errorf("kie.ai: %s: the upload has no download URL: %s", path, snippet(raw))
	}
	return up, nil
}
