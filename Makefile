.PHONY: test test-verbose spec-coverage build clean

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

## Build the importer binary
build:
	CGO_ENABLED=0 go build -o bin/$(BINARY) ./cmd/importer/

## Remove build artifacts
clean:
	rm -rf bin/
