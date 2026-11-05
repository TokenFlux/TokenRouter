package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
)

// Rate-cache construction needs a duration even when application configuration
// is absent. The fixture compares that missing configuration with an explicit
// positive seconds value, checking the billing default and the conversion used
// by gateway completion resolvers.

// TestGatewayGroupRateCacheTTL passes nil configuration and an explicit
// forty-five-second setting. The helper must return the billing default for the
// former and a matching duration for the latter.
func TestGatewayGroupRateCacheTTL(t *testing.T) {
	t.Run("resolve_user_group_rate_cache_ttl", func(t *testing.T) {
		require.Equal(t, billing.DefaultGroupRateCacheTTL, gatewayGroupRateCacheTTL(nil))
		cfg := &config.Config{Billing: config.BillingConfig{GroupRateCacheTTLSeconds: 45}}
		require.Equal(t, 45*time.Second, gatewayGroupRateCacheTTL(cfg))
	})
}
