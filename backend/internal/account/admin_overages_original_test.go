//go:build unit

package account_test

import (
	"context"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

type updateAccountOveragesRepoStub struct {
	accountcore.AdminStore
	account     *accountcore.Record
	updateCalls int
}

func (r *updateAccountOveragesRepoStub) GetByID(ctx context.Context, id int64) (*accountcore.Record, error) {
	value := accountcore.CloneRecord(r.account)
	if value != nil {
		value.LoadLocation = time.LoadLocation
	}
	return value, nil
}

func (r *updateAccountOveragesRepoStub) Update(ctx context.Context, account *accountcore.Record) error {
	r.updateCalls++
	r.account = accountcore.CloneRecord(account)
	return nil
}

func TestUpdateAccount_DisableOveragesClearsAICreditsKey(t *testing.T) {
	accountID := int64(101)
	repo := &updateAccountOveragesRepoStub{
		account: &accountcore.Record{
			ID:       accountID,
			Platform: capability.PlatformAntigravity,
			Type:     capability.AccountTypeOAuth,
			Status:   billing.StatusActive,
			Extra: map[string]any{
				"allow_overages":   true,
				"mixed_scheduling": true,
				"model_rate_limits": map[string]any{
					"claude-sonnet-4-5": map[string]any{
						"rate_limited_at":     "2026-03-15T00:00:00Z",
						"rate_limit_reset_at": "2099-03-15T00:00:00Z",
					},
					accountcore.CreditsExhaustedKey: map[string]any{
						"rate_limited_at":     "2026-03-15T00:00:00Z",
						"rate_limit_reset_at": time.Now().Add(5 * time.Hour).UTC().Format(time.RFC3339),
					},
				},
			},
		},
	}

	svc := newOriginalAccountEditor(repo)
	updated, err := svc.UpdateAccount(context.Background(), accountID, &accountcore.UpdateAccountInput{
		Extra: map[string]any{
			"mixed_scheduling": true,
			"model_rate_limits": map[string]any{
				"claude-sonnet-4-5": map[string]any{
					"rate_limited_at":     "2026-03-15T00:00:00Z",
					"rate_limit_reset_at": "2099-03-15T00:00:00Z",
				},
				accountcore.CreditsExhaustedKey: map[string]any{
					"rate_limited_at":     "2026-03-15T00:00:00Z",
					"rate_limit_reset_at": time.Now().Add(5 * time.Hour).UTC().Format(time.RFC3339),
				},
			},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Equal(t, 1, repo.updateCalls)
	require.False(t, updated.IsOveragesEnabled())

	// 关闭 overages 后，AICredits key 应被清除
	rawLimits, ok := repo.account.Extra["model_rate_limits"].(map[string]any)
	if ok {
		_, exists := rawLimits[accountcore.CreditsExhaustedKey]
		require.False(t, exists, "关闭 overages 时应清除 AICredits 限流 key")
	}
	// 普通模型限流应保留
	require.True(t, ok)
	_, exists := rawLimits["claude-sonnet-4-5"]
	require.True(t, exists, "普通模型限流应保留")
}

func TestUpdateAccount_EnableOveragesClearsModelRateLimitsBeforePersist(t *testing.T) {
	accountID := int64(102)
	repo := &updateAccountOveragesRepoStub{
		account: &accountcore.Record{
			ID:       accountID,
			Platform: capability.PlatformAntigravity,
			Type:     capability.AccountTypeOAuth,
			Status:   billing.StatusActive,
			Extra: map[string]any{
				"mixed_scheduling": true,
				"model_rate_limits": map[string]any{
					"claude-sonnet-4-5": map[string]any{
						"rate_limited_at":     "2026-03-15T00:00:00Z",
						"rate_limit_reset_at": "2099-03-15T00:00:00Z",
					},
				},
			},
		},
	}

	svc := newOriginalAccountEditor(repo)
	updated, err := svc.UpdateAccount(context.Background(), accountID, &accountcore.UpdateAccountInput{
		Extra: map[string]any{
			"mixed_scheduling": true,
			"allow_overages":   true,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Equal(t, 1, repo.updateCalls)
	require.True(t, updated.IsOveragesEnabled())

	_, exists := repo.account.Extra["model_rate_limits"]
	require.False(t, exists, "开启 overages 时应在持久化前清掉旧模型限流")
}

func TestUpdateAccount_EmptyExtraPayloadCanClearQuotaLimits(t *testing.T) {
	accountID := int64(103)
	repo := &updateAccountOveragesRepoStub{
		account: &accountcore.Record{
			ID:       accountID,
			Platform: capability.PlatformAnthropic,
			Type:     capability.AccountTypeAPIKey,
			Status:   billing.StatusActive,
			Extra: map[string]any{
				"quota_limit":        100.0,
				"quota_daily_limit":  10.0,
				"quota_weekly_limit": 40.0,
			},
		},
	}

	svc := newOriginalAccountEditor(repo)
	updated, err := svc.UpdateAccount(context.Background(), accountID, &accountcore.UpdateAccountInput{
		// 显式空对象：语义是“清空 extra 中的可配置键”（例如关闭配额限制）
		Extra: map[string]any{},
	})

	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Equal(t, 1, repo.updateCalls)
	require.NotNil(t, repo.account.Extra)
	require.NotContains(t, repo.account.Extra, "quota_limit")
	require.NotContains(t, repo.account.Extra, "quota_daily_limit")
	require.NotContains(t, repo.account.Extra, "quota_weekly_limit")
	require.Len(t, repo.account.Extra, 0)
}

func TestUpdateAccount_FixedWeeklyResetClearsLegacyRollingUsage(t *testing.T) {
	now := time.Now().UTC()
	daysSinceMonday := (int(now.Weekday()) + 6) % 7
	currentWeekStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -daysSinceMonday)
	legacyRollingStart := currentWeekStart.Add(-24 * time.Hour)
	accountID := int64(104)
	repo := &updateAccountOveragesRepoStub{
		account: &accountcore.Record{
			ID:       accountID,
			Platform: capability.PlatformAnthropic,
			Type:     capability.AccountTypeAPIKey,
			Status:   billing.StatusActive,
			Extra: map[string]any{
				"quota_weekly_limit": 40.0,
				"quota_weekly_used":  12.5,
				"quota_weekly_start": legacyRollingStart.Format(time.RFC3339),
			},
		},
	}

	svc := newOriginalAccountEditor(repo)
	updated, err := svc.UpdateAccount(context.Background(), accountID, &accountcore.UpdateAccountInput{
		Extra: map[string]any{
			"quota_weekly_limit":      40.0,
			"quota_weekly_reset_mode": "fixed",
			"quota_weekly_reset_day":  float64(1),
			"quota_weekly_reset_hour": float64(0),
			"quota_reset_timezone":    "UTC",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Equal(t, 1, repo.updateCalls)
	require.InDelta(t, 0.0, updated.GetQuotaWeeklyUsed(), 1e-9)
	require.Equal(t, currentWeekStart.Format(time.RFC3339), updated.Extra["quota_weekly_start"])
	require.False(t, updated.IsWeeklyQuotaPeriodExpired())
}
