package app

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// creativeExecutionGroupProbe 只提供原生分组读取，验证装配不提前取得账号或启动尝试。
type creativeExecutionGroupProbe struct{ reads int }

func (p *creativeExecutionGroupProbe) GetByIDLite(context.Context, int64) (*routing.Group, error) {
	p.reads++
	return &routing.Group{ID: 12, Platform: "gemini", ResponsesImagePolicy: "inherit"}, nil
}

func TestCreativeExecutorNativeAssemblyPreservesPrepareReads(t *testing.T) {
	groups := &creativeExecutionGroupProbe{}
	cfg := &config.Config{}
	cfg.Creative.ExecuteTimeoutSeconds = 17
	executor := provideCreativeExecutor(cfg, groups, nil, nil, nil)
	require.Zero(t, groups.reads)
	require.Equal(t, 17*time.Second, executor.Timeout)
	_, err := executor.Prepare(context.Background(), creative.CreativeRun{GroupID: 12, Model: "gemini-3.1-flash-image", Operation: creative.CreativeOperationGenerate})
	require.ErrorContains(t, err, "creative gateway service is not configured")
	require.Equal(t, 2, groups.reads, "保持平台读取与协议投影两次原读取时点")
	require.Equal(t, 5*time.Minute, provideCreativeExecutor(nil, nil, nil, nil, nil).Timeout)
}

type creativePolicyGroups struct{ group *routing.Group }

func (s creativePolicyGroups) GetByID(context.Context, int64) (*routing.Group, error) {
	return routing.CloneGroup(s.group), nil
}

func (s creativePolicyGroups) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	return s.GetByID(ctx, id)
}

type creativePolicyAccounts struct {
	selection.Accounts
	value *gatewayprovider.ExecutionAccount
}

func (s creativePolicyAccounts) GetByID(context.Context, int64) (*gatewayprovider.ExecutionAccount, error) {
	return s.value, nil
}

func (s creativePolicyAccounts) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]gatewayprovider.ExecutionAccount, error) {
	return []gatewayprovider.ExecutionAccount{*s.value}, nil
}

// 价格存储为空，验证分组策略不依赖价格配置关联。
type creativeNoPrices struct {
	routing.PricingConfigRepository
}

func (creativeNoPrices) ListAll(context.Context) ([]routing.PricingConfig, error) {
	return nil, nil
}

func (creativeNoPrices) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return nil, nil
}

func TestCreativeExecutorForwardsModelAllowedByGroupScheduler(t *testing.T) {
	ctx := context.Background()
	group := &routing.Group{ID: 12, Platform: creative.PlatformOpenAI, Status: "active", RoutingPolicy: routing.GroupRoutingPolicy{
		Enabled: true, RestrictModels: true, RestrictionModelSource: routing.BillingModelSourceUpstream,
		ModelMapping:  map[string]map[string]string{creative.PlatformOpenAI: {"gpt-image-1": "gpt-image-2"}},
		AllowedModels: map[string][]string{creative.PlatformOpenAI: {"gpt-image-2"}},
	}}
	groups := creativePolicyGroups{group}
	policies := routing.NewPricingConfigService(creativeNoPrices{}, nil, routing.PricingConfigOptions{ReadGroup: groups.GetByIDLite})
	accounts := creativePolicyAccounts{value: gatewayprovider.NewExecutionAccount(&account.Record{
		ID: 55, Platform: creative.PlatformOpenAI, Type: "apikey", Status: "active", Schedulable: true,
		GroupIDs: []int64{group.ID}, Credentials: map[string]any{"model_whitelist": []string{"gpt-image-1", "gpt-image-2"}},
	})}
	choices := selection.NewCompatible(selection.CompatibleDependencies{
		Reads: selection.Reads{Accounts: accounts, Groups: groups}, Shared: selection.Shared{GroupPolicies: policies},
	}, selection.DefaultOptions())
	executor := provideCreativeExecutor(nil, groups, &gatewayprovider.CreativeTargets{}, nil, choices)
	run := creative.CreativeRun{GroupID: group.ID, Model: "gpt-image-1", Operation: creative.CreativeOperationGenerate, ImageSize: "1K"}
	prepared, err := executor.Prepare(ctx, run)
	require.NoError(t, err)
	t.Cleanup(prepared.ReleaseFunc)
	require.Equal(t, "gpt-image-2", prepared.UpstreamModel)
	body, _, err := creativeprovider.BuildCreativeOpenAIRequestBody(run, creative.CreativeRunPayload{Prompt: "cat"}, prepared.UpstreamModel)
	require.NoError(t, err)
	require.Equal(t, "gpt-image-2", gjson.GetBytes(body, "model").String())

	// 目录投影与执行投影均深拷贝策略，不能反向改变持久化分组的数据。
	view := creativeGroupView(group)
	executionGroup, err := executor.Group(ctx, group.ID)
	require.NoError(t, err)
	view.RoutingPolicy.ModelMapping[creative.PlatformOpenAI]["gpt-image-1"] = "changed"
	executionGroup.RoutingPolicy.AllowedModels[creative.PlatformOpenAI][0] = "changed"
	require.Equal(t, "gpt-image-2", group.RoutingPolicy.ModelMapping[creative.PlatformOpenAI]["gpt-image-1"])
	require.Equal(t, []string{"gpt-image-2"}, group.RoutingPolicy.AllowedModels[creative.PlatformOpenAI])
}
