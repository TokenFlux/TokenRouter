// 本文件维护 egress 的所属能力；兼容入口复用唯一实现。
package egress

import (
	errors "errors"
	url "net/url"
	strings "strings"
)

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
