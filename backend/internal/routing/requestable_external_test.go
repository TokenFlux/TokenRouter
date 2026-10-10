package routing_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// countedCataloguePolicies 统计每次目录解析取得计费来源的次数。
type countedCataloguePolicies struct {
	*routing.PricingConfigService
	pricingReads int
}

// countedCatalogueRules 记录有限目录和指定型号查询的资格检查次数。
type countedCatalogueRules struct {
	routing.CatalogueRules
	checks *int
}

// TestCatalogueReadsGroupOnce 大目录和限制阶段均使用本次查询的分组快照。
func TestCatalogueReadsGroupOnce(t *testing.T) {
	for _, stage := range []string{routing.BillingModelSourceRequested, routing.BillingModelSourceGroupMapped, routing.BillingModelSourceUpstream} {
		t.Run(stage, func(t *testing.T) {
			ids := []string{"allowed"}
			for i := range 12000 {
				ids = append(ids, fmt.Sprintf("model-%d", i))
			}
			reads := 0
			policies := &countedCataloguePolicies{PricingConfigService: routing.NewPricingConfigService(nil, nil, routing.PricingConfigOptions{ReadGroup: func(context.Context, int64) (*routing.Group, error) {
				reads++
				return &routing.Group{ID: 1, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}, RoutingPolicy: routing.GroupRoutingPolicy{Enabled: true, RestrictModels: true, RestrictionModelSource: stage, AllowedModels: []string{"allowed"}}}, nil
			}})}
			resolver := routing.RequestableResolver{GroupPolicies: policies}
			records := []provider.Record{{Platform: "openai", Type: "apikey", Credentials: map[string]any{"model_whitelist": []string{"allowed"}}}}
			group := int64(1)
			result := resolver.ResolveWithProviders(context.Background(), &group, "", ids, gatewayprovider.CatalogueProviders(records))
			require.Equal(t, []string{"allowed"}, routing.RequestableModelIDs(result.Models))
			require.Equal(t, "allowed", result.Models[0].PricingModel)
			require.Equal(t, 1, reads)
			require.Equal(t, 1, policies.pricingReads)
		})
	}
}

// GetPricingConfigForGroup 记录计费来源的读取次数。
func (p *countedCataloguePolicies) GetPricingConfigForGroup(context.Context, int64) (*routing.PricingConfig, error) {
	p.pricingReads++
	return &routing.PricingConfig{BillingModelSource: routing.BillingModelSourceUpstream}, nil
}

func (r countedCatalogueRules) Supports(ctx context.Context, model string) bool {
	*r.checks++
	return r.CatalogueRules.Supports(ctx, model)
}

func (r countedCatalogueRules) SupportsClientProtocol(model string, source protocol.ProtocolID) bool {
	if rules, ok := r.CatalogueRules.(interface {
		SupportsClientProtocol(string, protocol.ProtocolID) bool
	}); ok {
		return rules.SupportsClientProtocol(model, source)
	}
	return true
}

// TestSelectedCatalogueChecksOnlyTargets 别名目标与请求名称复用一次策略读取，其他候选不做资格检查。
func TestSelectedCatalogueChecksOnlyTargets(t *testing.T) {
	groupID := int64(1)
	reads := 0
	policies := &countedCataloguePolicies{PricingConfigService: routing.NewPricingConfigService(nil, nil, routing.PricingConfigOptions{ReadGroup: func(context.Context, int64) (*routing.Group, error) {
		reads++
		return &routing.Group{ID: groupID, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}, ModelsListConfig: routing.GroupModelsListConfig{Enabled: true, Models: []string{"manual"}}}, nil
	}})}
	resolver := routing.RequestableResolver{GroupPolicies: policies}
	records := []provider.Record{{Platform: "openai", Type: "apikey", Credentials: map[string]any{"model_whitelist": []string{}, "model_mapping": map[string]any{"alias": "target"}}}}
	providers := gatewayprovider.CatalogueProviders(records)
	checks := 0
	providers[0].Rules = countedCatalogueRules{providers[0].Rules, &checks}
	result := resolver.ResolveSelectedWithProviders(context.Background(), &groupID, "", []string{"target", "manual", "not-declared"}, providers)
	require.ElementsMatch(t, []string{"target", "manual"}, routing.RequestableModelIDs(result.Models))
	require.Equal(t, 2, checks)
	require.Equal(t, 1, reads)
	require.Equal(t, 1, policies.pricingReads)
	result = resolver.ResolveWithProviders(context.Background(), &groupID, "", nil, providers)
	require.ElementsMatch(t, []string{"alias", "target", "manual"}, routing.RequestableModelIDs(result.Models))
}

// BenchmarkRequestableConfiguredProviders 测量 65 个提供商和 186 项配置的冷解析，包含规则准备。
func BenchmarkRequestableConfiguredProviders(b *testing.B) {
	whitelist := make([]any, 186)
	mapping := map[string]any{}
	for i := range whitelist {
		model := fmt.Sprintf("probe-model-%03d", i)
		whitelist[i] = model
		mapping[model] = model
	}
	records := make([]provider.Record, 65)
	for i := range records {
		records[i] = provider.Record{ID: int64(i + 1), Platform: "openai", Type: "apikey", Credentials: map[string]any{"model_whitelist": whitelist, "model_mapping": mapping}}
	}
	groupID := int64(1)
	policies := routing.NewPricingConfigService(nil, nil, routing.PricingConfigOptions{ReadGroup: func(context.Context, int64) (*routing.Group, error) {
		return &routing.Group{ID: groupID, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}}, nil
	}})
	resolver := routing.RequestableResolver{GroupPolicies: &countedCataloguePolicies{PricingConfigService: policies}}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result := resolver.ResolveWithProviders(context.Background(), &groupID, "", nil, gatewayprovider.CatalogueProviders(records))
		if len(result.Models) != 186 {
			b.Fatalf("models=%d", len(result.Models))
		}
	}
}
