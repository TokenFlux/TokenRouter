// Ollama Chat 的思考字段补齐只处理报文字节，账号资格由调用方决定。
package ollama

import (
	"strconv"
	"strings"

	wireopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func NormalizeOllamaCloudChatCompletionsRequest(body []byte) []byte {
	if !gjson.ValidBytes(body) {
		return body
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body
	}
	updated := body
	changed := false
	for i, msg := range messages.Array() {
		if msg.Get("role").String() != "assistant" {
			continue
		}
		reasoningContent, ok := jsonNonEmptyString(msg.Get("reasoning_content"))
		if !ok {
			continue
		}
		if _, has := jsonNonEmptyString(msg.Get("reasoning")); has {
			continue
		}
		if _, has := jsonNonEmptyString(msg.Get("thinking")); has {
			continue
		}
		next, err := sjson.SetBytes(updated, "messages."+strconv.Itoa(i)+".reasoning", reasoningContent)
		if err != nil {
			return body
		}
		updated = next
		changed = true
	}
	if !changed {
		return body
	}
	return updated
}
func NormalizeOllamaCloudChatCompletionsResponseJSON(body []byte) []byte {
	if !gjson.ValidBytes(body) {
		return body
	}
	choices := gjson.GetBytes(body, "choices")
	if !choices.IsArray() {
		return body
	}
	updated := body
	changed := false
	for i, choice := range choices.Array() {
		for _, container := range []string{"message", "delta"} {
			obj := choice.Get(container)
			if !obj.Exists() || !obj.IsObject() {
				continue
			}
			if obj.Get("reasoning_content").Exists() {
				continue
			}
			src, ok := jsonNonEmptyString(obj.Get("reasoning"))
			if !ok {
				src, ok = jsonNonEmptyString(obj.Get("thinking"))
			}
			if !ok {
				continue
			}
			next, err := sjson.SetBytes(updated, "choices."+strconv.Itoa(i)+"."+container+".reasoning_content", src)
			if err != nil {
				return body
			}
			updated = next
			changed = true
		}
	}
	if !changed {
		return body
	}
	return updated
}
func NormalizeOllamaCloudChatCompletionsSSELine(line string) string {
	payload, ok := wireopenai.ExtractSSEDataLine(line)
	if !ok {
		return line
	}
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" || trimmed == "[DONE]" {
		return line
	}
	rewritten := NormalizeOllamaCloudChatCompletionsResponseJSON([]byte(payload))
	if string(rewritten) == payload {
		return line
	}
	prefixLen := len(line) - len(payload)
	if prefixLen < 0 {
		return line
	}
	return line[:prefixLen] + string(rewritten)
}
func jsonNonEmptyString(v gjson.Result) (string, bool) {
	if v.Type != gjson.String || v.Str == "" {
		return "", false
	}
	return v.Str, true
}
