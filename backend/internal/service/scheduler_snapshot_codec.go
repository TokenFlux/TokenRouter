// LegacySchedulerCodec 保留旧账号 JSON 编码和字段投影，不持有缓存或调度状态；S11/S15 清理旧形状时退出。
package service

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

type LegacySchedulerCodec struct{}

func (LegacySchedulerCodec) Encode(value scheduler.SnapshotAccount) ([]byte, []byte, error) {
	v, err := LegacySnapshotValue(value)
	if err != nil {
		return nil, nil, err
	}
	if v == nil {
		return nil, nil, fmt.Errorf("nil scheduler account")
	}
	return marshalSchedulerCacheAccount(*v)
}
func (LegacySchedulerCodec) Decode(raw any) (scheduler.SnapshotAccount, error) {
	v, err := decodeCachedAccount(raw)
	return LegacySnapshotWrap(v), err
}
func (LegacySchedulerCodec) LastUsedAt(value scheduler.SnapshotAccount) (*time.Time, error) {
	v, err := LegacySnapshotValue(value)
	if err != nil || v == nil {
		return nil, err
	}
	return v.LastUsedAt, nil
}
func (LegacySchedulerCodec) SetLastUsedAt(value scheduler.SnapshotAccount, at *time.Time) error {
	v, err := LegacySnapshotValue(value)
	if err != nil {
		return err
	}
	if v != nil {
		v.LastUsedAt = at
	}
	return nil
}
func (LegacySchedulerCodec) Metadata(value Account) Account {
	return buildSchedulerMetadataAccount(value)
}
func decodeCachedAccount(val any) (*Account, error) {
	var payload []byte
	switch raw := val.(type) {
	case string:
		payload = []byte(raw)
	case []byte:
		payload = raw
	default:
		return nil, fmt.Errorf("unexpected account cache type: %T", val)
	}
	var account Account
	if err := json.Unmarshal(payload, &account); err != nil {
		return nil, err
	}
	return &account, nil
}

func marshalSchedulerCacheAccount(account Account) ([]byte, []byte, error) {
	fullPayload, err := json.Marshal(account)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal account: %w", err)
	}
	metaPayload, err := json.Marshal(buildSchedulerMetadataAccount(account))
	if err != nil {
		return nil, nil, fmt.Errorf("marshal account metadata: %w", err)
	}
	return fullPayload, metaPayload, nil
}

func buildSchedulerMetadataAccount(account Account) Account {
	return Account{
		ID:                      account.ID,
		Name:                    account.Name,
		Platform:                account.Platform,
		Type:                    account.Type,
		Concurrency:             account.Concurrency,
		LoadFactor:              account.LoadFactor,
		Priority:                account.Priority,
		RateMultiplier:          account.RateMultiplier,
		Status:                  account.Status,
		LastUsedAt:              account.LastUsedAt,
		ExpiresAt:               account.ExpiresAt,
		AutoPauseOnExpired:      account.AutoPauseOnExpired,
		Schedulable:             account.Schedulable,
		RateLimitedAt:           account.RateLimitedAt,
		RateLimitResetAt:        account.RateLimitResetAt,
		OverloadUntil:           account.OverloadUntil,
		TempUnschedulableUntil:  account.TempUnschedulableUntil,
		TempUnschedulableReason: account.TempUnschedulableReason,
		SessionWindowStart:      account.SessionWindowStart,
		SessionWindowEnd:        account.SessionWindowEnd,
		SessionWindowStatus:     account.SessionWindowStatus,
		ParentAccountID:         account.ParentAccountID,
		QuotaDimension:          account.QuotaDimension,
		AccountGroups:           filterSchedulerAccountGroups(account.AccountGroups),
		GroupIDs:                filterSchedulerGroupIDs(account.GroupIDs, account.AccountGroups),
		Credentials:             filterSchedulerCredentials(account.Credentials),
		Extra:                   filterSchedulerExtra(account.Extra),
	}
}

