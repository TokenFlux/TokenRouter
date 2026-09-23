package provider

import (
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/stretchr/testify/require"
)

func TestDefaultRequestModelIDsForPlatformQoder(t *testing.T) {
	require.Equal(t, qoder.DefaultRequestModelIDs(), DefaultRequestModels(capability.PlatformQoder))
}

func TestAvailableRequestModelsFromAccountsUsesQoderAccountSite(t *testing.T) {
	newAccount := func(id int64, site string) accountcore.Record {
		return accountcore.Record{
			ID:          id,
			Platform:    capability.PlatformQoder,
			Status:      accountcore.StatusActive,
			Schedulable: true,
			Credentials: map[string]any{"site": site},
		}
	}

	cnModels := rejectedModelsForContract([]accountcore.Record{newAccount(1, "cn")}, capability.PlatformQoder)
	require.ElementsMatch(t, qoder.DefaultRequestModelIDsForSite(qoder.SiteCN), cnModels)
	require.NotContains(t, cnModels, "claude-opus-4-6")

	globalModels := rejectedModelsForContract([]accountcore.Record{newAccount(2, "global")}, capability.PlatformQoder)
	require.ElementsMatch(t, qoder.DefaultRequestModelIDsForSite(qoder.SiteGlobal), globalModels)
	require.NotContains(t, globalModels, "minimax-m2.7")

	mixedModels := rejectedModelsForContract([]accountcore.Record{newAccount(3, "global"), newAccount(4, "cn")}, capability.PlatformQoder)
	require.ElementsMatch(t, qoder.DefaultRequestModelIDs(), mixedModels)
}

func TestAvailableRequestModelsFromAccountsFiltersConfiguredQoderModels(t *testing.T) {
	newAccount := func(id int64, site string, credentials map[string]any) accountcore.Record {
		credentials["site"] = site
		return accountcore.Record{
			ID:          id,
			Platform:    capability.PlatformQoder,
			Status:      accountcore.StatusActive,
			Schedulable: true,
			Credentials: credentials,
		}
	}

	cnWhitelist := newAccount(11, "cn", map[string]any{
		"model_whitelist": []any{"claude-opus-4-6", "qwen3.6-flash"},
	})
	cnModels := rejectedModelsForContract([]accountcore.Record{cnWhitelist}, capability.PlatformQoder)
	require.Equal(t, []string{"qwen3.6-flash"}, cnModels)

	cnMappingOverride := newAccount(12, "cn", map[string]any{
		"model_mapping": map[string]any{"claude-opus-4-6": "ultimate"},
	})
	overrideModels := rejectedModelsForContract([]accountcore.Record{cnMappingOverride}, capability.PlatformQoder)
	require.Equal(t, []string{"claude-opus-4-6"}, overrideModels)

	globalWhitelist := newAccount(13, "global", map[string]any{
		"model_whitelist": []any{"claude-opus-4-6", "qwen3.6-flash"},
	})
	mixedModels := rejectedModelsForContract([]accountcore.Record{cnWhitelist, globalWhitelist}, capability.PlatformQoder)
	require.ElementsMatch(t, []string{"claude-opus-4-6", "qwen3.6-flash"}, mixedModels)
}

// rejectedModelsForContract 只装配原生记录与 routing 规则，断言不经过旧账号形状。
func rejectedModelsForContract(values []accountcore.Record, platform string) []string {
	sources := make([]routing.ModelRejectionSource, len(values))
	for i := range values {
		sources[i] = ModelRejectionAccount(&values[i])
	}
	return routing.AvailableModelsForRejection(sources, platform)
}
