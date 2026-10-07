# Makefile for sep47idx. Every target here is run by CI or documented in
# CONTRIBUTING.md; keep the two in sync.

GO        ?= go
BINARY    := sep47idx
PKG       := ./cmd/sep47idx
FUZZTIME  ?= 30s
FUZZPKGS  ?= ./pkg/sepmeta
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)

export CGO_ENABLED := 0

.PHONY: all test lint fuzz fuzz-smoke build fixtures docs tidy-check clean

all: lint test build

## test: run unit tests with the race-free, network-free default set
test:
	$(GO) test ./...

## lint: golangci-lint (version pinned in .github/workflows/ci.yml)
lint:
	golangci-lint run ./...

## fuzz: run every Fuzz* target in FUZZPKGS for FUZZTIME each
fuzz:
	@for pkg in $(FUZZPKGS); do \
		targets=$$($(GO) test -list '^Fuzz' $$pkg | grep '^Fuzz' || true); \
		if [ -z "$$targets" ]; then echo "no fuzz targets in $$pkg"; continue; fi; \
		for t in $$targets; do \
			echo "== $$pkg $$t ($(FUZZTIME))"; \
			$(GO) test -run='^$$' -fuzz="^$$t$$" -fuzztime=$(FUZZTIME) $$pkg || exit 1; \
		done; \
	done

## fuzz-smoke: the CI variant of fuzz (30s per target)
fuzz-smoke:
	$(MAKE) fuzz FUZZTIME=30s

## build: static binary for the host platform
build:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) $(PKG)

## fixtures: rebuild golden Wasm under testdata/wasm (needs Rust; CI never does)
fixtures:
	./scripts/build-fixtures.sh

## docs: build the MkDocs site into ./site (needs docs/requirements.txt)
docs:
	mkdocs build --strict

## tidy-check: fail if go.mod/go.sum are not tidy
tidy-check:
	$(GO) mod tidy -diff

clean:
	rm -f $(BINARY)
	rm -rf site dist
