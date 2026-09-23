package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// ModelDefaults 提供原平台目录的懒读取入口，不复制映射或建立新缓存。
func ModelDefaults() account.ModelMappingDefaults {
	return account.ModelMappingDefaults{
		Antigravity:           func() map[string]string { return antigravity.DefaultAntigravityModelMapping },
		GoogleOne:             codeassist.GoogleOneModelMapping,
		AntigravityAgentModel: antigravity.AntigravityGemini31ProAgentModel,
	}
}

// ModelRules 仅在核心需要平台资格时读取站点；不提前解析或扩大账号原生能力。
func ModelRules(value *account.Record) account.ModelPlatformRules {
	return account.ModelPlatformRules{
		NormalizeQoder:      qoder.NormalizeModelForWhitelist,
		OpenAIOAuthServable: account.IsOpenAIOAuthServableModel,
		QoderCompatible: func(model string) bool {
			if value == nil {
				return false
			}
			site, err := qoder.ParseSite(value.GetCredential("site"))
			return err == nil && qoder.ModelCompatibleWithSite(site, model)
		},
	}
}

// SupportsOpenAIEndpoint 在端点能力检查实际需要时提供平台媒体资格，不提前读取资格。
func SupportsOpenAIEndpoint(value *account.Record, capability account.OpenAIEndpointCapability) bool {
	return value.SupportsOpenAIEndpointCapability(capability, func() (bool, string) {
		return account.GrokMediaGenerationEligibility(value, GrokTierRules())
	})
}
