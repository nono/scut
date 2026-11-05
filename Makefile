.PHONY: build test lint skill check-skill live

build:
	go build -o scut ./cmd/scut

test: node_modules
	go test ./...

# Prism for the contract tests. npm ci touches node_modules, so it runs again only when the lockfile changes.
node_modules: package-lock.json
	npm ci

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
