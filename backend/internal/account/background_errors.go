// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	errors "errors"
	fmt "fmt"
)

// ErrRefreshSkipped 表示刷新被跳过（锁竞争或已被其他路径刷新），不计入 failed 或 refreshed
var ErrRefreshSkipped = fmt.Errorf("refresh skipped")

type ProviderConfigurationRefreshError struct {
	Cause error `json:"-"`
}

type ProviderCycleContainmentRefreshError struct {
	Cause error `json:"-"`
}

type AccountPermanentRefreshError struct {
	Cause                   error `json:"-"`
	PersistentlyBlocked     bool  `json:"-"`
	CacheInvalidationFailed bool  `json:"-"`
}

type RefreshAttemptTimeoutError struct {
	Cause error `json:"-"`
}

func IsProviderScopedTerminalRefreshError(err error) bool {
	if err == nil {
		return false
	}
	var containmentErr *ProviderCycleContainmentRefreshError
	if errors.As(err, &containmentErr) {
		return true
	}
	var configurationErr *ProviderConfigurationRefreshError
	return errors.As(err, &configurationErr)
}

func (e *RefreshAttemptTimeoutError) Error() string {
	return "OAuth refresh attempt timed out"
}

func (e *RefreshAttemptTimeoutError) Unwrap() error {
	if e == nil || e.Cause == nil {
		return context.DeadlineExceeded
	}
	return e.Cause
}

func (e *ProviderConfigurationRefreshError) Error() string {
	return "provider OAuth configuration rejected"
}

func (e *ProviderCycleContainmentRefreshError) Error() string {
	return "provider OAuth failure contained for this cycle"
}

func (e *ProviderCycleContainmentRefreshError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *AccountPermanentRefreshError) Error() string {
	return "account OAuth credentials permanently rejected"
}

func (e *AccountPermanentRefreshError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *ProviderConfigurationRefreshError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
