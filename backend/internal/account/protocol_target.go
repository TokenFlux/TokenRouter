package account

import (
	"strings"

	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	routingcapability "github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// ProtocolTarget 组合本次协议选择与账号配置，只用于当前执行或维护查询。
// Protocol 不写入 Record 或共享缓存；零值沿用配置协议。
type ProtocolTarget struct {
	*Record
	Protocol protocolcore.ProtocolID
}

// GetOpenAIBaseURL 根据本次协议选择读取原地址，不改变保存的账号配置。
func (a ProtocolTarget) GetOpenAIBaseURL() string {
	return a.OpenAIBaseURL(a.IsAdaptiveAPIProtocol())
}

// GetAPIProtocol 为原有平台适配器提供协议变体：请求副本使用已解析目标，
// 统一账号的地址/维护流程使用分协议模式，旧对象继续保留历史读取默认值。
func (a ProtocolTarget) GetAPIProtocol() string {
	if a.Record != nil && a.Protocol != "" {
		switch a.Protocol {
		case protocolcore.ProtocolAnthropicMessages:
			return APIProtocolAnthropic
		case protocolcore.ProtocolOpenAIResponses:
			return APIProtocolResponses
		case protocolcore.ProtocolOpenAIChatCompletions:
			return APIProtocolChatCompletions
		}
	}

	return a.ConfiguredAPIProtocol()
}

// UsesNativeCNResponses 报告当前账号是否应按原生 Responses 协议转发
// （显式 responses，或 adaptive 且平台具备原生端点）。
func (a ProtocolTarget) UsesNativeCNResponses() bool {
	if a.Record == nil || !a.SupportsNativeCNResponses() {
		return false
	}
	switch a.GetAPIProtocol() {
	case APIProtocolResponses, APIProtocolAdaptive:
		return true
	default:
		return false
	}
}

// IsAdaptiveAPIProtocol 报告账号是否按入站协议动态选择供应商原生端点。
func (a ProtocolTarget) IsAdaptiveAPIProtocol() bool {
	return a.GetAPIProtocol() == APIProtocolAdaptive
}

// GetCNProtocolBaseURL 返回国产供应商指定协议的上游 base URL。
// adaptive 账号优先使用 api_base_urls 中的分协议地址，缺失时按平台和
// account_mode 使用官方默认端点。base_url 继续作为 Chat Completions 地址兼容旧字段。
func (a ProtocolTarget) GetCNProtocolBaseURL(protocol string) string {
	if a.Record == nil || !a.IsCNProvider() {
		return ""
	}
	if _, unified := a.Credentials[UpstreamProtocolsKey]; unified || a.IsAdaptiveAPIProtocol() {
		if baseURLs, ok := a.Credentials["api_base_urls"].(map[string]any); ok {
			if baseURL, ok := baseURLs[protocol].(string); ok && strings.TrimSpace(baseURL) != "" {
				return strings.TrimSpace(baseURL)
			}
		}
		if protocol == APIProtocolChatCompletions {
			if baseURL := strings.TrimSpace(a.GetCredential("base_url")); baseURL != "" {
				return baseURL
			}
		}
	}
	return a.DefaultCNProtocolBaseURL(protocol)
}

// IsAnthropicProtocol 报告账号是否以原生 Anthropic 协议接入上游
// （/v1/messages 直通，适配 Claude Code 等客户端）。
func (a ProtocolTarget) IsAnthropicProtocol() bool {
	return a.GetAPIProtocol() == APIProtocolAnthropic
}

// GetAnthropicProtocolBaseURL 返回 Anthropic 协议账号的上游 base_url
// （上游路径为 {base}/v1/messages）。优先取凭证 base_url，缺失时按
// 供应商 × 接入模式返回默认端点。非 Anthropic 协议账号返回空串。
func (a ProtocolTarget) GetAnthropicProtocolBaseURL() string {
	if a.Record == nil || (!a.IsAnthropicProtocol() && !a.IsAdaptiveAPIProtocol()) {
		return ""
	}
	if _, unified := a.Credentials[UpstreamProtocolsKey]; unified || a.IsAdaptiveAPIProtocol() {
		return a.GetCNProtocolBaseURL(APIProtocolAnthropic)
	}
	if a.Type == routingcapability.AccountTypeAPIKey || a.Type == routingcapability.AccountTypeUpstream {
		if baseURL := strings.TrimSpace(a.GetCredential("base_url")); baseURL != "" {
			return baseURL
		}
	}
	switch a.Platform {
	case routingcapability.PlatformKimi:
		if a.GetAccountMode() == AccountModeCoding {
			return DefaultKimiCodingAnthropicBaseURL
		}
		return DefaultKimiPayGAnthropicBaseURL
	case routingcapability.PlatformZhipu:
		return DefaultZhipuAnthropicBaseURL
	case routingcapability.PlatformDeepseek:
		return DefaultDeepseekAnthropicBaseURL
	default:
		return ""
	}
}

// GetOpenAIFormatBaseURL 返回供 OpenAI 格式端点（/v1/models、/v1/chat/completions
// 等）使用的 base。chat_completions / responses 协议下与 GetOpenAIBaseURL
// 一致；anthropic 协议下，官方端点映射到对应的 OpenAI 格式端点，自定义中继则
// 只移除末尾的 /anthropic 协议段，保留中继 host 与路径前缀。
func (a ProtocolTarget) GetOpenAIFormatBaseURL() string {
	if a.Record == nil {
		return ""
	}

	// 固定 Messages 账号通过地址槽识别模型同步根，保留中继路径前缀。
	anthropicBase := a.IsAnthropicProtocol()
	if _, unified := a.Credentials[UpstreamProtocolsKey]; unified && a.IsCNProvider() {
		urls, _ := a.Credentials["api_base_urls"].(map[string]any)
		chat, _ := urls[APIProtocolChatCompletions].(string)
		messages, _ := urls[APIProtocolAnthropic].(string)
		anthropicBase = chat == "" && messages != "" && messages == a.GetCredential("base_url")
	}
	if !anthropicBase {
		return a.GetOpenAIBaseURL()
	}

	if baseURL := strings.TrimSpace(a.GetCredential("base_url")); baseURL != "" {
		if !IsDefaultCNAnthropicBaseURL(baseURL) {
			return StripCNAnthropicPathSuffix(baseURL)
		}
	}
	switch a.Platform {
	case routingcapability.PlatformKimi:
		if a.GetAccountMode() == AccountModeCoding {
			return DefaultKimiCodingBaseURL
		}
		return DefaultKimiPayGBaseURL
	case routingcapability.PlatformZhipu:
		if a.GetAccountMode() == AccountModeCoding {
			return DefaultZhipuCodingBaseURL
		}
		return DefaultZhipuPayGBaseURL
	case routingcapability.PlatformDeepseek:
		return DefaultDeepseekBaseURL
	default:
		return a.GetOpenAIBaseURL()
	}
}
