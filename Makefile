.PHONY: generate build test lint skill check-skill

generate:
	go generate ./internal/v4

build:
	go build -o scut ./cmd/scut

test:
	go test ./...

skill:
	go run ./internal/cmd/genskill

check-skill:
	go run ./internal/cmd/genskill -check

lint: check-skill
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: files above need formatting"; exit 1; }
	go vet ./...
	golangci-lint run
