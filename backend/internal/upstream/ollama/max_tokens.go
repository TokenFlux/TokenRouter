// 输出上限使用显式账号配置投影；诊断由调用者在原位置记录。
package ollama

import (
	"encoding/json"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const MaxTokensCapExtraKey = "ollama_max_tokens_cap"
const DefaultMaxTokensCap = 65535

// ollamaCloudMaxTokensCap 返回账号配置的 max_tokens 上限。账号为 nil 或 extra 中
// 无该键时返回默认值；键值为数值类型（float64/int64/int/json.Number）时返回其整数
// 值（0 或负数表示显式禁用 clamp）；其它类型回退默认值。
func MaxTokensCap(value any, present bool) int64 {
	if !present {
		return DefaultMaxTokensCap
	}

	switch number := value.(type) {
	case float64:
		return int64(number)
	case int64:
		return number
	case int:
		return int64(number)
	case json.Number:
		parsed, err := number.Int64()
		if err != nil {
			return DefaultMaxTokensCap
		}
		return parsed
	default:
		return DefaultMaxTokensCap
	}
}

// clampOllamaCloudMaxTokens 把 body 中超过 cap 的 max_tokens / max_completion_tokens
// 单向压到 cap。cap <= 0 或 body 不是合法 JSON 时原样返回；sjson 出错时返回原始 body。
// 有任一字段被 clamp 时记录一条 Debug 日志。
func ClampMaxTokens(body []byte, cap int64) ([]byte, bool) {
	if cap <= 0 || !gjson.ValidBytes(body) {
		return body, false
	}
	clamped := false
	out := body
	for _, key := range []string{"max_tokens", "max_completion_tokens"} {
		result := gjson.GetBytes(out, key)
		if !result.Exists() || result.Type != gjson.Number || result.Int() <= cap {
			continue
		}
		updated, err := sjson.SetBytes(out, key, cap)
		if err != nil {
			return body, false
		}
		out = updated
		clamped = true
	}
	return out, clamped
}
