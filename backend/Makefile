.PHONY: build generate test test-architecture test-unit test-integration test-e2e test-e2e-local

VERSION ?= $(shell ./scripts/resolve-version.sh)
LDFLAGS ?= -s -w -X main.Version=$(VERSION)

build:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -trimpath -o bin/server ./cmd/server

generate:
	go generate ./ent
	go generate ./cmd/server

test: test-architecture
	go test ./...
	golangci-lint config verify
	golangci-lint run ./...

# 架构检查读取模块外的后端文件，关闭测试缓存以保证每次检查当前源码。
test-architecture:
	cd ../tools/architecture && GOWORK=off go test -count=1 ./...

test-unit:
	go test -tags=unit ./...

test-integration:
	go test -tags=integration ./...

test-e2e: test-e2e-local

test-e2e-local:
	go test -tags=e2e -v -timeout=300s ./tests/integration/...
