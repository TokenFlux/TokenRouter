//go:build unit

package selection

import (
	"context"
	slog "log/slog"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	"github.com/stretchr/testify/require"
)

// --- billingModelForRestriction ---

func TestModelForRestriction_Requested(t *testing.T) {
	t.Parallel()
	got := routing.ModelForRestriction(routing.BillingModelSourceRequested, "claude-sonnet-4-5", "claude-sonnet-4-6")
	require.Equal(t, "claude-sonnet-4-5", got)
}

func TestModelForRestriction_GroupMapped(t *testing.T) {
	t.Parallel()
	got := routing.ModelForRestriction(routing.BillingModelSourceGroupMapped, "claude-sonnet-4-5", "claude-sonnet-4-6")
	require.Equal(t, "claude-sonnet-4-6", got)
}

func TestModelForRestriction_Upstream(t *testing.T) {
	t.Parallel()
	got := routing.ModelForRestriction(routing.BillingModelSourceUpstream, "claude-sonnet-4-5", "claude-sonnet-4-6")
	require.Equal(t, "", got, "upstream should return empty (per-account check needed)")
}

func TestModelForRestriction_Empty(t *testing.T) {
	t.Parallel()
	got := routing.ModelForRestriction("", "claude-sonnet-4-5", "claude-sonnet-4-6")
	require.Equal(t, "claude-sonnet-4-6", got, "empty source defaults to group_mapped")
}

// --- resolveAccountUpstreamModel ---

func TestResolveAccountUpstreamModel_Antigravity(t *testing.T) {
	t.Parallel()
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity}}
	// Antigravity 平台使用 DefaultAntigravityModelMapping
	got := resolveAccountUpstreamModel(context.Background(), account, "claude-sonnet-4-6")
	require.Equal(t, "claude-sonnet-4-6", got)
}

func TestResolveAccountUpstreamModel_Antigravity_Unsupported(t *testing.T) {
	t.Parallel()
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity}}
	got := resolveAccountUpstreamModel(context.Background(), account, "totally-unknown-model")
	require.Equal(t, "", got, "unsupported model should return empty")
}

func TestResolveAccountUpstreamModel_NonAntigravity(t *testing.T) {
	t.Parallel()
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic}}
	got := resolveAccountUpstreamModel(context.Background(), account, "claude-sonnet-4-6")
	require.Equal(t, "claude-sonnet-4-6", got, "no mapping = passthrough")
}

func TestResolveAccountUpstreamModel_AnthropicOAuthAppliesMappingBeforeNormalization(t *testing.T) {
	t.Parallel()
	account := &gatewayprovider.ExecutionAccount{
		Record: accountcore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.AccountTypeOAuth,
			Credentials: map[string]any{
				"model_mapping": map[string]any{"client-alias": "claude-sonnet-4-5"},
			},
		},
	}

	got := resolveAccountUpstreamModel(context.Background(), account, "client-alias")
	require.Equal(t, "claude-sonnet-4-5-20250929", got)
}

func TestResolveAccountUpstreamModel_BedrockUsesRegionalFinalModel(t *testing.T) {
	t.Parallel()
	account := &gatewayprovider.ExecutionAccount{
		Record: accountcore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
			Type: capability.AccountTypeBedrock,
			Credentials: map[string]any{
				"aws_region": "us-east-1",
			},
		},
	}

	got := resolveAccountUpstreamModel(context.Background(), account, "claude-sonnet-4-5")
	require.Equal(t, "us.anthropic.claude-sonnet-4-5-20250929-v1:0", got)
}

func TestResolveAccountUpstreamModel_AntigravityUsesThinkingContext(t *testing.T) {
	t.Parallel()
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity}}
	ctx := requeststate.WithThinkingEnabled(context.Background(), true)

	got := resolveAccountUpstreamModel(ctx, account, "claude-sonnet-4-5")
	require.Equal(t, "claude-sonnet-4-5-thinking", got)
}

