package app

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/config"
)

// Response-header filtering compiles configured allow and remove rules for
// upstream output. The tests check disabled defaults, enabled connection-header
// removal, isolation from later configuration-slice changes, and the response-
// read limit selected during OpenAI output assembly.

// TestResponseHeaderFilterConfigurationProjection compiles disabled and enabled
// header options from mutable configuration slices. It checks default and
// forced rules, removes Connection, and confirms later input mutations do not
// alter the compiled filter.
func TestResponseHeaderFilterConfigurationProjection(t *testing.T) {
	require.Nil(t, provideResponseHeaderFilter(nil))
	cfg := &config.Config{}
	cfg.Gateway.ResponseHeaders.AdditionalAllowed = []string{"X-Trace-Custom"}
	cfg.Gateway.ResponseHeaders.ForceRemove = []string{"X-Request-Id"}
	disabled := provideResponseHeaderFilter(cfg)
	require.True(t, disabled.Allows("X-Request-Id"))
	require.False(t, disabled.Allows("X-Trace-Custom"))
	cfg.Gateway.ResponseHeaders.Enabled = true
	enabled := provideResponseHeaderFilter(cfg)
	require.False(t, enabled.Allows("X-Request-Id"))
	require.True(t, enabled.Allows("X-Trace-Custom"))
	require.False(t, enabled.Allows("Connection"))
	cfg.Gateway.ResponseHeaders.AdditionalAllowed[0] = "X-Replaced"
	cfg.Gateway.ResponseHeaders.ForceRemove[0] = "Content-Type"
	require.True(t, enabled.Allows("X-Trace-Custom"))
	require.True(t, enabled.Allows("Content-Type"))
	require.False(t, enabled.Allows("X-Replaced"))
}

// TestResolveUpstreamResponseReadLimit assembles OpenAI output with missing and
// positive configuration limits. The output options must use the default
// response-read bytes in the first case and the supplied value in the second.
func TestResolveUpstreamResponseReadLimit(t *testing.T) {
	t.Run("use default when config missing", func(t *testing.T) {
		require.Equal(t, config.DefaultUpstreamResponseReadMaxBytes, provideOpenAIResponseOutput(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil).Options.ReadLimit)
	})

	t.Run("use configured value", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Gateway.UpstreamResponseMaxBytes = 1234
		require.Equal(t, int64(1234), provideOpenAIResponseOutput(cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil).Options.ReadLimit)
	})
}
