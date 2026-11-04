.PHONY: generate build test lint

generate:
	go generate ./internal/v4

build:
	go build -o scut ./cmd/scut

test:
	go test ./...

lint:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: files above need formatting"; exit 1; }
	go vet ./...
	golangci-lint run
