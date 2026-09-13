// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	json "encoding/json"
	fmt "fmt"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	strings "strings"
	time "time"
)

const (
	OllamaCloudUsageDefaultIntervalMinutes = 60
	OllamaCloudUsageMinIntervalMinutes     = 15
	OllamaCloudUsageMaxIntervalMinutes     = 24 * 60
	OllamaCloudUsageDefaultDebounceMinutes = 1
	OllamaCloudUsageMinDebounceMinutes     = 1
	OllamaCloudUsageMaxDebounceMinutes     = 60
	OllamaCloudUsageMaxDelay               = 24 * time.Hour
)

func DefaultOllamaCloudUsageSettings() *OllamaCloudUsageSettings {
	return &OllamaCloudUsageSettings{
		Enabled:         false,
		IntervalMinutes: OllamaCloudUsageDefaultIntervalMinutes,
		DebounceMinutes: OllamaCloudUsageDefaultDebounceMinutes,
	}
}
func NormalizeOllamaCloudUsageSettings(settings *OllamaCloudUsageSettings) {
	if settings.IntervalMinutes < OllamaCloudUsageMinIntervalMinutes {
		settings.IntervalMinutes = OllamaCloudUsageMinIntervalMinutes
	}
	if settings.IntervalMinutes > OllamaCloudUsageMaxIntervalMinutes {
		settings.IntervalMinutes = OllamaCloudUsageMaxIntervalMinutes
	}
	if settings.DebounceMinutes <= 0 {
		settings.DebounceMinutes = OllamaCloudUsageDefaultDebounceMinutes
	}
	if settings.DebounceMinutes < OllamaCloudUsageMinDebounceMinutes {
		settings.DebounceMinutes = OllamaCloudUsageMinDebounceMinutes
	}
	if settings.DebounceMinutes > OllamaCloudUsageMaxDebounceMinutes {
		settings.DebounceMinutes = OllamaCloudUsageMaxDebounceMinutes
	}
}
func OllamaCloudUsageDurations(settings *OllamaCloudUsageSettings) (debounce, maxWait time.Duration) {
	normalized := DefaultOllamaCloudUsageSettings()
	if settings != nil {
		*normalized = *settings
	}
	NormalizeOllamaCloudUsageSettings(normalized)
	return time.Duration(normalized.DebounceMinutes) * time.Minute,
		time.Duration(normalized.IntervalMinutes) * time.Minute
}

// OllamaCloudUsageIsAutoRefreshDue 判断已启用自动刷新的分组现在是否应刷新。
// groupLastUsedAt 必须是精确 api_key 分组的 MAX(last_used_at)，避免共享身份的
// 多平台账号遗漏活动。
//
// 成功时，请求必须晚于 fetched_at，dueAt = min(lastUsed+debounce, fetchedAt+maxWait)。
// 失败时，请求必须晚于 last_attempt_at，活动到期时间使用相同的 min 公式；
// 随后 dueAt = max(activityDue, next_refresh_at)，确保 Retry-After 或指数退避优先。
// 快照缺失或无效时按首次刷新处理。
func OllamaCloudUsageIsAutoRefreshDue(
	snapshot *OllamaCloudUsageSnapshot,
	groupLastUsedAt *time.Time,
	now time.Time,
	debounce, maxWait time.Duration,
) bool {
	dueAt, ok := OllamaCloudUsageAutoRefreshDueAt(snapshot, groupLastUsedAt, debounce, maxWait)
	if !ok {
		return false
	}
	return !now.Before(dueAt)
}
func OllamaCloudUsageAutoRefreshDueAt(
	snapshot *OllamaCloudUsageSnapshot,
	groupLastUsedAt *time.Time,
	debounce, maxWait time.Duration,
) (time.Time, bool) {
	if debounce <= 0 {
		debounce = time.Duration(OllamaCloudUsageDefaultDebounceMinutes) * time.Minute
	}
	if maxWait <= 0 {
		maxWait = time.Duration(OllamaCloudUsageDefaultIntervalMinutes) * time.Minute
	}
	if snapshot == nil {
		return time.Time{}, true
	}
	switch snapshot.Status {
	case OllamaCloudUsageStatusOK:
		if snapshot.FetchedAt == nil || snapshot.FetchedAt.IsZero() {
			return time.Time{}, true
		}
		fetchedAt := snapshot.FetchedAt.UTC()
		if groupLastUsedAt == nil || !groupLastUsedAt.After(fetchedAt) {
			return time.Time{}, false
		}
		lastUsed := groupLastUsedAt.UTC()
		dueAt := ollamaEarlierTime(lastUsed.Add(debounce), fetchedAt.Add(maxWait))
		// 保留两次成功抓取之间原有的硬下限。成功路径已不再读取 next_refresh_at，
		// 而 NextOllamaCloudUsageDelay 原本通过该字段应用 OllamaCloudUsageMinIntervalMinutes；
		// 若无此限制，间隔略大于防抖期的请求流量会让分组上游抓取频率远高于既有下限。
		if floor := fetchedAt.Add(OllamaCloudUsageMinFetchInterval); dueAt.Before(floor) {
			return floor, true
		}
		return dueAt, true
	case OllamaCloudUsageStatusFailed, OllamaCloudUsageStatusUnauthorized:
		if snapshot.LastAttemptAt.IsZero() {
			return time.Time{}, true
		}
		lastAttempt := snapshot.LastAttemptAt.UTC()
		if groupLastUsedAt == nil || !groupLastUsedAt.After(lastAttempt) {
			return time.Time{}, false
		}
		lastUsed := groupLastUsedAt.UTC()
		activityDue := ollamaEarlierTime(lastUsed.Add(debounce), lastAttempt.Add(maxWait))
		if !snapshot.NextRefreshAt.IsZero() && snapshot.NextRefreshAt.UTC().After(activityDue) {
			return snapshot.NextRefreshAt.UTC(), true
		}
		return activityDue, true
	default:
		return time.Time{}, true
	}
}

