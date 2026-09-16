// 固定供应商余额/周期协议保持原请求顺序和归一化，不写账号或调度。
package usageclient

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"

	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

func CnParseF64(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// CnNormalizeResetTime 把上游重置时间（ISO8601 字符串 / 秒级 / 毫秒级数字）归一化为
// RFC3339 字符串；无法识别或非正时间戳返回空串。
func CnNormalizeResetTime(raw any) string {
	switch v := raw.(type) {
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return ""
		}
		if ts, err := timezone.ParseFlexibleTimestamp(s); err == nil {
			return ts.UTC().Format(time.RFC3339)
		}
		return ""
	case float64:
		return CnMillisToRFC3339(int64(v))
	case int:
		return CnMillisToRFC3339(int64(v))
	case int64:
		return CnMillisToRFC3339(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return CnMillisToRFC3339(n)
		}
		return ""
	default:
		return ""
	}
}

// CnMillisToRFC3339 把秒级（<1e12）或毫秒级时间戳转为 RFC3339 字符串；非正返回空串。
func CnMillisToRFC3339(n int64) string {
	if n <= 0 {
		return ""
	}
	var ms int64
	if n < 1_000_000_000_000 {
		ms = n * 1000
	} else {
		ms = n
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}
func CnUsageLimits(provider string, tiers []usageview.CNQuotaTier) (*usageview.UpstreamUsageInfo, error) {
	if len(tiers) == 0 {
		return nil, usageview.ErrUpstreamUsageInvalidResponse
	}
	limits := make([]usageview.UpstreamUsageLimit, 0, len(tiers))
	for _, tier := range tiers {
		name := strings.TrimSpace(tier.Window)
		if name == "" || !usageview.ValidFiniteNumber(tier.UsedPercent) || tier.UsedPercent < 0 {
			return nil, usageview.ErrUpstreamUsageInvalidResponse
		}
		remaining := 100 - tier.UsedPercent
		if remaining < 0 {
			remaining = 0
		}
		var resetAt *time.Time
		if parsed, err := time.Parse(time.RFC3339, tier.ResetAt); err == nil && !parsed.IsZero() {
			utc := parsed.UTC()
			resetAt = &utc
		}
		used := tier.UsedPercent
		limit := float64(100)
		limits = append(limits, usageview.UpstreamUsageLimit{Name: name, Used: &used, Limit: &limit, Remaining: &remaining, ResetAt: resetAt})
	}
	return &usageview.UpstreamUsageInfo{Provider: provider, Mode: "limits", Unit: "PERCENT", Limits: limits}, nil
}
func ValidateCNUsageStatus(status int) error {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == 401 || status == 403:
		return usageview.ErrUpstreamUsageAuthFailed
	case status == 429:
		return usageview.ErrUpstreamUsageRateLimited
	case status == 404 || status == 405:
		return usageview.ErrUpstreamUsageUnsupported
	default:
		return usageview.ErrUpstreamUsageInvalidResponse
	}
}

// CnUsageEndpoint 从已通过策略校验的账号 Base URL 派生固定路径。主机始终来自账号配置，
// 不从响应或任意用户输入推断，也不会把凭据发往其它主机。
func CnUsageEndpoint(base, path string, preserveCodingPrefix bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("invalid base URL")
	}
	prefix := ""
	if preserveCodingPrefix && strings.Contains(strings.ToLower(parsed.Path), "/coding") {
		prefix = "/coding"
	}
	parsed.Path = strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(path, "/")
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.User = nil
	return strings.TrimRight(parsed.String(), "/"), nil
}
