//go:build unit

package provider_test

import (
	"context"
	"reflect"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

type tokenRefreshAccountRepo struct {
	credentialReadStore
	updateCalls                  int
	fullUpdateCalls              int
	updateCredentialsCalls       int
	setErrorCalls                int
	clearTempCalls               int
	setTempUnschedCalls          int
	updateExtraCalls             int
	lastErrorMessage             string
	lastTempUnschedReason        string
	lastExtraUpdates             map[string]any
	lastAccount                  *gatewayprovider.ExecutionAccount
	updateErr                    error
	cancelOnUpdate               context.CancelFunc
	conditionalErrorCalls        int
	conditionalTempCalls         int
	conditionalSuccessCalls      int
	conditionalErrorErr          error
	conditionalTempErr           error
	conditionalSuccessErr        error
	snapshotReads                bool
	respectReadContext           bool
	getByIDCalls                 int
	durableReadDelay             time.Duration
	mutateSchedulingOnSuccessCAS bool
	reauthorizeOnErrorCAS        bool
	reauthorizeOnTempCAS         bool
	repairProxyOnErrorCAS        bool
	repairProxyOnTempCAS         bool
	setErrorErr                  error
	setTempUnschedErr            error
	beforeConditionalState       func()
}

func (r *tokenRefreshAccountRepo) Update(ctx context.Context, account *gatewayprovider.ExecutionAccount) error {
	r.updateCalls++
	r.fullUpdateCalls++
	r.lastAccount = account
	return r.updateErr
}

func (r *tokenRefreshAccountRepo) UpdateCredentials(ctx context.Context, id int64, credentials map[string]any) error {
	r.updateCalls++
	r.updateCredentialsCalls++
	if r.updateErr != nil {
		return r.updateErr
	}
	cloned := querycache.ShallowMap(credentials)
	if r.accountsByID != nil {
		if acc, ok := r.accountsByID[id]; ok && acc != nil {
			acc.Record.Credentials = cloned
			r.lastAccount = acc
			if r.cancelOnUpdate != nil {
				r.cancelOnUpdate()
			}
			return nil
		}
	}
	r.lastAccount = &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: id, Credentials: cloned}}
	if r.cancelOnUpdate != nil {
		r.cancelOnUpdate()
	}
	return nil
}

