package capability

import "slices"

// AccountProtocols 是候选判断的只读投影，不包含凭据或旧实体。
type AccountProtocols struct {
	Platform, Type, AuthMode string
	Enabled                  []ProtocolID
}

// ResolveRoute 保留原生优先、批量 provider 绑定及单步转换规则。
func ResolveRoute(account AccountProtocols, source ProtocolID, fallbacks map[ProtocolID]ProtocolID) (ProtocolID, bool) {
	enabled := account.Enabled
	if slices.Contains(enabled, source) && slices.Contains(NativeProtocolOptions(account.Platform, account.Type, account.AuthMode), source) {
		return source, true
	}
	if source == ProtocolImageBatches {
		// 批量作业沿用 provider 绑定，仅检查该 provider 的专用上游协议。
		target := ProtocolGeminiBatch
		if account.Type == AccountTypeServiceAccount {
			target = ProtocolVertexBatch
		}
		return target, account.Platform == PlatformGemini && slices.Contains(enabled, target)
	}
	if fallbacks == nil {
		return "", false
	}
	target := fallbacks[source]
	if slices.Contains(enabled, target) && SupportsProtocolConversion(account.Platform, account.Type, account.AuthMode, source, target) {
		return target, true
	}
	return "", false
}
