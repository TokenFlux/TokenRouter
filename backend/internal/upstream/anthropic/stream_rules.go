// 平台流策略只接受已投影参数，不读取配置或账号。
package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
)

const ClaudeCodeNoopDeltaKeepaliveMinVersion = "2.1.193"

func ShouldUseClaudeCodeNoopDeltaKeepalive(userAgent string) bool {
	version := ExtractCLIVersion(userAgent)
	if version == "" {
		return false
	}
	return clientmeta.CompareVersions(version, ClaudeCodeNoopDeltaKeepaliveMinVersion) >= 0
}
func ClaudeCodeKeepaliveDeltaTypeForContentBlock(blockType string) string {
	switch blockType {
	case "text":
		return "text_delta"
	case "tool_use":
		return "input_json_delta"
	case "thinking":
		return "thinking_delta"
	default:
		return ""
	}
}
func ClaudeCodeKeepaliveFieldForDeltaType(deltaType string) string {
	switch deltaType {
	case "text_delta":
		return "text"
	case "input_json_delta":
		return "partial_json"
	case "thinking_delta":
		return "thinking"
	default:
		return ""
	}
}
func BuildClaudeCodeNoopDeltaKeepalive(index int, deltaType string) (string, bool) {
	fieldName := ClaudeCodeKeepaliveFieldForDeltaType(deltaType)
	if fieldName == "" {
		return "", false
	}
	return fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":%d,\"delta\":{\"type\":\"%s\",\"%s\":\"\"}}\n\n", index, deltaType, fieldName), true
}
func SseEventIndex(event map[string]any) (int, bool) {
	switch v := event["index"].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

// ApplyCacheTTLOverride 将所有 cache creation tokens 归入指定的 TTL 类型。
// target 为 "5m" 或 "1h"。返回 true 表示发生了变更。
func ApplyCacheTTLOverride(usage *protocol.TokenUsage, target string) bool {
	return protocol.ApplyCacheTTLOverride(usage, target)
}

// RewriteCacheCreationJSON 在 JSON usage 对象中重写 cache_creation 嵌套对象的 TTL 分类。
// usageObj 是 usage JSON 对象（map[string]any）。
func RewriteCacheCreationJSON(usageObj map[string]any, target string) bool {
	ccObj, ok := usageObj["cache_creation"].(map[string]any)
	if !ok {
		return false
	}
	v5m, _ := wire.ParseSSEUsageInt(ccObj["ephemeral_5m_input_tokens"])
	v1h, _ := wire.ParseSSEUsageInt(ccObj["ephemeral_1h_input_tokens"])
	projected := protocol.TokenUsage{CacheCreation5mTokens: v5m, CacheCreation1hTokens: v1h}
	if !protocol.ApplyCacheTTLOverride(&projected, target) {
		return false
	}
	ccObj["ephemeral_5m_input_tokens"] = float64(projected.CacheCreation5mTokens)
	ccObj["ephemeral_1h_input_tokens"] = float64(projected.CacheCreation1hTokens)
	return true
}

// ReconcileCachedTokens 兼容 Kimi 等上游：
// 将 OpenAI 风格的 cached_tokens 映射到 Claude 标准的 cache_read_input_tokens
func ReconcileCachedTokens(usage map[string]any) bool {
	if usage == nil {
		return false
	}
	cacheRead, _ := usage["cache_read_input_tokens"].(float64)
	if cacheRead > 0 {
		return false // 已有标准字段，无需处理
	}
	cached, _ := usage["cached_tokens"].(float64)
	if cached <= 0 {
		return false
	}
	usage["cache_read_input_tokens"] = cached
	return true
}
