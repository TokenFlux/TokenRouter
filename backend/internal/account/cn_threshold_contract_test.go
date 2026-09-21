//go:build unit

package account

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

func attachCNMonitorLimits(account *Record, observedAt time.Time, limits []UpstreamUsageLimit) {
	if account.Extra == nil {
		account.Extra = make(map[string]any)
	}
	queryConfig, err := EffectiveUpstreamUsageConfig(account)
	if err != nil {
		panic(err)
	}
	queryConfig.Adapter = CNUpstreamUsageAdapterName(account)
	account.Extra[CNUsageMonitorSnapshotExtraKey] = &CNUsageMonitorSnapshot{
		Version:       CNUsageMonitorSnapshotVersion,
		Adapter:       queryConfig.Adapter,
		IdentityHash:  CNUsageMonitorIdentityFingerprint(account),
		Provider:      account.Platform,
		Mode:          "limits",
		Unit:          "PERCENT",
		Limits:        limits,
		ObservedAt:    &observedAt,
		LastAttemptAt: observedAt,
	}
}

func cnCodingTestAccount(platform string) *Record {
	return &Record{
		ID:       1,
		Platform: platform,
		Type:     capability.AccountTypeAPIKey,
		Status:   billing.StatusActive,
		Credentials: map[string]any{
			"api_key":      "sk-test",
			"account_mode": AccountModeCoding,
		},
		Extra: map[string]any{},
	}
}

// TestCNProviderThresholdCandidates 从统一监控快照读取 5h / weekly 候选。
func TestCNProviderThresholdCandidates(t *testing.T) {
	t.Parallel()
	observed := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	reset5h := observed.Add(3 * time.Hour)
	resetWeekly := observed.Add(4 * 24 * time.Hour)
	used5h, usedWeekly := 90.0, 50.0
	account := cnCodingTestAccount(capability.PlatformKimi)
	attachCNMonitorLimits(account, observed, []UpstreamUsageLimit{
		{Name: "5h", Used: &used5h, ResetAt: &reset5h},
		{Name: "weekly", Used: &usedWeekly, ResetAt: &resetWeekly},
	})
	cands := CNProviderThresholdCandidates(account, capability.PlatformKimi)
	require.Len(t, cands, 2)

	// 缺少 used 的窗口不产生候选。
	partial := cnCodingTestAccount(capability.PlatformKimi)
	attachCNMonitorLimits(partial, observed, []UpstreamUsageLimit{{Name: "5h", ResetAt: &reset5h}})
	require.Empty(t, filterNil(CNProviderThresholdCandidates(partial, capability.PlatformKimi)))

	// 身份变化后旧快照失效。
	account.Credentials["api_key"] = "sk-changed"
	require.Empty(t, CNProviderThresholdCandidates(account, capability.PlatformKimi))
}

func filterNil(cands []*SchedulingThresholdCandidate) []*SchedulingThresholdCandidate {
	var out []*SchedulingThresholdCandidate
	for _, c := range cands {
		if c != nil {
			out = append(out, c)
		}
	}
	return out
}

// TestEvaluateAccountSchedulingThreshold_KimiCodingPlan 集成验证：kimi coding 账号
// 5h 用量超阈值且窗口未重置 → 主动停调至 5h 重置点。
func TestEvaluateAccountSchedulingThreshold_KimiCodingPlan(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	reset := now.Add(3 * time.Hour)
	used5h, usedWeekly := 90.0, 30.0
	weeklyReset := now.Add(7 * 24 * time.Hour)
	account := cnCodingTestAccount(capability.PlatformKimi)
	attachCNMonitorLimits(account, now, []UpstreamUsageLimit{
		{Name: "5h", Used: &used5h, ResetAt: &reset},
		{Name: "weekly", Used: &usedWeekly, ResetAt: &weeklyReset},
	})
	decision := EvaluateAccountSchedulingThreshold(account, map[string]int{capability.PlatformKimi: 80}, now)
	require.True(t, decision.ShouldPause)
	require.Equal(t, capability.PlatformKimi, decision.Platform)
	require.Equal(t, "5h", decision.Window)
	require.InDelta(t, 90.0, decision.UsedPercent, 1e-9)
	require.NotNil(t, decision.Until)
	require.True(t, reset.Equal(*decision.Until))
}

