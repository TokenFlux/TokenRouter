// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	OAuthUsageAPICacheTTL         = 3 * time.Minute
	OAuthUsageAPIErrorCacheTTL    = 1 * time.Minute        // 负缓存 TTL：429 等错误缓存 1 分钟
	OAuthUsageAntigravityErrorTTL = 1 * time.Minute        // Antigravity 错误缓存 TTL（可恢复错误）
	OAuthUsageAPIQueryMaxJitter   = 800 * time.Millisecond // 用量查询最大随机延迟
	OAuthUsageWindowStatsCacheTTL = 1 * time.Minute
	OAuthUsageOpenAIProbeCacheTTL = 10 * time.Minute
	OAuthUsageGrokProbeRetryTTL   = 1 * time.Minute
	OAuthUsageGrokFreeQuotaWindow = 24 * time.Hour
)

// BuildPassiveUsageWindow 从 Extra 中的被动采样数据（utilization 为 0-1 小数、reset 为 Unix 秒）
// 构建用量窗口，无数据时返回 nil。
func BuildPassiveUsageWindow(extra map[string]any, utilKey, resetKey string, now func() time.Time) *UsageProgress {
	util := ParseExtraFloat64(extra[utilKey])
	resetRaw := ParseExtraFloat64(extra[resetKey])
	if util <= 0 && resetRaw <= 0 {
		return nil
	}
	var resetAt *time.Time
	var remaining int
	if resetRaw > 0 {
		t := time.Unix(int64(resetRaw), 0)
		resetAt = &t
		remaining = int(t.Sub(now()).Seconds())
		if remaining < 0 {
			remaining = 0
		}
	}
	return &UsageProgress{
		Utilization:      util * 100,
		ResetsAt:         resetAt,
		RemainingSeconds: remaining,
	}
}

// ApplyExtraToUsage rebuilds the codex 5h/7d windows in usage from the
// account's Extra map.  Called after mergeAccountExtra to make the in-memory
// UsageInfo consistent with the just-persisted Extra values.
func ApplyExtraToUsage(usage *UsageInfo, extra map[string]any, now time.Time, clock func() time.Time) {
	if usage == nil {
		return
	}
	if progress := BuildCodexUsageProgressFromExtra(extra, "5h", now, clock); progress != nil {
		usage.FiveHour = progress
	}
	if progress := BuildCodexUsageProgressFromExtra(extra, "7d", now, clock); progress != nil {
		usage.SevenDay = progress
	}
}

func BuildCodexUsageProgressFromExtra(extra map[string]any, window string, now time.Time, clock func() time.Time) *UsageProgress {
	if len(extra) == 0 {
		return nil
	}

	var (
		usedPercentKey string
		resetAfterKey  string
		resetAtKey     string
	)

	switch window {
	case "5h":
		usedPercentKey = "codex_5h_used_percent"
		resetAfterKey = "codex_5h_reset_after_seconds"
		resetAtKey = "codex_5h_reset_at"
	case "7d":
		usedPercentKey = "codex_7d_used_percent"
		resetAfterKey = "codex_7d_reset_after_seconds"
		resetAtKey = "codex_7d_reset_at"
	default:
		return nil
	}

	usedRaw, ok := extra[usedPercentKey]
	if !ok {
		return nil
	}

	progress := &UsageProgress{Utilization: ParseExtraFloat64(usedRaw)}
	if resetAtRaw, ok := extra[resetAtKey]; ok {
		if resetAt, err := ParseUsageTime(fmt.Sprint(resetAtRaw)); err == nil {
			progress.ResetsAt = &resetAt
			progress.RemainingSeconds = int(resetAt.Sub(clock()).Seconds())
			if progress.RemainingSeconds < 0 {
				progress.RemainingSeconds = 0
			}
		}
	}
	if progress.ResetsAt == nil {
		if resetAfterSeconds := ParseExtraInt(extra[resetAfterKey]); resetAfterSeconds > 0 {
			base := now
			if updatedAtRaw, ok := extra["codex_usage_updated_at"]; ok {
				if updatedAt, err := ParseUsageTime(fmt.Sprint(updatedAtRaw)); err == nil {
					base = updatedAt
				}
			}
			resetAt := base.Add(time.Duration(resetAfterSeconds) * time.Second)
			progress.ResetsAt = &resetAt
			progress.RemainingSeconds = int(resetAt.Sub(clock()).Seconds())
			if progress.RemainingSeconds < 0 {
				progress.RemainingSeconds = 0
			}
		}
	}

	// 窗口已过期（resetAt 在 now 之前）→ 额度已重置，归零
	if progress.ResetsAt != nil && !now.Before(*progress.ResetsAt) {
		progress.Utilization = 0
	}

	return progress
}

