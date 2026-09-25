// 本文件维护 dto 的所属能力；兼容入口复用唯一实现。
package dto

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	proxydto "github.com/TokenFlux/TokenRouter/internal/egress/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
)

func AccountFromRecordShallow(a *account.Record) *Account {
	if a == nil {
		return nil
	}
	a = account.CloneRecord(a)
	runtime := account.RuntimeConfig{Extra: a.Extra, Concurrency: a.Concurrency, SessionWindowStart: a.SessionWindowStart, SessionWindowEnd: a.SessionWindowEnd}
	redactedCreds, credsStatus := RedactCredentials(a.Credentials)
	extra := redactAccountManagedExtra(a.Extra)
	var ollamaCloudUsage *account.OllamaCloudUsageState
	if state := account.OllamaCloudUsageStateFromAccount(a); state.Eligible {
		ollamaCloudUsage = state
	}
	out := &Account{
		ID:                      a.ID,
		Name:                    a.Name,
		Notes:                   a.Notes,
		Platform:                a.Platform,
		Type:                    a.Type,
		Credentials:             redactedCreds,
		CredentialsStatus:       credsStatus,
		Extra:                   extra,
		OllamaCloudUsage:        ollamaCloudUsage,
		ProxyID:                 a.ProxyID,
		ProxyFallbackOriginID:   a.ProxyFallbackOriginID,
		ProxyFallbackOriginName: a.ProxyFallbackOriginName,
		Concurrency:             a.Concurrency,
		LoadFactor:              a.LoadFactor,
		Priority:                a.Priority,
		RateMultiplier:          a.BillingRateMultiplier(),
		Status:                  a.Status,
		ErrorMessage:            a.ErrorMessage,
		LastUsedAt:              a.LastUsedAt,
		ExpiresAt:               timeToUnixSeconds(a.ExpiresAt),
		AutoPauseOnExpired:      a.AutoPauseOnExpired,
		CreatedAt:               a.CreatedAt,
		UpdatedAt:               a.UpdatedAt,
		Schedulable:             a.Schedulable,
		RateLimitedAt:           a.RateLimitedAt,
		RateLimitResetAt:        a.RateLimitResetAt,
		OverloadUntil:           a.OverloadUntil,
		TempUnschedulableUntil:  a.TempUnschedulableUntil,
		TempUnschedulableReason: a.TempUnschedulableReason,
		QuotaAutoPaused:         a.QuotaAutoPaused,
		SessionWindowStart:      a.SessionWindowStart,
		SessionWindowEnd:        a.SessionWindowEnd,
		SessionWindowStatus:     a.SessionWindowStatus,
		GroupIDs:                a.GroupIDs,
		ParentAccountID:         a.ParentAccountID,
		QuotaDimension:          a.QuotaDimension,
	}

	// 提取 5h 窗口费用控制和会话数量控制配置（仅 Anthropic OAuth/SetupToken 账号有效）
	if a.IsAnthropicOAuthOrSetupToken() {
		if limit := runtime.GetWindowCostLimit(); limit > 0 {
			out.WindowCostLimit = &limit
		}
		if reserve := runtime.GetWindowCostStickyReserve(); reserve > 0 {
			out.WindowCostStickyReserve = &reserve
		}
		if maxSessions := runtime.GetMaxSessions(); maxSessions > 0 {
			out.MaxSessions = &maxSessions
		}
		if idleTimeout := runtime.GetSessionIdleTimeoutMinutes(); idleTimeout > 0 {
			out.SessionIdleTimeoutMin = &idleTimeout
		}
		if rpm := runtime.GetBaseRPM(); rpm > 0 {
			out.BaseRPM = &rpm
			strategy := runtime.GetRPMStrategy()
			out.RPMStrategy = &strategy
			buffer := runtime.GetRPMStickyBuffer()
			out.RPMStickyBuffer = &buffer
		}
		// 用户消息队列模式
		if mode := runtime.GetUserMsgQueueMode(); mode != "" {
			out.UserMsgQueueMode = &mode
		}
		// 会话ID伪装开关
		if a.IsSessionIDMaskingEnabled() {
			enabled := true
			out.EnableSessionIDMasking = &enabled
		}
		// 缓存 TTL 强制替换
		if a.IsCacheTTLOverrideEnabled() {
			enabled := true
			out.CacheTTLOverrideEnabled = &enabled
			target := a.GetCacheTTLOverrideTarget()
			out.CacheTTLOverrideTarget = &target
		}
		// 自定义 Base URL 中继转发
		if a.IsCustomBaseURLEnabled() {
			enabled := true
			out.CustomBaseURLEnabled = &enabled
			if customURL := a.GetCustomBaseURL(); customURL != "" {
				out.CustomBaseURL = &customURL
			}
		}
	}

	// TLS 指纹伪装字段支持 Anthropic OAuth/SetupToken 与 OpenAI OAuth。
	if a.SupportsTLSFingerprint() {
		if a.IsTLSFingerprintEnabled() {
			enabled := true
			out.EnableTLSFingerprint = &enabled
		}
		if profileID := a.GetTLSFingerprintProfileID(); profileID != 0 {
			out.TLSFingerprintProfileID = &profileID
		}
		if routerID := a.GetTLSFingerprintRouterID(); routerID != 0 {
			out.TLSFingerprintRouterID = &routerID
		}
	}

	if a.IsOpenAIOAuth() {
		policy := a.GetOpenAIOAuthClientPolicy()
		out.OpenAIOAuthClientPolicy = &policy
	}

	// 提取账号配额限制（apikey / bedrock 类型有效）
	if a.IsAPIKeyOrBedrock() {
		if limit := a.GetQuotaLimit(); limit > 0 {
			out.QuotaLimit = &limit
			used := a.GetQuotaUsed()
			out.QuotaUsed = &used
		}
		if limit := a.GetQuotaDailyLimit(); limit > 0 {
			out.QuotaDailyLimit = &limit
			used := a.GetQuotaDailyUsed()
			if a.IsDailyQuotaPeriodExpired() {
				used = 0
			}
			out.QuotaDailyUsed = &used
		}
		if limit := a.GetQuotaWeeklyLimit(); limit > 0 {
			out.QuotaWeeklyLimit = &limit
			used := a.GetQuotaWeeklyUsed()
			if a.IsWeeklyQuotaPeriodExpired() {
				used = 0
			}
			out.QuotaWeeklyUsed = &used
		}
		// 固定时间重置配置
		if mode := a.GetQuotaDailyResetMode(); mode == "fixed" {
			out.QuotaDailyResetMode = &mode
			hour := a.GetQuotaDailyResetHour()
			out.QuotaDailyResetHour = &hour
		}
		if mode := a.GetQuotaWeeklyResetMode(); mode == "fixed" {
			out.QuotaWeeklyResetMode = &mode
			day := a.GetQuotaWeeklyResetDay()
			out.QuotaWeeklyResetDay = &day
			hour := a.GetQuotaWeeklyResetHour()
			out.QuotaWeeklyResetHour = &hour
		}
		if a.GetQuotaDailyResetMode() == "fixed" || a.GetQuotaWeeklyResetMode() == "fixed" {
			tz := a.GetQuotaResetTimezone()
			out.QuotaResetTimezone = &tz
		}
		if a.Extra != nil {
			if v, ok := a.Extra["quota_daily_reset_at"].(string); ok && v != "" {
				out.QuotaDailyResetAt = &v
			}
			if v, ok := a.Extra["quota_weekly_reset_at"].(string); ok && v != "" {
				out.QuotaWeeklyResetAt = &v
			}
		}

		// 配额通知配置
		if enabled := a.GetQuotaNotifyDailyEnabled(); enabled {
			out.QuotaNotifyDailyEnabled = &enabled
		}
		if threshold := a.GetQuotaNotifyDailyThreshold(); threshold > 0 {
			out.QuotaNotifyDailyThreshold = &threshold
		}
		if enabled := a.GetQuotaNotifyWeeklyEnabled(); enabled {
			out.QuotaNotifyWeeklyEnabled = &enabled
		}
		if threshold := a.GetQuotaNotifyWeeklyThreshold(); threshold > 0 {
			out.QuotaNotifyWeeklyThreshold = &threshold
		}
		if enabled := a.GetQuotaNotifyTotalEnabled(); enabled {
			out.QuotaNotifyTotalEnabled = &enabled
		}
		if threshold := a.GetQuotaNotifyTotalThreshold(); threshold > 0 {
			out.QuotaNotifyTotalThreshold = &threshold
		}
	}

	return out
}

