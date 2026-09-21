//go:build unit

// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	testing "testing"
	time "time"

	require "github.com/stretchr/testify/require"
)

func TestRefreshIfNeeded_LocalLockWaitHonorsContext(t *testing.T) {
	account := &Record{ID: 80, Platform: PlatformGrok, Type: AccountTypeOAuth, Status: StatusActive}
	repo := &refreshCoreRepo{}
	executor := &refreshCoreExecutor{}
	api := NewOAuthRefreshAPI(repo, nil, RefreshOptions{})
	lock := api.getLocalLock(executor.CacheKey(account))
	require.NoError(t, lock.Lock(context.Background()))
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	result, err := api.RefreshIfNeeded(ctx, account, executor, time.Hour)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, result)
	require.Zero(t, executor.refreshCalls)
}

func TestNewOAuthRefreshAPI_DefaultTTL(t *testing.T) {
	api := NewOAuthRefreshAPI(nil, nil, RefreshOptions{})
	require.Equal(t, defaultRefreshLockTTL, api.lockTTL)
}

func TestNewOAuthRefreshAPI_CustomTTL(t *testing.T) {
	api := NewOAuthRefreshAPI(nil, nil, RefreshOptions{LockTTL: 90 * time.Second})
	require.Equal(t, 90*time.Second, api.lockTTL)
}

func TestNewOAuthRefreshAPI_ZeroTTLUsesDefault(t *testing.T) {
	api := NewOAuthRefreshAPI(nil, nil, RefreshOptions{})
	require.Equal(t, defaultRefreshLockTTL, api.lockTTL)
}

// 此夹具只覆盖取锁前的取消，数据库和交换器都不应被访问。
type refreshCoreRepo struct{}

func (*refreshCoreRepo) GetByID(context.Context, int64) (*Record, error) {
	panic("等待锁时不应访问仓储")
}

type refreshCoreExecutor struct{ refreshCalls int }

func (*refreshCoreExecutor) CanRefresh(*Record) bool                  { return true }
func (*refreshCoreExecutor) NeedsRefresh(*Record, time.Duration) bool { return true }
func (*refreshCoreExecutor) CacheKey(*Record) string                  { return "test:refresh" }
func (e *refreshCoreExecutor) Refresh(context.Context, *Record) (map[string]any, error) {
	e.refreshCalls++
	return nil, nil
}
