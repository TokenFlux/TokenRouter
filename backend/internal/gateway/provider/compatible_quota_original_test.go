package provider

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestEvaluateOpenAIQuotaAutoPause_UsesGlobalDefaultAndWindowReset(t *testing.T) {
	ctx := WithQuotaAutoPauseSettings(context.Background(), accountcore.QuotaAutoPauseSettings{DefaultThreshold5h: 0.95})
	now := time.Now().UTC()
	account := &ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9001,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Extra: map[string]any{
			"codex_usage_updated_at": now.Format(time.RFC3339),
			"codex_5h_used_percent":  96.0,
			"codex_5h_reset_at":      now.Add(time.Hour).Format(time.RFC3339),
		}},
	}

	paused, _ := OpenAIQuotaPause(ctx, account)
	require.True(t, paused)

	account.Record.Extra["codex_5h_reset_at"] = now.Add(-time.Minute).Format(time.RFC3339)
	paused, _ = OpenAIQuotaPause(ctx, account)
	require.False(t, paused)
}

func TestEvaluateOpenAIQuotaAutoPause_PerAccountDisableOverridesGlobalDefault(t *testing.T) {
	ctx := WithQuotaAutoPauseSettings(context.Background(), accountcore.QuotaAutoPauseSettings{DefaultThreshold5h: 0.95})
	account := &ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9002,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Extra: map[string]any{
			"codex_5h_used_percent":  99.0,
			"auto_pause_5h_disabled": true,
		}},
	}

	paused, _ := OpenAIQuotaPause(ctx, account)
	require.False(t, paused)
}

// 请求与派生 attempt 使用独立阈值，更新选择快照不能覆盖健康观察型号或父请求。
func TestCompatibleQuotaSnapshotPreservesParentAttemptState(t *testing.T) {
	parent := requeststate.WithExecutionHints(context.Background(), requeststate.ExecutionHints{HealthModel: "observed-model"})
	parent = WithQuotaAutoPauseSettings(parent, accountcore.QuotaAutoPauseSettings{DefaultThreshold5h: 0.99})
	child := WithQuotaAutoPauseSettings(parent, accountcore.QuotaAutoPauseSettings{DefaultThreshold5h: 0.95})
	now := time.Now()
	value := &ExecutionAccount{Record: accountcore.Record{ID: 9020, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Extra: map[string]any{"codex_usage_updated_at": now.UTC().Format(time.RFC3339), "codex_5h_used_percent": 96.0, "codex_5h_reset_at": now.Add(time.Hour).UTC().Format(time.RFC3339)}}}
	paused, _ := OpenAIQuotaPause(child, value)
	require.True(t, paused)
	paused, _ = OpenAIQuotaPause(parent, value)
	require.False(t, paused)
	require.Equal(t, "observed-model", requeststate.ExecutionHintsFromContext(child).HealthModel)
}