func TestIsModelSupportedByAccountWithContext_QoderUsesGroupMappedAccountLayerModel(t *testing.T) {
	t.Parallel()
	ch := routingtestkit.Configuration{
		ID:       1,
		Status:   billing.StatusActive,
		GroupIDs: []int64{10},
		ModelMapping: map[string]map[string]string{
			capability.PlatformQoder: {"my-qoder": "qmodel"},
		},
	}
	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(ch, map[int64]string{10: capability.PlatformQoder}))
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{GroupPolicies: pricingConfigSvc}}, nil)

	ctx := svc.withGroupContext(context.Background(), &routing.Group{
		ID:       10,
		Platform: capability.PlatformQoder,
		Status:   billing.StatusActive,
		Hydrated: true,
	})
	account := &gatewayprovider.ExecutionAccount{
		Record: accountcore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformQoder,
			Credentials: map[string]any{
				"model_mapping":   map[string]any{"qmodel": "ultimate"},
				"model_whitelist": []any{"ultimate"},
			},
		},
	}

	require.True(t, svc.isModelSupportedByAccountWithContext(ctx, account, "my-qoder"),
		"account whitelist should be checked after channel mapping and account mapping")
}

// --- checkGroupModelRestriction ---

func TestCheckGroupModelRestriction_NilGroupID(t *testing.T) {
	t.Parallel()
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads:  Reads{},
		Shared: Shared{GroupPolicies: routingtestkit.NewPricingConfigService(nil, nil, routing.PricingConfigOptions{Warn: slog.Warn, Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation})},
	}, nil)

	require.False(t, svc.checkGroupModelRestriction(context.Background(), nil, "claude-sonnet-4"))
}

func TestCheckGroupModelRestriction_NilPricingConfigService(t *testing.T) {
	t.Parallel()
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{}}, nil)

	gid := int64(10)
	require.False(t, svc.checkGroupModelRestriction(context.Background(), &gid, "claude-sonnet-4"))
}

func TestCheckGroupModelRestriction_EmptyModel(t *testing.T) {
	t.Parallel()
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads:  Reads{},
		Shared: Shared{GroupPolicies: routingtestkit.NewPricingConfigService(nil, nil, routing.PricingConfigOptions{Warn: slog.Warn, Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation})},
	}, nil)

	gid := int64(10)
	require.False(t, svc.checkGroupModelRestriction(context.Background(), &gid, ""))
}

func TestCheckGroupModelRestriction_GroupMapped_Restricted(t *testing.T) {
	t.Parallel()
	// 分组映射 claude-sonnet-4-5 → claude-sonnet-4-6，但定价列表只有 claude-opus-4-6
	ch := routingtestkit.Configuration{
		ID:                 1,
		Status:             billing.StatusActive,
		GroupIDs:           []int64{10},
		RestrictModels:     true,
		BillingModelSource: routing.BillingModelSourceGroupMapped,
		ModelPricing: []routing.ModelPricingEntry{
			{Platform: "anthropic", Models: []string{"claude-opus-4-6"}},
		},
		ModelMapping: map[string]map[string]string{
			"anthropic": {"claude-sonnet-4-5": "claude-sonnet-4-6"},
		},
	}
	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(ch, map[int64]string{10: "anthropic"}))
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{GroupPolicies: pricingConfigSvc}}, nil)

	gid := int64(10)
	require.True(t, svc.checkGroupModelRestriction(context.Background(), &gid, "claude-sonnet-4-5"),
		"mapped model claude-sonnet-4-6 is NOT in pricing → restricted")
}

