# Makefile builds the native and browser contributions as one validated release.
# Dependencies and lockfiles are initialized once before frozen release builds;
# development packages use the same declaration and assets as published archives.
SDK_VERSION ?= v0.1.0
TRPLUGIN ?= go run github.com/TokenFlux/TokenRouter/plugin-sdk/go/cmd/trplugin@$(SDK_VERSION)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: bootstrap frontend build dev pack test
# Bootstrap runs after the SDK versions are published; commit its two lockfiles.
bootstrap:
	go mod tidy
	pnpm --dir web install

frontend:
	pnpm --dir web install --frozen-lockfile
	pnpm --dir web build

build: frontend
	@set -eu; \
	for platform in $(PLATFORMS); do \
		os=$${platform%/*}; \
		arch=$${platform#*/}; \
		suffix=; \
		if [ "$$os" = windows ]; then \
			suffix=.exe; \
		fi; \
		mkdir -p "dist/$$platform/bin"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags="-s -w" -o "dist/$$platform/bin/plugin$$suffix" ./cmd/plugin; \
	done

# Point plugins.dev_dirs at the resulting absolute dev directory, then restart
# the plugin in TokenRouter after rebuilding; business data remains host-owned.
dev: frontend
	mkdir -p dev/bin dev/frontend
	go build -o "dev/bin/plugin$$(go env GOEXE)" ./cmd/plugin
	cp plugin.json dev/plugin.json
	cp dist/frontend/* dev/frontend/
	cp assets/icon.svg dev/icon.svg
	cp assets/menu.svg dev/frontend/menu.svg

pack: build
	$(TRPLUGIN) pack --manifest plugin.json --frontend dist/frontend --bin-dir dist --out release
	$(TRPLUGIN) validate release/*.tar.gz

test:
	go test -race ./...
	pnpm --dir web typecheck
