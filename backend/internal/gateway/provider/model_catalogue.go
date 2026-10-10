package provider

import (
	"context"
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

type catalogueRules struct{ policy ModelPolicy }

// SupportsClientProtocol 不把专用 Embeddings、Images 模型展示为普通对话候选。
// 提供商模型别名先按同一规则展开，再判断已有适配器支持的调用形状。
func (v catalogueRules) SupportsClientProtocol(model string, source capability.ProtocolID) bool {
	model = v.policy.Mapped(model)
	embedding := strings.HasPrefix(strings.ToLower(model), "text-embedding-")
	if embedding || source == capability.ProtocolEmbeddings {
		return embedding && source == capability.ProtocolEmbeddings
	}
	image := upstream.IsGPTImageGenerationModel(model) || upstream.IsGrokImageGenerationModel(model)
	if source == capability.ProtocolImagesGenerations || source == capability.ProtocolImagesEdits {
		return image
	}
	if image && source != capability.ProtocolOpenAIResponses && source != capability.ProtocolResponsesWebSocket {
		return false
	}
	if source == capability.ProtocolImageBatches {
		return upstream.IsGeminiImageGenerationModel(model)
	}
	return true
}

func (v catalogueRules) ConfiguredModels() []string {
	return v.policy.configuredModels()
}

func (v catalogueRules) Mapping() map[string]string {
	return v.policy.models.Mapping()
}

func (v catalogueRules) Supports(ctx context.Context, model string) bool {
	return v.policy.Supports(ctx, model)
}

func (v catalogueRules) UpstreamModels(ctx context.Context, model string) []string {
	return v.policy.ListingModels(ctx, model)
}

// CatalogueRules 为目录调用方准备可复用的模型规则。
func (p ModelPolicy) CatalogueRules() routing.CatalogueRules {
	return catalogueRules{policy: p.Prepared()}
}

// CatalogueProvider 准备提供商元数据和一次目录查询的模型规则。
func CatalogueProvider(value *provider.Record, route requeststate.AttemptRoute) routing.CatalogueProvider {
	policy := (ModelPolicy{Record: value, Route: route}).Prepared()
	snapshot := policy.CandidateSnapshot()
	groups := make([]int64, len(value.ProviderGroups))
	for i, g := range value.ProviderGroups {
		groups[i] = g.GroupID
	}
	return routing.CatalogueProvider{ProviderSnapshot: snapshot, GroupIDs: slices.Clone(value.GroupIDs), ProviderGroupIDs: groups, Passthrough: value.IsOpenAIPassthroughEnabled(), Rules: catalogueRules{policy}}
}

func CatalogueProviders(values []provider.Record) []routing.CatalogueProvider {
	if values == nil {
		return nil
	}
	out := make([]routing.CatalogueProvider, len(values))
	for i := range values {
		out[i] = CatalogueProvider(&values[i], requeststate.AttemptRoute{})
	}
	return out
}
