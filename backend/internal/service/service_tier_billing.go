package service

import (
	"strings"

	completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"
)

// ServiceTierBillingResolution 描述请求档位与上游实际档位之间的计费决策。
type ServiceTierBillingResolution = completion.ServiceTierBillingResolution

// ResolveBillingServiceTier 只信任上游把价格降档的声明，绝不因响应内容升档收费。
func ResolveBillingServiceTier(requested, observed string) ServiceTierBillingResolution {
	return completion.ResolveBillingServiceTier(requested, observed)
}

// ResolveOpenAIServiceTierBilling 按凭据类型应用上游 service tier 计费契约。
// 公共 OpenAI 响应的档位声明可降低计费；私有 ChatGPT Codex 常将有效 Fast 回显为
// default，因此 OAuth 类凭据保留最终出站档位。
func ResolveOpenAIServiceTierBilling(account *Account, requested, observed string) ServiceTierBillingResolution {
	return completion.ResolveOpenAIServiceTierBilling(account != nil && account.IsOpenAIOAuthLike(), requested, observed)
}

func codexOAuthResponseTierIsNonAuthoritative(observed string) bool {
	return completion.CodexOAuthResponseTierIsNonAuthoritative(observed)
}

// ApplyOpenAIServiceTierBillingResolution 仅在上游档位对当前凭据有权威性时降档。
func ApplyOpenAIServiceTierBillingResolution(account *Account, result *OpenAIForwardResult) ServiceTierBillingResolution {
	if result == nil {
		return ServiceTierBillingResolution{}
	}
	resolution := ResolveOpenAIServiceTierBilling(account, stringValueOrEmpty(result.ServiceTier), result.UpstreamResponseServiceTier)
	if resolution.Downgraded {
		billing := resolution.Billing
		result.ServiceTier = &billing
	}
	return resolution
}

// ApplyForwardServiceTierBillingResolution 是通用 Anthropic 转发结果的对应处理。
func ApplyForwardServiceTierBillingResolution(result *ForwardResult) ServiceTierBillingResolution {
	if result == nil {
		return ServiceTierBillingResolution{}
	}
	requested := stringValueOrEmpty(result.ServiceTier)
	if requested == "" {
		requested = strings.TrimSpace(result.Usage.Speed)
	}
	resolution := ResolveBillingServiceTier(requested, result.UpstreamResponseServiceTier)
	if resolution.Downgraded {
		billing := resolution.Billing
		result.ServiceTier = &billing
		// fork 的通用计费路径历史上从 Usage.Speed 取档位；同步更新它，
		// 让费用、Usage Log 和结果元数据保持同一口径。
		if result.Usage.Speed != "" {
			result.Usage.Speed = billing
		}
	}
	return resolution
}
