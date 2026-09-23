package app

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

// 原配置默认值和显式覆盖断言随配置投影迁入组合根。
func TestGatewayHotpathHelpers_CacheTTLAndStickyContext(t *testing.T) {
	t.Run("resolve_models_list_cache_ttl", func(t *testing.T) {
		require.Equal(t, 15*time.Second, resolveModelsListCacheTTL(nil))

		cfg := &config.Config{
			Gateway: config.GatewayConfig{
				ModelsListCacheTTLSeconds: 20,
			},
		}
		require.Equal(t, 20*time.Second, resolveModelsListCacheTTL(cfg))
	})
}
