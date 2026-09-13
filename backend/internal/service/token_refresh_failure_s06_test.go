package service

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"reflect"
)

// 竞争替身显式比较与生产 writer 相同的身份字段；nil 凭据沿用旧快照的空对象语义。
func refreshFailureMatchesFixture(value *Account, version account.RefreshFailureVersion) bool {
	if value == nil {
		return false
	}
	credentials := value.Credentials
	if credentials == nil {
		credentials = map[string]any{}
	}
	return value.ID == version.ID && value.Platform == version.Platform && value.Type == version.Type && value.Status == version.Status && value.Schedulable == version.Schedulable && reflect.DeepEqual(credentials, version.Credentials) && reflect.DeepEqual(value.ProxyID, version.ProxyID)
}
func (r *tokenRefreshCandidateRepo) ApplyOAuthRefreshFailure(ctx context.Context, version account.RefreshFailureVersion, failure account.RefreshFailure) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	matched := r.accounts == nil
	for i := range r.accounts {
		if r.accounts[i].ID == version.ID {
			matched = refreshFailureMatchesFixture(&r.accounts[i], version)
			break
		}
	}
	if !matched {
		return false, nil
	}
	if failure.Kind == account.RefreshFailurePermanent {
		r.setErrorCalls++
	} else {
		r.setTempUnschedCalls++
		r.lastTempUnschedReason = failure.Message
	}
	return true, nil
}

func (b *reconcileRuntimeBlocker) PrepareRefreshFailure(int64) func(account.RefreshFailureNotice) {
	return func(n account.RefreshFailureNotice) {
		b.BlockAccountScheduling(&Account{ID: n.AccountID}, n.Until, n.Reason)
	}
}
