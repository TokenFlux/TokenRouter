package service

import (
	"context"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestEvaluateOpenAIQuotaAutoPause_UsesGlobalDefaultAndWindowReset(t *testing.T) {
	ctx := WithOpenAIQuotaAutoPauseSettings(context.Background(), ops.OpsOpenAIAccountQuotaAutoPauseSettings{DefaultThreshold5h: 0.95})
	now := time.Now().UTC()
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9001,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Extra: map[string]any{
			"codex_usage_updated_at": now.Format(time.RFC3339),
			"codex_5h_used_percent":  96.0,
			"codex_5h_reset_at":      now.Add(time.Hour).Format(time.RFC3339),
		}},
	}

	require.True(t, EvaluateOpenAIQuotaAutoPause(ctx, account))

	account.Record.Extra["codex_5h_reset_at"] = now.Add(-time.Minute).Format(time.RFC3339)
	require.False(t, EvaluateOpenAIQuotaAutoPause(ctx, account))
}

func TestEvaluateOpenAIQuotaAutoPause_PerAccountDisableOverridesGlobalDefault(t *testing.T) {
	ctx := WithOpenAIQuotaAutoPauseSettings(context.Background(), ops.OpsOpenAIAccountQuotaAutoPauseSettings{DefaultThreshold5h: 0.95})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9002,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Extra: map[string]any{
			"codex_5h_used_percent":  99.0,
			"auto_pause_5h_disabled": true,
		}},
	}

	require.False(t, EvaluateOpenAIQuotaAutoPause(ctx, account))
}