func redactAccountManagedExtra(extra map[string]any) map[string]any {
	if extra == nil {
		return nil
	}
	redacted := make(map[string]any, len(extra))
	for key, value := range extra {
		switch key {
		case account.OllamaCloudUsageSessionExtraKey,
			account.OllamaCloudUsageAutoRefreshExtraKey,
			account.OllamaCloudUsageSnapshotExtraKey:
			continue
		case account.UpstreamUsageQueryExtraKey:
			// Extra 可能来自历史数据库记录；即使旧数据曾把凭据写进
			// 查询配置，也只能向浏览器返回允许的三项非敏感字段。
			redacted[key] = redactUpstreamUsageQuery(value)
		default:
			redacted[key] = account.CloneValues(map[string]any{key: value})[key]
		}
	}
	return redacted
}

func redactUpstreamUsageQuery(value any) map[string]any {
	object, ok := value.(map[string]any)
	if !ok {
		payload, err := json.Marshal(value)
		if err != nil {
			return map[string]any{}
		}
		if err := json.Unmarshal(payload, &object); err != nil || object == nil {
			return map[string]any{}
		}
	}
	result := make(map[string]any, 3)
	if enabled, ok := object["enabled"].(bool); ok {
		result["enabled"] = enabled
	}
	if adapter, ok := object["adapter"].(string); ok {
		// 只回显已注册协议名，历史记录中的任意字符串可能包含误写入的凭据。
		if adapter == account.UpstreamUsageAdapterSub2API || adapter == account.UpstreamUsageAdapterNewAPI || adapter == account.UpstreamUsageAdapterZivv {
			result["adapter"] = adapter
		}
	}
	if baseURL, ok := object["base_url"].(string); ok {
		if parsed, err := url.Parse(strings.TrimSpace(baseURL)); err == nil && parsed.Scheme != "" && parsed.Host != "" &&
			parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" &&
			(strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) {
			result["base_url"] = strings.TrimRight(strings.TrimSpace(baseURL), "/")
		}
	}
	return result
}

