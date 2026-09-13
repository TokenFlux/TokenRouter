// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	capability "github.com/TokenFlux/TokenRouter/internal/routing/capability"
	maps "maps"
	slices "slices"
	strings "strings"
)

func (a *Record) LegacyUpstreamProtocols(legacyMode string) []capability.ProtocolID {
	options := a.NativeProtocolOptions()
	if a.IsCNProvider() {
		switch legacyMode {
		case APIProtocolAdaptive:
			return options
		case APIProtocolAnthropic:
			return []capability.ProtocolID{capability.ProtocolAnthropicMessages}
		case APIProtocolResponses:
			return []capability.ProtocolID{capability.ProtocolOpenAIResponses}
		default:
			return []capability.ProtocolID{capability.ProtocolOpenAIChatCompletions}
		}
	}
	if a.IsOpenAIApiKey() {
		workloads, found := a.OpenAIWorkloadCapabilitySet()
		if found && !workloads["text_generation"] {
			options = slices.DeleteFunc(options, func(p capability.ProtocolID) bool {
				return p != capability.ProtocolEmbeddings && p != capability.ProtocolImagesGenerations && p != capability.ProtocolImagesEdits
			})
		}
		if found && !workloads["embeddings"] {
			options = slices.DeleteFunc(options, func(p capability.ProtocolID) bool { return p == capability.ProtocolEmbeddings })
		}
		mode := ResolveUpstreamTextProtocol(a.Extra, TextProtocolResponses)
		if mode == TextProtocolChatCompletions {
			options = slices.DeleteFunc(options, func(p capability.ProtocolID) bool { return p == capability.ProtocolOpenAIResponses })
		}
		if ResolveUpstreamTextProtocol(a.Extra, TextProtocolChatCompletions) == TextProtocolResponses {
			options = slices.DeleteFunc(options, func(p capability.ProtocolID) bool { return p == capability.ProtocolOpenAIChatCompletions })
		}
	}
	return options
}

func HasLegacyProtocolPatch(credentials, extra map[string]any) bool {
	for _, key := range []string{"api_protocol", OpenAIWorkloadCapabilitiesCredentialKey, LegacyOpenAICapabilitiesCredentialKey} {
		if _, ok := credentials[key]; ok {
			return true
		}
	}
	for _, key := range []string{ExtraKeyTextRouteMode, LegacyOpenAIResponsesModeExtraKey} {
		if _, ok := extra[key]; ok {
			return true
		}
	}
	return false
}

func ApplyLegacyProtocolPatch(account *Record, credentials, extra map[string]any) {
	if _, explicit := credentials[UpstreamProtocolsKey]; explicit || !HasLegacyProtocolPatch(credentials, extra) {
		return
	}
	if _, unified := account.Credentials[UpstreamProtocolsKey]; !unified {
		return
	}
	// 旧输入只覆盖原来的文本/工作负载维度，独立媒体原生集合保持不变。
	selected := account.UpstreamProtocols()
	legacy := *account
	legacy.Credentials = maps.Clone(account.Credentials)
	delete(legacy.Credentials, UpstreamProtocolsKey)
	legacyProtocols := legacy.LegacyUpstreamProtocols(legacy.ConfiguredAPIProtocol())
	isText := func(p capability.ProtocolID) bool {
		return p == capability.ProtocolAnthropicMessages || p == capability.ProtocolOpenAIResponses || p == capability.ProtocolOpenAIChatCompletions
	}
	hasWorkload := false
	for _, key := range []string{OpenAIWorkloadCapabilitiesCredentialKey, LegacyOpenAICapabilitiesCredentialKey} {
		if _, ok := credentials[key]; ok {
			hasWorkload = true
		}
	}
	selected = slices.DeleteFunc(selected, func(p capability.ProtocolID) bool {
		return isText(p) || (hasWorkload && p == capability.ProtocolEmbeddings)
	})
	for _, p := range legacyProtocols {
		if isText(p) || (hasWorkload && p == capability.ProtocolEmbeddings) {
			selected = append(selected, p)
		}
	}
	account.Credentials = maps.Clone(account.Credentials)
	account.Credentials[UpstreamProtocolsKey] = selected
}

func PreserveProtocolCredentials(existing, incoming map[string]any) map[string]any {
	out := maps.Clone(incoming)
	if out == nil {
		out = map[string]any{}
	}
	if !HasLegacyProtocolPatch(incoming, nil) {
		for _, key := range []string{UpstreamProtocolsKey, "api_base_urls"} {
			if _, supplied := out[key]; !supplied {
				if value, exists := existing[key]; exists {
					out[key] = value
				}
			}
		}
	}
	return out
}

// MigrateLegacyProtocolCredentials 在统一集合保存后保留旧端点并移除旧配置字段。
func MigrateLegacyProtocolCredentials(account *Record) {
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
	delete(account.Credentials, OpenAIWorkloadCapabilitiesCredentialKey)
	delete(account.Credentials, LegacyOpenAICapabilitiesCredentialKey)
	account.Extra = maps.Clone(account.Extra)
	delete(account.Extra, ExtraKeyTextRouteMode)
	delete(account.Extra, LegacyOpenAIResponsesModeExtraKey)
}