// CodexWindowStatsStart 按 Codex 上游返回的重置时间对齐本地用量统计窗口。
func CodexWindowStatsStart(progress *UsageProgress, fallbackWindow time.Duration, now time.Time) time.Time {
	if progress != nil && progress.ResetsAt != nil && now.Before(*progress.ResetsAt) {
		return progress.ResetsAt.Add(-fallbackWindow)
	}
	return now.Add(-fallbackWindow)
}

// ParseUsageTime 尝试多种格式解析时间
func ParseUsageTime(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.000Z",
	}
	for _, format := range formats {
		if t, err := time.Parse(format, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse time: %s", s)
}

// BuildUsageInfo 构建UsageInfo
func BuildUsageInfo(resp *ClaudeUsageResponse, updatedAt *time.Time, now func() time.Time, logf func(string, ...any)) *UsageInfo {
	info := &UsageInfo{
		UpdatedAt: updatedAt,
	}

	// 5小时窗口 - 始终创建对象（即使 ResetsAt 为空）
	info.FiveHour = &UsageProgress{
		Utilization: resp.FiveHour.Utilization,
	}
	if resp.FiveHour.ResetsAt != "" {
		if fiveHourReset, err := ParseUsageTime(resp.FiveHour.ResetsAt); err == nil {
			info.FiveHour.ResetsAt = &fiveHourReset
			info.FiveHour.RemainingSeconds = int(fiveHourReset.Sub(now()).Seconds())
		} else {
			logf("Failed to parse FiveHour.ResetsAt: %s, error: %v", resp.FiveHour.ResetsAt, err)
		}
	}

	// 7天窗口
	if resp.SevenDay.ResetsAt != "" {
		if sevenDayReset, err := ParseUsageTime(resp.SevenDay.ResetsAt); err == nil {
			info.SevenDay = &UsageProgress{
				Utilization:      resp.SevenDay.Utilization,
				ResetsAt:         &sevenDayReset,
				RemainingSeconds: int(sevenDayReset.Sub(now()).Seconds()),
			}
		} else {
			logf("Failed to parse SevenDay.ResetsAt: %s, error: %v", resp.SevenDay.ResetsAt, err)
			info.SevenDay = &UsageProgress{
				Utilization: resp.SevenDay.Utilization,
			}
		}
	}

	// 7天Sonnet窗口
	if resp.SevenDaySonnet.ResetsAt != "" {
		if sonnetReset, err := ParseUsageTime(resp.SevenDaySonnet.ResetsAt); err == nil {
			info.SevenDaySonnet = &UsageProgress{
				Utilization:      resp.SevenDaySonnet.Utilization,
				ResetsAt:         &sonnetReset,
				RemainingSeconds: int(sonnetReset.Sub(now()).Seconds()),
			}
		} else {
			logf("Failed to parse SevenDaySonnet.ResetsAt: %s, error: %v", resp.SevenDaySonnet.ResetsAt, err)
			info.SevenDaySonnet = &UsageProgress{
				Utilization: resp.SevenDaySonnet.Utilization,
			}
		}
	}

	// 7天Fable窗口（响应头 7d_oi 对应的窗口）
	if fable := resp.SevenDayOverageIncluded; fable.ResetsAt != "" {
		if fableReset, err := ParseUsageTime(fable.ResetsAt); err == nil {
			info.SevenDayFable = &UsageProgress{
				Utilization:      fable.Utilization,
				ResetsAt:         &fableReset,
				RemainingSeconds: int(fableReset.Sub(now()).Seconds()),
			}
		} else {
			logf("Failed to parse SevenDayFable.ResetsAt: %s, error: %v", fable.ResetsAt, err)
			info.SevenDayFable = &UsageProgress{
				Utilization: fable.Utilization,
			}
		}
	}

	return info
}

// EstimateSetupTokenUsage 根据session_window推算Setup Token账号的使用量
func EstimateSetupTokenUsage(account *Record, now func() time.Time) *UsageInfo {
	info := &UsageInfo{}

	// 如果有session_window信息
	if account.SessionWindowEnd != nil {
		remaining := int(account.SessionWindowEnd.Sub(now()).Seconds())
		if remaining < 0 {
			remaining = 0
		}

		// 优先使用响应头中存储的真实 utilization 值（0-1 小数，转为 0-100 百分比）
		var utilization float64
		var found bool
		if stored, ok := account.Extra["session_window_utilization"]; ok {
			switch v := stored.(type) {
			case float64:
				utilization = v * 100
				found = true
			case json.Number:
				if f, err := v.Float64(); err == nil {
					utilization = f * 100
					found = true
				}
			}
		}

		// 如果没有存储的 utilization，回退到状态估算
		if !found {
			switch account.SessionWindowStatus {
			case "rejected":
				utilization = 100.0
			case "allowed_warning":
				utilization = 80.0
			}
		}

		info.FiveHour = &UsageProgress{
			Utilization:      utilization,
			ResetsAt:         clonePointer(account.SessionWindowEnd),
			RemainingSeconds: remaining,
		}

		// 窗口已过期（resetAt 在 now 之前）→ 额度已重置，归零；
		// 与 Codex 分支 BuildCodexUsageProgressFromExtra 保持一致，避免
		// UI 在 active poll 没回写 SessionWindowEnd 时渲染矛盾状态。
		if info.FiveHour.ResetsAt != nil && !now().Before(*info.FiveHour.ResetsAt) {
			info.FiveHour.Utilization = 0
			info.FiveHour.ResetsAt = nil
			info.FiveHour.RemainingSeconds = 0
		}
	} else {
		// 没有窗口信息，返回空数据
		info.FiveHour = &UsageProgress{
			Utilization:      0,
			RemainingSeconds: 0,
		}
	}

	// Setup Token无法获取7d数据
	return info
}

func BuildGeminiUsageProgress(used, limit int64, resetAt time.Time, tokens int64, cost float64, now time.Time) *UsageProgress {
	// limit <= 0 means "no local quota window" (unknown or unlimited).
	if limit <= 0 {
		return nil
	}
	utilization := (float64(used) / float64(limit)) * 100
	remainingSeconds := int(resetAt.Sub(now).Seconds())
	if remainingSeconds < 0 {
		remainingSeconds = 0
	}
	resetCopy := resetAt
	return &UsageProgress{
		Utilization:      utilization,
		ResetsAt:         &resetCopy,
		RemainingSeconds: remainingSeconds,
		UsedRequests:     used,
		LimitRequests:    limit,
		WindowStats: &WindowStats{
			Requests: used,
			Tokens:   tokens,
			Cost:     cost,
		},
	}
}

// RecalcAntigravityRemainingSeconds 重新计算 Antigravity UsageInfo 中各窗口的 RemainingSeconds
// 用于从缓存取出时更新倒计时，避免返回过时的剩余秒数
func RecalcAntigravityRemainingSeconds(info *UsageInfo, now func() time.Time) {
	if info == nil {
		return
	}
	if info.FiveHour != nil && info.FiveHour.ResetsAt != nil {
		remaining := int(info.FiveHour.ResetsAt.Sub(now()).Seconds())
		if remaining < 0 {
			remaining = 0
		}
		info.FiveHour.RemainingSeconds = remaining
	}
}

// AntigravityCacheTTL 根据 UsageInfo 内容决定缓存 TTL
// 403 forbidden 状态稳定，缓存与成功相同（3 分钟）；
// 其他错误（401/网络）可能快速恢复，缓存 1 分钟。
func AntigravityCacheTTL(info *UsageInfo) time.Duration {
	if info == nil {
		return OAuthUsageAntigravityErrorTTL
	}
	if info.IsForbidden {
		return OAuthUsageAPICacheTTL // 封号/验证状态不会很快变
	}
	if info.ErrorCode != "" || info.Error != "" {
		return OAuthUsageAntigravityErrorTTL
	}
	return OAuthUsageAPICacheTTL
}
