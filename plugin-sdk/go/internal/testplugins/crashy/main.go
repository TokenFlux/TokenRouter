package main

import (
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/internal/testplugins/fixture"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

// main.go starts the deliberately faulty process used by supervisor tests.
// Faults require an explicit HTTP query parameter, allowing the same executable
// to become healthy before a test requests a panic, exit, stall, or invalid response.

// main serves the crash fixture using the production SDK handshake path.
func main() { sdk.Serve(fixture.New("crashy")) }
