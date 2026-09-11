package openai

import (
	"encoding/json"
	"strings"
)

// MarshalJSON 将 ResponsesStreamEvent 渲染成实际传输的 wire 结构。
//
// OpenAI Responses 流式协议要求某些字段即使为零值也必须存在：
// output_index/content_index/summary_index 的 0 是有效索引；function_call
// item 必须始终带 call_id/name/arguments（arguments 可以是空字符串）；
// message item 必须带 content:[]；output_text part 必须带
// text/annotations/logprobs。Go 的 `omitempty` 会刚好丢掉这些零值，严格的
// Codex CLI 会拒绝缺少必需字段的 item/delta。
//
// 这里不再依赖 omitempty 后补 JSON，而是显式构造每一种流式事件，作为
// Responses SSE 字段存在性的单一来源，并统一作用于 Chat→Responses 桥和
// Anthropic→Responses 转换器。
//
// 未列出的 event type 继续走默认结构体序列化，限制该方法的影响范围。
func (e ResponsesStreamEvent) MarshalJSON() ([]byte, error) {
	switch e.Type {
	case "response.output_text.delta", "response.output_text.done":
		m := e.wireBase()
		e.putItemID(m)
		m["output_index"] = e.OutputIndex
		m["content_index"] = e.ContentIndex
		if e.Type == "response.output_text.done" {
			m["text"] = e.Text
		} else {
			m["delta"] = e.Delta
		}
		return json.Marshal(m)

	case "response.content_part.added", "response.content_part.done":
		m := e.wireBase()
		e.putItemID(m)
		m["output_index"] = e.OutputIndex
		m["content_index"] = e.ContentIndex
		m["part"] = outputTextPartWire(e.Part)
		return json.Marshal(m)

	case "response.reasoning_summary_text.delta", "response.reasoning_summary_text.done":
		m := e.wireBase()
		e.putItemID(m)
		m["output_index"] = e.OutputIndex
		m["summary_index"] = e.SummaryIndex
		if e.Type == "response.reasoning_summary_text.done" {
			m["text"] = e.Text
		} else {
			m["delta"] = e.Delta
		}
		return json.Marshal(m)

	case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		m := e.wireBase()
		e.putItemID(m)
		m["output_index"] = e.OutputIndex
		m["summary_index"] = e.SummaryIndex
		m["part"] = summaryTextPartWire(e.Part)
		return json.Marshal(m)

	case "response.output_item.added", "response.output_item.done":
		m := e.wireBase()
		m["output_index"] = e.OutputIndex
		m["item"] = responsesItemWire(e.Item)
		return json.Marshal(m)

	case "response.function_call_arguments.delta", "response.function_call_arguments.done":
		m := e.wireBase()
		e.putItemID(m)
		m["output_index"] = e.OutputIndex
		if e.CallID != "" {
			m["call_id"] = e.CallID
		}
		if e.Name != "" {
			m["name"] = e.Name
		}
		if e.Type == "response.function_call_arguments.done" {
			m["arguments"] = e.Arguments
		} else {
			m["delta"] = e.Delta
		}
		return json.Marshal(m)

	case "response.custom_tool_call_input.delta", "response.custom_tool_call_input.done":
		m := e.wireBase()
		e.putItemID(m)
		m["output_index"] = e.OutputIndex
		if e.CallID != "" {
			m["call_id"] = e.CallID
		}
		if e.Name != "" {
			m["name"] = e.Name
		}
		if e.Type == "response.custom_tool_call_input.done" {
			m["input"] = e.Input
		} else {
			m["delta"] = e.Delta
		}
		return json.Marshal(m)

	default:
		// response.created / completed / done / failed / incomplete 等未显式
		// 建模的事件继续保留默认结构体序列化。
		type alias ResponsesStreamEvent
		return json.Marshal(alias(e))
	}
}

