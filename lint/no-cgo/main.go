// no-cgo enforces the hard rule from plan 0007 §A: this module is pure
// Go. No `import "C"`, no `// +build cgo` or `//go:build cgo` directives.
//
// FFmpeg is a subprocess, never a cgo binding. Crash isolation,
// multi-vendor build hygiene, and avoiding the lpms binding model
// motivate this lint.
package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Finding describes one violation.
type Finding struct {
	Path string
	Line int
	Msg  string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: no-cgo: %s", f.Path, f.Line, f.Msg)
}

// CheckRepo walks `root` for .go files and returns all findings.
func CheckRepo(root string) ([]Finding, error) {
	var findings []Finding
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if strings.HasPrefix(base, ".") || base == "vendor" || base == "bin" || base == "node_modules" || base == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		got, err := CheckFile(root, path)
		if err != nil {
			return err
		}
		findings = append(findings, got...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Line < findings[j].Line
	})
	return findings, nil
}

// CheckFile inspects a single file's import block + build directives.
func CheckFile(root, path string) ([]Finding, error) {
	rel, _ := filepath.Rel(root, path)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var out []Finding
	for _, imp := range file.Imports {
		ip := strings.Trim(imp.Path.Value, `"`)
		if ip == "C" {
			pos := fset.Position(imp.Pos())
			out = append(out, Finding{
				Path: rel, Line: pos.Line,
				Msg: `import "C": cgo is forbidden in transcode-worker-node`,
			})
		}
	}
	// Scan for // +build cgo or //go:build cgo directives via raw read,
	// because parser.ImportsOnly doesn't expose build tags as comments
	// reliably across Go versions.
	b, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	for ln, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//go:build ") && strings.Contains(t, "cgo") {
			out = append(out, Finding{Path: rel, Line: ln + 1, Msg: t})
		}
		if strings.HasPrefix(t, "// +build ") && strings.Contains(t, "cgo") {
			out = append(out, Finding{Path: rel, Line: ln + 1, Msg: t})
		}
	}
	return out, nil
}

// Run is the CLI entry point. Returns 0 clean, 1 findings, 2 IO error.
func Run(root string, stderr io.Writer) int {
	findings, err := CheckRepo(root)
	if err != nil {
		fmt.Fprintf(stderr, "no-cgo: %v\n", err)
		return 2
	}
	for _, f := range findings {
		fmt.Fprintln(stderr, f)
	}
	if len(findings) > 0 {
		fmt.Fprintln(stderr, "\nno-cgo: cgo is forbidden in transcode-worker-node.")
		fmt.Fprintln(stderr, "Remediation: route the dependency through internal/providers/* (subprocess wrapper).")
		return 1
	}
	return 0
}

func main() {
	root := "."
	for i, a := range os.Args[1:] {
		if a == "--root" || a == "-root" {
			if i+2 <= len(os.Args)-1 {
				root = os.Args[i+2]
			}
		}
	}
	os.Exit(Run(root, os.Stderr))
}
