# Makefile defines repository development, verification, and release-build commands.
# CI jobs invoke the same targets used locally. Go formatting supports a comparison
# base, frontend checks run against the full frontend, and integration acceptance
# requires the external tools and container services described in development docs.
# GOTOOLCHAIN follows the backend module requirement; Python checks suppress bytecode
# artifacts so validation leaves source directories free of interpreter cache files.
export GOTOOLCHAIN := go$(shell sed -n 's/^go //p' backend/go.mod)
export PYTHONDONTWRITEBYTECODE := 1

# PNPM uses the packageManager version recorded in frontend/package.json; GOLANGCI
# runs the pinned backend linter wrapper from its expected module directory.
PNPM := pnpm --dir frontend
GOLANGCI := cd backend && bash ../tools/golangci-lint.sh
# INTEGRATION_PKGS selects packages carrying the integration build constraint;
# test-integration runs these packages with Docker-backed service dependencies.
INTEGRATION_PKGS = $(shell cd backend && grep -rlE '^//go:build integration$$' --include='*.go' . | xargs -n1 dirname | sort -u)

.PHONY: build build-frontend build-backend generate \
	fmt fmt-check fmt-go fmt-go-check fmt-frontend fmt-frontend-check lint lint-go lint-frontend \
	test test-go test-frontend test-frontend-tools test-integration test-embed test-scripts test-installer test-tools test-e2e \
	check verify security security-go security-frontend hooks

.PHONY: prepare-plugin-sdk-web lint-plugin-sdk test-plugin-sdk plugin-proto-check fmt-plugin-sdk fmt-plugin-sdk-check

# build packages frontend assets before compiling the server that embeds those assets.
build: build-frontend
	cd backend && CGO_ENABLED=0 go build -tags=embed -trimpath -o bin/server ./cmd/server

# build-frontend bundles the browser assets used by the embedded server.
build-frontend:
	$(PNPM) run build:assets

# build-backend compiles the development server used with a separate Vite process.
build-backend:
	$(MAKE) -C backend build

# generate runs the backend generators from their owning module directory.
generate:
	$(MAKE) -C backend generate

# Go formatting selects changed files and honors BASE; frontend formatting covers
# every handwritten file selected by its configuration and resource exclusions.
# fmt applies the formatters for both repository implementation languages.
fmt: fmt-go fmt-frontend fmt-plugin-sdk

# fmt-check checks Go selection and all included frontend source files.
fmt-check: fmt-go-check fmt-frontend-check fmt-plugin-sdk-check

# fmt-go formats changed handwritten Go files through the repository selector.
fmt-go:
	python3 tools/format_go.py

# fmt-go-check checks selected Go files and accepts the optional BASE revision.
fmt-go-check:
	python3 tools/format_go.py --check $(if $(BASE),--base "$(BASE)")

# fmt-frontend formats all frontend files included by the pinned formatter.
fmt-frontend:
	$(PNPM) run format

# fmt-frontend-check validates frontend layout while leaving source files unchanged.
fmt-frontend-check:
	$(PNPM) run format:check

# lint runs static verification for both backend and frontend source.
lint: lint-go lint-frontend lint-plugin-sdk

# lint-go checks architecture and Go sources with the integration build tag.
# This includes ordinary and integration files; test-embed covers embedded-resource files.
lint-go:
	cd tools/architecture && GOWORK=off go test -count=1 ./...
	$(GOLANGCI) config verify
	$(GOLANGCI) run --build-tags=integration ./...

# lint-frontend checks formatting, source policies, syntax, and frontend types.
lint-frontend:
	$(PNPM) run lint:check
	$(PNPM) run typecheck

# test runs the default backend and frontend regression suites.
test: test-go test-frontend test-plugin-sdk

# prepare-plugin-sdk-web installs the SDK's frozen graph without requiring host UI dependencies.
prepare-plugin-sdk-web:
	pnpm --dir plugin-sdk/web install --frozen-lockfile

# lint-plugin-sdk checks the independent Go module and the public frontend contract.
lint-plugin-sdk: prepare-plugin-sdk-web
	cd plugin-sdk/go && bash ../../tools/golangci-lint.sh run ./...
	pnpm --dir plugin-sdk/web run format:check
	pnpm --dir plugin-sdk/web run typecheck

# test-plugin-sdk exercises protocol, process fixtures, publication, and frontend bundling.
test-plugin-sdk: prepare-plugin-sdk-web
	cd plugin-sdk/go && go test ./...
	pnpm --dir plugin-sdk/web test

# plugin-proto-check regenerates committed bindings and rejects any resulting drift.
plugin-proto-check:
	$(MAKE) -C plugin-sdk/go proto
	git diff --exit-code -- plugin-sdk/go/pluginpb plugin-sdk/go/proto

