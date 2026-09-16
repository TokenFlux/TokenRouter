// 原流报文解析与重建保持逐次调用和原未知字段；不执行 I/O 或读取平台账号。
package bridge

import (
	"encoding/json"
	"sort"
	"strings"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
) // ResponsesStreamOutputItems 按 output_index 记录每个
// response.output_item.done 事件携带的原始 item。
//
// ReconstructResponseOutputFromSSE 重建缓冲响应时已经优先使用 done item，而不是
// delta 累积结果，因为累积器只建模“一个 reasoning、一个 message、N 个 function
// call”，无法保留 item 身份、逐项 status/phase、顺序或未知 item 类型。流式路径
// 无法一次看到完整正文，这个收集器为它提供同等能力。
type ResponsesStreamOutputItems struct {
	items map[int]json.RawMessage
}

func NewResponsesStreamOutputItems() *ResponsesStreamOutputItems {
	return &ResponsesStreamOutputItems{items: make(map[int]json.RawMessage)}
}

// Observe 原样记录 response.output_item.done 事件中的 item。保留原始 JSON 字节，
// 使厂商扩展字段和未来字段在重建时不丢失。
func (r *ResponsesStreamOutputItems) Observe(data []byte) {
	if r == nil || len(data) == 0 || !gjson.ValidBytes(data) {
		return
	}
	if strings.TrimSpace(gjson.GetBytes(data, "type").String()) != "response.output_item.done" {
		return
	}
	item := gjson.GetBytes(data, "item")
	if !item.Exists() || !item.IsObject() {
		return
	}
	index := int(gjson.GetBytes(data, "output_index").Int())
	r.items[index] = json.RawMessage(append([]byte(nil), item.Raw...))
}

func (r *ResponsesStreamOutputItems) HasItems() bool {
	return r != nil && len(r.items) > 0
}

// Count 返回流中报告为 done 的不同 output item 数量。
func (r *ResponsesStreamOutputItems) Count() int {
	if r == nil {
		return 0
	}
	return len(r.items)
}

