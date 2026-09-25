// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// QuotaAutoPauseSettings 是动态阈值的只读输入，省略值保持原回退语义。
type QuotaAutoPauseSettings struct {
	DefaultThreshold5h float64 `json:"default_threshold_5h"`
	DefaultThreshold7d float64 `json:"default_threshold_7d"`
}

// QuotaAutoPauseDecision 仅表达窗口裁决，不写数据库或调度缓存。
type QuotaAutoPauseDecision struct {
	Window                 string
	Threshold, Utilization float64
}

const CodexAutoPauseStaleAfter = 2 * time.Hour

func quotaClamp(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// ResolveAccountExtraBool 从账号 extra 中读取类 bool 值，并兼容 JSON 反序列化
// 可能产生的几种形态（bool、"true"/"false" 字符串、0/1 数字）。
func ResolveAccountExtraBool(extra map[string]any, key string) bool {
	if len(extra) == 0 {
		return false
	}
	value, ok := extra[key]
	if !ok || value == nil {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		return err == nil && parsed
	case float64:
		return v != 0
	case float32:
		return v != 0
	case int:
		return v != 0
	case int64:
		return v != 0
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i != 0
		}
	}
	return false
}
func ResolveAccountExtraNumber(extra map[string]any, keys ...string) (float64, bool) {
	if len(extra) == 0 {
		return 0, false
	}
	for _, key := range keys {
		value, ok := extra[key]
		if !ok || value == nil {
			continue
		}
		switch v := value.(type) {
		case float64:
			return v, true
		case float32:
			return float64(v), true
		case int:
			return float64(v), true
		case int64:
			return float64(v), true
		case json.Number:
			parsed, err := v.Float64()
			if err == nil {
				return parsed, true
			}
		case string:
			parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err == nil {
				return parsed, true
			}
		}
	}
	return 0, false
}

// ResolveOpenAIQuotaUtilization 读取有效窗口利用率；已重置或陈旧快照不能继续暂停账号。
func ResolveOpenAIQuotaUtilization(extra map[string]any, window string, now time.Time) (float64, bool) {
	usedPercent := ReadOpenAIQuotaUsedPercent(extra, window)
	if usedPercent <= 0 {
		return 0, false
	}
	if OpenAIQuotaWindowReset(extra, window, now) {
		return 0, false
	}
	// 快照过于陈旧（账号长期未收到流量刷新）时，不再据此暂停。放行后下一次响应头
	// 会刷新快照实现自愈，避免账号在错误/过期的 used% 上被永久跳过（issue #2994）。
	if OpenAICodexSnapshotStaleForPause(extra, now) {
		return 0, false
	}
	return usedPercent / 100, true
}

// OpenAICodexSnapshotStaleForPause 以原写入时间判断陈旧；缺失或无法解析时仍视为有效，避免绕过暂停。
func OpenAICodexSnapshotStaleForPause(extra map[string]any, now time.Time) bool {
	if len(extra) == 0 {
		return false
	}
	updatedRaw, ok := extra["codex_usage_updated_at"]
	if !ok {
		return false
	}
	updatedAt, err := ParseUsageTime(fmt.Sprint(updatedRaw))
	if err != nil {
		return false
	}
	return now.Sub(updatedAt) >= CodexAutoPauseStaleAfter
}

// OpenAIQuotaWindowReset 优先绝对重置时间，再使用以快照写入时间为基准的相对秒数。
func OpenAIQuotaWindowReset(extra map[string]any, window string, now time.Time) bool {
	if len(extra) == 0 {
		return false
	}
	if resetAtRaw, ok := extra["codex_"+window+"_reset_at"]; ok {
		if resetAt, err := ParseUsageTime(fmt.Sprint(resetAtRaw)); err == nil {
			return !now.Before(resetAt)
		}
	}
	resetAfter := ParseExtraInt(extra["codex_"+window+"_reset_after_seconds"])
	if resetAfter <= 0 {
		return false
	}
	base := now
	if updatedRaw, ok := extra["codex_usage_updated_at"]; ok {
		if updatedAt, err := ParseUsageTime(fmt.Sprint(updatedRaw)); err == nil {
			base = updatedAt
		}
	}
	resetAt := base.Add(time.Duration(resetAfter) * time.Second)
	return !now.Before(resetAt)
}
func ReadOpenAIQuotaUsedPercent(extra map[string]any, window string) float64 {
	if len(extra) == 0 {
		return 0
	}
	if value, ok := ResolveAccountExtraNumber(extra, "codex_"+window+"_used_percent"); ok {
		return value
	}
	return 0
}
func ResolveQuotaAutoPauseThresholds(extra map[string]any, settings QuotaAutoPauseSettings) (float64, float64) {
	threshold5h, _ := ResolveAccountExtraNumber(extra, "auto_pause_5h_threshold")
	threshold7d, _ := ResolveAccountExtraNumber(extra, "auto_pause_7d_threshold")
	threshold5h = quotaClamp(threshold5h)
	threshold7d = quotaClamp(threshold7d)
	if threshold5h > 0 && threshold7d > 0 {
		return threshold5h, threshold7d
	}
	if threshold5h <= 0 {
		threshold5h = quotaClamp(settings.DefaultThreshold5h)
	}
	if threshold7d <= 0 {
		threshold7d = quotaClamp(settings.DefaultThreshold7d)
	}
	return threshold5h, threshold7d
}
func EvaluateQuotaAutoPause(platform string, extra map[string]any, settings QuotaAutoPauseSettings, now time.Time) (bool, QuotaAutoPauseDecision) {
	if platform != PlatformOpenAI {
		return false, QuotaAutoPauseDecision{}
	}
	// 账号级显式禁用标记优先于全局默认值。否则账号阈值留空会表示“使用全局默认”，
	// 一旦存在全局默认值，管理员就无法让单个账号豁免自动暂停。
	// 禁用标记按窗口拆分，因此账号可以只退出 5h 或只退出 7d 自动暂停。
	disabled5h := ResolveAccountExtraBool(extra, "auto_pause_5h_disabled")
	disabled7d := ResolveAccountExtraBool(extra, "auto_pause_7d_disabled")
	threshold5h, threshold7d := ResolveQuotaAutoPauseThresholds(extra, settings)
	if !disabled5h && threshold5h > 0 {
		if utilization, ok := ResolveOpenAIQuotaUtilization(extra, "5h", now); ok && utilization >= threshold5h {
			return true, QuotaAutoPauseDecision{Window: "5h", Threshold: threshold5h, Utilization: utilization}
		}
	}
	if !disabled7d && threshold7d > 0 {
		if utilization, ok := ResolveOpenAIQuotaUtilization(extra, "7d", now); ok && utilization >= threshold7d {
			return true, QuotaAutoPauseDecision{Window: "7d", Threshold: threshold7d, Utilization: utilization}
		}
	}
	return false, QuotaAutoPauseDecision{}
}
