//go:build unit

package account_test

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// TestParentHealthyForShadow covers the pure helper function used across
// scheduler + gateway selection + WS forwarder.
func TestParentHealthyForShadow(t *testing.T) {
	pid := int64(100)

	// 所有母账号 fixture 均设 Type=oauth:account.ParentHealthyForShadow 现要求母账号仍是 OpenAI OAuth
	// (外审 D fail-closed),不设则各用例会因"非 oauth"而非被测原因失败,使断言失去意义。
	healthyParent := &account.Record{
		ID:          100,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
	}
	unhealthyParent := &account.Record{
		ID:          100,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      account.StatusError,
		Schedulable: true, // Schedulable flag is set, but Status=error → IsSchedulable()==false
	}
	shadow := &account.Record{
		ID:              200,
		ParentAccountID: &pid,
		QuotaDimension:  account.QuotaDimensionSpark,
		Platform:        capability.PlatformOpenAI,
		Status:          billing.StatusActive,
		Schedulable:     true,
	}
	normalAccount := &account.Record{
		ID:          300,
		Platform:    capability.PlatformOpenAI,
		Status:      billing.StatusActive,
		Schedulable: true,
	}

	t.Run("shadow_of_healthy_parent_is_healthy", func(t *testing.T) {
		lookup := func(id int64) *account.Record {
			if id == healthyParent.ID {
				return healthyParent
			}
			return nil
		}
		require.True(t, account.ParentHealthyForShadow(shadow, lookup))
	})

	t.Run("shadow_of_unhealthy_parent_is_not_healthy", func(t *testing.T) {
		// Parent Status=error means IsActive()==false → IsSchedulable()==false.
		require.False(t, unhealthyParent.IsSchedulable(), "precondition: unhealthy parent must not be schedulable")
		lookup := func(id int64) *account.Record {
			if id == unhealthyParent.ID {
				return unhealthyParent
			}
			return nil
		}
		require.False(t, account.ParentHealthyForShadow(shadow, lookup))
	})

	t.Run("shadow_parent_not_found_is_not_healthy", func(t *testing.T) {
		lookup := func(_ int64) *account.Record { return nil }
		require.False(t, account.ParentHealthyForShadow(shadow, lookup))
	})

	t.Run("normal_account_always_healthy", func(t *testing.T) {
		// lookup should never be called for non-shadow accounts.
		calledLookup := false
		lookup := func(_ int64) *account.Record {
			calledLookup = true
			return nil
		}
		require.True(t, account.ParentHealthyForShadow(normalAccount, lookup))
		require.False(t, calledLookup, "lookup must not be called for non-shadow accounts")
	})

	t.Run("nil_account_always_healthy", func(t *testing.T) {
		lookup := func(_ int64) *account.Record { return nil }
		require.True(t, account.ParentHealthyForShadow(nil, lookup))
	})

	t.Run("manual_schedulable_false_parent_does_not_block_shadow", func(t *testing.T) {
		// F1 决策 A:母账号被手动暂停(Schedulable=false)是「调度配置」而非「凭据不可用」,
		// 不传播到影子——影子有自己的 Schedulable 开关。凭据(active+未过期)仍可用 → 影子健康。
		manualPausedParent := &account.Record{
			ID:          100,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeOAuth,
			Status:      billing.StatusActive,
			Schedulable: false,
		}
		require.False(t, manualPausedParent.IsSchedulable(), "precondition: 母账号被手动暂停不可调度")
		lookup := func(id int64) *account.Record {
			if id == manualPausedParent.ID {
				return manualPausedParent
			}
			return nil
		}
		require.True(t, account.ParentHealthyForShadow(shadow, lookup),
			"母账号手动暂停不应连坐影子(凭据仍可用)")
	})

	t.Run("global_rate_limited_parent_does_not_block_shadow", func(t *testing.T) {
		// F1 核心修复:母账号 global 429(RateLimitResetAt 未来)是 global 维度限流,
		// spark 有独立窗口 → 不得连坐影子,否则违背「global 枯竭后 spark 仍独立」目标。
		resetAt := time.Now().Add(1 * time.Hour)
		rateLimitedParent := &account.Record{
			ID:               100,
			Platform:         capability.PlatformOpenAI,
			Type:             capability.AccountTypeOAuth,
			Status:           billing.StatusActive,
			Schedulable:      true,
			RateLimitResetAt: &resetAt,
		}
		require.False(t, rateLimitedParent.IsSchedulable(), "precondition: global 限流母账号自身不可调度")
		lookup := func(id int64) *account.Record {
			if id == rateLimitedParent.ID {
				return rateLimitedParent
			}
			return nil
		}
		require.True(t, account.ParentHealthyForShadow(shadow, lookup),
			"母账号 global 限流不应连坐 spark 影子")
	})

	t.Run("overloaded_parent_does_not_block_shadow", func(t *testing.T) {
		// 过载退避(OverloadUntil)同属 global 维度运行态,不连坐影子。
		until := time.Now().Add(30 * time.Minute)
		overloadedParent := &account.Record{
			ID:            100,
			Platform:      capability.PlatformOpenAI,
			Type:          capability.AccountTypeOAuth,
			Status:        billing.StatusActive,
			Schedulable:   true,
			OverloadUntil: &until,
		}
		require.False(t, overloadedParent.IsSchedulable(), "precondition: 过载母账号自身不可调度")
		lookup := func(id int64) *account.Record {
			if id == overloadedParent.ID {
				return overloadedParent
			}
			return nil
		}
		require.True(t, account.ParentHealthyForShadow(shadow, lookup),
			"母账号过载退避不应连坐 spark 影子")
	})

	t.Run("temp_unschedulable_parent_blocks_shadow", func(t *testing.T) {
		// G2:TempUnschedulableUntil 对 OpenAI 账号由 401/token 刷新耗尽/transport·proxy 写入,
		// 代表共享凭据/传输坏死 → 影子共享母 token+proxy,应被挡(与 global 限流 RateLimitResetAt 区分)。
		until := time.Now().Add(15 * time.Minute)
		tempUnschedParent := &account.Record{
			ID:                     100,
			Platform:               capability.PlatformOpenAI,
			Type:                   capability.AccountTypeOAuth,
			Status:                 billing.StatusActive,
			Schedulable:            true,
			TempUnschedulableUntil: &until,
		}
		lookup := func(id int64) *account.Record {
			if id == tempUnschedParent.ID {
				return tempUnschedParent
			}
			return nil
		}
		require.False(t, account.ParentHealthyForShadow(shadow, lookup),
			"母账号 TempUnschedulableUntil(凭据/传输坏死)冷却期内应挡住影子")
	})

	t.Run("expired_parent_credentials_block_shadow", func(t *testing.T) {
		// 凭据真正过期(AutoPauseOnExpired + ExpiresAt 已过)→ 透传 token 不可用 → 影子应被挡。
		expiredAt := time.Now().Add(-1 * time.Hour)
		expiredParent := &account.Record{
			ID:                 100,
			Platform:           capability.PlatformOpenAI,
			Type:               capability.AccountTypeOAuth,
			Status:             billing.StatusActive,
			Schedulable:        true,
			AutoPauseOnExpired: true,
			ExpiresAt:          &expiredAt,
		}
		lookup := func(id int64) *account.Record {
			if id == expiredParent.ID {
				return expiredParent
			}
			return nil
		}
		require.False(t, account.ParentHealthyForShadow(shadow, lookup),
			"母账号凭据过期时影子应被挡(透传 token 不可用)")
	})

	t.Run("non_oauth_parent_blocks_shadow", func(t *testing.T) {
		// 外审 D:母账号被改成非 OpenAI OAuth(如 apikey)后,透传凭据解析必失败,
		// 影子应 fail-closed 不进调度候选(即便账号 active、凭据未过期)。
		apikeyParent := &account.Record{
			ID:          100,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
		}
		lookup := func(id int64) *account.Record {
			if id == apikeyParent.ID {
				return apikeyParent
			}
			return nil
		}
		require.False(t, account.ParentHealthyForShadow(shadow, lookup),
			"母账号非 OpenAI OAuth 时影子应被挡(fail-closed)")
	})
}
