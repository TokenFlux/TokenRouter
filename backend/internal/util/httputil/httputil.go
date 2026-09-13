package httputil

import (
	json "encoding/json"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	http "net/http"
	strings "strings"
)

func IsCloudflareChallengeResponse(statusCode int, headers http.Header, body []byte) bool {
	return egress.IsCloudflareChallengeResponse(statusCode, headers, body)
}

func ExtractCloudflareRayID(headers http.Header, body []byte) string {
	return egress.ExtractCloudflareRayID(headers, body)
}

func FormatCloudflareChallengeMessage(base string, headers http.Header, body []byte) string {
	return egress.FormatCloudflareChallengeMessage(base, headers, body)
}

// ExtractUpstreamErrorCodeAndMessage extracts structured error code/message from common JSON layouts.
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

func TruncateBody(body []byte, max int) string {
	return egress.TruncateBody(body, max)
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
