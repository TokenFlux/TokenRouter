package service

import (
	"strconv"
	"strings"
)

const (
	// AccountTestModeDefault drives the standard /responses connection test.
	AccountTestModeDefault = "default"
	// AccountTestModeCompact drives the native remote compaction v2 probe.
	AccountTestModeCompact = "compact"
	// AccountTestModeLegacyCompact drives the legacy /responses/compact probe.
	AccountTestModeLegacyCompact = "legacy_compact"
)

const (
	// 原生 V2 与旧端点状态分别保存，旧端点 404 不得污染 V2 能力判定。
	openAINativeCompactionV2ModeExtraKey       = "openai_native_compaction_v2_mode"
	openAINativeCompactionV2SupportedExtraKey  = "openai_native_compaction_v2_supported"
	openAINativeCompactionV2CheckedAtExtraKey  = "openai_native_compaction_v2_checked_at"
	openAINativeCompactionV2LastStatusExtraKey = "openai_native_compaction_v2_last_status"
	openAINativeCompactionV2LastErrorExtraKey  = "openai_native_compaction_v2_last_error"
)

func normalizeAccountTestMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case AccountTestModeCompact:
		return AccountTestModeCompact
	case AccountTestModeLegacyCompact:
		return AccountTestModeLegacyCompact
	default:
		return AccountTestModeDefault
	}
}

// createOpenAICompactProbePayload 构造原生 V2 的流式 Responses 请求。V2 的关键
// 契约是最后一个 input 为 compaction_trigger，而非旧端点路径。
func createOpenAICompactProbePayload(model string, isOAuth bool) map[string]any {
	payload := map[string]any{
		"model":        strings.TrimSpace(model),
		"instructions": "You are a helpful coding assistant.",
		"input": []any{
			map[string]any{
				"type":    "message",
				"role":    "user",
				"content": "Respond with OK.",
			},
			map[string]any{"type": "compaction_trigger"},
		},
		"stream": true,
	}
	if isOAuth {
		// ChatGPT 内部 Responses API 与正常 OAuth 转发保持相同的 store 约束。
		payload["store"] = false
	}
	return payload
}

// createOpenAILegacyCompactProbePayload 保留旧端点的 unary 载荷形状。它只用于
// 管理员显式兼容性测试，绝不能作为原生 V2 的能力依据。
func createOpenAILegacyCompactProbePayload(model string) map[string]any {
	return map[string]any{
		"model":        strings.TrimSpace(model),
		"instructions": "You are a helpful coding assistant.",
		"input": []any{
			map[string]any{
				"type":    "message",
				"role":    "user",
				"content": "Respond with OK.",
			},
		},
	}
}

// openAICompactProbeFoundCompactionItem 确认原生 V2 响应确实给出了 compaction
// item。单纯 2xx 可能表示中间链路吞掉 trigger，不能误判为 V2 可用。
func openAICompactProbeFoundCompactionItem(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	bodyText := string(body)
	if _, found := findRawCompactionItemFromSSE(bodyText); found {
		return true
	}
	if finalResponse, ok := extractCodexFinalResponse(bodyText); ok && responsesOutputHasCompactionItem(finalResponse) {
		return true
	}
	return responsesOutputHasCompactionItem(body)
}

func mergeExtraUpdates(base map[string]any, more map[string]any) map[string]any {
	if len(base) == 0 && len(more) == 0 {
		return nil
	}
	out := make(map[string]any, len(base)+len(more))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range more {
		out[key] = value
	}
	return out
}

func compactProbeSessionID(accountID int64) string {
	if accountID <= 0 {
		return deriveStableUUIDv4("tokenrouter:openai-native-compaction-v2-probe:anonymous")
	}
	return deriveStableUUIDv4("tokenrouter:openai-native-compaction-v2-probe:" + strconv.FormatInt(accountID, 10))
}

// legacyCompactProbeSessionID 保持旧端点既有会话格式，避免兼容性测试本身改变
// legacy 上游对请求形状的识别。
func legacyCompactProbeSessionID(accountID int64) string {
	if accountID <= 0 {
		return "probe_compact"
	}
	return "probe_compact_" + strconv.FormatInt(accountID, 10)
}
