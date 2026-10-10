.PHONY: build test lint clean

VERSION ?= dev

build:
	CGO_ENABLED=0 go build -ldflags "-X main.version=$(VERSION) -s -w" -o bin/deploy-agent ./cmd/deploy-agent

test:
	go test ./...

lint:
	gofmt -l .
	go vet ./...

clean:
	rm -rf bin