func TestCheckGroupModelRestriction_GroupMapped_Allowed(t *testing.T) {
	t.Parallel()
	// 分组映射 claude-sonnet-4-5 → claude-sonnet-4-6，定价列表包含 claude-sonnet-4-6
	ch := routingtestkit.Configuration{
		ID:                 1,
		Status:             billing.StatusActive,
		GroupIDs:           []int64{10},
		RestrictModels:     true,
		BillingModelSource: routing.BillingModelSourceGroupMapped,
		ModelPricing: []routing.ModelPricingEntry{
			{Platform: "anthropic", Models: []string{"claude-sonnet-4-6"}},
		},
		ModelMapping: map[string]map[string]string{
			"anthropic": {"claude-sonnet-4-5": "claude-sonnet-4-6"},
		},
	}
	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(ch, map[int64]string{10: "anthropic"}))
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{GroupPolicies: pricingConfigSvc}}, nil)

	gid := int64(10)
	require.False(t, svc.checkGroupModelRestriction(context.Background(), &gid, "claude-sonnet-4-5"),
		"mapped model claude-sonnet-4-6 IS in pricing → allowed")
}

func TestCheckGroupModelRestriction_QoderGroupMappedBasisAllowsConfiguredRouteKey(t *testing.T) {
	t.Parallel()
	price := 1e-6
	ch := routingtestkit.Configuration{
		ID:                 1,
		Status:             billing.StatusActive,
		GroupIDs:           []int64{10},
		RestrictModels:     true,
		BillingModelSource: routing.BillingModelSourceGroupMapped,
		ModelPricing: []routing.ModelPricingEntry{
			{Platform: capability.PlatformQoder, Models: []string{"qmodel"}, BillingMode: routing.BillingModeToken},
			{Platform: capability.PlatformQoder, Models: []string{"qwen3.7-plus"}, BillingMode: routing.BillingModeToken, InputPrice: &price},
		},
		ModelMapping: map[string]map[string]string{
			capability.PlatformQoder: {"qwen3.7-plus": "qmodel"},
		},
	}
	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(ch, map[int64]string{10: capability.PlatformQoder}))
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{GroupPolicies: pricingConfigSvc}}, nil)

	gid := int64(10)
	require.False(t, svc.checkGroupModelRestriction(context.Background(), &gid, "qwen3.7-plus"),
		"Qoder 与其他平台一样按已配置的模型白名单放行，不要求填写单价")
}

func TestCheckGroupModelRestriction_Requested_Restricted(t *testing.T) {
	t.Parallel()
	// billing_model_source=requested，定价列表有 claude-sonnet-4-6 但请求的是 claude-sonnet-4-5
	ch := routingtestkit.Configuration{
		ID:                 1,
		Status:             billing.StatusActive,
		GroupIDs:           []int64{10},
		RestrictModels:     true,
		BillingModelSource: routing.BillingModelSourceRequested,
		ModelPricing: []routing.ModelPricingEntry{
			{Platform: "anthropic", Models: []string{"claude-sonnet-4-6"}},
		},
	}
	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(ch, map[int64]string{10: "anthropic"}))
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{GroupPolicies: pricingConfigSvc}}, nil)

	gid := int64(10)
	require.True(t, svc.checkGroupModelRestriction(context.Background(), &gid, "claude-sonnet-4-5"),
		"requested model claude-sonnet-4-5 is NOT in pricing → restricted")
}

func TestCheckGroupModelRestriction_Requested_Allowed(t *testing.T) {
	t.Parallel()
	ch := routingtestkit.Configuration{
		ID:                 1,
		Status:             billing.StatusActive,
		GroupIDs:           []int64{10},
		RestrictModels:     true,
		BillingModelSource: routing.BillingModelSourceRequested,
		ModelPricing: []routing.ModelPricingEntry{
			{Platform: "anthropic", Models: []string{"claude-sonnet-4-5"}},
		},
	}
	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(ch, map[int64]string{10: "anthropic"}))
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{GroupPolicies: pricingConfigSvc}}, nil)

	gid := int64(10)
	require.False(t, svc.checkGroupModelRestriction(context.Background(), &gid, "claude-sonnet-4-5"),
		"requested model IS in pricing → allowed")
}

