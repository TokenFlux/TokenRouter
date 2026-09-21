//go:build unit

package provider_test

import (
	"context"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	qoder "github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/stretchr/testify/require"
)

func TestCompositeTokenCacheInvalidator_QoderCosy(t *testing.T) {
	cache := &qoderInvalidationCache{}
	provider := accountprovider.NewQoderTokenProvider(qoder.SessionBuilder{})
	provider.Core.Sessions[42] = accountcore.QoderSessionCacheEntry[*qoder.SessionContext]{CredentialsHash: "old"}
	invalidator := accountcore.NewCompositeTokenCacheInvalidator(cache, provider, nil)
	account := &accountcore.Record{
		ID:       42,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
	}

	err := invalidator.InvalidateToken(context.Background(), account)

	require.NoError(t, err)
	require.Contains(t, cache.deletedKeys, "qoder:account:42")
	provider.Core.Mu.Lock()
	_, cached := provider.Core.Sessions[42]
	provider.Core.Mu.Unlock()
	require.False(t, cached, "qoder provider session cache should be invalidated too")
}

// qoderInvalidationCache 仅记录本契约实际使用的删除端口。
type qoderInvalidationCache struct {
	accountcore.AccessTokenCache
	deletedKeys []string
}

func (c *qoderInvalidationCache) DeleteAccessToken(_ context.Context, key string) error {
	c.deletedKeys = append(c.deletedKeys, key)
	return nil
}

func TestCompositeTokenCacheInvalidator_QoderCosyInvalidatesProviderWithoutExternalCache(t *testing.T) {
	provider := accountprovider.NewQoderTokenProvider(qoder.SessionBuilder{})
	provider.Core.Sessions[43] = accountcore.QoderSessionCacheEntry[*qoder.SessionContext]{CredentialsHash: "old"}
	invalidator := accountcore.NewCompositeTokenCacheInvalidator(nil, provider, nil)
	account := &accountcore.Record{
		ID:       43,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
	}

	err := invalidator.InvalidateToken(context.Background(), account)

	require.NoError(t, err)
	provider.Core.Mu.Lock()
	_, cached := provider.Core.Sessions[43]
	provider.Core.Mu.Unlock()
	require.False(t, cached)
}
