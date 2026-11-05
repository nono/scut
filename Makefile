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
# deadcode exits 0 on findings, so its output is the failure.
lint: check-skill
	golangci-lint run
	govulncheck ./...
	go mod tidy -diff
	@out=$$(deadcode -test ./...) && [ -z "$$out" ] || { echo "$$out"; exit 1; }
	actionlint
	zizmor --offline .github/workflows

live:
	go test -tags live -count=1 -v ./internal/live
