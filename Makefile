BINARY    := fingerprinter
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS   := -ldflags="-s -w -X main.version=$(VERSION)"
GOFILES   := $(shell find . -name '*.go' -not -path './vendor/*')

.PHONY: all build build-all run test lint clean docker

all: lint test build

build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/fingerprinter

build-all:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY)-linux-amd64 ./cmd/fingerprinter
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o bin/$(BINARY)-linux-arm64 ./cmd/fingerprinter
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY)-darwin-amd64 ./cmd/fingerprinter
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o bin/$(BINARY)-darwin-arm64 ./cmd/fingerprinter

run: build
	./bin/$(BINARY) --config config.yml

test:
	go test -race -cover ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

docker:
	docker compose -f docker-compose.dev.yml build

docker-run:
	docker compose -f docker-compose.dev.yml up -d