func filterSchedulerAccountGroups(accountGroups []AccountGroup) []AccountGroup {
	if len(accountGroups) == 0 {
		return nil
	}

	filtered := make([]AccountGroup, 0, len(accountGroups))
	for _, ag := range accountGroups {
		if ag.GroupID <= 0 {
			continue
		}
		filtered = append(filtered, AccountGroup{
			AccountID: ag.AccountID,
			GroupID:   ag.GroupID,
			CreatedAt: ag.CreatedAt,
		})
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func filterSchedulerGroupIDs(groupIDs []int64, accountGroups []AccountGroup) []int64 {
	if len(groupIDs) == 0 && len(accountGroups) == 0 {
		return nil
	}

	seen := make(map[int64]struct{}, len(groupIDs)+len(accountGroups))
	filtered := make([]int64, 0, len(groupIDs)+len(accountGroups))
	for _, id := range groupIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		filtered = append(filtered, id)
	}
	for _, ag := range accountGroups {
		if ag.GroupID <= 0 {
			continue
		}
		if _, ok := seen[ag.GroupID]; ok {
			continue
		}
		seen[ag.GroupID] = struct{}{}
		filtered = append(filtered, ag.GroupID)
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func filterSchedulerCredentials(credentials map[string]any) map[string]any {
	if len(credentials) == 0 {
		return nil
	}
	keys := []string{"model_mapping", "compact_model_mapping", "model_whitelist", "upstream_protocols", "auth_mode", "openai_auth_mode", "account_mode", "api_protocol", "openai_workload_capabilities", "api_key", "project_id", "oauth_type", "plan_type"}
	filtered := make(map[string]any)
	for _, key := range keys {
		if value, ok := credentials[key]; ok && value != nil {
			filtered[key] = value
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func filterSchedulerExtra(extra map[string]any) map[string]any {
	if len(extra) == 0 {
		return nil
	}
	keys := []string{
		"quota_limit",
		"quota_used",
		"quota_daily_limit",
		"quota_daily_used",
		"quota_daily_start",
		"quota_daily_reset_mode",
		"quota_daily_reset_hour",
		"quota_weekly_limit",
		"quota_weekly_used",
		"quota_weekly_start",
		"quota_weekly_reset_mode",
		"quota_weekly_reset_day",
		"quota_weekly_reset_hour",
		"quota_reset_timezone",
		"mixed_scheduling",
		"window_cost_limit",
		"window_cost_sticky_reserve",
		"max_sessions",
		"session_idle_timeout_minutes",
		"openai_oauth_responses_websockets_v2_enabled",
		"openai_oauth_responses_websockets_v2_mode",
		"openai_apikey_responses_websockets_v2_enabled",
		"openai_apikey_responses_websockets_v2_mode",
		"responses_websockets_v2_enabled",
		"openai_ws_enabled",
		"openai_ws_force_http",
		"openai_text_route_mode",
		"openai_compact_mode",
		"openai_native_compaction_v2_mode",
		"openai_responses_continuation_supported",
		// 透传开关必须进投影：候选过滤(ListSchedulableAccounts)读的是本投影，
		// 而 Account.IsModelSupported 靠 extra 上的这两个键短路 model_mapping 白名单。
		// 裁掉它们，透传账号在选号阶段会退回按(常为过期的)白名单判定并被误判为
		// model_not_supported —— 转发阶段却仍按透传工作，表现为"单独测账号能通、
		// 走网关报 no available accounts"。
		"openai_passthrough",
		"openai_oauth_passthrough",
		"codex_fingerprint_mode",
		"codex_fingerprint_seed",
		"codex_5h_used_percent",
		"codex_7d_used_percent",
		"codex_5h_reset_at",
		"codex_7d_reset_at",
		"codex_5h_reset_after_seconds",
		"codex_7d_reset_after_seconds",
		"codex_usage_updated_at",
		"auto_pause_5h_threshold",
		"auto_pause_7d_threshold",
		"auto_pause_5h_disabled",
		"auto_pause_7d_disabled",
		"model_rate_limits",
		// 媒体资格判定依赖显式覆盖和精简计费观测，调度缓存不得丢失。
		GrokMediaEligibleExtraKey,
		"grok_billing_snapshot",
	}
	filtered := make(map[string]any)
	for _, key := range keys {
		if value, ok := extra[key]; ok && value != nil {
			filtered[key] = value
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

// AcquireBucketLease 为生产重建提供持有者安全的释放句柄，复用原 Redis 客户端。
