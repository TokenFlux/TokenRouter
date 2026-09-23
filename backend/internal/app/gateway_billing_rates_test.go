package app

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

// 原默认与显式TTL断言归配置装配所有者，避免通过旧网关读取配置。
func TestGatewayGroupRateCacheTTL(t *testing.T) {
	t.Run("resolve_user_group_rate_cache_ttl", func(t *testing.T) {
		require.Equal(t, billing.DefaultGroupRateCacheTTL, gatewayGroupRateCacheTTL(nil))
		cfg := &config.Config{Gateway: config.GatewayConfig{UserGroupRateCacheTTLSeconds: 45}}
		require.Equal(t, 45*time.Second, gatewayGroupRateCacheTTL(cfg))
	})
}
