package service

import (
	"maps"
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai_compat"
)

func (a *Account) legacyUpstreamProtocols() []domain.ProtocolID {
	options := a.NativeProtocolOptions()
	if a.IsCNProvider() {
		switch a.GetAPIProtocol() {
		case APIProtocolAdaptive:
			return options
		case APIProtocolAnthropic:
			return []domain.ProtocolID{domain.ProtocolAnthropicMessages}
		case APIProtocolResponses:
			return []domain.ProtocolID{domain.ProtocolOpenAIResponses}
		default:
			return []domain.ProtocolID{domain.ProtocolOpenAIChatCompletions}
		}
	}
	if a.IsOpenAIApiKey() {
		workloads, found := a.openAIWorkloadCapabilitySet()
		if found && !workloads["text_generation"] {
			options = slices.DeleteFunc(options, func(p domain.ProtocolID) bool {
				return p != domain.ProtocolEmbeddings && p != domain.ProtocolImagesGenerations && p != domain.ProtocolImagesEdits
			})
		}
		if found && !workloads["embeddings"] {
			options = slices.DeleteFunc(options, func(p domain.ProtocolID) bool { return p == domain.ProtocolEmbeddings })
		}
		mode := openai_compat.ResolveUpstreamTextProtocol(a.Extra, openai_compat.TextProtocolResponses)
		if mode == openai_compat.TextProtocolChatCompletions {
			options = slices.DeleteFunc(options, func(p domain.ProtocolID) bool { return p == domain.ProtocolOpenAIResponses })
		}
		if openai_compat.ResolveUpstreamTextProtocol(a.Extra, openai_compat.TextProtocolChatCompletions) == openai_compat.TextProtocolResponses {
			options = slices.DeleteFunc(options, func(p domain.ProtocolID) bool { return p == domain.ProtocolOpenAIChatCompletions })
		}
	}
	return options
}

func hasLegacyProtocolPatch(credentials, extra map[string]any) bool {
	for _, key := range []string{"api_protocol", openAIWorkloadCapabilitiesCredentialKey, legacyOpenAICapabilitiesCredentialKey} {
		if _, ok := credentials[key]; ok {
			return true
		}
	}
	for _, key := range []string{openai_compat.ExtraKeyTextRouteMode, legacyOpenAIResponsesModeExtraKey} {
		if _, ok := extra[key]; ok {
			return true
		}
	}
	return false
}

func applyLegacyProtocolPatch(account *Account, credentials, extra map[string]any) {
	if _, explicit := credentials[upstreamProtocolsKey]; explicit || !hasLegacyProtocolPatch(credentials, extra) {
		return
	}
	if _, unified := account.Credentials[upstreamProtocolsKey]; !unified {
		return
	}
	// 旧输入只覆盖原来的文本/工作负载维度，独立媒体原生集合保持不变。
	selected := account.UpstreamProtocols()
	legacy := *account
	legacy.Credentials = maps.Clone(account.Credentials)
	delete(legacy.Credentials, upstreamProtocolsKey)
	legacyProtocols := legacy.legacyUpstreamProtocols()
	isText := func(p domain.ProtocolID) bool {
		return p == domain.ProtocolAnthropicMessages || p == domain.ProtocolOpenAIResponses || p == domain.ProtocolOpenAIChatCompletions
	}
	hasWorkload := false
	for _, key := range []string{openAIWorkloadCapabilitiesCredentialKey, legacyOpenAICapabilitiesCredentialKey} {
		if _, ok := credentials[key]; ok {
			hasWorkload = true
		}
	}
	selected = slices.DeleteFunc(selected, func(p domain.ProtocolID) bool { return isText(p) || (hasWorkload && p == domain.ProtocolEmbeddings) })
	for _, p := range legacyProtocols {
		if isText(p) || (hasWorkload && p == domain.ProtocolEmbeddings) {
			selected = append(selected, p)
		}
	}
	account.Credentials = maps.Clone(account.Credentials)
	account.Credentials[upstreamProtocolsKey] = selected
}

func preserveProtocolCredentials(existing, incoming map[string]any) map[string]any {
	out := maps.Clone(incoming)
	if out == nil {
		out = map[string]any{}
	}
	if !hasLegacyProtocolPatch(incoming, nil) {
		for _, key := range []string{upstreamProtocolsKey, "api_base_urls"} {
			if _, supplied := out[key]; !supplied {
				if value, exists := existing[key]; exists {
					out[key] = value
				}
			}
		}
	}
	return out
}

// migrateLegacyProtocolCredentials 在统一集合保存后保留旧端点并移除旧配置字段。
func migrateLegacyProtocolCredentials(account *Account) {
	// 固定 CN 端点迁入分协议地址，避免移除旧选项后改变自定义 base_url 的含义。
	if account.IsCNProvider() {
		legacy := account.GetCredential("api_protocol")
		if legacy != "" && legacy != APIProtocolAdaptive {
			urls, _ := account.Credentials["api_base_urls"].(map[string]any)
			urls = maps.Clone(urls)
			if urls == nil {
				urls = map[string]any{}
			}
			if base := strings.TrimSpace(account.GetCredential("base_url")); base != "" {
				if _, exists := urls[legacy]; !exists {
					urls[legacy] = base
				}
			}
			account.Credentials["api_base_urls"] = urls
		}
	}
	delete(account.Credentials, "api_protocol")
	delete(account.Credentials, openAIWorkloadCapabilitiesCredentialKey)
	delete(account.Credentials, legacyOpenAICapabilitiesCredentialKey)
	account.Extra = maps.Clone(account.Extra)
	delete(account.Extra, openai_compat.ExtraKeyTextRouteMode)
	delete(account.Extra, legacyOpenAIResponsesModeExtraKey)
}

// legacyGroupProtocolPatch 只表达旧客户端明确修改的开关，缺省字段保持现状。
type legacyGroupProtocolPatch struct {
	messages, image, batch, live *bool
}

// applyLegacyGroupProtocolPatch 只修改已校验的集合，避免兼容转换掩盖非法输入。
func applyLegacyGroupProtocolPatch(group *Group, patch *legacyGroupProtocolPatch) {
	if patch == nil {
		return
	}
	supported := domain.SupportedGroupClientProtocols(group.Platform)
	set := func(protocol domain.ProtocolID, value *bool) {
		if value != nil && slices.Contains(supported, protocol) {
			group.AllowedProtocols = domain.SetGroupClientProtocol(group.AllowedProtocols, protocol, *value)
		}
	}
	set(domain.ProtocolAnthropicMessages, patch.messages)
	set(domain.ProtocolImagesGenerations, patch.image)
	set(domain.ProtocolImagesEdits, patch.image)
	if patch.image != nil && !*patch.image {
		set(domain.ProtocolImageBatches, patch.image)
	}
	set(domain.ProtocolImageBatches, patch.batch)
	set(domain.ProtocolLive, patch.live)
}
