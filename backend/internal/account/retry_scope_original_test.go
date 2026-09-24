package account_test

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"context"

	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/stretchr/testify/require"
)

type capacityShedAccountRepoStub struct {
	accountcore.RetryCooldownStore
	// 嵌入接口，未实现的方法会 panic（不应被调用）

	tempUnschedCalls int
}

func (r *capacityShedAccountRepoStub) SetTempUnschedulable(_ context.Context, _ int64, _ time.Time, _ string) error {
	r.tempUnschedCalls++
	return nil
}

func (r *capacityShedAccountRepoStub) GetByID(_ context.Context, id int64) (*accountcore.Record, error) {
	return &accountcore.Record{LoadLocation: time.LoadLocation, ID: id, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}, nil
}

func TestTempUnscheduleRetryableErrorSkipsRequestScopedTransient(t *testing.T) {
	t.Run("请求级瞬时故障不写账号状态", func(t *testing.T) {
		repo := &capacityShedAccountRepoStub{}
		svc := accountcore.NewRetryCooldown(repo, accountcore.RetryCooldownOptions{})

		svc.Apply(context.Background(), accountcore.RetryCooldownInput{AccountID: 1, Status: 502, Retryable: true, RequestScopedTransient: true})

		require.Zero(t, repo.tempUnschedCalls)
	})

	// 对照组：同样的 502 在未标记请求级瞬时故障时仍按原有语义临时摘号，
	// 确认上面的断言来自新增守卫而非其他前置条件。
	t.Run("未标记时保持原有临时摘号语义", func(t *testing.T) {
		repo := &capacityShedAccountRepoStub{}
		svc := accountcore.NewRetryCooldown(repo, accountcore.RetryCooldownOptions{})

		svc.Apply(context.Background(), accountcore.RetryCooldownInput{AccountID: 1, Status: 502, Retryable: true})

		require.Equal(t, 1, repo.tempUnschedCalls)
	})
}
