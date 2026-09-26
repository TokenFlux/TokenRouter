package routing

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// policyPriceStore 只提供价格读取，测试中的分组策略由单独端口持有。
type policyPriceStore struct {
	PricingConfigRepository
	configs []PricingConfig
}

func (s *policyPriceStore) ListAll(context.Context) ([]PricingConfig, error) { return s.configs, nil }

func (s *policyPriceStore) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return map[int64]string{1: PlatformOpenAI, 2: PlatformOpenAI}, nil
}

func TestPricingChangesDoNotChangeGroupPolicy(t *testing.T) {
	store := &policyPriceStore{configs: []PricingConfig{{ID: 9, Status: StatusActive, GroupIDs: []int64{1, 2}, BillingModelSource: BillingModelSourceRequested}}}
	groups := map[int64]*Group{}
	for _, id := range []int64{1, 2} {
		groups[id] = &Group{ID: id, Platform: PlatformOpenAI, RoutingPolicy: GroupRoutingPolicy{
			Enabled: true, RestrictModels: true, RestrictionModelSource: BillingModelSourceGroupMapped,
			AllowedModels:  map[string][]string{PlatformOpenAI: {"gpt-allowed"}},
			ModelMapping:   map[string]map[string]string{PlatformOpenAI: {"alias": "gpt-allowed"}},
			FeaturesConfig: map[string]any{"web_search_emulation": map[string]any{PlatformOpenAI: true}},
		}}
	}
	service := NewPricingConfigService(store, nil, PricingConfigOptions{ReadGroup: func(_ context.Context, id int64) (*Group, error) { return groups[id], nil }})
	ctx := context.Background()
	assertPolicy := func(id int64) {
		t.Helper()
		result := service.ResolveGroupMapping(ctx, id, "alias")
		require.Equal(t, "gpt-allowed", result.MappedModel)
		require.Equal(t, BillingModelSourceGroupMapped, result.RestrictionModelSource)
		require.False(t, service.IsModelRestricted(ctx, id, "gpt-allowed"))
		require.True(t, service.IsModelRestricted(ctx, id, "gpt-denied"))
		policy, err := service.GetGroupPolicy(ctx, id)
		require.NoError(t, err)
		require.True(t, policy.IsWebSearchEmulationEnabled(PlatformOpenAI))
	}
	assertPolicy(1)
	store.configs[0].ModelPricing = []ModelPricingEntry{{Platform: PlatformOpenAI, Models: []string{"gpt-denied"}}}
	store.configs[0].BillingModelSource = BillingModelSourceUpstream
	service.InvalidateCache()
	assertPolicy(1)
	store.configs[0].Status = StatusDisabled
	service.InvalidateCache()
	assertPolicy(1)
	store.configs = nil
	service.InvalidateCache()
	assertPolicy(1)
	assertPolicy(2)
	groups[1].RoutingPolicy.ModelMapping[PlatformOpenAI]["alias"] = "changed"
	require.Equal(t, "changed", service.ResolveGroupMapping(ctx, 1, "alias").MappedModel)
	require.Equal(t, "gpt-allowed", service.ResolveGroupMapping(ctx, 2, "alias").MappedModel)
}

func TestGroupPolicyAllowlistAndSnapshot(t *testing.T) {
	policy := GroupRoutingPolicy{Enabled: true, RestrictModels: true, AllowedModels: map[string][]string{PlatformAnthropic: {"claude-sonnet-*"}}, FeaturesConfig: map[string]any{"nested": []any{map[string]any{"enabled": true}}}}
	copy := policy.Clone()
	copy.AllowedModels[PlatformAnthropic][0] = "changed"
	copiedItems, ok := copy.FeaturesConfig["nested"].([]any)
	require.True(t, ok)
	copiedObject, ok := copiedItems[0].(map[string]any)
	require.True(t, ok)
	copiedObject["enabled"] = false
	require.Equal(t, "claude-sonnet-*", policy.AllowedModels[PlatformAnthropic][0])
	originalItems, ok := policy.FeaturesConfig["nested"].([]any)
	require.True(t, ok)
	originalObject, ok := originalItems[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, originalObject["enabled"])
	view := &GroupPolicyView{GroupRoutingPolicy: policy, Platform: PlatformAnthropic}
	require.False(t, view.IsModelRestricted("Claude-Sonnet-4.6"))
	require.True(t, view.IsModelRestricted("claude-opus-4-6"))
	view.Platform = PlatformAntigravity
	require.True(t, view.IsModelRestricted("claude-sonnet-4-6"))
	view.AllowedModels = nil
	require.True(t, view.IsModelRestricted("anything"))
	view.RestrictModels = false
	require.False(t, view.IsModelRestricted("anything"))
}

func TestGroupPolicyReadFailureDoesNotAllowRequests(t *testing.T) {
	service := NewPricingConfigService(&policyPriceStore{}, nil, PricingConfigOptions{ReadGroup: func(context.Context, int64) (*Group, error) { return nil, errors.New("database unavailable") }})
	require.True(t, service.IsModelRestricted(context.Background(), 1, "model"))
	corrupt := DecodeGroupRoutingPolicy([]byte(`{"enabled":`))
	require.True(t, corrupt.Enabled)
	require.True(t, corrupt.RestrictModels)
}
