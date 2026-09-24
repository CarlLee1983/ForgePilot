.PHONY: verify format

verify: format
	go vet ./...
	go test ./...
	sh scripts/release/build_trial_assets_test.sh
	sh scripts/release/publish_trial_assets_workflow_test.sh
	sh scripts/onboarding/onboarding_test.sh
	sh scripts/skills/check_adapters_test.sh
	sh scripts/skills/short_prompt_regression_test.sh
	@tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; go build -o "$$tmp/forgepilot" ./cmd/forgepilot

format:
	@diff=$$(find . -name '*.go' -not -path './.forgepilot/*' -exec gofmt -d {} +) || exit 1; \
		test -z "$$diff" || { echo 'gofmt required' >&2; exit 1; }
