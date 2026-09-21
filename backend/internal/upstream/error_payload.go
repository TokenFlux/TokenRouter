package upstream

import (
	"encoding/json"
	"strings"
)

// ExtractUpstreamErrorCodeAndMessage 从既有 JSON 形状提取错误码和消息，保持字节截断边界。
func ExtractUpstreamErrorCodeAndMessage(body []byte) (string, string) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "", ""
	}
	if !json.Valid([]byte(trimmed)) {
		return "", truncateMessage(trimmed, 256)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return "", truncateMessage(trimmed, 256)
	}

	code := firstNonEmpty(
		extractNestedString(payload, "error", "code"),
		extractRootString(payload, "code"),
	)
	message := firstNonEmpty(
		extractNestedString(payload, "error", "message"),
		extractRootString(payload, "message"),
		extractNestedString(payload, "error", "detail"),
		extractRootString(payload, "detail"),
	)
	return strings.TrimSpace(code), truncateMessage(strings.TrimSpace(message), 512)
}

func truncateMessage(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func extractRootString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func extractNestedString(m map[string]any, parent, key string) string {
	if m == nil {
		return ""
	}
	node, ok := m[parent]
	if !ok {
		return ""
	}
	child, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := child[key].(string)
	return s
}