# fmt-plugin-sdk formats SDK web source and the minimal consumer test fixtures.
fmt-plugin-sdk:
	pnpm --dir plugin-sdk/web run format

# fmt-plugin-sdk-check verifies SDK web formatting without rewriting author source files.
fmt-plugin-sdk-check:
	pnpm --dir plugin-sdk/web run format:check

# test-go runs backend tests selected by the default build tags.
test-go:
	cd backend && go test ./...

# test-frontend runs tool fixtures before the complete application test suite.
test-frontend: test-frontend-tools
	$(PNPM) run test:run

# test-frontend-tools runs parser and formatter fixtures independently from application tests.
test-frontend-tools:
	$(PNPM) run test:tools

# test-integration limits package concurrency to control simultaneously running
# PostgreSQL and Redis containers used by integration fixture suites.
test-integration:
	cd backend && go test -tags=integration -p=4 $(INTEGRATION_PKGS)

# test-embed checks the server variant that embeds compiled frontend assets.
# A missing build aborts before tests because the placeholder cannot represent
# the production resource tree consumed by the embedded server.
test-embed:
	@test -s backend/internal/web/dist/index.html || { echo '缺少前端构建产物，请先运行 make build-frontend'; exit 1; }
	$(GOLANGCI) run --build-tags=embed ./internal/web/... ./cmd/server/...
	cd backend && go test -tags=embed ./internal/web/...

# test-scripts verifies deployment shell syntax and platform-compatible script fixtures.
test-scripts:
	@for script in deploy/*.sh deploy/tests/*.sh deploy/tests/fixtures/bin/*; do \
		/bin/bash -n "$$script" || exit 1; \
	done
	/bin/bash deploy/tests/install-github-token-test.sh
	/bin/bash deploy/tests/apple-container-test.sh
	/bin/sh deploy/tests/docker-compose-security-test.sh
	/bin/sh deploy/tests/docker-compose-postgres-test.sh
	/bin/sh deploy/tests/docker-compose-variants-test.sh
	/bin/sh deploy/tests/docker-compose-gateway-env-test.sh
	/bin/sh deploy/tests/docker-runtime-resources-test.sh
	/bin/sh deploy/tests/caddyfile-cache-test.sh
	/bin/sh tools/goreleaser_prebuilt_test.sh

# test-installer runs installer fixtures in Linux with Bash 4 and sha256sum.
# macOS uses an Ubuntu container because its bundled Bash lacks the required
# shell features used by the Linux installer and its fixtures.
test-installer:
	@if [ "$$(uname -s)" = Linux ]; then bash deploy/tests/install-brand-test.sh; else docker run --rm -v "$(CURDIR):/source:ro" -w /source ubuntu:24.04 bash deploy/tests/install-brand-test.sh; fi

# test-tools runs Python tooling and declaration-order regressions; formatter
# fixtures invoke the pinned golangci-lint installation used by normal repository commands.
test-tools:
	python3 -m unittest discover -s tools -p 'test_*.py'
	python3 -m unittest discover -s .agents/skills/tr-release/scripts -p 'test_*.py'
	cd tools/declorder && GOWORK=off go test ./...

# test-e2e connects to a running service using addresses and credentials from
# environment variables; callers provide an isolated test environment before invocation.
test-e2e:
	cd backend && go test -count=1 -tags=e2e -v -timeout=300s ./tests/integration/...

# check selects pre-push checks from changed files in the working tree.
# BASE defaults to the upstream common ancestor, matching the hook entry point.
check:
	python3 tools/check_changed.py $(if $(BASE),--base "$(BASE)")

# verify runs the CI acceptance targets locally and requires Docker services.
verify: lint test-tools test test-integration build test-embed test-scripts test-installer

# security checks application and plugin SDK dependencies against current reports.
security: security-go security-frontend

# security-go checks the backend and Go plugin SDK with the pinned govulncheck tool.
# Each module is scanned from its own directory so SDK-only packages and dependency
# versions are included.
security-go:
	cd backend && go run golang.org/x/vuln/cmd/govulncheck@$$(cat ../.govulncheck-version) ./...
	cd plugin-sdk/go && go run golang.org/x/vuln/cmd/govulncheck@$$(cat ../../.govulncheck-version) ./...

# security-frontend installs and audits frontend and Web SDK production dependencies.
# Frontend findings use the repository exception list; high and critical SDK
# findings fail directly because frontend mitigations do not establish SDK safety.
security-frontend:
	$(PNPM) install --frozen-lockfile
	python3 tools/check_pnpm_audit_exceptions.py --run --exceptions .github/audit-exceptions.yml
	pnpm --dir plugin-sdk/web install --frozen-lockfile
	pnpm --dir plugin-sdk/web audit --prod --audit-level=high

# hooks enables repository pre-push checks for this local checkout.
hooks:
	git config --local core.hooksPath .githooks
