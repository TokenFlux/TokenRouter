//go:build unit

package selection

import (
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestGrokModelQuotaBlock_FiltersOnlyNamedModel(t *testing.T) {
	id := time.Now().UnixNano()%1_000_000 + 5000
	accountcore.MarkGrokModelQuotaBlock(id, "grok-4.5", time.Now().Add(time.Hour))
	now := time.Now()
	require.True(t, accountcore.IsGrokModelQuotaBlocked(id, "grok-4.5", now))
	require.False(t, accountcore.IsGrokModelQuotaBlocked(id, "grok-4.3", now))

	accounts := []gatewayprovider.ExecutionAccount{
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: id, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}},
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: id + 1, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}},
	}
	filtered := filterGrokModelQuotaBlockedAccounts(accounts, "grok-4.5", now)
	require.Len(t, filtered, 1)
	require.Equal(t, id+1, filtered[0].Record.ID)
}

func TestGrokModelQuotaBlockFiltersMappedUpstreamModel(t *testing.T) {
	id := time.Now().UnixNano()%1_000_000 + 7000
	accountcore.MarkGrokModelQuotaBlock(id, "grok-4.5", time.Now().Add(time.Hour))
	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: id,
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-*": "grok-4.5"},
		}},
	}

	require.Empty(t, filterGrokModelQuotaBlockedAccounts([]gatewayprovider.ExecutionAccount{account}, "gpt-5", time.Now()))
}
