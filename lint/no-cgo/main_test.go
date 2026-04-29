package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckFileClean(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := `package x

import "fmt"

func F() { fmt.Println("hi") }
`
	p := filepath.Join(dir, "x.go")
	os.WriteFile(p, []byte(src), 0o600)
	findings, err := CheckFile(dir, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings=%v", findings)
	}
}

func TestCheckFileCgoImport(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := `package x

import "C"

func F() {}
`
	p := filepath.Join(dir, "x.go")
	os.WriteFile(p, []byte(src), 0o600)
	findings, err := CheckFile(dir, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 {
		t.Fatal("expected finding")
	}
}

func TestCheckFileBuildTag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := `//go:build cgo

package x

func F() {}
`
	p := filepath.Join(dir, "x.go")
	os.WriteFile(p, []byte(src), 0o600)
	findings, err := CheckFile(dir, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 {
		t.Fatal("expected finding for go:build cgo")
	}
}

func TestCheckFileLegacyBuildTag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := `// +build cgo

package x

func F() {}
`
	p := filepath.Join(dir, "x.go")
	os.WriteFile(p, []byte(src), 0o600)
	findings, err := CheckFile(dir, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 {
		t.Fatal("expected finding for legacy build tag")
	}
}

func TestRunClean(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.go"), []byte("package x\n"), 0o600)
	var buf bytes.Buffer
	if got := Run(dir, &buf); got != 0 {
		t.Errorf("got=%d output=%s", got, buf.String())
	}
}

func TestRunFinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.go"), []byte("package x\nimport \"C\"\n"), 0o600)
	var buf bytes.Buffer
	if got := Run(dir, &buf); got != 1 {
		t.Errorf("got=%d", got)
	}
}

func TestRunIOError(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if got := Run("/nonexistent/path", &buf); got != 2 {
		t.Errorf("got=%d", got)
	}
}

func TestCheckRepoSkipsHidden(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755)
	os.WriteFile(filepath.Join(dir, ".hidden", "bad.go"), []byte("package x\nimport \"C\"\n"), 0o600)
	findings, err := CheckRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("hidden dir should be skipped: %v", findings)
	}
}
