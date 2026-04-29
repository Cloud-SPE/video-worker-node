package main

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const forbiddenPrefix = `"github.com/Cloud-SPE/livepeer-modules/chain-commons`

// Run walks the worker module rooted at root and emits ERROR lines for any
// Go source that imports the forbidden chain-commons module path.
//
// Returns the process exit code.
func Run(root string, out io.Writer) int {
	failures := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || name == ".git" || name == "lint" || name == "proto" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
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
		inImport := false
		for scan.Scan() {
			ln++
			line := scan.Text()
			t := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(t, "import ("):
				inImport = true
				continue
			case t == ")" && inImport:
				inImport = false
				continue
			}
			if !inImport && !strings.HasPrefix(t, "import ") {
				continue
			}
			if !strings.Contains(line, forbiddenPrefix) {
				continue
			}
			failures++
			rel, _ := filepath.Rel(root, path)
			fmt.Fprintf(out,
				"ERROR %s:%d  forbidden chain-commons import\n"+
					"      %s\n"+
					"      → Remediation: this module is workload-only. Route any chain interaction through "+
					"the local payment-daemon over gRPC (internal/providers/paymentclient). "+
					"See docs/design-docs/core-beliefs.md.\n",
				rel, ln, strings.TrimSpace(line))
		}
		return nil
	})
	if failures == 0 {
		return 0
	}
	return 1
}
