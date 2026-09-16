// Chat 原生分片、工具身份和终态解析保持独立，不引入账号或取消策略。
package openai

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// EnsureOpenAIChatStreamUsage 确保 raw Chat Completions 流式请求会让上游返回 usage。
// usage 也会继续向下游透传，支持级联代理和下游计费系统。
func EnsureOpenAIChatStreamUsage(body []byte) ([]byte, error) {
	updated, err := sjson.SetBytes(body, "stream_options.include_usage", true)
	if err != nil {
		return body, err
	}
	return updated, nil
}

func IsOpenAIChatUsageOnlyStreamChunk(payload string) bool {
	if strings.TrimSpace(payload) == "" {
		return false
	}
	if !gjson.Get(payload, "usage").Exists() {
		return false
	}
	choices := gjson.Get(payload, "choices")
	return choices.Exists() && choices.IsArray() && len(choices.Array()) == 0
}

// ExtractCCStreamUsage 从单个 CC 流式 chunk 的 payload 中提取 usage 字段。
// CC 协议中 usage 仅出现在末尾 chunk（且仅当 include_usage 生效时），
// 但上游可能在多个 chunk 中重复——总是用最新值。
func ExtractCCStreamUsage(payload string) *ForwardUsage {
	usageResult := gjson.Get(payload, "usage")
	if !usageResult.Exists() || !usageResult.IsObject() {
		return nil
	}
	u, ok := OpenAIUsageFromGJSON(usageResult)
	if !ok {
		return nil
	}
	return &u
}

