.PHONY: test test-go test-e2e lint install-hooks

# Run all tests: lint, fast Go unit tests, and browser E2E tests.
test: lint test-go test-e2e

# Fast Go HTTP integration tests (excludes browser tests via -short).
# No external services required — Redis is provided in-process by miniredis.
test-go:
	go test ./... -race -timeout 60s -count=1 -short

# Browser E2E tests using go-rod + headless Chromium.
# Requires Chromium to be installed (rod auto-downloads if not found).
test-e2e:
	go test ./internal/browser/... -v -timeout 120s

# JS lint via Biome.
lint:
	npm run lint

# Activate git hooks stored in .githooks/
install-hooks:
	git config core.hooksPath .githooks
