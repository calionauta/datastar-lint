# datastar-lint Makefile
#
# Targets:
#   build          — build binary with version injected via ldflags
#   install        — install binary via go install
#   test           — run all tests
#   test-verbose   — run all tests with verbose output
#   lint           — run golangci-lint (if installed)
#   clean          — remove build artifacts

GIT_VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags="-X main.version=$(GIT_VERSION)"
BINARY := datastar-lint

.PHONY: build install test test-verbose lint clean release

build:
	go build $(LDFLAGS) -o $(BINARY) .

install:
	go install $(LDFLAGS) .

test:
	go test -count=1 ./...

test-verbose:
	go test -count=1 -v ./...

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed — skipping"; \
	fi

release:
	@test -n "$(V)" || (echo 'Usage: make release V=v0.X.Y'; exit 1)
	git tag -a $(V) -m "Release $(V)"
	git push origin $(V)
	@echo "Tag $(V) pushed. CI will build and deploy."

clean:
	rm -f $(BINARY)
