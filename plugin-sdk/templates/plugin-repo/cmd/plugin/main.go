package main

import (
	"github.com/example/tr-plugin-checkin/internal/app"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

// main.go starts the daily check-in example as a TokenRouter plugin process.
// The SDK owns handshake, callback connection, health reporting, and shutdown;
// the example supplies only its business handlers and configuration behavior.

// main serves the example plugin through the shared SDK process entry point.
func main() { sdk.Serve(app.New()) }
