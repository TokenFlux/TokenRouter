package requeststate

import "github.com/tidwall/gjson"

// IsRealUserMessage 检测是否为真实用户消息（非 tool_result）
// 与 claude-relay-service 的检测逻辑一致：
// 1. messages 非空
// 2. 最后一条消息 role == "user"
// 3. 最后一条消息 content（如果是数组）中不含 type:"tool_result" / "tool_use_result"
func IsRealUserMessage(parsed *ParsedRequest) bool {
	if parsed == nil {
		return false
	}
	messagesRaw := parsed.MessagesRaw()
	if len(messagesRaw) == 0 {
		return false
	}

	messages := gjson.ParseBytes(messagesRaw)
	if !messages.IsArray() {
		return false
	}
	lastMsg := gjson.Result{}
	messages.ForEach(func(_, msg gjson.Result) bool {
		lastMsg = msg
		return true
	})
	if !lastMsg.Exists() || !lastMsg.IsObject() {
		return false
	}
	if lastMsg.Get("role").String() != "user" {
		return false
	}

	content := lastMsg.Get("content")
	if !content.Exists() {
		return true
	}
	if !content.IsArray() {
		return true
	}

	isReal := true
	content.ForEach(func(_, item gjson.Result) bool {
		itemType := item.Get("type").String()
		if itemType == "tool_result" || itemType == "tool_use_result" {
			isReal = false
			return false
		}
		return true
	})
	return isReal
}
