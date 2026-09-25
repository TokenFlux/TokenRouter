// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

func (a *Record) SupportsOpenAIEndpointCapability(requested OpenAIEndpointCapability, grokMedia func() (bool, string)) bool {
	if a == nil {
		return false
	}
	if requested == "" {
		return true
	}
	if !a.IsOpenAICompatible() {
		return false
	}
	if _, unified := a.Credentials[UpstreamProtocolsKey]; unified && !a.IsGrok() {
		enabled := a.UpstreamProtocols()
		has := func(p capability.ProtocolID) bool { return slices.Contains(enabled, p) }
		switch requested {
		case OpenAIEndpointCapabilityTextGeneration:
			if !has(capability.ProtocolOpenAIResponses) && !has(capability.ProtocolOpenAIChatCompletions) && !has(capability.ProtocolAnthropicMessages) {
				return false
			}
		case OpenAIEndpointCapabilityResponses, OpenAIEndpointCapabilityRemoteCompactionV2:
			if !has(capability.ProtocolOpenAIResponses) {
				return false
			}
		case OpenAIEndpointCapabilityEmbeddings:
			if !has(capability.ProtocolEmbeddings) {
				return false
			}
		case OpenAIEndpointCapabilityLive:
			if !has(capability.ProtocolLive) {
				return false
			}
		case OpenAIEndpointCapabilityAlphaSearch:
			if !has(capability.ProtocolAlphaSearch) && (!a.IsOpenAIPersonalAccessToken() || !has(capability.ProtocolOpenAIResponses)) {
				return false
			}
		}
	}
	if a.IsGrok() {
		switch requested {
		case OpenAIEndpointCapabilityTextGeneration:
			return true
		case OpenAIEndpointCapabilityGrokMediaGeneration:
			if grokMedia == nil {
				return false
			}
			eligible, reason := grokMedia()
			// 尚无观测的 OAuth 账号仍作为调度候选，供请求路径在转发前执行计费探测。
			// 如果探测不可用或无法提供明确的付费资格证据，转发门控会拒绝该账号。
			return eligible || reason == "billing_unobserved"
		default:
			return false
		}
	}
	switch requested {
	case OpenAIEndpointCapabilityTextGeneration:
	case OpenAIEndpointCapabilityLive:
		return a.Platform == PlatformOpenAI &&
			a.Type == AccountTypeOAuth &&
			!a.IsOpenAIPersonalAccessToken() &&
			!a.IsOpenAIAgentIdentity()
	case OpenAIEndpointCapabilityRemoteCompactionV2:
		if !a.AllowsOpenAINativeCompactionV2() {
			return false
		}
		fallthrough
	case OpenAIEndpointCapabilityResponses:
		// 生图等原生 Responses 路径不能降级；使用 Responses 首选协议解析后，
		// 管理员只启用 Chat 的 APIKey 账号必须排除。
		if a.Type == AccountTypeAPIKey && ResolveUpstreamTextProtocol(
			a.Extra,
			TextProtocolResponses,
		) != TextProtocolResponses {
			return false
		}
		// 支持 Responses 的上游同样需具备普通文本能力：复用下方 text_generation
		// 配置集校验。
		requested = OpenAIEndpointCapabilityTextGeneration
	case OpenAIEndpointCapabilityAlphaSearch:
		// alpha/search 的转发按账号类型分流：OAuth/PAT 走
		// chatgpt.com/backend-api/codex/alpha/search，API key 走
		// {base_url}/v1/alpha/search（见 openAIAlphaSearchURL），两类账号
		// 都可承接独立搜索请求。上游不支持该端点时由转发层 failover 兜底。
		if a.Type != AccountTypeOAuth && a.Type != AccountTypeAPIKey {
			return false
		}
	case OpenAIEndpointCapabilityEmbeddings:
		if a.Type != AccountTypeAPIKey {
			return false
		}
	default:
		return false
	}

	configured, found := a.OpenAIWorkloadCapabilitySet()
	if !found {
		return true
	}
	if requested == OpenAIEndpointCapabilityAlphaSearch && configured[string(OpenAIEndpointCapabilityTextGeneration)] {
		return true
	}
	return configured[string(requested)]
}
