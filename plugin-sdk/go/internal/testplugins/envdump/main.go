package main

import (
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/internal/testplugins/fixture"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

// main.go starts a fixture that reports its own child-process environment.
// Tests inject a synthetic host secret before launch and assert that it is
// absent from the response, without printing real host environment values.

// main serves the environment fixture through the production SDK entry point.
func main() { sdk.Serve(fixture.New("envdump")) }
