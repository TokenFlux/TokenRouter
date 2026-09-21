package modelidentity

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// CandidatesFactory 为一次目录查询固化动态 Grok 模型身份，价格回退仍由纯定价负责。
func CandidatesFactory() func(string) []string {
	options := grok.RuntimeModelMappingOptions()
	return func(model string) []string {
		return pricing.BuildModelLookupCandidates(model, func(candidate string) (string, bool) {
			if !grok.IsGrokTextResponsesModelID(candidate) {
				return "", false
			}
			return grok.ResolveGrokTextResponsesModelID(candidate, options.DefaultText), true
		})
	}
}

// LookupCandidates 保留每次查询只读取一次平台快照的边界。
func LookupCandidates(model string) []string {
	return CandidatesFactory()(model)
}

// Identity 投影一次模型查询所需的候选与规范名称。
func Identity(model string) billing.ModelIdentity {
	return billing.ModelIdentity{Candidates: LookupCandidates(model), NormalizedOpenAI: NormalizeOpenAI(model)}
}

// PricingPolicy 把平台型号身份投影给唯一的纯定价规则。
func PricingPolicy(model string) pricing.ModelPolicy {
	normalized := NormalizeOpenAI(model)
	return pricing.ModelPolicy{
		NativeGrokModel:       strings.ToLower(strings.TrimSpace(grok.StripGrokProviderPrefix(model))),
		NormalizedOpenAIModel: normalized,
		IsGPT56:               IsGPT56(normalized),
	}
}