// MaxOllamaCloudUsageGroupLastUsed 返回分组成员中最新的 last_used_at。
func MaxOllamaCloudUsageGroupLastUsed(accounts []Record) *time.Time {
	var latest *time.Time
	for i := range accounts {
		candidate := accounts[i].LastUsedAt
		if candidate == nil || candidate.IsZero() {
			continue
		}
		if latest == nil || candidate.After(*latest) {
			ts := candidate.UTC()
			latest = &ts
		}
	}
	return latest
}
func NextOllamaCloudUsageDelay(intervalMinutes, failureCount int, retryAfterDuration time.Duration, jitter func(int64) int64) time.Duration {
	minimumDelay := retryAfterDuration
	base := time.Duration(intervalMinutes) * time.Minute
	if base < OllamaCloudUsageMinIntervalMinutes*time.Minute {
		base = OllamaCloudUsageMinIntervalMinutes * time.Minute
	}
	if failureCount > 0 {
		shift := min(failureCount-1, 6)
		base *= time.Duration(1 << shift)
	}
	if base > OllamaCloudUsageMaxDelay {
		base = OllamaCloudUsageMaxDelay
	}
	if retryAfterDuration > base {
		base = retryAfterDuration
	}
	jitterRange := base / 10
	if jitterRange > 5*time.Minute {
		jitterRange = 5 * time.Minute
	}
	if jitterRange > 0 {
		base += time.Duration(jitter(int64(jitterRange)*2+1)) - jitterRange
	}
	if base < minimumDelay {
		return minimumDelay
	}
	if base < time.Minute {
		return time.Minute
	}
	return base
}
func ollamaEarlierTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// DecodeOllamaCloudUsageSettings 只解析原 JSON 和默认值，不读取存储。
func DecodeOllamaCloudUsageSettings(raw string) (*OllamaCloudUsageSettings, error) {
	defaults := DefaultOllamaCloudUsageSettings()
	if strings.TrimSpace(raw) == "" {
		return defaults, nil
	}
	settings := *defaults
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return nil, fmt.Errorf("parse Ollama Cloud usage settings: %w", err)
	}
	if settings.IntervalMinutes == 0 {
		settings.IntervalMinutes = defaults.IntervalMinutes
	}
	if settings.DebounceMinutes == 0 {
		settings.DebounceMinutes = defaults.DebounceMinutes
	}
	NormalizeOllamaCloudUsageSettings(&settings)
	return &settings, nil
}

// EncodeOllamaCloudUsageSettings 保留验证、默认值和 JSON 编码的原顺序。
func EncodeOllamaCloudUsageSettings(settings *OllamaCloudUsageSettings) (string, error) {
	if settings == nil {
		return "", infraerrors.BadRequest("INVALID_OLLAMA_CLOUD_USAGE_SETTINGS", "settings cannot be nil")
	}
	if settings.DebounceMinutes == 0 {
		// 旧客户端省略 debounce_minutes 时沿用安全默认值。
		settings.DebounceMinutes = OllamaCloudUsageDefaultDebounceMinutes
	}
	if settings.IntervalMinutes < OllamaCloudUsageMinIntervalMinutes || settings.IntervalMinutes > OllamaCloudUsageMaxIntervalMinutes {
		return "", infraerrors.BadRequest(
			"INVALID_OLLAMA_CLOUD_USAGE_INTERVAL",
			fmt.Sprintf("interval_minutes must be between %d and %d", OllamaCloudUsageMinIntervalMinutes, OllamaCloudUsageMaxIntervalMinutes),
		)
	}
	if settings.DebounceMinutes < OllamaCloudUsageMinDebounceMinutes || settings.DebounceMinutes > OllamaCloudUsageMaxDebounceMinutes {
		return "", infraerrors.BadRequest(
			"INVALID_OLLAMA_CLOUD_USAGE_DEBOUNCE",
			fmt.Sprintf("debounce_minutes must be between %d and %d", OllamaCloudUsageMinDebounceMinutes, OllamaCloudUsageMaxDebounceMinutes),
		)
	}
	// 到期时间为 min(lastUsed+debounce, fetchedAt+maxWait)。当 debounce 达到 maxWait 时，
	// 防抖项永远无法生效，配置会被静默忽略，因此拒绝该组合。
	if settings.DebounceMinutes >= settings.IntervalMinutes {
		return "", infraerrors.BadRequest(
			"INVALID_OLLAMA_CLOUD_USAGE_DEBOUNCE",
			fmt.Sprintf("debounce_minutes (%d) must be less than interval_minutes (%d)", settings.DebounceMinutes, settings.IntervalMinutes),
		)
	}
	NormalizeOllamaCloudUsageSettings(settings)
	data, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("marshal Ollama Cloud usage settings: %w", err)
	}
	return string(data), nil
}
