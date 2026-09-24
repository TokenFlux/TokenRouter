//go:build unit

package account_test

import (
	"context"

	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// TestCheckErrorPolicy — 6 table-driven cases for the pure logic function
// ---------------------------------------------------------------------------

// TestGatewayFailoverSideEffects_BedrockUsesMappedModel 验证 Bedrock 显式临时规则
// 使用实际上游模型，并禁止池模式同账号重试。

// ---------------------------------------------------------------------------
// TestApplyErrorPolicy — 4 table-driven cases for the wrapper method
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// errorPolicyRepoStub — minimal AccountRepository stub for error policy tests
// ---------------------------------------------------------------------------

// retryExhaustedCooldownRepoStub 记录同账号重试耗尽后的本地冷却写入。
type retryExhaustedCooldownRepoStub struct {
	accountcore.RetryCooldownStore

	account   *accountcore.Record
	tempCalls int
}

func (r *retryExhaustedCooldownRepoStub) GetByID(context.Context, int64) (*accountcore.Record, error) {
	return r.account, nil
}

func (r *retryExhaustedCooldownRepoStub) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.tempCalls++
	return nil
}

// TestTempUnscheduleRetryableError_PoolModeSkipsLegacyCooldown 验证池模式的
// 同账号重试耗尽后只切号，不复用旧版 400/502 一分钟冷却。
func TestTempUnscheduleRetryableError_PoolModeSkipsLegacyCooldown(t *testing.T) {
	poolAccount := &accountcore.Record{LoadLocation: time.LoadLocation, ID: 81,
		Type:     capability.AccountTypeAPIKey,
		Platform: capability.PlatformAnthropic,
		Credentials: map[string]any{
			"pool_mode": true,
		},
	}
	repo := &retryExhaustedCooldownRepoStub{account: poolAccount}
	svc := accountcore.NewRetryCooldown(repo, accountcore.RetryCooldownOptions{})

	svc.Apply(context.Background(), accountcore.RetryCooldownInput{AccountID: poolAccount.ID, Status: 502, Retryable: true})

	require.Zero(t, repo.tempCalls)

	// 非池账号继续保留旧版特殊错误的冷却行为。
	repo.account = &accountcore.Record{LoadLocation: time.LoadLocation, ID: 82, Type: capability.AccountTypeOAuth, Platform: capability.PlatformAntigravity}
	svc.Apply(context.Background(), accountcore.RetryCooldownInput{AccountID: repo.account.ID, Status: 502, Retryable: true})
	require.Equal(t, 1, repo.tempCalls)
}
