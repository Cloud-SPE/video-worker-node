.PHONY: build test test-race lint doc-lint coverage-check proto docker-build clean help

BINARY      := bin/livepeer-video-worker-node
PKG         := ./...
DOCKER_REGISTRY ?= tztcloud
DOCKER_TAG      ?= dev
DOCKER_TARGET   ?= runtime-nvidia

.DEFAULT_GOAL := help

help:
	@echo "Worker module targets:"
	@echo "  make build           — go build ./..."
	@echo "  make test            — go test ./..."
	@echo "  make test-race       — go test -race ./..."
	@echo "  make lint            — go vet + custom lints"
	@echo "  make doc-lint        — placeholder until doc-gardener is wired"
	@echo "  make coverage-check  — 75% per-package gate"
	@echo "  make proto           — regenerate vendored proto stubs"
	@echo "  make docker-build    — build a vendor variant (DOCKER_TARGET=runtime-{nvidia,intel,amd})"
	@echo "  make clean           — wipe bin/ and module cache for this module"

build:
	@mkdir -p bin
	go build -o $(BINARY) ./cmd/livepeer-video-worker-node

test:
	go test $(PKG)

test-race:
	go test -race $(PKG)

lint:
	go vet $(PKG)
	@if [ -d lint/no-cgo ];          then go run ./lint/no-cgo/...          ./...; fi
	@if [ -d lint/no-chain-commons ];then go run ./lint/no-chain-commons/...  ./...; fi
	@if [ -d lint/layer-check ];     then go run ./lint/layer-check/...     ./...; fi
	@if [ -d lint/no-secrets-in-logs ];then go run ./lint/no-secrets-in-logs/... ./...; fi

doc-lint:
	@echo "doc-lint: placeholder until doc-gardener is wired in"

coverage-check:
	@pkgs=$$(go list -f '{{if or (gt (len .TestGoFiles) 0) (gt (len .XTestGoFiles) 0)}}{{.ImportPath}}{{end}}' $(PKG) | sed '/^$$/d'); \
	if [ -n "$$pkgs" ]; then \
		go test -coverprofile=coverage.out $$pkgs; \
	else \
		echo "coverage-check: no test packages found"; \
		exit 1; \
	fi
	@if [ -d lint/coverage-gate ]; then go run ./lint/coverage-gate/... -threshold=75 -coverage=coverage.out; fi

proto:
	@if [ -d proto ]; then \
		echo "Regenerate proto stubs (requires buf CLI in PATH)"; \
		cd proto && buf generate || echo "buf not installed; skipping (stubs are vendored)"; \
	fi

docker-build:
	docker buildx build \
		--target $(DOCKER_TARGET) \
		--build-arg REGISTRY=$(DOCKER_REGISTRY) \
		--build-arg TAG=$(DOCKER_TAG) \
		-t $(DOCKER_REGISTRY)/livepeer-video-worker-node-$(subst runtime-,,$(DOCKER_TARGET)):$(DOCKER_TAG) \
		.

clean:
	rm -rf bin coverage.out
