.PHONY: build install check fmt lint test datastar-lint vet

build:
	go build -o bin/datastar-lint .

install:
	go install .

# Quality gate — mirrors cali-go-stack's `make check`.
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

datastar-lint:
	go run . -r ./testdata/
