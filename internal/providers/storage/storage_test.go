package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeHTTP struct {
	respBody   []byte
	statusCode int
	err        error
	gotMethod  string
	gotBody    []byte
}

func (f *fakeHTTP) Do(req *http.Request) (*http.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.gotMethod = req.Method
	if req.Body != nil {
		f.gotBody, _ = io.ReadAll(req.Body)
	}
	resp := &http.Response{
		StatusCode: f.statusCode,
		Body:       io.NopCloser(bytes.NewReader(f.respBody)),
		Header:     make(http.Header),
	}
	return resp, nil
}

func TestHTTPFetchAndUpload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	srvData := []byte("video bytes")
	upload := bytes.Buffer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write(srvData)
		case http.MethodPut:
			io.Copy(&upload, r.Body)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	s := New()
	dest := filepath.Join(dir, "in.mp4")
	n, err := s.Fetch(context.Background(), srv.URL, dest)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(srvData)) {
		t.Errorf("size=%d want %d", n, len(srvData))
	}
	src := filepath.Join(dir, "out.mp4")
	if err := os.WriteFile(src, []byte("encoded"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Upload(context.Background(), srv.URL, src, "video/mp4"); err != nil {
		t.Fatal(err)
	}
	if upload.String() != "encoded" {
		t.Errorf("uploaded=%q", upload.String())
	}
}

func TestHTTPFetchEmptyURL(t *testing.T) {
	t.Parallel()
	s := New()
	if _, err := s.Fetch(context.Background(), "", "/tmp/x"); err == nil {
		t.Fatal("expected error")
	}
	if err := s.Upload(context.Background(), "", "/tmp/x", "video/mp4"); err == nil {
		t.Fatal("expected error")
	}
}

func TestHTTPFetchNon2xx(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := &HTTP{Client: &fakeHTTP{statusCode: 404, respBody: []byte("nope")}}
	_, err := s.Fetch(context.Background(), "https://example/x", filepath.Join(dir, "x"))
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err=%v", err)
	}
}

func TestHTTPFetchClientError(t *testing.T) {
	t.Parallel()
	s := &HTTP{Client: &fakeHTTP{err: errors.New("network")}}
	if _, err := s.Fetch(context.Background(), "https://x/", t.TempDir()+"/y"); err == nil {
		t.Fatal("expected error")
	}
}

func TestHTTPUploadNon2xx(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	os.WriteFile(src, []byte("hi"), 0o600)
	s := &HTTP{Client: &fakeHTTP{statusCode: 500}}
	if err := s.Upload(context.Background(), "https://x/", src, "video/mp4"); err == nil {
		t.Fatal("expected error")
	}
}

func TestHTTPUploadMissingFile(t *testing.T) {
	t.Parallel()
	s := &HTTP{Client: &fakeHTTP{statusCode: 200}}
	if err := s.Upload(context.Background(), "https://x", "/no/such/file", "video/mp4"); err == nil {
		t.Fatal("expected open error")
	}
}

func TestFake(t *testing.T) {
	t.Parallel()
	f := NewFake()
	f.Inputs["k"] = []byte("hello")
	dir := t.TempDir()
	dest := filepath.Join(dir, "out")
	n, err := f.Fetch(context.Background(), "k", dest)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("n=%d", n)
	}
	src := filepath.Join(dir, "src")
	os.WriteFile(src, []byte("xyz"), 0o600)
	if err := f.Upload(context.Background(), "out", src, ""); err != nil {
		t.Fatal(err)
	}
	if string(f.Uploads["out"]) != "xyz" {
		t.Fatalf("uploaded=%q", f.Uploads["out"])
	}
	if _, err := f.Fetch(context.Background(), "missing", dest); err == nil {
		t.Fatal("expected fetch error")
	}
	f.FailFetch = "k"
	if _, err := f.Fetch(context.Background(), "k", dest); err == nil {
		t.Fatal("expected fail-fetch")
	}
	f.FailUpload = "out"
	if err := f.Upload(context.Background(), "out", src, ""); err == nil {
		t.Fatal("expected fail-upload")
	}
}
