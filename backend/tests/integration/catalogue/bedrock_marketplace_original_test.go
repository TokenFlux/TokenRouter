//go:build unit

package catalogue_test

import (
	"context"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 原地域模型与市场一致性断言使用相同的原生目录和报价实现。
func newBedrockRoutingTestAccount(id int64, region string, forceGlobal bool) accountcore.Record {
	account := accountcore.Record{
		ID: id, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeBedrock,
		Status: billing.StatusActive, Schedulable: true, Concurrency: 5, Priority: int(id),
		Credentials: map[string]any{
			"aws_region": region, "auth_mode": "sigv4",
			"aws_access_key_id": "test-akid", "aws_secret_access_key": "test-secret",
		},
	}
	if forceGlobal {
		account.Credentials["aws_force_global"] = "true"
	}
	return account
}

type bedrockMarketplaceGroups struct {
	routing.GroupRepository
	groups []routing.Group
}

func (s *bedrockMarketplaceGroups) ListActive(context.Context) ([]routing.Group, error) {
	return s.groups, nil
}
func TestBedrockRegionRouting_MarketplaceUsesSharedResolution(t *testing.T) {
	groupID := int64(5201)
	for _, forceGlobal := range []bool{false, true} {
		name := "地域不可用"
		if forceGlobal {
			name = "全局可用"
		}
		t.Run(name, func(t *testing.T) {
			account := newBedrockRoutingTestAccount(1, "ap-northeast-1", forceGlobal)
			account.GroupIDs = []int64{groupID}
			account.AccountGroups = []accountcore.GroupMembership{{AccountID: 1, GroupID: groupID}}
			account.Credentials["model_mapping"] = map[string]any{"client-alias": "claude-sonnet-5"}
			repo := &modelsListAccountRepoStub{all: []accountcore.Record{account}, byGroup: map[int64][]accountcore.Record{groupID: {account}}}
			gateway := newCatalogueFixture(repo, nil, nil)
			models := gateway.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformAnthropic)
			if forceGlobal {
				require.Contains(t, routing.RequestableModelIDs(models.Models), "client-alias")
			} else {
				require.NotContains(t, routing.RequestableModelIDs(models.Models), "client-alias")
			}
			marketplace := newCatalogueMarketplace(
				&bedrockMarketplaceGroups{groups: []routing.Group{{ID: groupID, Name: "Bedrock", Platform: capability.PlatformAnthropic, Status: billing.StatusActive, RateMultiplier: 1, ActiveAccountCount: 1}}},
				gateway, billingtestkit.Calculator(0, nil, nil),
			)
			groups, err := marketplace.ListPublic(context.Background())
			require.NoError(t, err)
			require.Len(t, groups, 1)
			var publicModels []string
			for _, model := range groups[0].Models {
				publicModels = append(publicModels, model.ID)
			}
			// 其它可请求型号仍正常展示；只移除本地区不可用的型号及其别名。
			require.ElementsMatch(t, routing.RequestableModelIDs(models.Models), publicModels)
			if forceGlobal {
				require.Contains(t, publicModels, "client-alias")
			} else {
				require.NotContains(t, publicModels, "client-alias")
				require.NotContains(t, publicModels, "claude-sonnet-5")
			}
		})
	}
}
