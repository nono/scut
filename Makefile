.PHONY: build test lint skill check-skill live

build:
	go build -o scut ./cmd/scut

test: node_modules
	go test -race ./...

# Prism for the contract tests. npm ci touches node_modules, so it runs again only when the lockfile changes.
node_modules: package-lock.json
	npm ci

skill:
	go run ./internal/cmd/genskill

check-skill:
	go run ./internal/cmd/genskill -check

# golangci-lint runs gofmt and go vet too (see .golangci.yml).
lint: check-skill
	golangci-lint run
	govulncheck ./...

live:
	go test -tags live -count=1 -v ./internal/live