// TestEvaluateAccountSchedulingThreshold_CNWindowResetSkipped 窗口已重置（reset<=now）
// 或用量低于阈值 → 不停调（candidateMatchesThreshold 要求 until.After(now)）。
func TestEvaluateAccountSchedulingThreshold_CNWindowResetSkipped(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	// 重置时间已过。
	expiredUsed := 99.0
	expiredReset := now.Add(-time.Hour)
	expired := cnCodingTestAccount(capability.PlatformZhipu)
	attachCNMonitorLimits(expired, now, []UpstreamUsageLimit{{Name: "5h", Used: &expiredUsed, ResetAt: &expiredReset}})
	require.False(t, EvaluateAccountSchedulingThreshold(expired, map[string]int{capability.PlatformZhipu: 80}, now).ShouldPause)

	// 用量低于阈值。
	lowUsed := 20.0
	lowReset := now.Add(3 * time.Hour)
	low := cnCodingTestAccount(capability.PlatformZhipu)
	attachCNMonitorLimits(low, now, []UpstreamUsageLimit{{Name: "5h", Used: &lowUsed, ResetAt: &lowReset}})
	require.False(t, EvaluateAccountSchedulingThreshold(low, map[string]int{capability.PlatformZhipu: 80}, now).ShouldPause)
}

// TestCNProviderQuotaSnapshotReset Coding Plan 429 冷却：取快照中最早的「仍在未来」窗口重置点。
func TestCNProviderQuotaSnapshotReset(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	future5h := now.Add(2 * time.Hour)
	futureWeekly := now.Add(3 * 24 * time.Hour)
	pastWeekly := now.Add(-24 * time.Hour)

	// 5h 在未来、weekly 已过期 → 返回 5h。
	used := 100.0
	account := cnCodingTestAccount(capability.PlatformKimi)
	attachCNMonitorLimits(account, now, []UpstreamUsageLimit{
		{Name: "5h", Used: &used, ResetAt: &future5h},
		{Name: "weekly", Used: &used, ResetAt: &pastWeekly},
	})
	got := CNProviderQuotaSnapshotReset(account, now)
	require.NotNil(t, got)
	require.True(t, future5h.Equal(*got))

	// 两窗口均在未来 → 取较早者（429 多由 5h 窗口触发，避免冷却到 weekly 重置）。
	both := cnCodingTestAccount(capability.PlatformKimi)
	attachCNMonitorLimits(both, now, []UpstreamUsageLimit{
		{Name: "5h", Used: &used, ResetAt: &future5h},
		{Name: "weekly", Used: &used, ResetAt: &futureWeekly},
	})
	gotBoth := CNProviderQuotaSnapshotReset(both, now)
	require.NotNil(t, gotBoth)
	require.True(t, future5h.Equal(*gotBoth))

	// 两窗口均过期 → nil。
	expired := cnCodingTestAccount(capability.PlatformKimi)
	attachCNMonitorLimits(expired, now, []UpstreamUsageLimit{
		{Name: "5h", Used: &used, ResetAt: &pastWeekly},
		{Name: "weekly", Used: &used, ResetAt: &pastWeekly},
	})
	require.Nil(t, CNProviderQuotaSnapshotReset(expired, now))

	// payg 账号（非 coding）→ nil（余额型走余额检测）。
	payg := cnCodingTestAccount(capability.PlatformKimi)
	payg.Credentials["account_mode"] = AccountModePayG
	require.Nil(t, CNProviderQuotaSnapshotReset(payg, now))
}
