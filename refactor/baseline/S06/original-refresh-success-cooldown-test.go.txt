package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// 迟到的成功后置动作不能清除管理员已经更新的凭据与临时停调。
type refreshSuccessCooldownRepo struct {
	AccountRepository
	current *Account
	clears  int
}

func (r *refreshSuccessCooldownRepo) ClearTempUnschedulable(context.Context, int64) error {
	r.clears++
	r.current.TempUnschedulableUntil = nil
	r.current.TempUnschedulableReason = ""
	return nil
}
func TestRefreshSuccessCannotClearNewAdministratorCooldown(t *testing.T) {
	until := time.Now().Add(time.Hour)
	observed := &Account{ID: 941, Platform: PlatformGemini, Type: AccountTypeOAuth, Status: StatusActive, Credentials: map[string]any{"refresh_token": "observed"}, TempUnschedulableUntil: &until, TempUnschedulableReason: "old"}
	current := *observed
	current.Credentials = map[string]any{"refresh_token": "administrator"}
	current.TempUnschedulableReason = "new cooldown"
	repo := &refreshSuccessCooldownRepo{current: &current}
	svc := &TokenRefreshService{accountRepo: repo}
	svc.postRefreshActions(context.Background(), observed)
	require.Zero(t, repo.clears)
	require.NotNil(t, current.TempUnschedulableUntil)
	require.Equal(t, "new cooldown", current.TempUnschedulableReason)
}

// 完成清理后发布的缓存投影必须反映已经提交的健康状态。
type refreshSuccessScheduler struct {
	SchedulerCache
	last *Account
}

func (s *refreshSuccessScheduler) SetAccount(_ context.Context, v *Account) error {
	copy := *v
	s.last = &copy
	return nil
}
func TestRefreshSuccessPublishesClearedCooldown(t *testing.T) {
	until := time.Now().Add(time.Hour)
	observed := &Account{ID: 942, Platform: PlatformGemini, Type: AccountTypeOAuth, Status: StatusActive, TempUnschedulableUntil: &until, TempUnschedulableReason: "old"}
	current := *observed
	repo := &refreshSuccessCooldownRepo{current: &current}
	cache := &refreshSuccessScheduler{}
	svc := &TokenRefreshService{accountRepo: repo, schedulerCache: cache}
	svc.postRefreshActions(context.Background(), observed)
	require.Equal(t, 1, repo.clears)
	require.NotNil(t, cache.last)
	require.Nil(t, cache.last.TempUnschedulableUntil)
	require.Empty(t, cache.last.TempUnschedulableReason)
}
