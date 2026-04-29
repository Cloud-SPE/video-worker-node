// doc-gardener delegates module-internal cross-link integrity checks to
// the monorepo-level doc-gardener at lint/doc-gardener/run.mjs in the
// repo root. This Go-side stub exists for consistency so module Makefile
// targets can invoke a local lint without crossing into Node.
//
// When module-only checks accumulate (e.g., proto-stub freshness), they
// land here.
package main

import "io"

// Run is a no-op at this stage; see package doc.
func Run(_ string, _ io.Writer) int {
	return 0
}
