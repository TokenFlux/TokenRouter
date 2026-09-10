package domain

import "fmt"

// GroupClientProtocol 表示客户端调用分组时使用的公开协议与业务入口。
type GroupClientProtocol string

// canonicalGroupClientProtocols 从唯一目录派生顺序，避免新增协议遗漏校验。
var canonicalGroupClientProtocols = func() []GroupClientProtocol {
	out := []GroupClientProtocol{}
	for _, protocol := range protocolCatalog {
		if !protocol.UpstreamOnly {
			out = append(out, protocol.ID)
		}
	}
	return out
}()

// SupportedGroupClientProtocols 返回平台实际实现的客户端入口。
func SupportedGroupClientProtocols(platform string) []GroupClientProtocol {
	out := []GroupClientProtocol{}
	for _, protocol := range protocolCatalog {
		if protocol.UpstreamOnly {
			continue
		}
		for _, candidate := range protocol.Platforms {
			if platform == candidate {
				out = append(out, protocol.ID)
				break
			}
		}
	}
	return out
}

// DefaultGroupClientProtocols 返回新建分组的协议默认值。
// 默认值只决定初始选择，管理员可以在保存时关闭任意协议。
func DefaultGroupClientProtocols(platform string) []GroupClientProtocol {
	switch platform {
	case PlatformAnthropic:
		return []GroupClientProtocol{ProtocolAnthropicMessages}
	case PlatformGrok:
		return []GroupClientProtocol{ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions, ProtocolImagesGenerations, ProtocolImagesEdits}
	case PlatformOpenAI:
		return []GroupClientProtocol{
			ProtocolOpenAIResponses,
			ProtocolOpenAIChatCompletions,
		}
	case PlatformKimi, PlatformZhipu, PlatformDeepseek:
		return []GroupClientProtocol{
			ProtocolAnthropicMessages,
			ProtocolOpenAIResponses,
			ProtocolOpenAIChatCompletions,
		}
	case PlatformGemini:
		return []GroupClientProtocol{ProtocolGeminiGenerateContent}
	case PlatformAntigravity:
		return []GroupClientProtocol{
			ProtocolAnthropicMessages,
			ProtocolGeminiGenerateContent,
		}
	default:
		return []GroupClientProtocol{}
	}
}

// ValidateGroupClientProtocols 校验完整协议集合并返回固定顺序的副本。
func ValidateGroupClientProtocols(platform string, protocols []GroupClientProtocol) ([]GroupClientProtocol, error) {
	supported := make(map[GroupClientProtocol]struct{})
	for _, protocol := range SupportedGroupClientProtocols(platform) {
		supported[protocol] = struct{}{}
	}
	known := make(map[GroupClientProtocol]struct{}, len(canonicalGroupClientProtocols))
	for _, protocol := range canonicalGroupClientProtocols {
		known[protocol] = struct{}{}
	}

	seen := make(map[GroupClientProtocol]struct{}, len(protocols))
	for i, protocol := range protocols {
		if _, ok := known[protocol]; !ok {
			return nil, fmt.Errorf("allowed_protocols[%d] contains unknown protocol %q", i, protocol)
		}
		if _, ok := supported[protocol]; !ok {
			return nil, fmt.Errorf("protocol %q is not supported by platform %q", protocol, platform)
		}
		if _, ok := seen[protocol]; ok {
			return nil, fmt.Errorf("protocol %q is duplicated", protocol)
		}
		seen[protocol] = struct{}{}
	}
	out := make([]GroupClientProtocol, 0, len(seen))
	for _, protocol := range canonicalGroupClientProtocols {
		if _, ok := seen[protocol]; ok {
			out = append(out, protocol)
		}
	}
	return out, nil
}

// HasGroupClientProtocol 判断集合是否包含指定协议。
func HasGroupClientProtocol(protocols []GroupClientProtocol, target GroupClientProtocol) bool {
	for _, protocol := range protocols {
		if protocol == target {
			return true
		}
	}
	return false
}

// SetGroupClientProtocol 更新单个协议并保持公共契约规定的顺序。
func SetGroupClientProtocol(protocols []GroupClientProtocol, target GroupClientProtocol, enabled bool) []GroupClientProtocol {
	selected := make(map[GroupClientProtocol]struct{}, len(protocols)+1)
	for _, protocol := range protocols {
		selected[protocol] = struct{}{}
	}
	if enabled {
		selected[target] = struct{}{}
	} else {
		delete(selected, target)
	}
	out := make([]GroupClientProtocol, 0, len(selected))
	for _, protocol := range canonicalGroupClientProtocols {
		if _, ok := selected[protocol]; ok {
			out = append(out, protocol)
		}
	}
	return out
}
