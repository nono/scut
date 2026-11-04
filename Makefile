.PHONY: build test lint skill check-skill live

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

live:
	go test -tags live -count=1 -v ./internal/live
