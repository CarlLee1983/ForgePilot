.PHONY: verify

verify:
	@test -z "$$(gofmt -d $$(find . -name '*.go' -not -path './.forgepilot/*'))" || (echo 'gofmt required' >&2; exit 1)
	go vet ./...
	go test ./...
	sh scripts/release/build_trial_assets_test.sh
	sh scripts/onboarding/onboarding_test.sh
	sh scripts/skills/check_adapters_test.sh
	sh scripts/skills/short_prompt_regression_test.sh
	@tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; go build -o "$$tmp/forgepilot" ./cmd/forgepilot
