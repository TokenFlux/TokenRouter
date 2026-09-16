// 仅移除 thinking.signature，保留文本、其他字段及大整数；不等同于移除整个 thinking 块。
package anthropic

import (
	"bytes"

	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
)

// StripThinkingSignaturesJSON 从 Claude 历史记录中移除 thinking.signature，
// 使另一个 Grok OAuth 账号在解密失败后仍能接收多轮工具续接。没有修改时返回 false。
func StripThinkingSignaturesJSON(body []byte) ([]byte, bool) {
	if len(body) == 0 || !bytes.Contains(body, []byte(`"signature"`)) {
		return body, false
	}
	var req map[string]any
	if err := wirejson.DecodeUseNumber(body, &req); err != nil {
		return body, false
	}
	messages, ok := req["messages"].([]any)
	if !ok || len(messages) == 0 {
		return body, false
	}
	changed := false
	for _, rawMsg := range messages {
		msg, ok := rawMsg.(map[string]any)
		if !ok {
			continue
		}
		content, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, rawBlock := range content {
			block, ok := rawBlock.(map[string]any)
			if !ok {
				continue
			}
			if typ, _ := block["type"].(string); typ != "thinking" {
				continue
			}
			if _, has := block["signature"]; has {
				delete(block, "signature")
				changed = true
			}
		}
	}
	if !changed {
		return body, false
	}
	out, err := wirejson.Marshal(req)
	if err != nil {
		return body, false
	}
	return out, true
}
