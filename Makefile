# mem7 — build and developer convenience targets
#
# Version is sourced from `git describe` at build time and injected
# into internal/memory.Version via -ldflags. A plain `go build` without
# the Makefile reports "dev" as the version.

MODULE      := github.com/KTCrisis/flux7-memory
BINARY      := mem7
PKG         := ./cmd/mem7
# Install target mirrors flux7-mesh : user-facing path, ignores GOBIN
# so flux7-mesh configs that hardcode ~/go/bin/mem7 keep working.
BIN         := $(HOME)/go/bin/$(BINARY)

VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X $(MODULE)/internal/memory.Version=$(VERSION)

.PHONY: all build install test vet fmt clean rescan version help

all: build

## build : compile the binary into ./bin/mem7
build:
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(PKG)
	@echo "built bin/$(BINARY) version=$(VERSION)"

## install : install the binary into ~/go/bin with version metadata
install:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)
	@echo "installed $(BIN) version=$(VERSION)"

## test : run the full test suite
test:
	go test ./...

## vet : static checks
vet:
	go vet ./...

## fmt : format all Go sources
fmt:
	gofmt -s -w .

## clean : remove build artefacts
clean:
	rm -rf bin/

## rescan : rebuild the SQLite index from the live markdown workspace
rescan: install
	$(BIN) rescan

## version : print the version that would be embedded at build time
version:
	@echo $(VERSION)

## help : list targets
help:
	@grep -E '^## ' Makefile | sed 's/## //'
