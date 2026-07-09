.PHONY: build install check fmt lint vet test test-all datastar-lint

build:
	go build -o bin/datastar-lint .

build-all:
	go build -tags "analyzer_python analyzer_ts" -o bin/datastar-lint .

install:
	go install .

install-all:
	go install -tags "analyzer_python analyzer_ts" .

# Quality gate (default: HTML + Go analyzers only).
check: fmt lint vet test
	@echo "✅ All checks passed"

fmt:
	@test -z "$$(gofumpt -l .)" || (echo "❌ gofumpt issues:"; gofumpt -l .; exit 1)
	@test -z "$$(goimports -l .)" || (echo "❌ goimports issues:"; goimports -l .; exit 1)

lint:
	golangci-lint run ./...

vet:
	go vet ./...

test:
	go test -race ./... -count=1

# Full test suite including Python/TypeScript analyzers.
test-all:
	go test -tags "analyzer_python analyzer_ts" -race ./... -count=1
	go test -race ./... -count=1  # also verify default build

datastar-lint:
	go run . -r ./testdata/
