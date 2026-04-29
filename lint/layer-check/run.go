// layer-check delegates the bulk of layer enforcement to .golangci.yml's
// depguard rules. This Go-side checker exists so the Makefile target works
// and to provide a hook for future rule additions that don't fit depguard.
//
// Per the harness PDF: every lint should produce remediation hints when
// it fails. For now this lint is a no-op (depguard does the work); when
// rules accumulate that depguard can't express, they land here.
package main

import "io"

// Run walks the repo for layer-rule violations beyond what depguard
// handles. Returns the process exit code.
func Run(_ string, _ io.Writer) int {
	// No-op: see package doc. Returns 0 (success).
	return 0
}
