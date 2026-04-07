.PHONY: test test-verbose test-e2e spec-coverage build clean lint image image-push setup

BINARY   := compliance-operator-importer
IMAGE    ?= ghcr.io/guzalv/stackrox-co-importer
TAG      ?= latest
ARCHS    := amd64 arm64

## Configure git to use the repo's hooks (run once after cloning)
setup:
	git config core.hooksPath .githooks

## Run all tests (Godog scenarios + Go unit tests)
test: _check-hooks
	go test ./features/... ./internal/...

## Warn if git hooks aren't activated (not fatal — hooks can't be forced)
_check-hooks:
	@if [ "$$(git config core.hooksPath)" != ".githooks" ]; then \
		echo "WARNING: pre-commit hooks not active. Run 'make setup' to enable lint+test on every commit."; \
	fi

## Run tests with verbose Godog output (shows each step)
test-verbose:
	go test -v ./features/... ./internal/...

## Run tests with coverage report
test-cover:
	go test -coverprofile=coverage.out ./internal/...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## Run linter
lint:
	golangci-lint run ./...

## Check that all IMP-* requirement IDs from specs appear in test code
spec-coverage:
	./hack/check-spec-coverage.sh

## Run e2e/acceptance tests against a real cluster (requires env vars, see CLAUDE.md)
test-e2e:
	go test -v -tags=e2e -timeout=10m ./e2e/...

## Smoke test: build + dry-run against real cluster (quick real-cluster validation)
smoke:
	$(MAKE) build
	./bin/$(BINARY) --endpoint "$${ROX_ENDPOINT}" --dry-run --insecure-skip-verify

## Build the importer binary
build:
	CGO_ENABLED=0 go build -o bin/$(BINARY) ./cmd/importer/

## Build container image for host architecture (IMP-IMG-004)
image: build
	cp bin/$(BINARY) $(BINARY)
	docker build -t $(IMAGE):$(TAG) .
	rm -f $(BINARY)

## Build and push multi-arch images + manifest (IMP-IMG-003, IMP-IMG-004)
image-push:
	$(foreach arch,$(ARCHS),\
		CGO_ENABLED=0 GOOS=linux GOARCH=$(arch) go build -o $(BINARY) ./cmd/importer/ && \
		docker build --platform linux/$(arch) -t $(IMAGE):$(TAG)-$(arch) . && \
		docker push $(IMAGE):$(TAG)-$(arch) ;)
	docker manifest create $(IMAGE):$(TAG) $(foreach arch,$(ARCHS),$(IMAGE):$(TAG)-$(arch))
	docker manifest push $(IMAGE):$(TAG)
	rm -f $(BINARY)

## Remove build artifacts
clean:
	rm -rf bin/ coverage.out coverage.html
