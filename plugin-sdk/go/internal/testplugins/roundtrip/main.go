package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

// main.go serves a minimal process fixture for the SDK transport test.
// Requests echo their converted fields and perform a host wallet callback,
// while configuration and shutdown expose observable lifecycle effects without
// implementing a publishable plugin or duplicating check-in business rules.

// plugin retains the initialized environment used by transport test requests.
type plugin struct {
	env *sdk.Env // initialized process environment
}

// main starts the fixture through the public plugin SDK entrypoint.
func main() {
	sdk.Serve(&plugin{})
}

// Init retains the broker environment supplied by the SDK initialization call.
func (p *plugin) Init(_ context.Context, env *sdk.Env) error {
	p.env = env
	return nil
}

// Handler exposes the echo endpoint used by the real-process test.
func (p *plugin) Handler() http.Handler {
	return http.HandlerFunc(p.serveHTTP)
}

// serveHTTP echoes converted request fields after issuing a host wallet callback.
// The test supplies the caller, amount, and idempotency key. Errors produce HTTP
// failures so the parent can distinguish conversion failures from callback failures.
func (p *plugin) serveHTTP(w http.ResponseWriter, r *http.Request) {
	caller, ok := sdk.CallerFrom(r.Context())
	if !ok {
		http.Error(w, "missing test caller", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	wallet, err := p.env.Host.Wallet.Credit(r.Context(), sdk.CreditRequest{
		UserID:         caller.UserID,
		Amount:         p.env.Config.String("amount"),
		IdempotencyKey: r.Header.Get("X-Test-Key"),
		Reason:         "SDK transport fixture",
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("X-Test-Reply", "roundtrip")
	sdk.WriteJSON(w, http.StatusOK, map[string]string{
		"method":   r.Method,
		"path":     r.URL.Path,
		"query":    r.URL.RawQuery,
		"body":     string(body),
		"user":     strconv.FormatInt(caller.UserID, 10),
		"username": caller.Username,
		"balance":  wallet.BalanceAfter,
		"amount":   p.env.Config.String("amount"),
		"plugin":   p.env.PluginID,
	})
}

// ConfigChanged verifies that the live handle already exposes the callback snapshot.
// A mismatch returns an error to the test instead of accepting a callback that
// would leave subsequent requests reading the previous configuration.
func (p *plugin) ConfigChanged(_ context.Context, next sdk.Config) error {
	if p.env.Config.String("amount") != next.String("amount") {
		return fmt.Errorf("configuration handle was not updated")
	}
	return nil
}

// Shutdown records that the author callback ran before the test kills the process.
// The marker stays inside the test-owned state directory and lets the parent
// distinguish SDK cleanup from merely terminating a child process.
func (p *plugin) Shutdown(context.Context) error {
	return os.WriteFile(filepath.Join(p.env.StateDir, "shutdown"), []byte("complete"), 0o600)
}
