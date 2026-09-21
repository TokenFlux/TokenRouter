package anthropic

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RateLimitReset 表达供应商限流头的重置观测，不执行账号状态写入。
type RateLimitReset struct {
	ResetAt       time.Time  // 限流重置时间
	FiveHourReset *time.Time // 五小时窗口重置时间，缺失时为 nil
}

type WindowLimit struct {
	Window        string
	ResetAt       time.Time
	FiveHourReset *time.Time
	Reason        string
}

func SelectExhaustedWindow(headers http.Header, now time.Time) *WindowLimit {
	reset5h, ok5hReset := parseWindowReset(headers, "5h", now)
	reset7d, ok7dReset := parseWindowReset(headers, "7d", now)

	exceeded5h := isFiveHourRejected(headers) || isWindowExceeded(headers, "5h")
	exceeded7d := isWindowExceeded(headers, "7d")

	if exceeded7d && ok7dReset {
		limit := &WindowLimit{
			Window:  "7d",
			ResetAt: reset7d,
			Reason:  "anthropic_7d_window_exhausted",
		}
		if ok5hReset {
			limit.FiveHourReset = &reset5h
		}
		return limit
	}
	if exceeded5h && ok5hReset {
		return &WindowLimit{
			Window:        "5h",
			ResetAt:       reset5h,
			FiveHourReset: &reset5h,
			Reason:        "anthropic_5h_window_exhausted",
		}
	}
	return nil
}

func isFiveHourRejected(headers http.Header) bool {
	return isWindowRejected(headers, "5h")
}

func isWindowRejected(headers http.Header, window string) bool {
	return strings.EqualFold(strings.TrimSpace(headers.Get("anthropic-ratelimit-unified-"+window+"-status")), "rejected")
}

func parseWindowReset(headers http.Header, window string, now time.Time) (time.Time, bool) {
	maxAge := 8 * 24 * time.Hour
	if window == "5h" {
		maxAge = 6 * time.Hour
	}
	return parseResetTimestamp(headers.Get("anthropic-ratelimit-unified-"+window+"-reset"), now, maxAge)
}

// parseResetTimestamp 解析 Anthropic reset 头的 Unix 时间戳（自动识别毫秒），
// 并校验落在 (now, now+maxAge] 的合理区间内。
func parseResetTimestamp(raw string, now time.Time, maxAge time.Duration) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	ts, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	if ts > 1e11 {
		ts = ts / 1000
	}
	resetAt := time.Unix(ts, 0)
	if !resetAt.After(now) || resetAt.After(now.Add(maxAge)) {
		return time.Time{}, false
	}
	return resetAt, true
}

// SelectFableWindowLimit 解析 Anthropic 7d_oi 模型窗口响应头。
// 该窗口只约束 Fable 家族，不能让账号对其它模型失去调度资格。
// surpassed-threshold 使用浮点数而非布尔值，因此 status=rejected 或
// utilization >= 1.0 都视为超限；缺少专用 reset 时回退到聚合 reset。
func SelectFableWindowLimit(headers http.Header, now time.Time) *WindowLimit {
	if !isWindowRejected(headers, "7d_oi") && !isWindowExceeded(headers, "7d_oi") {
		return nil
	}
	resetAt, ok := parseWindowReset(headers, "7d_oi", now)
	if !ok {
		resetAt, ok = parseAggregateReset(headers, now)
	}
	if !ok {
		return nil
	}
	return &WindowLimit{
		Window:  "7d_oi",
		ResetAt: resetAt,
		Reason:  FableWindowReason,
	}
}

// parseAggregateReset 按 7 天窗口的合理范围解析聚合 reset 响应头。
func parseAggregateReset(headers http.Header, now time.Time) (time.Time, bool) {
	return parseResetTimestamp(headers.Get("anthropic-ratelimit-unified-reset"), now, 8*24*time.Hour)
}

// CalculateRateLimitReset 解析 5h/7d 响应头；缺少窗口时由调用方保留聚合头回退。
func CalculateRateLimitReset(headers http.Header, info func(string, ...any)) *RateLimitReset {
	reset5hStr := headers.Get("anthropic-ratelimit-unified-5h-reset")
	reset7dStr := headers.Get("anthropic-ratelimit-unified-7d-reset")

	if reset5hStr == "" && reset7dStr == "" {
		return nil
	}

	var reset5h, reset7d *time.Time
	if ts, err := strconv.ParseInt(reset5hStr, 10, 64); err == nil {
		t := time.Unix(ts, 0)
		reset5h = &t
	}
	if ts, err := strconv.ParseInt(reset7dStr, 10, 64); err == nil {
		t := time.Unix(ts, 0)
		reset7d = &t
	}

	is5hExceeded := isWindowExceeded(headers, "5h")
	is7dExceeded := isWindowExceeded(headers, "7d")

	if info != nil {
		info("anthropic_429_window_analysis",
			"is_5h_exceeded", is5hExceeded,
			"is_7d_exceeded", is7dExceeded,
			"reset_5h", reset5hStr,
			"reset_7d", reset7dStr,
		)

	}
	// 按实际耗尽窗口选择重置时间。
	var chosen *time.Time
	switch {
	case is5hExceeded && is7dExceeded:
		// 两个窗口均耗尽时优先 7d，缺失则回退 5h。
		chosen = reset7d
		if chosen == nil {
			chosen = reset5h
		}
	case is5hExceeded:
		chosen = reset5h
	case is7dExceeded:
		chosen = reset7d
	default:
		// 没有明确耗尽标记时沿用更早的重置时间。
		chosen = pickSooner(reset5h, reset7d)
	}

	if chosen == nil {
		return nil
	}
	return &RateLimitReset{ResetAt: *chosen, FiveHourReset: reset5h}
}

// isWindowExceeded 保留 surpassed-threshold 和 utilization 的原判定容差。
func isWindowExceeded(headers http.Header, window string) bool {
	prefix := "anthropic-ratelimit-unified-" + window + "-"

	// 优先读取明确的超限标记。
	if st := headers.Get(prefix + "surpassed-threshold"); strings.EqualFold(st, "true") {
		return true
	}

	// 再读取利用率。
	if utilStr := headers.Get(prefix + "utilization"); utilStr != "" {
		if util, err := strconv.ParseFloat(utilStr, 64); err == nil && util >= 1.0-1e-9 {
			// 保留浮点容差。
			return true
		}
	}

	return false
}

// pickSooner 返回非空且更早的时间；均缺失时返回 nil。
func pickSooner(a, b *time.Time) *time.Time {
	switch {
	case a != nil && b != nil:
		if a.Before(*b) {
			return a
		}
		return b
	case a != nil:
		return a
	default:
		return b
	}
}

// FableWindowReason 保留模型窗口持久化原因。
const FableWindowReason = "anthropic_7d_oi_window_exhausted"
