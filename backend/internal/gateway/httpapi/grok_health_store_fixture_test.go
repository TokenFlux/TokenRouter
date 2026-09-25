//go:build unit

package httpapi

import (
	"context"
	"errors"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

type grokQuotaAccountRepo struct {
	*grokFixtureAccounts
	updates               map[int64]map[string]any
	updateCalls           int
	rateLimitedCalls      int
	lastRateLimitedID     int64
	lastRateLimitResetAt  time.Time
	tempUnschedCalls      int
	lastTempUnschedID     int64
	lastTempUnschedUntil  time.Time
	lastTempUnschedReason string
	recoveryClearCalls    int
	recoveryObservedAt    time.Time
	recoveryObservedReset time.Time
	recoveryClearResult   bool
}

func (r *grokQuotaAccountRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.updateCalls++
	if r.updates == nil {
		r.updates = make(map[int64]map[string]any)
	}
	r.updates[id] = updates
	if r.grokFixtureAccounts != nil {
		value := r.accountsByID[id]
		if value != nil {
			if value.Record.Extra == nil {
				value.Record.Extra = make(map[string]any)
			}
			for key, v := range updates {
				value.Record.Extra[key] = v
			}
		}
	}

	return nil
}

func (r *grokQuotaAccountRepo) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitedCalls++
	r.lastRateLimitedID = id
	r.lastRateLimitResetAt = resetAt
	return nil
}

func (r *grokQuotaAccountRepo) SetRateLimitedIfLater(ctx context.Context, id int64, resetAt time.Time) error {
	return r.SetRateLimited(ctx, id, resetAt)
}

func (r *grokQuotaAccountRepo) ClearRateLimitIfObserved(_ context.Context, _ int64, observedLimitedAt, observedResetAt time.Time) (bool, error) {
	r.recoveryClearCalls++
	r.recoveryObservedAt = observedLimitedAt
	r.recoveryObservedReset = observedResetAt
	return r.recoveryClearResult, nil
}

func (r *grokQuotaAccountRepo) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.tempUnschedCalls++
	r.lastTempUnschedID = id
	r.lastTempUnschedUntil = until
	r.lastTempUnschedReason = reason
	return nil
}

// grokFixtureAccounts 保留原按ID回读的指针和计数，其他写入复用测试底座。
type grokFixtureAccounts struct {
	gatewaytestkit.HealthStoreBase
	accountsByID map[int64]*gatewayprovider.ExecutionAccount
	getByIDCalls int
}

func (r *grokFixtureAccounts) GetByID(_ context.Context, id int64) (*gatewayprovider.ExecutionAccount, error) {
	r.getByIDCalls++
	if value, ok := r.accountsByID[id]; ok {
		return value, nil
	}
	return nil, errors.New("account not found")
}
func newHTTPGrokTokenFixture(store gatewayprovider.ExecutionAccountStore, cache accountcore.AccessTokenCache) *accountcore.GrokTokenSource {
	return &accountcore.GrokTokenSource{Repository: gatewaytestkit.TokenRepository(store), Cache: cache, Policy: accountcore.GrokProviderRefreshPolicy()}
}
