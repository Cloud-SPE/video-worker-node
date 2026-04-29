// no-chain-commons enforces the workload-only invariant: this worker
// has no chain-glue dependency. Any imports of livepeer-modules's
// chain-commons library are flagged.
//
// Defense in depth alongside .golangci.yml's depguard rule.
package main

import (
	"flag"
	"os"
)

func main() {
	root := flag.String("root", ".", "module root")
	flag.Parse()
	os.Exit(Run(*root, os.Stderr))
}