func (r *tokenRefreshAccountRepo) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionAccount, error) {
	if r.respectReadContext && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	r.getByIDCalls++
	if r.getByIDCalls > 1 && r.durableReadDelay > 0 {
		timer := time.NewTimer(r.durableReadDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	account, err := r.credentialReadStore.GetByID(ctx, id)
	if err != nil || !r.snapshotReads {
		return account, err
	}
	return grokCredentialStoredSnapshot(account), nil
}

func (r *tokenRefreshAccountRepo) SetError(ctx context.Context, id int64, errorMsg string) error {
	r.setErrorCalls++
	r.lastErrorMessage = errorMsg
	return r.setErrorErr
}

func (r *tokenRefreshAccountRepo) ClearTempUnschedulable(ctx context.Context, id int64) error {
	r.clearTempCalls++
	return nil
}

func (r *tokenRefreshAccountRepo) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	r.setTempUnschedCalls++
	r.lastTempUnschedReason = reason
	return r.setTempUnschedErr
}

func (r *tokenRefreshAccountRepo) SetGrokCredentialErrorIfMatch(
	_ context.Context,
	id int64,
	snapshot accountcore.CredentialMutationSnapshot,
	errorMsg string,
) (bool, error) {
	if r.beforeConditionalState != nil {
		hook := r.beforeConditionalState
		r.beforeConditionalState = nil
		hook()
	}
	account := r.accountsByID[id]
	if !grokCredentialSnapshotMatchesAccount(account, snapshot) ||
		(errorMsg == string(forwardcore.GrokCredentialReasonProxyInvalid) && account.Record.Proxy != nil) {
		return false, nil
	}
	r.setErrorCalls++
	r.lastErrorMessage = errorMsg
	if r.setErrorErr != nil {
		return false, r.setErrorErr
	}
	account.Record.Status = accountcore.StatusError
	account.Record.Schedulable = false
	account.Record.ErrorMessage = errorMsg
	return true, nil
}

func (r *tokenRefreshAccountRepo) SetGrokCredentialTempUnschedulableIfMatch(
	_ context.Context,
	id int64,
	snapshot accountcore.CredentialMutationSnapshot,
	until time.Time,
	reason string,
) (bool, error) {
	if r.beforeConditionalState != nil {
		hook := r.beforeConditionalState
		r.beforeConditionalState = nil
		hook()
	}
	account := r.accountsByID[id]
	if !grokCredentialSnapshotMatchesAccount(account, snapshot) {
		return false, nil
	}
	r.setTempUnschedCalls++
	r.lastTempUnschedReason = reason
	if r.setTempUnschedErr != nil {
		return false, r.setTempUnschedErr
	}
	value := until
	account.Record.TempUnschedulableUntil = &value
	return true, nil
}

func grokCredentialSnapshotMatchesAccount(account *gatewayprovider.ExecutionAccount, snapshot accountcore.CredentialMutationSnapshot) bool {
	return account != nil && account.View().IsGrokOAuth() && account.View().IsSchedulable() &&
		grokCredentialMutationSnapshot(account).CredentialsJSON == snapshot.CredentialsJSON &&
		accountcore.GrokCredentialProxyIDsEqual(account.Record.ProxyID, snapshot.ProxyID)
}

func (r *tokenRefreshAccountRepo) SetGrokOAuthRefreshErrorIfCredentialsUnchanged(
	_ context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	errorMsg string,
) (bool, error) {
	r.conditionalErrorCalls++
	if r.conditionalErrorErr != nil {
		return false, r.conditionalErrorErr
	}
	account := r.accountsByID[id]
	if account == nil {
		return false, nil
	}
	if r.reauthorizeOnErrorCAS {
		r.reauthorizeOnErrorCAS = false
		account.Record.Credentials = map[string]any{
			"access_token":   "fresh-access",
			"refresh_token":  "fresh-refresh",
			"_token_version": int64(2),
		}
		account.Record.Status = billing.StatusActive
		account.Record.Schedulable = true
	}
	if r.repairProxyOnErrorCAS {
		r.repairProxyOnErrorCAS = false
		proxyID := int64(902)
		account.Record.ProxyID = &proxyID
	}
	if account.Record.Status != billing.StatusActive || account.Record.Platform != capability.PlatformGrok || account.Record.Type != capability.AccountTypeOAuth ||
		!reflect.DeepEqual(account.Record.Credentials, expectedCredentials) || !reflect.DeepEqual(account.Record.ProxyID, expectedProxyID) {
		return false, nil
	}
	r.setErrorCalls++
	r.lastErrorMessage = errorMsg
	account.Record.Status = accountcore.StatusError
	account.Record.Schedulable = false
	account.Record.ErrorMessage = errorMsg
	return true, nil
}

func (r *tokenRefreshAccountRepo) UpdateGrokOAuthCredentialsIfUnchanged(
	_ context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	credentials map[string]any,
) (bool, error) {
	r.conditionalSuccessCalls++
	if r.conditionalSuccessErr != nil {
		return false, r.conditionalSuccessErr
	}
	account := r.accountsByID[id]
	if account != nil && r.mutateSchedulingOnSuccessCAS {
		r.mutateSchedulingOnSuccessCAS = false
		account.Record.Status = billing.StatusDisabled
		account.Record.Schedulable = false
		resetAt := time.Now().Add(30 * time.Minute)
		account.Record.RateLimitResetAt = &resetAt
	}
	if account == nil || account.Record.Platform != capability.PlatformGrok ||
		account.Record.Type != capability.AccountTypeOAuth || !reflect.DeepEqual(account.Record.Credentials, expectedCredentials) ||
		!reflect.DeepEqual(account.Record.ProxyID, expectedProxyID) {
		return false, nil
	}
	r.updateCalls++
	r.updateCredentialsCalls++
	account.Record.Credentials = querycache.ShallowMap(credentials)
	r.lastAccount = account
	if r.cancelOnUpdate != nil {
		r.cancelOnUpdate()
	}
	return true, nil
}

func (r *tokenRefreshAccountRepo) SetGrokOAuthRefreshTempUnschedulableIfCredentialsUnchanged(
	_ context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	until time.Time,
	reason string,
) (bool, error) {
	r.conditionalTempCalls++
	if r.conditionalTempErr != nil {
		return false, r.conditionalTempErr
	}
	account := r.accountsByID[id]
	if account == nil {
		return false, nil
	}
	if r.reauthorizeOnTempCAS {
		r.reauthorizeOnTempCAS = false
		account.Record.Credentials = map[string]any{
			"access_token":   "fresh-access",
			"refresh_token":  "fresh-refresh",
			"_token_version": int64(2),
		}
		account.Record.Status = billing.StatusActive
		account.Record.Schedulable = true
	}
	if r.repairProxyOnTempCAS {
		r.repairProxyOnTempCAS = false
		proxyID := int64(902)
		account.Record.ProxyID = &proxyID
	}
	if account.Record.Status != billing.StatusActive || account.Record.Platform != capability.PlatformGrok || account.Record.Type != capability.AccountTypeOAuth ||
		!reflect.DeepEqual(account.Record.Credentials, expectedCredentials) || !reflect.DeepEqual(account.Record.ProxyID, expectedProxyID) {
		return false, nil
	}
	r.setTempUnschedCalls++
	r.lastTempUnschedReason = reason
	account.Record.TempUnschedulableUntil = &until
	account.Record.TempUnschedulableReason = reason
	return true, nil
}

func (r *tokenRefreshAccountRepo) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	r.updateExtraCalls++
	r.lastExtraUpdates = querycache.ShallowMap(updates)
	if r.accountsByID != nil {
		if acc, ok := r.accountsByID[id]; ok && acc != nil {
			if acc.Record.Extra == nil {
				acc.Record.Extra = make(map[string]any, len(updates))
			}
			for k, v := range updates {
				acc.Record.Extra[k] = v
			}
		}
	}
	return nil
}

type tokenRefresherStub struct {
	credentials map[string]any
	err         error
	calls       int
}

func (r *tokenRefresherStub) CanRefresh(account *accountcore.Record) bool {
	return true
}

func (r *tokenRefresherStub) NeedsRefresh(account *accountcore.Record, refreshWindowDuration time.Duration) bool {
	return true
}

func (r *tokenRefresherStub) Refresh(ctx context.Context, account *accountcore.Record) (map[string]any, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	return r.credentials, nil
}

func (r *tokenRefresherStub) CacheKey(account *accountcore.Record) string {
	return "test:stub:" + account.Platform
}

// ========== Path A (refreshAPI) 测试用例 ==========

// grokCredentialStoredSnapshot 只为旧消费者测试生成独立夹具，运行实现归 account。
func grokCredentialStoredSnapshot(value *gatewayprovider.ExecutionAccount) *gatewayprovider.ExecutionAccount {
	copy := gatewayprovider.NewExecutionAccount(gatewayprovider.ExecutionRecord(value))
	if copy != nil && copy.Record.Credentials == nil {
		copy.Record.Credentials = map[string]any{}
	}
	return copy
}
