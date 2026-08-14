package service

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// countGrokNativeSearchCallsFromJSONBytes 统计 Responses JSON 中已完成的原生搜索工具调用，
// 包括 web_search_call、x_search_call、tool_search_call 及同名 function/custom tool 调用。
func countGrokNativeSearchCallsFromJSONBytes(body []byte) int {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return 0
	}
	// 兼容层可能同时保留顶层 output 和 response.output；优先规范嵌套响应，避免重复计费。
	if nested := gjson.GetBytes(body, "response.output"); nested.IsArray() {
		return countGrokNativeSearchCallsInOutputArray(nested)
	}
	return countGrokNativeSearchCallsInOutputArray(gjson.GetBytes(body, "output"))
}

func countGrokNativeSearchCallsFromSSEBody(body string) int {
	if strings.TrimSpace(body) == "" {
		return 0
	}
	seen := make(map[string]struct{})
	total := 0
	forEachOpenAISSEDataPayload(body, func(data []byte) {
		total += countGrokNativeSearchCallsInSSEDataDedup(data, seen)
	})
	return total
}

// countGrokNativeSearchCallsInSSEData 统计单个 SSE payload，不跨事件去重；
// 实时流应使用 Dedup 版本，避免 item.done 与 response.completed 重复计费。
func countGrokNativeSearchCallsInSSEData(data []byte) int {
	n, _ := countGrokNativeSearchCallsInSSEDataWithKeys(data)
	return n
}

// countGrokNativeSearchCallsInSSEDataDedup 只累计未出现过的调用 ID，调用方必须在整个流中复用 seen。
// 缺少 call_id/id 时用类型、名称和序号构造合成键，避免跨事件重复计费。
func countGrokNativeSearchCallsInSSEDataDedup(data []byte, seen map[string]struct{}) int {
	if seen == nil {
		return countGrokNativeSearchCallsInSSEData(data)
	}
	n, keys := countGrokNativeSearchCallsInSSEDataWithKeys(data)
	if n <= 0 {
		return 0
	}
	// 优先使用稳定 ID，缺失时生成合成键，不能直接累加原始数量。
	if len(keys) < n {
		// 为所有条目重建键，使无 ID 条目也具备指纹。
		keys = collectGrokNativeSearchCallKeys(data)
	}
	if len(keys) == 0 {
		// n 大于零却没有键属于异常，按零附加费 fail-closed。
		return 0
	}
	added := 0
	local := make(map[string]struct{}, len(keys))
	isItemDone := strings.TrimSpace(gjson.GetBytes(data, "type").String()) == "response.output_item.done"
	for _, k := range keys {
		if k == "" {
			continue
		}
		if _, ok := local[k]; ok {
			continue
		}
		local[k] = struct{}{}
		if _, ok := seen[k]; ok {
			if !isItemDone || !strings.HasPrefix(k, "synth:") {
				continue
			}
			// 每个无 ID 的 item.done 都是独立完成调用，递增序号以准确结算中断流。
			separator := strings.LastIndexByte(k, ':')
			if separator < 0 {
				continue
			}
			base := k[:separator]
			for ordinal := 2; ; ordinal++ {
				candidate := base + ":" + strconv.Itoa(ordinal)
				if _, exists := seen[candidate]; !exists {
					k = candidate
					break
				}
			}
		}
		seen[k] = struct{}{}
		added++
	}
	return added
}

func collectGrokNativeSearchCallKeys(data []byte) []string {
	if len(data) == 0 || !gjson.ValidBytes(data) {
		return nil
	}
	// 空 type 表示没有 SSE envelope 的裸条目；其它非完成事件不包含可计费调用。
	switch strings.TrimSpace(gjson.GetBytes(data, "type").String()) {
	case "response.output_item.done", "response.completed", "response.done", "":
	default:
		return nil
	}
	var keys []string
	syntheticOrdinals := make(map[string]int)
	consider := func(item gjson.Result) {
		if !isGrokNativeSearchOutputItem(item) {
			return
		}
		key := firstNonEmpty(
			strings.TrimSpace(item.Get("call_id").String()),
			strings.TrimSpace(item.Get("id").String()),
			strings.TrimSpace(item.Get("item.call_id").String()),
			strings.TrimSpace(item.Get("item.id").String()),
		)
		if key == "" {
			// 同类调用加入序号，避免一个完成响应中的多个无 ID 搜索被 type:name 合并。
			base := "synth:" + strings.ToLower(strings.TrimSpace(item.Get("type").String())) +
				":" + strings.ToLower(strings.TrimSpace(item.Get("name").String()))
			syntheticOrdinals[base]++
			key = base + ":" + strconv.Itoa(syntheticOrdinals[base])
		}
		keys = append(keys, key)
	}
	if item := gjson.GetBytes(data, "item"); item.Exists() {
		consider(item)
	}
	gjson.GetBytes(data, "response.output").ForEach(func(_, item gjson.Result) bool {
		consider(item)
		return true
	})
	gjson.GetBytes(data, "output").ForEach(func(_, item gjson.Result) bool {
		consider(item)
		return true
	})
	if len(keys) == 0 && isGrokNativeSearchOutputItem(gjson.ParseBytes(data)) {
		consider(gjson.ParseBytes(data))
	}
	return keys
}

func countGrokNativeSearchCallsInSSEDataWithKeys(data []byte) (int, []string) {
	if len(data) == 0 || !gjson.ValidBytes(data) {
		return 0, nil
	}
	// 仅在条目或响应完成时计数，不在每个 delta 上累加；空 type 表示裸条目。
	switch strings.TrimSpace(gjson.GetBytes(data, "type").String()) {
	case "response.output_item.done", "response.completed", "response.done", "":
	default:
		return 0, nil
	}
	var keys []string
	n := 0
	consider := func(item gjson.Result) {
		if !isGrokNativeSearchOutputItem(item) {
			return
		}
		n++
		key := firstNonEmpty(
			strings.TrimSpace(item.Get("call_id").String()),
			strings.TrimSpace(item.Get("id").String()),
			strings.TrimSpace(item.Get("item.call_id").String()),
			strings.TrimSpace(item.Get("item.id").String()),
		)
		if key != "" {
			keys = append(keys, key)
		}
	}
	if item := gjson.GetBytes(data, "item"); item.Exists() {
		consider(item)
	}
	gjson.GetBytes(data, "response.output").ForEach(func(_, item gjson.Result) bool {
		consider(item)
		return true
	})
	gjson.GetBytes(data, "output").ForEach(func(_, item gjson.Result) bool {
		consider(item)
		return true
	})
	// 兼容没有嵌套 item 键的裸输出条目事件。
	if n == 0 && isGrokNativeSearchOutputItem(gjson.ParseBytes(data)) {
		consider(gjson.ParseBytes(data))
	}
	return n, keys
}

func countGrokNativeSearchCallsInOutputArray(output gjson.Result) int {
	if !output.IsArray() {
		return 0
	}
	count := 0
	output.ForEach(func(_, item gjson.Result) bool {
		if isGrokNativeSearchOutputItem(item) {
			count++
		}
		return true
	})
	return count
}

func isGrokNativeSearchOutputItem(item gjson.Result) bool {
	if !item.Exists() {
		return false
	}
	itemType := strings.ToLower(strings.TrimSpace(item.Get("type").String()))
	switch itemType {
	case "web_search_call", "x_search_call", "tool_search_call":
		return true
	case "function_call", "custom_tool_call":
		name := strings.ToLower(strings.TrimSpace(item.Get("name").String()))
		return name == "web_search" || name == "x_search" || name == "tool_search"
	default:
		return false
	}
}
