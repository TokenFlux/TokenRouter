package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/config"
)

// Proxy exit probes can query configured URLs even when no proxy address is
// supplied. A local server returns one IP result, and the test checks both the
// parsed address and the number of upstream calls.

// TestNewProxyExitInfoProberUsesConfiguredTargets configures a local ipify-
// style probe endpoint and supplies an empty proxy. It checks the parsed exit
// IP and confirms exactly one call reaches the configured server.
func TestNewProxyExitInfoProberUsesConfiguredTargets(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"ip":"203.0.113.42"}`))
	}))
	defer server.Close()
	prober := provideEgressProbe(&config.Config{Egress: config.EgressConfig{ProxyProbe: config.EgressProxyProbeConfig{URLs: []config.ProbeURLConfig{{URL: server.URL, Parser: "ipify"}}}}})
	result, _, err := prober.ProbeProxy(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, "203.0.113.42", result.IP)
	require.Equal(t, 1, calls)
}
