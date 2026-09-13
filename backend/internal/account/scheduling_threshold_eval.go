// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	json "encoding/json"
	math "math"
	strconv "strconv"
	strings "strings"
	time "time"
)

// AccountSchedulingThresholdDecision 表示单个账号的纯停调判定结果。
type AccountSchedulingThresholdDecision struct {
	ShouldPause      bool
	Platform         string
	Window           string
	Scope            string
	ThresholdPercent int
	UsedPercent      float64
	Until            *time.Time
}

type SchedulingThresholdCandidate struct {
	Window      string
	Scope       string
	UsedPercent float64
	Until       *time.Time
}

const accountSchedulingThresholdCredentialKey = "account_scheduling_threshold"

// EvaluateAccountSchedulingThreshold 根据当前平台阈值快照判断账号是否应暂停调度。
func EvaluateAccountSchedulingThreshold(account *Record, thresholds map[string]int, now time.Time) AccountSchedulingThresholdDecision {
	decision := AccountSchedulingThresholdDecision{}
	if account == nil {
		return decision
	}

	decision.Platform = strings.ToLower(strings.TrimSpace(account.Platform))
	if decision.Platform == "" {
		return decision
	}
	if !isAllowedSchedulingThresholdPlatform(decision.Platform) {
		return decision
	}

	threshold, ok := resolveEffectiveAccountSchedulingThreshold(account, thresholds, decision.Platform)
	decision.ThresholdPercent = threshold
	if !ok || threshold >= 100 {
		return decision
	}

	var winner *SchedulingThresholdCandidate
	switch decision.Platform {
	case PlatformOpenAI:
		winner = pickLatestResetSchedulingCandidate(openAIThresholdCandidates(account, now), threshold, now)
	case PlatformAnthropic:
		winner = pickLatestResetSchedulingCandidate(anthropicThresholdCandidates(account), threshold, now)
	case PlatformGrok:
		winner = pickLatestResetSchedulingCandidate(grokThresholdCandidates(account), threshold, now)
	case PlatformKimi:
		winner = pickLatestResetSchedulingCandidate(CNProviderThresholdCandidates(account, PlatformKimi), threshold, now)
	case PlatformZhipu:
		winner = pickLatestResetSchedulingCandidate(CNProviderThresholdCandidates(account, PlatformZhipu), threshold, now)
	default:
		return decision
	}

	if winner == nil {
		return decision
	}

	decision.ShouldPause = true
	decision.Window = winner.Window
	decision.Scope = winner.Scope
	decision.UsedPercent = winner.UsedPercent
	decision.Until = winner.Until
	return decision
}

func EvaluateAnthropicFableSchedulingThreshold(account *Record, thresholds map[string]int, now time.Time) AccountSchedulingThresholdDecision {
	decision := AccountSchedulingThresholdDecision{}
	if account == nil || !strings.EqualFold(strings.TrimSpace(account.Platform), PlatformAnthropic) {
		return decision
	}

	decision.Platform = PlatformAnthropic
	threshold, ok := resolveEffectiveAccountSchedulingThreshold(account, thresholds, PlatformAnthropic)
	decision.ThresholdPercent = threshold
	if !ok || threshold >= 100 {
		return decision
	}

	candidate := anthropicFableThresholdCandidate(account)
	if !CandidateMatchesThreshold(candidate, threshold, now) {
		return decision
	}

	decision.ShouldPause = true
	decision.Window = candidate.Window
	decision.Scope = candidate.Scope
	decision.UsedPercent = candidate.UsedPercent
	decision.Until = candidate.Until
	return decision
}

func isAllowedSchedulingThresholdPlatform(platform string) bool {
	for _, allowed := range AllowedSchedulingThresholdPlatforms {
		if platform == allowed {
			return true
		}
	}
	return false
}

func resolveEffectiveAccountSchedulingThreshold(account *Record, thresholds map[string]int, platform string) (int, bool) {
	if account != nil {
		if threshold, ok := accountSchedulingThresholdOverride(account); ok {
			return threshold, true
		}
	}
	return lookupAccountSchedulingThreshold(thresholds, platform)
}

func accountSchedulingThresholdOverride(account *Record) (int, bool) {
	if account == nil || len(account.Credentials) == 0 {
		return 0, false
	}
	raw, ok := account.Credentials[accountSchedulingThresholdCredentialKey]
	if !ok {
		return 0, false
	}
	return parseAccountSchedulingThresholdValue(raw)
}

func parseAccountSchedulingThresholdValue(raw any) (int, bool) {
	var value int
	switch v := raw.(type) {
	case int:
		value = v
	case int64:
		value = int(v)
	case float64:
		value = int(math.Round(v))
	case float32:
		value = int(math.Round(float64(v)))
	case json.Number:
		parsed, err := v.Float64()
		if err != nil {
			return 0, false
		}
		value = int(math.Round(parsed))
	case string:
		raw := strings.TrimSpace(v)
		parsed, err := strconv.Atoi(raw)
		if err == nil {
			value = parsed
			break
		}
		parsedFloat, floatErr := strconv.ParseFloat(raw, 64)
		if floatErr != nil {
			return 0, false
		}
		value = int(math.Round(parsedFloat))
	default:
		return 0, false
	}
	if value < 1 || value > 100 {
		return 0, false
	}
	return value, true
}