// BuildOutput 按 output_index 排序返回已记录的 item。
func (r *ResponsesStreamOutputItems) BuildOutput() ([]byte, bool) {
	if !r.HasItems() {
		return nil, false
	}
	indexes := make([]int, 0, len(r.items))
	for index := range r.items {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	ordered := make([]json.RawMessage, 0, len(indexes))
	for _, index := range indexes {
		ordered = append(ordered, r.items[index])
	}
	encoded, err := json.Marshal(ordered)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

func NormalizeResponsesStreamingTerminalOutput(data []byte, acc *BufferedResponseAccumulator, doneItems *ResponsesStreamOutputItems, imageOutputs []json.RawMessage) ([]byte, bool) {
	eventType := strings.TrimSpace(gjson.GetBytes(data, "type").String())
	switch eventType {
	case "response.completed", "response.done", "response.incomplete", "response.cancelled", "response.canceled":
	default:
		return data, false
	}

	output := gjson.GetBytes(data, "response.output")
	hasAccumulatedOutput := (acc != nil && acc.HasContent()) || len(imageOutputs) > 0 || doneItems.HasItems()
	if output.Exists() && output.IsArray() {
		terminalCount := len(output.Array())
		// 终止 output 至少包含流中报告数量的 item 时保持不变；数量更少表示终止事件
		// 丢弃了流中已经报告为 done 的 item，此时以已报告 item 作为本轮权威记录。
		if terminalCount > 0 && terminalCount >= doneItems.Count() {
			return data, false
		}
		if terminalCount == 0 && !hasAccumulatedOutput {
			return data, false
		}
	}

	outputJSON := []byte("[]")
	// 与 ReconstructResponseOutputFromSSE 保持相同优先级：流实际报告的 item 优先于
	// delta 重建结果。图片生成 item 也会以 done 事件到达，因此此处不能再拼接 imageOutputs。
	if reconstructed, ok := doneItems.BuildOutput(); ok {
		outputJSON = reconstructed
	} else if reconstructed, ok := BuildResponsesOutputJSON(acc, imageOutputs); ok {
		outputJSON = reconstructed
	}
	updated, err := sjson.SetRawBytes(data, "response.output", outputJSON)
	if err != nil {
		return data, false
	}
	return updated, true
}

// CollectRawResponsesOutputItemsFromSSE 按到达顺序收集 SSE 流中
// response.output_item.done 携带的原始 item。除已产生结果但仍停留在进行中
// 的图片状态外，item 以 raw JSON 逐字节保留，
// 避免经窄结构体重建时丢弃 encrypted_content/summary/opaque 等 compact
// 专属或未来新增字段（#3777 问题 2）。若整条流没有任何 done 事件，退回
// 收集 output_item.added 中的 compaction 类 item——compaction 结果没有
// delta 事件，部分上游只在 added 事件中携带完整 item。
func CollectRawResponsesOutputItemsFromSSE(bodyText string) ([]byte, bool) {
	var items []json.RawMessage
	seen := make(map[string]struct{})
	hasCompactionItem := false
	appendItem := func(item gjson.Result) {
		if !item.Exists() || !item.IsObject() {
			return
		}
		key := strings.TrimSpace(item.Get("id").String())
		if key == "" {
			key = item.Raw
		}
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		if protocolopenai.IsResponsesCompactionItemType(item.Get("type").String()) {
			hasCompactionItem = true
		}
		items = append(items, json.RawMessage(item.Raw))
	}
	protocolopenai.ForEachSSEDataPayload(bodyText, func(data []byte) {
		if normalized, changed := protocolopenai.NormalizeCompletedImageGenerationStatus(data); changed {
			data = normalized
		}
		if strings.TrimSpace(gjson.GetBytes(data, "type").String()) != "response.output_item.done" {
			return
		}
		appendItem(gjson.GetBytes(data, "item"))
	})
	// done 事件未携带 compaction item 时再看 added：覆盖"其他 item 有 done、
	// compaction 只在 added 中"的混合形态；done 已含 compaction 时跳过，
	// 避免同一 item 在无 id 可去重时被收集两份（Codex 要求恰好一个）。
	if !hasCompactionItem {
		protocolopenai.ForEachSSEDataPayload(bodyText, func(data []byte) {
			if strings.TrimSpace(gjson.GetBytes(data, "type").String()) != "response.output_item.added" {
				return
			}
			item := gjson.GetBytes(data, "item")
			if !protocolopenai.IsResponsesCompactionItemType(item.Get("type").String()) {
				return
			}
			appendItem(item)
		})
	}
	if len(items) == 0 {
		return nil, false
	}
	outputJSON, err := json.Marshal(items)
	if err != nil {
		return nil, false
	}
	return outputJSON, true
}

// ReconstructResponseOutputFromSSE 扫描原始 SSE 正文，为 output 为空的终止
// 事件重建 JSON output 数组。优先使用 output_item.done 中的原始条目，因为
// Responses 协议将其定义为条目的最终形态；delta 累加仅覆盖文本、函数调用和
// reasoning 内容，会静默丢弃 compaction 等未知类型，进而导致 Codex remote
// compact v2 报告没有拿到唯一的 compaction 条目（#3887）。无法重建时返回
// (nil, false)。
func ReconstructResponseOutputFromSSE(bodyText string) ([]byte, bool) {
	if outputJSON, ok := CollectRawResponsesOutputItemsFromSSE(bodyText); ok {
		return outputJSON, true
	}
	acc := NewBufferedResponseAccumulator()
	imageOutputs := make([]json.RawMessage, 0, 1)
	seenImages := make(map[string]struct{})
	protocolopenai.ForEachSSEDataPayload(bodyText, func(data []byte) {
		if imageOutput, ok := protocolopenai.ExtractImageGenerationOutputFromSSEData(data, seenImages); ok {
			imageOutputs = append(imageOutputs, imageOutput)
		}
		eventType := strings.TrimSpace(gjson.GetBytes(data, "type").String())
		if protocolopenai.ResponsesStreamEventMayContributeToOutput(eventType) {
			var event protocolopenai.ResponsesStreamEvent
			if err := json.Unmarshal(data, &event); err == nil {
				acc.ProcessEvent(&event)
			}
		}
	})
	return BuildResponsesOutputJSON(acc, imageOutputs)
}

func BuildResponsesOutputJSON(acc *BufferedResponseAccumulator, imageOutputs []json.RawMessage) ([]byte, bool) {
	if (acc == nil || !acc.HasContent()) && len(imageOutputs) == 0 {
		return nil, false
	}

	var output []json.RawMessage
	if acc != nil && acc.HasContent() {
		outputJSON, err := json.Marshal(acc.BuildOutput())
		if err != nil {
			return nil, false
		}
		if err := json.Unmarshal(outputJSON, &output); err != nil {
			return nil, false
		}
	}
	output = append(output, imageOutputs...)

	outputJSON, err := json.Marshal(output)
	if err != nil {
		return nil, false
	}
	return outputJSON, true
}
