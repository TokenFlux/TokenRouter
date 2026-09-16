// 本文件按最终 beta 净化协议字段，未知其它字段保持原样。
package anthropic

import (
	"strings"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// AnthropicBetaContextManagementToken 是 context_management 字段受的 beta token。
// 与 BetaContextManagement 保持一致；在本文件本地定义以避免震荡
// claude package 的该常量含义。
const AnthropicBetaContextManagementToken = "context-management-2025-06-27"

// SanitizeAnthropicBodyForBetaTokens 是对 Anthropic 直连路径上 body↔beta header
// **能力维度**对称约束的统一实现，与 Bedrock 路径的
// `sanitizeBedrockFieldsForBetaTokens` 对称。
//
// 问题场景：
//   - context_management 是 Claude Code CLI 2.1.87+ 默认携带的 beta 字段
//     （含 clear_thinking_20251015 等清理策略）
//   - 其被 Anthropic 上游接受的前提是 anthropic-beta header 含
//     `context-management-2025-06-27`
//   - 若两侧不一致上游 Pydantic schema 拒收：
//     "context_management: Extra inputs are not permitted"
//
// fallbacks 场景（与 context_management 同构）：
//   - `fallbacks` / `fallback_credit_token` 是 beta Messages API 的
//     server-side refusal fallback 字段；标准 Messages schema 没有它们，
//     客户端（Claude Code / SDK / OpenCode 等）最近开始默认透传
//     `"fallbacks":"default"`（或模型列表）
//   - 上游接受的前提是 anthropic-beta 含 `server-side-fallback-2026-07-01`
//     （fallback_credit_token 额外接受 credit beta，见下）
//   - 缺 token 时上游 Pydantic extra='forbid' 拒收：
//     "fallbacks: Extra inputs are not permitted"
//   - 本仓不写入该字段，全部来自客户端透传；OAuth mimic 用
//     FullClaudeCodeMimicryBetas 覆盖客户端 beta（该列表不含 fallback beta），
//     若不 strip，body 字段与 header 不对称 → 所有模型 400
//
// 本函数按最终发送的 anthropic-beta header 决定是否保留 body 中的上述字段：
// 缺对应 beta token → strip；客户端 header 已带对应 beta → 保留（不过度删除）。
// 这将限制完全建立在 "能力维度" 上，与 model 名 / token type / mimicry 子路径无关。
//
// 调用约束：必须在生成上游请求前调用，确保 body 与最终 anthropic-beta
// header 表达的能力集合一致。
//
// 返回 (sanitized, changed)：changed 表示是否发生实际删除，供调用方决定
// 是否重用原 body 引用。
func SanitizeAnthropicBodyForBetaTokens(body []byte, anthropicBetaHeader string) ([]byte, bool) {
	if len(body) == 0 {
		return body, false
	}
	updated := body
	changed := false
	if gjson.GetBytes(updated, "context_management").Exists() &&
		!AnthropicBetaTokensContains(anthropicBetaHeader, AnthropicBetaContextManagementToken) {
		if b, err := sjson.DeleteBytes(updated, "context_management"); err == nil {
			updated = b
			changed = true
		} else {
			logger.LegacyPrintf("service.gateway", "[CtxMgmtSanitize] 删除 context_management 失败: %v (body len=%d)", err, len(body))
		}
	}
	// Claude Fast 要求 speed 与 beta token 同时存在；系统过滤 beta 后必须同步删除 speed。
	if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(updated, "speed").String()), "fast") &&
		!AnthropicBetaTokensContains(anthropicBetaHeader, BetaFastMode) {
		if b, err := sjson.DeleteBytes(updated, "speed"); err == nil {
			updated = b
			changed = true
		} else {
			logger.LegacyPrintf("service.gateway", "[FastModeSanitize] 删除 speed 失败: %v (body len=%d)", err, len(body))
		}
	}
	// fallbacks 与 fallback_credit_token 依赖对应 beta；缺失时删除，避免上游 schema 拒绝。
	if b, deleted := StripAnthropicBodyFieldUnlessBeta(updated, "fallbacks", anthropicBetaHeader, BetaServerSideFallback); deleted {
		updated, changed = b, true
	}
	if b, deleted := StripAnthropicBodyFieldUnlessBeta(updated, "fallback_credit_token", anthropicBetaHeader, BetaServerSideFallback, BetaFallbackCredit, BetaFallbackCreditLegacy); deleted {
		updated, changed = b, true
	}
	return updated, changed
}

// StripAnthropicBodyFieldUnlessBeta 当 body 字段存在且 header 缺少任一 required token 时删除字段。
func StripAnthropicBodyFieldUnlessBeta(body []byte, field, anthropicBetaHeader string, requiredTokens ...string) ([]byte, bool) {
	if !gjson.GetBytes(body, field).Exists() {
		return body, false
	}
	for _, token := range requiredTokens {
		if AnthropicBetaTokensContains(anthropicBetaHeader, token) {
			return body, false
		}
	}
	b, err := sjson.DeleteBytes(body, field)
	if err != nil {
		logger.LegacyPrintf("service.gateway", "[BetaFieldSanitize] 删除 %s 失败: %v (body len=%d)", field, err, len(body))
		return body, false
	}
	return b, true
}

// AnthropicBetaTokensContains 检测逗号分隔的 anthropic-beta header 是否含指定 token。
// 宋体空格宽容；区分大小写（Anthropic beta token 始终是小写）。
func AnthropicBetaTokensContains(header, token string) bool {
	if header == "" || token == "" {
		return false
	}
	for _, part := range strings.Split(header, ",") {
		if strings.TrimSpace(part) == token {
			return true
		}
	}
	return false
}