func TestCheckGroupModelRestriction_Upstream_SkipsPreCheck(t *testing.T) {
	t.Parallel()
	// upstream 模式：预检查始终跳过（返回 false），需逐账号检查
	ch := routingtestkit.Configuration{
		ID:                 1,
		Status:             billing.StatusActive,
		GroupIDs:           []int64{10},
		RestrictModels:     true,
		BillingModelSource: routing.BillingModelSourceUpstream,
		ModelPricing: []routing.ModelPricingEntry{
			{Platform: "anthropic", Models: []string{"claude-opus-4-6"}},
		},
	}
	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(ch, map[int64]string{10: "anthropic"}))
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{GroupPolicies: pricingConfigSvc}}, nil)

	gid := int64(10)
	require.False(t, svc.checkGroupModelRestriction(context.Background(), &gid, "unknown-model"),
		"upstream mode should skip pre-check (per-account check needed)")
}

func TestCheckGroupModelRestriction_RestrictModelsDisabled(t *testing.T) {
	t.Parallel()
	ch := routingtestkit.Configuration{
		ID:             1,
		Status:         billing.StatusActive,
		GroupIDs:       []int64{10},
		RestrictModels: false, // 未开启模型限制
		ModelPricing: []routing.ModelPricingEntry{
			{Platform: "anthropic", Models: []string{"claude-opus-4-6"}},
		},
	}
	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(ch, map[int64]string{10: "anthropic"}))
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{GroupPolicies: pricingConfigSvc}}, nil)

	gid := int64(10)
	require.False(t, svc.checkGroupModelRestriction(context.Background(), &gid, "any-model"),
		"RestrictModels=false → always allowed")
}

func TestCheckGroupModelRestriction_NoPricingConfig(t *testing.T) {
	t.Parallel()
	// 分组没有配置独立模型策略
	repo := &routingtestkit.PricingConfigRepositoryStub{
		ListAllFn: func(_ context.Context) ([]routingtestkit.Configuration, error) { return nil, nil },
	}
	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(repo)
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{GroupPolicies: pricingConfigSvc}}, nil)

	gid := int64(999)
	require.False(t, svc.checkGroupModelRestriction(context.Background(), &gid, "any-model"),
		"no channel for group → allowed")
}

// --- isUpstreamModelRestrictedByGroup ---

func TestIsUpstreamModelRestrictedByGroup_Restricted(t *testing.T) {
	t.Parallel()
	ch := routingtestkit.Configuration{
		ID:             1,
		Status:         billing.StatusActive,
		GroupIDs:       []int64{10},
		RestrictModels: true,
		ModelPricing: []routing.ModelPricingEntry{
			{Platform: "anthropic", Models: []string{"claude-opus-4-6"}},
		},
	}
	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(ch, map[int64]string{10: "anthropic"}))
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{GroupPolicies: pricingConfigSvc}}, nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.
		// claude-sonnet-4-6 在 DefaultAntigravityModelMapping 中，映射后仍为 claude-sonnet-4-6
		// 但定价列表只有 claude-opus-4-6
		LoadLocation, Platform: capability.PlatformAntigravity}}

	require.True(t, svc.isUpstreamModelRestrictedByGroup(context.Background(), 10, account, "claude-sonnet-4-6"),
		"upstream model claude-sonnet-4-6 NOT in pricing → restricted")
}

func TestIsUpstreamModelRestrictedByGroup_Allowed(t *testing.T) {
	t.Parallel()
	ch := routingtestkit.Configuration{
		ID:             1,
		Status:         billing.StatusActive,
		GroupIDs:       []int64{10},
		RestrictModels: true,
		ModelPricing: []routing.ModelPricingEntry{
			{Platform: "anthropic", Models: []string{"claude-sonnet-4-6"}},
		},
	}
	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(ch, map[int64]string{10: "anthropic"}))
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{GroupPolicies: pricingConfigSvc}}, nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity}}
	require.False(t, svc.isUpstreamModelRestrictedByGroup(context.Background(), 10, account, "claude-sonnet-4-6"),
		"upstream model claude-sonnet-4-6 IS in pricing → allowed")
}

