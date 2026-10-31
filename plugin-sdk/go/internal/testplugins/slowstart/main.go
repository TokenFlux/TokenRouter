package main

import (
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/internal/testplugins/fixture"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

// main.go starts a fixture whose initialization waits for host cancellation.
// Supervisor tests use it to verify startup deadlines and cleanup without
// relying on a fixed delay or allowing a failed generation to accept traffic.

// main serves the blocking-start fixture through the production SDK entry point.
func main() { sdk.Serve(fixture.New("slowstart")) }
