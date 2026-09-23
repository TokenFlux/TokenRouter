//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// TestCheckErrorPolicy — 6 table-driven cases for the pure logic function
// ---------------------------------------------------------------------------

// TestGatewayFailoverSideEffects_BedrockUsesMappedModel 验证 Bedrock 显式临时规则
// 使用实际上游模型，并禁止池模式同账号重试。
func TestGatewayFailoverSideEffects_BedrockUsesMappedModel(t *testing.T) {
	repo := &gatewaytestkit.ErrorPolicyStore{}
	healthObserver := newUpstreamHealthForTest(repo, &config.Config{}, nil, accountcore.HealthOptions{}, nil)

	svc := withSchedulerParametersForTest(&GatewayService{healthObserver: healthObserver})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 20503,
		Type:     capability.AccountTypeBedrock,
		Platform: capability.PlatformAnthropic,
		Credentials: map[string]any{
			"pool_mode":                  true,
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_rules": []any{
				map[string]any{
					"error_code":       float64(http.StatusServiceUnavailable),
					"keywords":         []any{"maintenance"},
					"duration_minutes": float64(30),
				},
			},
		}},
	}
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"maintenance"}}`)),
	}

	decision := svc.handleFailoverSideEffects(context.Background(), resp, account, "anthropic.claude-mapped")

	require.Equal(t, accountcore.ErrorPolicyTempUnscheduled, decision.Policy)
	require.True(t, decision.StopScheduling)
	require.False(t, decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), http.StatusServiceUnavailable))
	require.Len(t, repo.ModelRateLimitCalls, 1)
	require.Equal(t, "anthropic.claude-mapped", repo.ModelRateLimitCalls[0].Scope)
	require.Zero(t, repo.TempCalls)
}

// ---------------------------------------------------------------------------
// TestApplyErrorPolicy — 4 table-driven cases for the wrapper method
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// errorPolicyRepoStub — minimal AccountRepository stub for error policy tests
// ---------------------------------------------------------------------------

// retryExhaustedCooldownRepoStub 记录同账号重试耗尽后的本地冷却写入。
type retryExhaustedCooldownRepoStub struct {
	gatewayprovider.ExecutionAccountStore

	account   *gatewayprovider.ExecutionAccount
	tempCalls int
}

func (r *retryExhaustedCooldownRepoStub) GetByID(context.Context, int64) (*gatewayprovider.ExecutionAccount, error) {
	return r.account, nil
}

func (r *retryExhaustedCooldownRepoStub) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.tempCalls++
	return nil
}

// TestTempUnscheduleRetryableError_PoolModeSkipsLegacyCooldown 验证池模式的
// 同账号重试耗尽后只切号，不复用旧版 400/502 一分钟冷却。
func TestTempUnscheduleRetryableError_PoolModeSkipsLegacyCooldown(t *testing.T) {
	poolAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 81,
		Type:     capability.AccountTypeAPIKey,
		Platform: capability.PlatformAnthropic,
		Credentials: map[string]any{
			"pool_mode": true,
		}},
	}
	repo := &retryExhaustedCooldownRepoStub{account: poolAccount}
	svc := withSchedulerParametersForTest(&GatewayService{accountRepo: repo})

	svc.TempUnscheduleRetryableError(context.Background(), poolAccount.Record.ID, &forwardcore.UpstreamFailoverError{
		StatusCode:             http.StatusBadGateway,
		RetryableOnSameAccount: true,
	})

	require.Zero(t, repo.tempCalls)

	// 非池账号继续保留旧版特殊错误的冷却行为。
	repo.account = &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 82, Type: capability.AccountTypeOAuth, Platform: capability.PlatformAntigravity}}
	svc.TempUnscheduleRetryableError(context.Background(), repo.account.Record.ID, &forwardcore.UpstreamFailoverError{
		StatusCode:             http.StatusBadGateway,
		RetryableOnSameAccount: true,
	})
	require.Equal(t, 1, repo.tempCalls)
}
