// 共用 OpenAI 兼容版本路径拼接，原端点及查询处理保持。
package httpclient

import (
	"net/url"
	"strings"
)

// BuildOpenAIEndpointURL 按 OpenAI 兼容上游 base_url 形态拼出目标端点。
// 已包含完整端点时原样返回；base_url 以 /v1、/v4、/v1beta 等版本段结尾时，
// 直接追加不带 /v1 前缀的相对端点；其它情况追加完整默认端点。
func BuildOpenAIEndpointURL(base string, endpoint string) string {
	normalized := strings.TrimSpace(base)
	endpoint = "/" + strings.TrimLeft(strings.TrimSpace(endpoint), "/")
	relative := strings.TrimPrefix(endpoint, "/v1")
	parsed, err := url.Parse(normalized)
	if err != nil {
		return strings.TrimRight(normalized, "/") + endpoint
	}
	path := strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(path, endpoint) && !strings.HasSuffix(path, relative) {
		if OpenAIBaseURLHasVersionSuffix(path) {
			path += relative
		} else {
			path += endpoint
		}
	}
	parsed.Path = path
	parsed.RawPath = ""
	parsed.Fragment = ""
	return parsed.String()
}

func BuildOpenAIResponsesInputTokensURL(base string) string {
	normalized := strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(normalized, "/responses") {
		return normalized + "/input_tokens"
	}
	return BuildOpenAIEndpointURL(base, "/v1/responses/input_tokens")
}

// OpenAIBaseURLHasVersionSuffix 判断 base_url 路径最后一段是否是 API 版本段。
func OpenAIBaseURLHasVersionSuffix(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}

	pathValue := ""
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		pathValue = parsed.Path
	} else if slash := strings.Index(trimmed, "/"); slash >= 0 {
		pathValue = trimmed[slash:]
	}

	pathValue = strings.TrimRight(pathValue, "/")
	if pathValue == "" {
		return false
	}
	lastSlash := strings.LastIndex(pathValue, "/")
	segment := pathValue
	if lastSlash >= 0 {
		segment = pathValue[lastSlash+1:]
	}
	return IsOpenAIAPIVersionSegment(segment)
}

// IsOpenAIAPIVersionSegment 识别 v1、v4、v1.1、v1beta、v1-preview 等版本段。
func IsOpenAIAPIVersionSegment(segment string) bool {
	s := strings.ToLower(strings.TrimSpace(segment))
	if len(s) < 2 || s[0] != 'v' || !IsASCIIDigit(s[1]) {
		return false
	}

	i := 1
	for i < len(s) && IsASCIIDigit(s[i]) {
		i++
	}
	if i == len(s) {
		return true
	}
	if s[i] == '.' {
		i++
		if i == len(s) || !IsASCIIDigit(s[i]) {
			return false
		}
		for i < len(s) && IsASCIIDigit(s[i]) {
			i++
		}
		return i == len(s)
	}

	suffix := s[i:]
	return strings.HasPrefix(suffix, "alpha") ||
		strings.HasPrefix(suffix, "beta") ||
		strings.HasPrefix(suffix, "preview")
}

func IsASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
}
