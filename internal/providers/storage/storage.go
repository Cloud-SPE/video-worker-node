// Package storage handles pre-signed URL I/O.
//
// Inputs are pulled from a pre-signed HTTP GET URL into a local file;
// outputs are uploaded from a local file to a pre-signed HTTP PUT URL.
// Single-shot only at v1 — multipart upload is a future plan.
package storage

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

// Storage handles pre-signed-URL fetch and upload.
type Storage interface {
	// Fetch downloads the URL into destPath. Returns the size in bytes.
	Fetch(ctx context.Context, url, destPath string) (int64, error)
	// Upload uploads srcPath to the pre-signed PUT URL.
	Upload(ctx context.Context, url, srcPath string, contentType string) error
}

// HTTPClient is the minimal subset of http.Client we use. Tests inject a
// fake.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// HTTP is the production Storage backed by net/http.
type HTTP struct {
	Client HTTPClient
}

// New returns an HTTP storage with a 30s default timeout client.
func New() *HTTP {
	return &HTTP{Client: &http.Client{Timeout: 30 * time.Minute}}
}

// Fetch downloads url into destPath, creating parent dirs as needed.
func (h *HTTP) Fetch(ctx context.Context, url, destPath string) (int64, error) {
	if url == "" {
		return 0, errors.New("storage: empty url")
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return 0, fmt.Errorf("mkdir: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return 0, fmt.Errorf("storage: GET returned %d", resp.StatusCode)
	}
	f, err := os.Create(destPath) //nolint:gosec
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		return n, fmt.Errorf("copy: %w", err)
	}
	return n, nil
}

// Upload PUTs srcPath to url. Sets Content-Type when non-empty.
func (h *HTTP) Upload(ctx context.Context, url, srcPath string, contentType string) error {
	if url == "" {
		return errors.New("storage: empty url")
	}
	f, err := os.Open(srcPath) //nolint:gosec
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, f)
	if err != nil {
		return err
	}
	req.ContentLength = stat.Size()
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return fmt.Errorf("http put: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("storage: PUT returned %d", resp.StatusCode)
	}
	return nil
}

// Fake is an in-memory Storage for tests. Fetch reads from Inputs[url];
// Upload records the bytes in Uploads[url].
type Fake struct {
	Inputs  map[string][]byte
	Uploads map[string][]byte
	FailFetch  string
	FailUpload string
}

// NewFake returns an empty Fake.
func NewFake() *Fake {
	return &Fake{
		Inputs:  map[string][]byte{},
		Uploads: map[string][]byte{},
	}
}

// Fetch satisfies Storage.
func (f *Fake) Fetch(_ context.Context, url, dest string) (int64, error) {
	if url == f.FailFetch {
		return 0, errors.New("fake fetch failure")
	}
	b, ok := f.Inputs[url]
	if !ok {
		return 0, fmt.Errorf("fake: no input for %s", url)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(dest, b, 0o600); err != nil {
		return 0, err
	}
	return int64(len(b)), nil
}

// Upload satisfies Storage.
func (f *Fake) Upload(_ context.Context, url, src, _ string) error {
	if url == f.FailUpload {
		return errors.New("fake upload failure")
	}
	b, err := os.ReadFile(src) //nolint:gosec
	if err != nil {
		return err
	}
	f.Uploads[url] = b
	return nil
}
