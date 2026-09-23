package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/tidwall/gjson"
)

type openAICyberTranscriptBlockKeys struct {
	lookupKeys          []string
	preLatestUserKey    string
	lookupKeysTruncated bool
}

// 限制单请求的 Redis 查询工作量，保留最可能匹配续接的近期转录前缀。
const maxOpenAICyberTranscriptLookupKeys = 256

// deriveOpenAICyberTranscriptBlockKeys 返回累计语义历史散列和最新用户轮次前的上下文键。
// 上下文键必须观察到模型生成历史，避免共享首轮模板误屏蔽无关会话。
func deriveOpenAICyberTranscriptBlockKeys(apiKeyID int64, body []byte) openAICyberTranscriptBlockKeys {
	if len(body) == 0 {
		return openAICyberTranscriptBlockKeys{}
	}
	root := openai.RequestPayloadView(body)
	if !root.Exists() || !root.IsObject() {
		return openAICyberTranscriptBlockKeys{}
	}

	h := sha256.New()
	_, _ = h.Write([]byte("cyber-transcript:v3|api_key="))
	_, _ = h.Write([]byte(strconv.FormatInt(apiKeyID, 10)))
	// 模型与工具定义属于请求配置；根指令仍是模型可见的上下文，参与身份计算。
	for _, field := range []string{"instructions"} {
		v := root.Get(field)
		if !v.Exists() || (v.Type == gjson.String && strings.TrimSpace(v.String()) == "") {
			continue
		}
		canonical := openai.NormalizeCompatSeedJSON(json.RawMessage(v.Raw))
		if v.Type == gjson.String {
			canonical = v.String()
		}
		_, _ = h.Write([]byte("|"))
		_, _ = h.Write([]byte(field))
		_, _ = h.Write([]byte("="))
		_, _ = h.Write([]byte(canonical))
	}

	appendSequence := func(sequence gjson.Result) openAICyberTranscriptBlockKeys {
		if !sequence.Exists() || !sequence.IsArray() {
			return openAICyberTranscriptBlockKeys{}
		}
		result := openAICyberTranscriptBlockKeys{
			lookupKeys: make([]string, 0, maxOpenAICyberTranscriptLookupKeys),
		}
		nextLookupKey := 0
		lookupKeysRotated := false
		lastLookupKey := ""
		// 这里只使用熵启发式，不能证明来源；若要准确区分固定 few-shot 助手条目，
		// 需要经过认证的服务端历史。
		hasModelGeneratedItem := false
		sequence.ForEach(func(_, item gjson.Result) bool {
			canonical := item.Raw
			switch item.Type {
			case gjson.String:
				encoded, _ := json.Marshal(item.String())
				canonical = string(encoded)
			case gjson.JSON:
				canonical = openai.NormalizeCompatSeedJSON(json.RawMessage(item.Raw))
			}
			if strings.TrimSpace(canonical) == "" {
				return true
			}
			if openAICyberTranscriptItemStartsUserTurn(item) && hasModelGeneratedItem && lastLookupKey != "" {
				result.preLatestUserKey = lastLookupKey
			}
			_, _ = h.Write([]byte("|item="))
			_, _ = h.Write([]byte(canonical))
			lastLookupKey = hex.EncodeToString(h.Sum(nil))
			if len(result.lookupKeys) < maxOpenAICyberTranscriptLookupKeys {
				result.lookupKeys = append(result.lookupKeys, lastLookupKey)
			} else {
				result.lookupKeys[nextLookupKey] = lastLookupKey
				nextLookupKey = (nextLookupKey + 1) % maxOpenAICyberTranscriptLookupKeys
				lookupKeysRotated = true
				result.lookupKeysTruncated = true
			}
			if openAICyberTranscriptItemIsModelGenerated(item) {
				hasModelGeneratedItem = true
			}
			return true
		})
		if lookupKeysRotated {
			ordered := make([]string, 0, len(result.lookupKeys))
			ordered = append(ordered, result.lookupKeys[nextLookupKey:]...)
			ordered = append(ordered, result.lookupKeys[:nextLookupKey]...)
			result.lookupKeys = ordered
		}
		return result
	}

	if messages := root.Get("messages"); messages.Exists() {
		return appendSequence(messages)
	}
	input := root.Get("input")
	if input.IsArray() {
		return appendSequence(input)
	}
	if input.Type == gjson.String && strings.TrimSpace(input.String()) != "" {
		encoded, _ := json.Marshal(input.String())
		_, _ = h.Write([]byte("|item="))
		_, _ = h.Write(encoded)
		return openAICyberTranscriptBlockKeys{lookupKeys: []string{hex.EncodeToString(h.Sum(nil))}}
	}
	return openAICyberTranscriptBlockKeys{}
}

func openAICyberTranscriptItemStartsUserTurn(item gjson.Result) bool {
	if strings.EqualFold(strings.TrimSpace(item.Get("role").String()), "user") {
		content := item.Get("content")
		if content.IsArray() {
			hasUserContent := false
			content.ForEach(func(_, block gjson.Result) bool {
				switch strings.ToLower(strings.TrimSpace(block.Get("type").String())) {
				case "tool_result", "function_call_output", "custom_tool_call_output", "computer_call_output":
				default:
					hasUserContent = true
				}
				return !hasUserContent
			})
			return hasUserContent
		}
		return content.Exists()
	}
	return strings.EqualFold(strings.TrimSpace(item.Get("type").String()), "input_text")
}

func openAICyberTranscriptItemIsModelGenerated(item gjson.Result) bool {
	switch strings.ToLower(strings.TrimSpace(item.Get("role").String())) {
	case "assistant", "model":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(item.Get("type").String())) {
	case "output_text", "function_call", "tool_call", "custom_tool_call", "computer_call":
		return true
	default:
		return false
	}
}