func ChatChunkStartsResponsesOutput(chunk *ChatCompletionsChunk) bool {
	if chunk == nil {
		return false
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != nil || choice.Delta.ReasoningText() != nil || len(choice.Delta.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

// ChatCompletionsChunkHasToolCallDelta 判断当前分片是否携带工具调用增量。
func ChatCompletionsChunkHasToolCallDelta(chunk *ChatCompletionsChunk) bool {
	if chunk == nil {
		return false
	}
	for _, choice := range chunk.Choices {
		if len(choice.Delta.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

// OpenAIChatCompletionServiceTierEventType 为 Chat Completions 的结束 chunk
// 补出终止事件语义，使终态实际档位可以覆盖早期请求档位回显。
func OpenAIChatCompletionServiceTierEventType(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	if IsOpenAIChatUsageOnlyStreamChunk(string(payload)) {
		return "response.completed"
	}
	for _, choice := range gjson.GetBytes(payload, "choices").Array() {
		if strings.TrimSpace(choice.Get("finish_reason").String()) != "" {
			return "response.completed"
		}
	}
	return ""
}

// StripEmptyChatToolCallIdentityFromSSELine 从 CC 流式 SSE 行中剔除
// choices[*].delta.tool_calls[*] 上的空 id / 空 function.name 字段。
//
// DashScope/DeepSeek 等 OpenAI 兼容上游会把同一个 tool_calls[index]
// 拆成多个 delta：首个 delta 带合法 id + function.name（arguments 可能
// 为空），后续参数 delta 带 id:"" 与 function.name:"" 只追加 arguments
// 碎片。dsh 等客户端按 `!== undefined` 合并字段，空串会被当成有效值
// 覆盖首包合法 id/name，最终得到 {"id":"","name":"",...} 导致
// ToolNotFoundError: unknown tool ""。这里无状态剔除空串字段（缺失
// 即不覆盖），不补写、不记忆首包 id/name，适用于所有走 raw CC 直转
// 路径的账号（不限定 DeepSeek）。
//
// 只处理流式 chunk 的 delta.tool_calls；非流式 message.tool_calls 不属于
// 本 helper 范围。
func StripEmptyChatToolCallIdentityFromSSELine(line string) string {
	payload, ok := ExtractSSEDataLine(line)
	if !ok {
		return line
	}
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" || trimmed == "[DONE]" {
		return line
	}
	rewritten, changed := StripEmptyChatToolCallIdentity([]byte(payload))
	if !changed {
		return line
	}
	prefixLen := len(line) - len(payload)
	if prefixLen < 0 {
		return line
	}
	return line[:prefixLen] + string(rewritten)
}

// StripEmptyChatToolCallIdentity 从单个 CC 流式 chunk payload 中删除
// choices[*].delta.tool_calls[*] 上存在但为空字符串的 id 与
// function.name 字段；arguments（即使是空串）、index、type 与其它字段
// 一律保留，非空 id/name 不动。多 choice、多 index 都会处理。
//
// 返回 (原始 payload, false) 当：payload 为空、不含 "tool_calls"、
// 非法 JSON、无 choices / delta / tool_calls 数组、或没有需要删除的
// 字段。sjson 删除失败时 fail-closed 返回原始 payload。
func StripEmptyChatToolCallIdentity(payload []byte) ([]byte, bool) {
	if len(payload) == 0 {
		return payload, false
	}
	// 热路径快速失败：绝大多数 chunk 没有 tool_calls。
	if !bytes.Contains(payload, []byte("tool_calls")) {
		return payload, false
	}
	if !gjson.ValidBytes(payload) {
		return payload, false
	}
	choices := gjson.GetBytes(payload, "choices")
	if !choices.Exists() || !choices.IsArray() {
		return payload, false
	}
	updated := payload
	changed := false
	for ci, choice := range choices.Array() {
		delta := choice.Get("delta")
		if !delta.Exists() || !delta.IsObject() {
			continue
		}
		toolCalls := delta.Get("tool_calls")
		if !toolCalls.Exists() || !toolCalls.IsArray() {
			continue
		}
		for ti, tc := range toolCalls.Array() {
			if id := tc.Get("id"); id.Exists() && id.Type == gjson.String && id.Str == "" {
				next, err := sjson.DeleteBytes(updated, "choices."+strconv.Itoa(ci)+".delta.tool_calls."+strconv.Itoa(ti)+".id")
				if err != nil {
					return payload, false
				}
				updated = next
				changed = true
			}
			if name := tc.Get("function.name"); name.Exists() && name.Type == gjson.String && name.Str == "" {
				next, err := sjson.DeleteBytes(updated, "choices."+strconv.Itoa(ci)+".delta.tool_calls."+strconv.Itoa(ti)+".function.name")
				if err != nil {
					return payload, false
				}
				updated = next
				changed = true
			}
		}
	}
	if !changed {
		return payload, false
	}
	return updated, true
}

// RawStreamTerminalState 记录 raw Chat Completions SSE 流是否收到过
// **终止信号**。
//
// 背景：CC 直转路径把上游 SSE 原样透传，此前只要 HTTP 状态是 200 就按成功收尾——
// 上游中途断流（Cloudflare edge reset、后端 worker 掉线）会被伪装成
// `HTTP 200 + usage 0/0`：客户端拿到半截回答，网关既不报错也不计入 SLA，
// Ops 侧完全不可见。
//
// 三种终止信号任一出现即认为上游"讲完了"，只是尾巴可能丢失，不作截断处理：
//
//   - [DONE]        —— OpenAI CC 协议标准哨兵
//   - usage chunk   —— include_usage 生效时的末尾用量帧（网关强制打开）
//   - finish_reason —— 生成正常结束（stop/length/tool_calls/...）
//
// 只认 [DONE] 会误伤那些跑完最后一帧就直接 EOF 的兼容上游；只认 usage 会误伤
// 不支持 include_usage 的上游。三者取并集，把误判压到"上游确实在生成中途被切断"。
type RawStreamTerminalState struct {
	// sawDataLine 表示上游至少发过一行 `data:`，即响应确实是 SSE 语义流。
	// 非 SSE 响应体（上游对 stream 请求返回裸 JSON）不参与截断判定，保持既有透传行为。
	sawDataLine     bool
	sawDone         bool
	sawUsage        bool
	sawFinishReason bool
}

// ObserveDataLine 从单行 SSE `data:` 载荷中提取终止信号。payload 需已 TrimSpace。
func (t *RawStreamTerminalState) ObserveDataLine(payload string) {
	if t == nil {
		return
	}
	t.sawDataLine = true
	if payload == "[DONE]" {
		t.sawDone = true
		return
	}
	if usage := gjson.Get(payload, "usage"); usage.Exists() && usage.IsObject() {
		t.sawUsage = true
	}
	if t.sawFinishReason {
		return
	}
	for _, choice := range gjson.Get(payload, "choices").Array() {
		// finish_reason 为 null 时 String() 返回空串，不算终止。
		if strings.TrimSpace(choice.Get("finish_reason").String()) != "" {
			t.sawFinishReason = true
			return
		}
	}
}

// Terminated 表示上游给出过终止信号。
func (t *RawStreamTerminalState) Terminated() bool {
	return t != nil && (t.sawDone || t.sawUsage || t.sawFinishReason)
}

// IsTruncated 判定上游是否在任何终止信号之前结束。clientOutputStarted 用于放行
// 非 SSE 响应体：那类响应本就没有 data: 行，既有行为是原样透传，不在本次判定范围内；
// 但"一个字节都没收到"的空 200 依然算截断。
func (t *RawStreamTerminalState) IsTruncated(clientOutputStarted bool) bool {
	if t == nil || t.Terminated() {
		return false
	}
	return t.sawDataLine || !clientOutputStarted
}

// SawDataLine 提供原截断日志所需的只读观测。
func (t *RawStreamTerminalState) SawDataLine() bool { return t != nil && t.sawDataLine }
