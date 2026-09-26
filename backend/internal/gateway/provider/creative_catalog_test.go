package provider

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
)

type creativeCatalogRows struct{ values []creative.CatalogAccount }

func (s creativeCatalogRows) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]creative.CatalogAccount, error) {
	return s.values, nil
}

// 生产目录使用真实上游模型规则，透传账号不能靠未执行的账号映射通过白名单。
func TestCreativeCatalogUsesExecutionModelPolicy(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		value := &account.Record{
			Platform: creative.PlatformOpenAI, Type: "apikey", Status: "active", Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{"gpt-image-1": "gpt-image-2"}},
			Extra:       map[string]any{"openai_passthrough": passthrough},
		}
		public := creative.Public{AccountRepo: creativeCatalogRows{[]creative.CatalogAccount{CreativeCatalogAccount(value)}}}
		group := &creative.GroupView{ID: 12, Platform: creative.PlatformOpenAI, RoutingPolicy: routing.GroupRoutingPolicy{
			Enabled: true, RestrictModels: true, RestrictionModelSource: routing.BillingModelSourceUpstream,
			AllowedModels: map[string][]string{creative.PlatformOpenAI: {"gpt-image-2"}},
		}}
		models, err := public.CreativeModelsForGroup(context.Background(), group)
		require.NoError(t, err)
		if passthrough {
			require.NotContains(t, models, "gpt-image-1")
		} else {
			require.Equal(t, "gpt-image-2", models["gpt-image-1"])
		}
	}
}
