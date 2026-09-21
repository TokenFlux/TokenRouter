package openai

import (
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// CompactionTestPayload 构造原生 V2 的流式 Responses 请求。V2 的关键
// 契约是最后一个 input 为 compaction_trigger，而非旧端点路径。
func CompactionTestPayload(model string, isOAuth bool) map[string]any {
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

// LegacyCompactionTestPayload 保留旧端点的 unary 载荷形状。它只用于
// 管理员显式兼容性测试，绝不能作为原生 V2 的能力依据。
func LegacyCompactionTestPayload(model string) map[string]any {
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

// CompactionTestHasOutput 确认原生 V2 响应确实给出了 compaction
// item。单纯 2xx 可能表示中间链路吞掉 trigger，不能将其报告为测试成功。
func CompactionTestHasOutput(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	bodyText := string(body)
	if _, found := openai.FindRawCompactionItemFromSSE(bodyText); found {
		return true
	}
	if finalResponse, ok := openai.ExtractCodexFinalResponse(bodyText); ok && openai.ResponsesOutputHasCompactionItem(finalResponse) {
		return true
	}
	return openai.ResponsesOutputHasCompactionItem(body)
}

func CompactionTestSessionID(accountID int64) string {
	// 保留既有会话标识，避免重命名内部函数改变上游对测试请求的处理。
	if accountID <= 0 {
		return DeriveStableUUIDv4("tokenrouter:openai-native-compaction-v2-probe:anonymous")
	}
	return DeriveStableUUIDv4("tokenrouter:openai-native-compaction-v2-probe:" + strconv.FormatInt(accountID, 10))
}

// LegacyCompactionTestSessionID 保持旧端点既有会话格式，避免兼容性测试本身改变
// legacy 上游对请求形状的识别。
func LegacyCompactionTestSessionID(accountID int64) string {
	if accountID <= 0 {
		return "probe_compact"
	}
	return "probe_compact_" + strconv.FormatInt(accountID, 10)
}
