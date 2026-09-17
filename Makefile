.PHONY: verify

verify:
	@test -z "$$(gofmt -d $$(find . -name '*.go' -not -path './.forgepilot/*'))" || (echo 'gofmt required' >&2; exit 1)
	go vet ./...
	go test ./...
	sh scripts/onboarding/onboarding_test.sh
	@tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; go build -o "$$tmp/forgepilot" ./cmd/forgepilot
