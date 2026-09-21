//go:build unit

package account_test

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/stretchr/testify/require"
)

type resetAccountQuotaRepoStub struct {
	account.AdminStore
	account             *account.Record
	getByIDErr          error
	resetErr            error
	resetCalls          int
	clearRateLimitCalls int
	callOrder           []string
	overloaded          bool
}

func (r *resetAccountQuotaRepoStub) GetByID(context.Context, int64) (*account.Record, error) {
	return account.CloneRecord(r.account), r.getByIDErr
}

func (r *resetAccountQuotaRepoStub) ResetQuotaUsedAndClearRateLimitCooldown(context.Context, int64) error {
	r.resetCalls++
	r.callOrder = append(r.callOrder, "reset_quota_and_clear_rate_limit_cooldown")
	return r.resetErr
}

func (r *resetAccountQuotaRepoStub) ClearRateLimit(context.Context, int64) error {
	r.clearRateLimitCalls++
	r.callOrder = append(r.callOrder, "clear_rate_limit")
	r.overloaded = false
	return nil
}

func TestResetAccountQuota_ClearsSchedulerRateLimitWithoutClearingOverload(t *testing.T) {
	repo := &resetAccountQuotaRepoStub{account: &account.Record{ID: 42}, overloaded: true}
	svc := account.NewAdmin(repo, account.AdminOptions{Quotas: repo})

	err := svc.ResetAccountQuota(context.Background(), 42)

	require.NoError(t, err)
	require.Equal(t, 1, repo.resetCalls)
	require.Zero(t, repo.clearRateLimitCalls)
	require.Equal(t, []string{"reset_quota_and_clear_rate_limit_cooldown"}, repo.callOrder)
	require.True(t, repo.overloaded, "quota reset must preserve an unrelated overload block")
}

func TestResetAccountQuota_PreservesLookupAndSparkShadowShortCircuits(t *testing.T) {
	t.Run("lookup failure", func(t *testing.T) {
		getErr := errors.New("get account failed")
		repo := &resetAccountQuotaRepoStub{getByIDErr: getErr}
		svc := account.NewAdmin(repo, account.AdminOptions{Quotas: repo})

		err := svc.ResetAccountQuota(context.Background(), 42)

		require.ErrorIs(t, err, getErr)
		require.Zero(t, repo.resetCalls)
		require.Zero(t, repo.clearRateLimitCalls)
	})

	t.Run("spark shadow", func(t *testing.T) {
		parentID := int64(7)
		repo := &resetAccountQuotaRepoStub{
			account: &account.Record{ID: 42, ParentAccountID: &parentID},
		}
		svc := account.NewAdmin(repo, account.AdminOptions{Quotas: repo})

		err := svc.ResetAccountQuota(context.Background(), 42)

		require.Error(t, err)
		require.Zero(t, repo.resetCalls)
		require.Zero(t, repo.clearRateLimitCalls)
	})
}

func TestResetAccountQuota_PropagatesAtomicRepositoryFailure(t *testing.T) {
	resetErr := errors.New("atomic reset failed")
	repo := &resetAccountQuotaRepoStub{
		account:  &account.Record{ID: 42},
		resetErr: resetErr,
	}
	svc := account.NewAdmin(repo, account.AdminOptions{Quotas: repo})

	err := svc.ResetAccountQuota(context.Background(), 42)

	require.ErrorIs(t, err, resetErr)
	require.Equal(t, 1, repo.resetCalls)
	require.Zero(t, repo.clearRateLimitCalls)
}
