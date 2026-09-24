package provider

import (
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
)

// ShouldFlattenOpenAIResponsesNamespaces 判定原生 Responses 转发前是否摊平
// Codex namespace 工具。OAuth 普通 Responses 默认保留 namespace，避免破坏模型按
// functions.<namespace>.<tool> 寻址的约定；compact 端点及账号兼容开关保持旧行为。
// WSv2 出口不经过 HTTP 回程还原，因此始终保持 namespace 原样。
func ShouldFlattenOpenAIResponsesNamespaces(
	account *ExecutionAccount,
	transport egress.OpenAIUpstreamTransport,
	passthroughEnabled bool,
	compactPath bool,
) bool {
	if account == nil || !account.View().IsOpenAIOAuthLike() {
		return false
	}
	if !compactPath && !account.View().IsOpenAIResponsesFlattenNamespacesEnabled() {
		return false
	}
	if transport == egress.OpenAIUpstreamTransportResponsesWebsocketV2 && !passthroughEnabled {
		return false
	}
	return true
}

// ShouldStripOpenAIResponsesInputNamespaces 判定 HTTP 转发是否清理历史项的 namespace。
// 原生 WSv2 保留该字段，并沿用不做 HTTP 回程恢复的规则。
func ShouldStripOpenAIResponsesInputNamespaces(account *ExecutionAccount, transport egress.OpenAIUpstreamTransport, passthroughEnabled bool) bool {
	if account == nil || (!account.View().IsOpenAIOAuthLike() && !account.View().IsOpenAIApiKey()) {
		return false
	}
	if transport == egress.OpenAIUpstreamTransportResponsesWebsocketV2 && !passthroughEnabled {
		return false
	}
	return true
}

// ShouldKeepOpenAIResponsesToolCallNamespaces 判定清理 input 残留 namespace 时是否
// 保留工具调用项上的 namespace。
//
// 上游对这个字段有两套互斥要求，判定按「出口 + 端点」而非工具声明内容：
//   - /backend-api/codex/responses 会按 namespace 解析历史调用，缺字段直接 400
//     `Missing namespace for function_call '...'. Round-trip the model's
//     function_call item with its namespace field included.`（issue #4761 回帖），
//     故 OAuth 非 compact 请求必须保留。
//   - compact 端点的 schema 不含该字段，携带即 400 `Unknown parameter:
//     input[N].namespace`（issue #4761 正文），故 compact 一律清理。
//   - API Key 出口默认按标准 Responses API 处理并清理该字段；但当请求本身声明
//     namespace 工具时，上游显然使用了 namespace 扩展，此时必须保留调用项上的
//     namespace，否则声明与历史调用会失配并触发 Missing namespace。
//   - 摊平模式下调用项已被改写成平名，残留 namespace 指向的声明已不存在，一律清理。
func ShouldKeepOpenAIResponsesToolCallNamespaces(
	account *ExecutionAccount,
	transport egress.OpenAIUpstreamTransport,
	passthroughEnabled bool,
	compactPath bool,
	body []byte,
) bool {
	if account == nil {
		return false
	}
	if compactPath {
		return false
	}
	if account.View().IsOpenAIApiKey() {
		return protocolbridge.HasOpenAIResponsesNamespaceToolDeclaration(body)
	}
	if !account.View().IsOpenAIOAuthLike() {
		return false
	}
	return !ShouldFlattenOpenAIResponsesNamespaces(account, transport, passthroughEnabled, compactPath)
}