func AccountFromRecord(a *account.Record) *Account {
	if a == nil {
		return nil
	}
	out := AccountFromRecordShallow(a)
	out.Proxy = proxydto.ProxyFromEgress(a.Proxy)
	if len(a.AccountGroups) > 0 {
		out.AccountGroups = make([]AccountGroup, 0, len(a.AccountGroups))
		for i := range a.AccountGroups {
			ag := a.AccountGroups[i]
			out.AccountGroups = append(out.AccountGroups, *AccountGroupFromRecord(&ag))
		}
	}
	if len(a.Groups) > 0 {
		out.Groups = make([]*Group, 0, len(a.Groups))
		for _, g := range a.Groups {
			out.Groups = append(out.Groups, routingdto.GroupFromRouting((*routing.Group)(g)))
		}
	}
	return out
}

func AccountGroupFromRecord(ag *account.GroupMembership) *AccountGroup {
	if ag == nil {
		return nil
	}
	return &AccountGroup{
		AccountID: ag.AccountID,
		GroupID:   ag.GroupID,
		CreatedAt: ag.CreatedAt,
		Account:   AccountFromRecordShallow(ag.Account),
		Group:     routingdto.GroupFromRouting((*routing.Group)(ag.Group)),
	}
}

func timeToUnixSeconds(value *time.Time) *int64 {
	if value == nil {
		return nil
	}
	ts := value.Unix()
	return &ts
}
