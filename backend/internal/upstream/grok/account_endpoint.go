package grok

import (
	"net/url"
	"strings"
)

// ResolveAccountBaseURL 保留 OAuth 官方地址归一化与显式中继规则。
// 此处不执行目标安全校验，实际构造请求时仍使用调用方的出站策略。
func ResolveAccountBaseURL(oauth bool, configured, fallback string) string {
	fallback = strings.TrimRight(strings.TrimSpace(fallback), "/")
	if fallback == "" {
		if oauth {
			fallback = DefaultCLIBaseURL
		} else {
			fallback = DefaultBaseURL
		}
	}
	base := strings.TrimSpace(configured)
	if base == "" {
		return fallback
	}
	if !oauth {
		return base
	}
	if validated, err := ValidateTrustedBaseURL(base); err == nil {
		return validated
	}
	if parsed, err := url.Parse(base); err == nil && parsed.Scheme != "" && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" {
		return strings.TrimRight(base, "/")
	}
	return fallback
}
