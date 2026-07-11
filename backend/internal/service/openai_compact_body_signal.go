package service

import "github.com/tidwall/gjson"

// HasCompactionTriggerInInput 检测 Codex remote compact v2 的请求体信号：
// input 中存在 type 为 "compaction_trigger" 的条目。客户端把它放在普通
// POST /v1/responses 而不是 POST /v1/responses/compact 中时，仍必须按
// compact 请求处理，否则上游路径、模型映射和请求体归一化都会出错，并导致
// Codex 收到非 compact 响应后报错：
//
//	"remote compaction v2 expected exactly one compaction output item, got 0"
//
// gateway handler 会在流式字段解析、compact 请求体归一化和账号调度之前改写
// URL path，让两种入站形态复用同一条 compact 链路。
func HasCompactionTriggerInInput(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return false
	}
	found := false
	input.ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() == "compaction_trigger" {
			found = true
			return false
		}
		return true
	})
	return found
}
