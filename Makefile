.PHONY: test test-verbose test-e2e spec-coverage build clean

BINARY := compliance-operator-importer
GOFLAGS := -v

## Run all tests (Godog scenarios + Go unit tests)
test:
	go test ./features/... ./internal/...

## Run tests with verbose Godog output (shows each step)
test-verbose:
	go test -v ./features/... ./internal/...

## Check that all IMP-* requirement IDs from specs appear in test code
spec-coverage:
	./hack/check-spec-coverage.sh

## Run e2e/acceptance tests against a real cluster (requires env vars, see CLAUDE.md)
test-e2e:
	go test -v -tags=e2e -timeout=5m ./e2e/...

## Smoke test: build + dry-run against real cluster (quick real-cluster validation)
smoke:
	$(MAKE) build
	./bin/$(BINARY) --endpoint "$${ROX_ENDPOINT}" --dry-run --insecure-skip-verify

## Build the importer binary
build:
	CGO_ENABLED=0 go build -o bin/$(BINARY) ./cmd/importer/

## Remove build artifacts
clean:
	rm -rf bin/
