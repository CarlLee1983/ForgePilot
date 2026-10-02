.PHONY: verify format

verify: format
	go vet ./...
	go test ./...
	@tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; go build -o "$$tmp/forgepilot" ./cmd/forgepilot

format:
	@diff=$$(find . -name '*.go' -not -path './.forgepilot/*' -exec gofmt -d {} +) || exit 1; \
		test -z "$$diff" || { echo 'gofmt required' >&2; exit 1; }
