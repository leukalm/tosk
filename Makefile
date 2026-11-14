VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/leukalm/tosk/internal/cli.Version=$(VERSION)

.PHONY: build test lint fmt install snapshot clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/tosk .

test:
	go test -race ./...

lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:"; gofmt -l .; exit 1)

fmt:
	gofmt -w .

install:
	go install -ldflags "$(LDFLAGS)" .

snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf bin dist