func TestIsUpstreamModelRestrictedByGroup_AppliesGroupMappingBeforeAccountMapping(t *testing.T) {
	t.Parallel()
	price := 1e-6
	ch := routingtestkit.Configuration{
		ID:                 1,
		Status:             billing.StatusActive,
		GroupIDs:           []int64{10},
		RestrictModels:     true,
		BillingModelSource: routing.BillingModelSourceUpstream,
		ModelPricing: []routing.ModelPricingEntry{
			{Platform: capability.PlatformQoder, Models: []string{"ultimate"}, BillingMode: routing.BillingModeToken, InputPrice: &price},
		},
		ModelMapping: map[string]map[string]string{
			capability.PlatformQoder: {"my-qoder": "qmodel"},
		},
	}
	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(ch, map[int64]string{10: capability.PlatformQoder}))
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{GroupPolicies: pricingConfigSvc}}, nil)

	account := &gatewayprovider.ExecutionAccount{
		Record: accountcore.Record{
			LoadLocation: time.LoadLocation, Platform: capability.PlatformQoder,
			Credentials: map[string]any{
				"model_mapping": map[string]any{"qmodel": "ultimate"},
			},
		},
	}

	require.False(t, svc.isUpstreamModelRestrictedByGroup(context.Background(), 10, account, "my-qoder"),
		"upstream restriction should check the final model after channel mapping and account mapping")
}

func TestIsUpstreamModelRestrictedByGroup_QoderUpstreamBasisAllowsConfiguredUpstream(t *testing.T) {
	t.Parallel()
	price := 1e-6
	ch := routingtestkit.Configuration{
		ID:                 1,
		Status:             billing.StatusActive,
		GroupIDs:           []int64{10},
		RestrictModels:     true,
		BillingModelSource: routing.BillingModelSourceUpstream,
		ModelPricing: []routing.ModelPricingEntry{
			{Platform: capability.PlatformQoder, Models: []string{"qmodel"}, BillingMode: routing.BillingModeToken},
			{Platform: capability.PlatformQoder, Models: []string{"qwen3.7-plus"}, BillingMode: routing.BillingModeToken, InputPrice: &price},
		},
		ModelMapping: map[string]map[string]string{
			capability.PlatformQoder: {"qwen3.7-plus": "qmodel"},
		},
	}
	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(ch, map[int64]string{10: capability.PlatformQoder}))
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{GroupPolicies: pricingConfigSvc}}, nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformQoder}}

	require.False(t, svc.isUpstreamModelRestrictedByGroup(context.Background(), 10, account, "qwen3.7-plus"),
		"已配置的上游模型属于白名单，价格为空不影响放行")
}

func TestIsUpstreamModelRestrictedByGroup_UnsupportedModel(t *testing.T) {
	t.Parallel()
	ch := routingtestkit.Configuration{
		ID:             1,
		Status:         billing.StatusActive,
		GroupIDs:       []int64{10},
		RestrictModels: true,
		ModelPricing: []routing.ModelPricingEntry{
			{Platform: "anthropic", Models: []string{"claude-opus-4-6"}},
		},
	}
	pricingConfigSvc := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(ch, map[int64]string{10: "anthropic"}))
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{GroupPolicies: pricingConfigSvc}}, nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.
		// totally-unknown-model 不在 DefaultAntigravityModelMapping 中 → 映射结果为空
		LoadLocation, Platform: capability.PlatformAntigravity}}

	require.False(t, svc.isUpstreamModelRestrictedByGroup(context.Background(), 10, account, "totally-unknown-model"),
		"unmappable model → upstream model empty → not restricted (account filter handles this)")
}
