package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

// main.go serves the installed-package lifecycle fixture used by plugin integration
// tests. Initialization accepts only the synthetic secret supplied by those tests,
// proving that encrypted database configuration reaches the native process as
// plaintext without reflecting that plaintext into HTTP responses or logs.

// installationPlugin verifies configuration before accepting ordinary health and HTTP calls.
type installationPlugin struct{}

// main starts the real SDK handshake and lifecycle services for the fixture.
func main() { sdk.Serve(&installationPlugin{}) }

// Init requires the synthetic configured secret before the plugin becomes available.
// The test knows the expected value in advance. A ciphertext or absent setting
// fails initialization without including the supplied value in its error message.
func (*installationPlugin) Init(_ context.Context, environment *sdk.Env) error {
	if environment.Config.Secret("credential") != "fixture-secret" {
		return fmt.Errorf("fixture credential did not reach initialization")
	}
	return nil
}

// Handler returns a stable readiness payload without exposing initialization secrets.
func (*installationPlugin) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sdk.WriteJSON(w, http.StatusOK, map[string]bool{"ready": true})
	})
}
