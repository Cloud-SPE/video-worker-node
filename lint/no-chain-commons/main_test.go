package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPassesOnCleanRepo(t *testing.T) {
	dir := t.TempDir()
	clean := `package x
import "fmt"
var _ = fmt.Sprintln
`
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(clean), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var buf bytes.Buffer
	rc := Run(dir, &buf)
	if rc != 0 {
		t.Fatalf("expected rc=0, got %d, output: %s", rc, buf.String())
	}
}

func TestRunFlagsForbiddenImport(t *testing.T) {
	dir := t.TempDir()
	dirty := `package x
import (
	"github.com/Cloud-SPE/livepeer-modules/chain-commons/providers/rpc"
)
var _ = rpc.RPC(nil)
`
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(dirty), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var buf bytes.Buffer
	rc := Run(dir, &buf)
	if rc == 0 {
		t.Fatalf("expected non-zero rc, got 0")
	}
	if !strings.Contains(buf.String(), "forbidden chain-commons import") {
		t.Fatalf("output missing remediation hint:\n%s", buf.String())
	}
}