func lookupAccountSchedulingThreshold(thresholds map[string]int, platform string) (int, bool) {
	if len(thresholds) == 0 {
		return 0, false
	}
	value, ok := thresholds[platform]
	return value, ok
}

func openAIThresholdCandidates(account *Record, now time.Time) []*SchedulingThresholdCandidate {
	if account == nil {
		return nil
	}
	if !openAICodexSnapshotIdentityTrusted(account) {
		return nil
	}
	return []*SchedulingThresholdCandidate{
		openAIThresholdCandidate(account.Extra, "5h", now),
		openAIThresholdCandidate(account.Extra, "7d", now),
	}
}

func openAICodexSnapshotIdentityTrusted(account *Record) bool {
	if account == nil || !account.IsOpenAIOAuth() || len(account.Extra) == 0 {
		return true
	}

	if identityValuesConflict(
		firstStringValue(account.Credentials, "email"),
		firstStringValue(account.Extra, "email", "email_address"),
	) {
		return false
	}
	if identityValuesConflict(
		firstStringValue(account.Credentials, "chatgpt_account_id"),
		firstStringValue(account.Extra, "chatgpt_account_id", "account_id"),
	) {
		return false
	}
	if identityValuesConflict(
		firstStringValue(account.Credentials, "workspace_id", "chatgpt_workspace_id", "organization_id", "org_id"),
		firstStringValue(account.Extra, "workspace_id", "chatgpt_workspace_id", "organization_id", "org_id"),
	) {
		return false
	}
	return true
}

func identityValuesConflict(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	return left != "" && right != "" && !strings.EqualFold(left, right)
}

// firstStringValue 返回给定键中第一个非空字符串，用于 OpenAI Codex 阈值快照的身份匹配。
func firstStringValue(values map[string]any, keys ...string) string {
	if len(values) == 0 {
		return ""
	}
	for _, key := range keys {
		raw, ok := values[key]
		if !ok || raw == nil {
			continue
		}
		switch typed := raw.(type) {
		case string:
			if v := strings.TrimSpace(typed); v != "" {
				return v
			}
		default:
			encoded, err := json.Marshal(raw)
			if err == nil {
				v := strings.Trim(strings.TrimSpace(string(encoded)), `"`)
				if v != "" && v != "null" {
					return v
				}
			}
		}
	}
	return ""
}

func openAIThresholdCandidate(extra map[string]any, window string, now time.Time) *SchedulingThresholdCandidate {
	if len(extra) == 0 {
		return nil
	}

	var (
		usedPercentKey string
		resetAtKey     string
	)
	switch window {
	case "5h":
		usedPercentKey = "codex_5h_used_percent"
		resetAtKey = "codex_5h_reset_at"
	case "7d":
		usedPercentKey = "codex_7d_used_percent"
		resetAtKey = "codex_7d_reset_at"
	default:
		return nil
	}

	usedPercent, ok := extra[usedPercentKey]
	if !ok {
		return nil
	}
	if OpenAIQuotaWindowReset(extra, window, now) || OpenAICodexSnapshotStaleForPause(extra, now) {
		return nil
	}
	return &SchedulingThresholdCandidate{
		Window:      window,
		UsedPercent: schedulingPercentValue(usedPercent),
		Until:       parseSchedulingResetAt(extra[resetAtKey]),
	}
}

func anthropicThresholdCandidates(account *Record) []*SchedulingThresholdCandidate {
	if account == nil {
		return nil
	}

	var candidates []*SchedulingThresholdCandidate
	if usedPercent := utilizationAsPercent(account.Extra["session_window_utilization"]); usedPercent > 0 {
		candidates = append(candidates, &SchedulingThresholdCandidate{
			Window:      "5h",
			UsedPercent: usedPercent,
			Until:       CloneThresholdTime(account.SessionWindowEnd),
		})
	}
	if usedPercent := utilizationAsPercent(account.Extra["passive_usage_7d_utilization"]); usedPercent > 0 {
		candidates = append(candidates, &SchedulingThresholdCandidate{
			Window:      "7d",
			UsedPercent: usedPercent,
			Until:       parseSchedulingResetAt(account.Extra["passive_usage_7d_reset"]),
		})
	}
	return candidates
}

func anthropicFableThresholdCandidate(account *Record) *SchedulingThresholdCandidate {
	if account == nil {
		return nil
	}
	usedPercent := utilizationAsPercent(account.Extra["passive_usage_7d_oi_utilization"])
	if usedPercent <= 0 {
		return nil
	}
	return &SchedulingThresholdCandidate{
		Window:      "7d_oi",
		Scope:       AnthropicFableRateLimitKey,
		UsedPercent: usedPercent,
		Until:       parseSchedulingResetAt(account.Extra["passive_usage_7d_oi_reset"]),
	}
}

