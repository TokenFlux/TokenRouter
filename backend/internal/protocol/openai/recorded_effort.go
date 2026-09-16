// 记录用 effort 只接受既有可记录档位，不改变请求字段本身。
package openai

import "strings"

func NormalizeRecordedReasoningEffort(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return ""
	}

	// 兼容客户端常见的 x-high / x_high / x high 写法。
	value = strings.NewReplacer("-", "", "_", "", " ", "").Replace(value)

	switch value {
	case "none", "minimal":
		return ""
	case "low", "medium", "high":
		return value
	case "xhigh", "extrahigh":
		return "xhigh"
	case "max":
		return value
	default:
		// 只记录已知档位，避免未知客户端字段污染使用记录。
		return ""
	}
}
