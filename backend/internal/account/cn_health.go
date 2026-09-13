// 本文件拥有已解析供应商信号的可恢复健康规则，报文识别仍由平台适配完成。
package account

import (
	"context"
	"strings"
	"time"
)

// ApplyCNConcurrencyLimit 将 Kimi 并发限制写为短期临时不可调度，
// 保留当前请求的切号信号，并确保不会进入累计 403 永久禁用计数。
func (s *HealthService) ApplyCNConcurrencyLimit(
	ctx context.Context,
	account *Record, reason string,
) {
	until := s.options.Now().Add(time.Duration(OpenAI403CooldownMinutesDefault) * time.Minute)
	s.notifyAccountSchedulingBlocked(account, until, "cn_concurrency_limit")
	if err := s.accountRepo.SetTempUnschedulable(ctx, account.ID, until, reason); err != nil {
		s.options.Warn("cn_concurrency_limit_set_temp_unschedulable_failed", "account_id", account.ID, "error", err)
		return
	}
	s.options.Info("cn_provider_concurrency_limited",
		"account_id", account.ID,
		"platform", account.Platform,
		"until", until.UTC(),
	)
}

// ApplyCNInsufficientBalance 把余额不足标记为可恢复的临时停调：
// 写入绑定身份的临时停调，冷却覆盖两个余额检测周期，
// 由周期任务在余额恢复后清除。返回前已通知调度阻塞。
func (s *HealthService) ApplyCNInsufficientBalance(
	ctx context.Context,
	account *Record,
	upstreamMsg string,
) {
	identityHash := CNUsageMonitorIdentityFingerprint(account)
	if identityHash == "" {
		identityHash = "unknown"
	}
	msg := CNUsageMonitorReason(identityHash)
	if upstreamMsg = strings.TrimSpace(upstreamMsg); upstreamMsg != "" {
		msg += ": " + upstreamMsg
	}

	until := s.options.Now().Add(CNBalanceCooldownDuration(s.options.CNIntervalMinutes))
	s.notifyAccountSchedulingBlocked(account, until, "cn_insufficient_balance")
	if err := s.accountRepo.SetTempUnschedulable(ctx, account.ID, until, msg); err != nil {
		s.options.Warn("cn_balance_set_temp_unschedulable_failed", "account_id", account.ID, "error", err)
		return
	}
	s.options.Info("cn_provider_insufficient_balance",
		"account_id", account.ID,
		"platform", account.Platform,
		"until", until.UTC(),
	)
}

// CNBalanceCooldownDuration 返回余额不足临时停调的持续时长（= 2× 余额检测周期，
// 默认 20 分钟）。周期任务会在余额恢复后提前清除，故此处只需保证冷却覆盖到下一次
// 周期检测即可。
func CNBalanceCooldownDuration(intervalMinutes int) time.Duration {
	minutes := 10
	if intervalMinutes > 0 {
		minutes = intervalMinutes
	}
	cooldown := time.Duration(minutes) * time.Minute * 2
	if cooldown < time.Minute {
		cooldown = 10 * time.Minute
	}
	return cooldown
}

// CNProviderQuotaSnapshotReset 读取 Coding Plan 统一快照中最早一个仍在未来的窗口
// 重置时间（5h / weekly）。429 多数由 5h 滚动窗口触发，取较早的重置点可避免
// 把账号冷却到 weekly 重置（可达数天）的过度停调；如果确是 weekly 窗口耗尽，
// 周期额度探测刷新快照后阈值评估会再次停调到正确的时间点。
// 无快照或均已过期返回 nil。
func CNProviderQuotaSnapshotReset(account *Record, now time.Time) *time.Time {
	if account == nil || !account.IsCNProvider() || !account.IsCodingPlan() {
		return nil
	}
	snapshot := ValidCNUsageMonitorSnapshot(account)
	if snapshot == nil || snapshot.Mode != "limits" {
		return nil
	}
	var earliest *time.Time
	for _, limit := range snapshot.Limits {
		t := clonePointer(limit.ResetAt)
		if t == nil || !t.After(now) {
			continue
		}
		if earliest == nil || t.Before(*earliest) {
			earliest = t
		}
	}
	return earliest
}
func (s *HealthService) ApplyCNQuotaSnapshotCooldown(ctx context.Context, account *Record) bool {
	if account == nil || !account.IsCNProvider() {
		return false
	}
	// 2) Coding Plan 窗口耗尽：冷却到快照中最早的窗口重置点（见
	// CNProviderQuotaSnapshotReset：429 多由 5h 窗口触发，取较早点避免过度停调）。
	if account.IsCodingPlan() {
		if until := CNProviderQuotaSnapshotReset(account, s.options.Now()); until != nil {
			s.notifyAccountSchedulingBlocked(account, *until, "429")
			if err := s.accountRepo.SetRateLimited(ctx, account.ID, *until); err != nil {
				s.options.Warn("rate_limit_set_failed", "account_id", account.ID, "error", err)
				return true
			}
			s.options.Info("cn_coding_plan_rate_limited",
				"account_id", account.ID,
				"platform", account.Platform,
				"reset_at", *until,
			)
			return true
		}
	}
	return false
}
