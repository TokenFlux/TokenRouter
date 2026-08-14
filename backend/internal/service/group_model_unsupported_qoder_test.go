package service

import (
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/pkg/qoder"
	"github.com/stretchr/testify/require"
)

func TestDefaultRequestModelIDsForPlatformQoder(t *testing.T) {
	require.Equal(t, qoder.DefaultRequestModelIDs(), defaultRequestModelIDsForPlatform(PlatformQoder))
}

func TestAvailableRequestModelsFromAccountsUsesQoderAccountSite(t *testing.T) {
	newAccount := func(id int64, site string) Account {
		return Account{
			ID:          id,
			Platform:    PlatformQoder,
			Status:      StatusActive,
			Schedulable: true,
			Credentials: map[string]any{"site": site},
		}
	}

	cnModels := availableRequestModelsFromAccounts([]Account{newAccount(1, "cn")}, PlatformQoder)
	require.ElementsMatch(t, qoder.DefaultRequestModelIDsForSite(qoder.SiteCN), cnModels)
	require.NotContains(t, cnModels, "claude-opus-4-6")

	globalModels := availableRequestModelsFromAccounts([]Account{newAccount(2, "global")}, PlatformQoder)
	require.ElementsMatch(t, qoder.DefaultRequestModelIDsForSite(qoder.SiteGlobal), globalModels)
	require.NotContains(t, globalModels, "minimax-m2.7")

	mixedModels := availableRequestModelsFromAccounts([]Account{newAccount(3, "global"), newAccount(4, "cn")}, PlatformQoder)
	require.ElementsMatch(t, qoder.DefaultRequestModelIDs(), mixedModels)
}

func TestAvailableRequestModelsFromAccountsFiltersConfiguredQoderModels(t *testing.T) {
	newAccount := func(id int64, site string, credentials map[string]any) Account {
		credentials["site"] = site
		return Account{
			ID:          id,
			Platform:    PlatformQoder,
			Status:      StatusActive,
			Schedulable: true,
			Credentials: credentials,
		}
	}

	cnWhitelist := newAccount(11, "cn", map[string]any{
		"model_whitelist": []any{"claude-opus-4-6", "qwen3.6-flash"},
	})
	cnModels := availableRequestModelsFromAccounts([]Account{cnWhitelist}, PlatformQoder)
	require.Equal(t, []string{"qwen3.6-flash"}, cnModels)

	cnMappingOverride := newAccount(12, "cn", map[string]any{
		"model_mapping": map[string]any{"claude-opus-4-6": "ultimate"},
	})
	overrideModels := availableRequestModelsFromAccounts([]Account{cnMappingOverride}, PlatformQoder)
	require.Equal(t, []string{"claude-opus-4-6"}, overrideModels)

	globalWhitelist := newAccount(13, "global", map[string]any{
		"model_whitelist": []any{"claude-opus-4-6", "qwen3.6-flash"},
	})
	mixedModels := availableRequestModelsFromAccounts([]Account{cnWhitelist, globalWhitelist}, PlatformQoder)
	require.ElementsMatch(t, []string{"claude-opus-4-6", "qwen3.6-flash"}, mixedModels)
}