func (e ResponsesStreamEvent) wireBase() map[string]any {
	m := map[string]any{
		"type":            e.Type,
		"sequence_number": e.SequenceNumber,
	}
	return m
}

func (e ResponsesStreamEvent) putItemID(m map[string]any) {
	if e.ItemID != "" {
		m["item_id"] = e.ItemID
	}
}

// outputTextPartWire 渲染 message output_text content part，并始终带上
// text/annotations/logprobs。
func outputTextPartWire(part *ResponsesContentPart) map[string]any {
	text := ""
	if part != nil {
		text = part.Text
	}
	return map[string]any{
		"type":        "output_text",
		"text":        text,
		"annotations": []any{},
		"logprobs":    []any{},
	}
}

// summaryTextPartWire 渲染 reasoning summary part。
func summaryTextPartWire(part *ResponsesContentPart) map[string]any {
	text := ""
	if part != nil {
		text = part.Text
	}
	return map[string]any{
		"type": "summary_text",
		"text": text,
	}
}

// responsesItemWire 渲染 output_item，并按 item type 补齐必需字段，包括
// omitempty 原本会丢掉的空数组和空字符串。
func responsesItemWire(item *ResponsesOutput) map[string]any {
	if item == nil {
		return map[string]any{}
	}
	m := map[string]any{
		"type": item.Type,
		"id":   item.ID,
	}
	if item.Status != "" {
		m["status"] = item.Status
	}
	switch item.Type {
	case "message":
		role := item.Role
		if role == "" {
			role = "assistant"
		}
		m["role"] = role
		m["content"] = messageContentWire(item.Content)
	case "reasoning":
		m["summary"] = reasoningSummaryWire(item.Summary)
		if item.EncryptedContent != "" {
			m["encrypted_content"] = item.EncryptedContent
		}
	case "function_call":
		m["call_id"] = item.CallID
		m["name"] = item.Name
		m["arguments"] = item.Arguments
		// namespace 子工具的还原调用：codex 按 namespace+name 路由，缺少该字段
		// 会被判为 unsupported call。
		if item.Namespace != "" {
			m["namespace"] = item.Namespace
		}
	case "custom_tool_call":
		// custom/freeform 工具调用（如 codex 的 exec）：input 为自由文本。缺少
		// call_id/name 时 codex 无法路由该调用（表现为 unsupported call）。
		m["call_id"] = item.CallID
		m["name"] = item.Name
		m["input"] = item.Input
	case "tool_search_call":
		// tool_search 调用还原项：execution 必须为 "client"（否则 codex 忽略该
		// 调用），arguments 在线上是 JSON 对象而非字符串。
		m["call_id"] = item.CallID
		m["execution"] = "client"
		m["arguments"] = ToolSearchCallArgumentsJSON(item.Arguments)
	}
	return m
}

// messageContentWire 渲染 message item 的 content 数组；结果永远是数组而非 null。
func messageContentWire(parts []ResponsesContentPart) []map[string]any {
	out := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		typ := p.Type
		if typ == "" {
			typ = "output_text"
		}
		out = append(out, map[string]any{"type": typ, "text": p.Text})
	}
	return out
}

// reasoningSummaryWire 渲染 reasoning item 的 summary 数组；结果永远是数组。
func reasoningSummaryWire(summary []ResponsesSummary) []map[string]any {
	out := make([]map[string]any, 0, len(summary))
	for _, s := range summary {
		typ := s.Type
		if typ == "" {
			typ = "summary_text"
		}
		out = append(out, map[string]any{"type": typ, "text": s.Text})
	}
	return out
}

// ToolSearchCallArgumentsJSON 把降级 function 调用累积的 arguments 字符串还原为
// tool_search_call 线上要求的 JSON 对象；模型未按 schema 输出（非法 JSON）时按
// 字符串值兜底，交由 codex 解析报错后让模型重试。
func ToolSearchCallArgumentsJSON(arguments string) json.RawMessage {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	fallback, _ := json.Marshal(arguments)
	return fallback
}
