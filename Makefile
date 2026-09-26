# Huginn developer tasks. Every target is a thin wrapper around the go
# tool, so Windows users can run the same commands directly.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/ghiloufibg/huginn/internal/buildinfo.Version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: all build test race lint demo cross schema tidy

all: lint test build

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/huginn ./cmd/huginn

test:
	go test ./...

race:
	go test -race ./...

lint:
	golangci-lint run

demo: build
	./bin/huginn --demo

schema:
	go generate ./internal/config/...

cross:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "build $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o bin/huginn-$$os-$$arch$$ext ./cmd/huginn || exit 1; \
	done

tidy:
	go mod tidy
