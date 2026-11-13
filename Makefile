.PHONY: build test lint install golden

build:
	go build -o gh-spruce .

test:
	go test -race ./...

lint:
	golangci-lint run ./...

golden:
	go test ./internal/card -update

# Install the local build as a gh extension (run from the repo root).
install: build
	gh extension remove spruce 2>/dev/null || true
	gh extension install .
