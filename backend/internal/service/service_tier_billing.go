package service

import (
	"strings"

	completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
)

// ServiceTierBillingResolution 描述请求档位与上游实际档位之间的计费决策。

// ResolveOpenAIServiceTierBilling 按凭据类型应用上游 service tier 计费契约。
// 公共 OpenAI 响应的档位声明可降低计费；私有 ChatGPT Codex 常将有效 Fast 回显为
// default，因此 OAuth 类凭据保留最终出站档位。
func ResolveOpenAIServiceTierBilling(account *Account, requested, observed string) completion.ServiceTierBillingResolution {
	return completion.ResolveOpenAIServiceTierBilling(account != nil && account.IsOpenAIOAuthLike(), requested, observed)
}

// ApplyOpenAIServiceTierBillingResolution 仅在上游档位对当前凭据有权威性时降档。
func ApplyOpenAIServiceTierBillingResolution(account *Account, result *forwardcore.OpenAIResult) completion.ServiceTierBillingResolution {
	if result == nil {
		return completion.ServiceTierBillingResolution{}
	}
	resolution := ResolveOpenAIServiceTierBilling(account, stringValueOrEmpty(result.ServiceTier), result.UpstreamResponseServiceTier)
	if resolution.Downgraded {
		billing := resolution.Billing
		result.ServiceTier = &billing
	}
	return resolution
}

// ApplyForwardServiceTierBillingResolution 是通用 Anthropic 转发结果的对应处理。
func ApplyForwardServiceTierBillingResolution(result *forwardcore.MessagesResult) completion.ServiceTierBillingResolution {
	if result == nil {
		return completion.ServiceTierBillingResolution{}
	}
	requested := stringValueOrEmpty(result.ServiceTier)
	if requested == "" {
		requested = strings.TrimSpace(result.Usage.Speed)
	}
	resolution := completion.ResolveBillingServiceTier(requested, result.UpstreamResponseServiceTier)
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
