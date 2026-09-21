// 本文件维护 egress 的所属能力；兼容入口复用唯一实现。
package egress

import (
	errors "errors"
	url "net/url"
	strings "strings"
)

// UsageURLPolicy 区分未装配配置与显式关闭白名单，保留原查询端点校验顺序。
type UsageURLPolicy struct {
	Configured        bool
	Enabled           bool
	AllowInsecureHTTP bool
	AllowPrivateHosts bool
	UpstreamHosts     []string
}

func (p UsageURLPolicy) Validate(raw string) (string, error) {
	if err := ValidateUsageBaseURLFormat(raw); err != nil {
		return "", err
	}
	if !p.Configured {
		return ValidateHTTPSURL(raw, ValidationOptions{AllowPrivate: false})
	}
	if !p.Enabled {
		return ValidateURLFormat(raw, p.AllowInsecureHTTP)
	}
	return ValidateHTTPSURL(raw, ValidationOptions{AllowedHosts: p.UpstreamHosts, RequireAllowlist: true, AllowPrivate: p.AllowPrivateHosts})
}

func ValidateUsageBaseURLFormat(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("invalid base URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("base URL must not contain credentials, query, or fragment")
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return errors.New("base URL scheme is not supported")
	}
	if len(raw) > 2048 {
		return errors.New("base URL is too long")
	}
	return nil
}
