# The SDK pins generators through go.mod so local builds and CI produce the
# same protocol bindings. Generated files remain checked in for plugin authors.
.PHONY: proto test lint
proto:
	go tool buf format -w
	go tool buf generate

test:
	go test ./...

lint:
	bash ../../tools/golangci-lint.sh run ./...
