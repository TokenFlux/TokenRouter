// 共用 Google wire 用量投影保持字段与 nil 语义，不持有平台执行状态。
package bridge

import "github.com/TokenFlux/TokenRouter/internal/protocol"

func NativePickGeminiCollectResult(last map[string]any, lastWithParts map[string]any) map[string]any {
	if lastWithParts != nil {
		return lastWithParts
	}
	if last != nil {
		return last
	}
	return map[string]any{}
}

// NativeUsageProjection 保留 nil 与未观测字段的零值，结算归属仍在旧网关。
func NativeUsageProjection(usage *NativeGeminiUsage) *protocol.TokenUsage {
	if usage == nil {
		return nil
	}
	return &protocol.TokenUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CacheReadInputTokens: usage.CacheReadInputTokens, ImageOutputTokens: usage.ImageOutputTokens}
}
