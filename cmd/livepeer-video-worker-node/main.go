// livepeer-video-worker-node is the entry point for the transcode
// worker daemon. Boots either VOD, ABR, or Live mode. Provider wiring,
// preflight, and lifecycle live in run.go.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Stderr))
}
