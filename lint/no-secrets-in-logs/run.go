// no-secrets-in-logs scans Go source for log calls that pass values
// whose names suggest they hold secrets, without an obvious redaction.
//
// Heuristic-based; produces remediation hints per the harness PDF
// convention.
package main

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	logCall      = regexp.MustCompile(`\b(?:logger|log|slog|cfg\.Logger|r\.cfg\.Logger|s\.logger)\.(?:Info|Warn|Error|Debug|Print(?:f|ln)?|Errorf|Fatalf)\(`)
	secretToken  = regexp.MustCompile(`\b(?:[A-Za-z0-9_]*?(?:secret|password|passwd)[A-Za-z0-9_]*|stream[_-]?key|api[_-]?key|private[_-]?key|signing[_-]?key|access[_-]?token|bearer[_-]?token|auth[_-]?token)\b`)
	safeToken    = regexp.MustCompile(`\b(?:key_hash|keyHash|api_key_hash|apiKeyHash|stream_key_hash|streamKeyHash|csrf_token|csrfToken|key_id|keyId)\b`)
	disable      = regexp.MustCompile(`no-secrets-in-logs[ -]?disable`)
	redactor     = regexp.MustCompile(`\b(?:redact|maskSecret|sanitize)\(`)
)

// Run walks the worker module rooted at root and emits ERROR lines for
// suspect log calls. Returns the process exit code.
func Run(root string, out io.Writer) int {
	failures := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Skip vendor, lint internals, generated proto stubs.
		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || name == ".git" || name == "lint" || name == "proto" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		scan := bufio.NewScanner(f)
		scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		ln := 0
		for scan.Scan() {
			ln++
			line := scan.Text()
			if disable.MatchString(line) {
				continue
			}
			if !logCall.MatchString(line) {
				continue
			}
			if !secretToken.MatchString(line) {
				continue
			}
			if safeToken.MatchString(line) {
				continue
			}
			if redactor.MatchString(line) {
				continue
			}
			failures++
			rel, _ := filepath.Rel(root, path)
			fmt.Fprintf(out,
				"ERROR %s:%d  log call references potentially-secret variable\n"+
					"      %s\n"+
					"      → Remediation: redact via redact(...), maskSecret(...), or token[:6]+\"...\" before logging. "+
					"Or add a `// no-secrets-in-logs disable` comment with a one-line justification.\n",
				rel, ln, strings.TrimSpace(line))
		}
		return nil
	})
	if failures == 0 {
		return 0
	}
	return 1
}
