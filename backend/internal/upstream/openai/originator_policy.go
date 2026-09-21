package openai

import "strings"

// ResolveUpstreamOriginator 保留路由覆盖、客户端原值、官方缺省及兼容缺省的顺序。
func ResolveUpstreamOriginator(read func() string, official, matched bool, routed string) string {
	if matched {
		if value := strings.TrimSpace(routed); value != "" {
			return value
		}
	}
	if value := strings.TrimSpace(read()); value != "" {
		return value
	}
	if official {
		return ResolveCodexOutboundIdentity("").Originator
	}
	return "opencode"
}