// grokThresholdCandidates 只读取响应头投影的滚动额度窗口，
// 不使用官方账单的 7 天或 30 天窗口执行自动停调。
func grokThresholdCandidates(account *Record) []*SchedulingThresholdCandidate {
	if account == nil {
		return nil
	}
	return []*SchedulingThresholdCandidate{
		{
			Window:      "quota",
			Scope:       "grok",
			UsedPercent: schedulingPercentValue(account.Extra["grok_sched_utilization"]),
			Until:       parseSchedulingResetAt(account.Extra["grok_sched_reset_at"]),
		},
	}
}

// CNProviderThresholdCandidates 读取独立监控保存的统一窗口快照。快照身份必须与
// 当前凭据、地址、代理和 TLS 设置一致，避免旧身份结果参与调度。
func CNProviderThresholdCandidates(account *Record, provider string) []*SchedulingThresholdCandidate {
	if account == nil || account.Platform != provider || !account.IsCodingPlan() {
		return nil
	}
	snapshot := ValidCNUsageMonitorSnapshot(account)
	if snapshot == nil || snapshot.Mode != "limits" || snapshot.ObservedAt == nil {
		return nil
	}
	candidates := make([]*SchedulingThresholdCandidate, 0, len(snapshot.Limits))
	for _, limit := range snapshot.Limits {
		if limit.Used == nil || limit.ResetAt == nil {
			continue
		}
		candidates = append(candidates, &SchedulingThresholdCandidate{
			Window:      limit.Name,
			Scope:       provider,
			UsedPercent: *limit.Used,
			Until:       CloneThresholdTime(limit.ResetAt),
		})
	}
	return candidates
}

func pickLatestResetSchedulingCandidate(candidates []*SchedulingThresholdCandidate, threshold int, now time.Time) *SchedulingThresholdCandidate {
	var winner *SchedulingThresholdCandidate
	for _, candidate := range candidates {
		if !CandidateMatchesThreshold(candidate, threshold, now) {
			continue
		}
		if winner == nil || candidate.Until.After(*winner.Until) {
			winner = candidate
			continue
		}
		if winner.Until.Equal(*candidate.Until) && candidate.UsedPercent > winner.UsedPercent {
			winner = candidate
		}
	}
	return winner
}

func CandidateMatchesThreshold(candidate *SchedulingThresholdCandidate, threshold int, now time.Time) bool {
	if candidate == nil || candidate.Until == nil || !candidate.Until.After(now) {
		return false
	}
	return candidate.UsedPercent >= float64(threshold)
}

func utilizationAsPercent(raw any) float64 {
	switch v := raw.(type) {
	case float64:
		if v >= 0 && v <= 1 {
			return v * 100
		}
		return v
	case float32:
		value := float64(v)
		if value >= 0 && value <= 1 {
			return value * 100
		}
		return value
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		value, err := v.Float64()
		if err != nil {
			return 0
		}
		if strings.Contains(v.String(), ".") && value >= 0 && value <= 1 {
			return value * 100
		}
		return value
	case string:
		trimmed := strings.TrimSpace(v)
		value, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0
		}
		if strings.Contains(trimmed, ".") && value >= 0 && value <= 1 {
			return value * 100
		}
		return value
	default:
		return 0
	}
}

func schedulingPercentValue(raw any) float64 {
	switch v := raw.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		value, err := v.Float64()
		if err != nil {
			return 0
		}
		return value
	case string:
		value, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0
		}
		return value
	default:
		return 0
	}
}

func parseSchedulingResetAt(raw any) *time.Time {
	switch v := raw.(type) {
	case nil:
		return nil
	case time.Time:
		ts := v
		return &ts
	case *time.Time:
		return CloneThresholdTime(v)
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil
		}
		ts, err := ParseSchedulingTime(trimmed)
		if err != nil {
			return nil
		}
		return &ts
	case json.Number:
		if value, err := v.Int64(); err == nil && value > 0 {
			ts := time.Unix(value, 0)
			return &ts
		}
		if value, err := v.Float64(); err == nil && value > 0 {
			ts := time.Unix(int64(value), 0)
			return &ts
		}
	case float64:
		if v > 0 {
			ts := time.Unix(int64(v), 0)
			return &ts
		}
	case float32:
		if v > 0 {
			ts := time.Unix(int64(v), 0)
			return &ts
		}
	case int:
		if v > 0 {
			ts := time.Unix(int64(v), 0)
			return &ts
		}
	case int64:
		if v > 0 {
			ts := time.Unix(v, 0)
			return &ts
		}
	}
	return nil
}

func ParseSchedulingTime(raw string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.000Z",
	}
	for _, format := range formats {
		if ts, err := time.Parse(format, raw); err == nil {
			return ts, nil
		}
	}
	return time.Time{}, strconv.ErrSyntax
}

func CloneThresholdTime(src *time.Time) *time.Time {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}
