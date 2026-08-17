BINARY  := whodar
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/kordloom/whodar/cmd.version=$(VERSION)

.PHONY: build install test e2e vet fmt lint release site-docs clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

install:
	go install -ldflags "$(LDFLAGS)" .

test:
	go test ./...

e2e:
	go test -count=1 -v -run TestFullPipeline ./internal/simorg

vet:
	go vet ./...

fmt:
	gofmt -w .

lint:
	golangci-lint run

release:
	goreleaser release --clean

site-docs:
	go -C site/gen run .

clean:
	rm -f $(BINARY)
