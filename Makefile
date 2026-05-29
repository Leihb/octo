# Octo — Go build & test orchestration
#
# Usage:
#   make            same as `make test`
#   make build      build the octo binary at repo root (./octo)
#   make install    install to $GOPATH/bin
#   make test       go test -race ./...
#   make cover      generate coverage.out + coverage.html
#   make vet        go vet ./...
#   make fmt        gofmt -w on the tree
#   make fmt-check  fail if anything would be reformatted
#   make tidy       go mod tidy
#   make clean      remove build artefacts
#
# Multi-SWE-bench eval (manual; needs a model key + Docker + Python, NOT in CI):
#   make eval-mswe-build                build the ./mswe-eval tool
#   make eval-mswe-inspect DATASET=...  confirm the dataset schema
#   make eval-mswe DATASET=...          generate patches with octo, then judge
# See dev-docs/mswe-eval.md.

GOTAGS ?=
GOFLAGS ?=

# Inject version + commit at build time so `octo version` reports a real SHA.
#
# Auto-detection via `git describe` is intentionally avoided here because the
# repo still carries Ruby-era tags (v0.11.2, v0.11.2-final-ruby) that would
# pollute the reported version. Dev builds say "<next>-dev"; release builds
# should set VERSION explicitly:
#
#   VERSION=0.5.0 make build
#
VERSION ?= 0.5.0-dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X github.com/Leihb/octo-agent/internal/version.Version=$(VERSION) \
           -X github.com/Leihb/octo-agent/internal/version.Commit=$(COMMIT)

.PHONY: all build install test cover vet fmt fmt-check tidy clean \
        eval-mswe-build eval-mswe-inspect eval-mswe

all: test

build:
	go build $(GOFLAGS) -tags='$(GOTAGS)' -ldflags='$(LDFLAGS)' -o octo ./cmd/octo

install:
	go install $(GOFLAGS) -tags='$(GOTAGS)' -ldflags='$(LDFLAGS)' ./cmd/octo

test:
	go test -race $(GOFLAGS) -tags='$(GOTAGS)' ./...

cover:
	go test $(GOFLAGS) -tags='$(GOTAGS)' -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Open coverage.html in a browser to see line coverage."

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt found unformatted files:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

tidy:
	go mod tidy

clean:
	rm -f octo octo.exe mswe-eval coverage.out coverage.html
	rm -rf dist/

# ── Multi-SWE-bench eval (manual; see dev-docs/mswe-eval.md) ────────────────
# DATASET must point at a Go-filtered Multi-SWE-bench JSONL. LIMIT caps the
# instance count. These call a real model and (for judging) Docker + Python —
# never run them in CI.
LIMIT  ?= 5

eval-mswe-build:
	go build $(GOFLAGS) -o mswe-eval ./cmd/mswe-eval

eval-mswe-inspect: eval-mswe-build build
	./mswe-eval inspect --dataset '$(DATASET)' --limit $(LIMIT)

eval-mswe: eval-mswe-build build
	./mswe-eval run --dataset '$(DATASET)' --limit $(LIMIT) --octo ./octo --out predictions.jsonl
