package app

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

// 静态配置只在组合根投影，编译结果保留 nil、默认、显式增删及输入隔离。
func TestResponseHeaderFilterConfigurationProjection(t *testing.T) {
	require.Nil(t, provideResponseHeaderFilter(nil))
	cfg := &config.Config{}
	cfg.Security.ResponseHeaders.AdditionalAllowed = []string{"X-Trace-Custom"}
	cfg.Security.ResponseHeaders.ForceRemove = []string{"X-Request-Id"}
	disabled := provideResponseHeaderFilter(cfg)
	require.True(t, disabled.Allows("X-Request-Id"))
	require.False(t, disabled.Allows("X-Trace-Custom"))
	cfg.Security.ResponseHeaders.Enabled = true
	enabled := provideResponseHeaderFilter(cfg)
	require.False(t, enabled.Allows("X-Request-Id"))
	require.True(t, enabled.Allows("X-Trace-Custom"))
	require.False(t, enabled.Allows("Connection"))
	cfg.Security.ResponseHeaders.AdditionalAllowed[0] = "X-Replaced"
	cfg.Security.ResponseHeaders.ForceRemove[0] = "Content-Type"
	require.True(t, enabled.Allows("X-Trace-Custom"))
	require.True(t, enabled.Allows("Content-Type"))
	require.False(t, enabled.Allows("X-Replaced"))
}
