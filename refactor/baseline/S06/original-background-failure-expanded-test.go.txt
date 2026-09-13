//go:build unit

package service

import (
	"context"
	"errors"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// 返回旧交换失败之前，模拟管理员已经持久化一份新凭据。
type s06OldFailureRefresher struct {
	row     *Account
	failure error
}

func (*s06OldFailureRefresher) CanRefresh(*Account) bool                  { return true }
func (*s06OldFailureRefresher) NeedsRefresh(*Account, time.Duration) bool { return true }
func (*s06OldFailureRefresher) CacheKey(*Account) string                  { return "fixture:old-failure" }
func (r *s06OldFailureRefresher) Refresh(context.Context, *Account) (map[string]any, error) {
	r.row.Credentials = map[string]any{"access_token": "fresh-admin-fixture", "refresh_token": "fresh-refresh-fixture"}
	return nil, r.failure
}

type s06FailureBlocker struct{ calls int }

func (b *s06FailureBlocker) BlockAccountScheduling(*Account, time.Time, string) { b.calls++ }
func (*s06FailureBlocker) ClearAccountSchedulingBlock(int64)                    {}
func TestS06BackgroundFailureCannotBlockNewCredentials(t *testing.T) {
	for _, engine := range []string{"fallback", "unified"} {
		for _, platform := range []string{PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformAntigravity, PlatformQoder} {
			for _, mode := range []string{"permanent", "retry_exhausted"} {
				t.Run(platform+"/"+mode+"/"+engine, func(t *testing.T) {
					row := &Account{ID: 1, Platform: platform, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"access_token": "old-fixture", "refresh_token": "old-refresh-fixture"}}
					if platform == PlatformQoder {
						row.Type = AccountTypeCosy
					}
					if platform == PlatformAntigravity {
						row.Extra = antigravityForceTokenRefreshExtra("fixture 401")
					}
					snapshot := *row
					snapshot.Credentials = shallowCopyMap(row.Credentials)
					repo := &tokenRefreshAccountRepo{}
					repo.accountsByID = map[int64]*Account{1: row}
					blocker := &s06FailureBlocker{}
					s := NewTokenRefreshService(repo, nil, nil, nil, nil, nil, nil, &config.Config{TokenRefresh: config.TokenRefreshConfig{MaxRetries: 1}}, nil, nil, nil)
					s.SetAccountRuntimeBlocker(blocker)
					if engine == "unified" {
						s.SetRefreshAPI(NewOAuthRefreshAPI(repo, nil))
					}
					message := "invalid_grant fixture"
					if mode == "retry_exhausted" {
						message = "temporary upstream fixture"
					}
					refresher := &s06OldFailureRefresher{row: row, failure: errors.New(message)}
					require.Error(t, s.refreshWithRetry(context.Background(), &snapshot, refresher, refresher, time.Hour))
					require.Zero(t, repo.setErrorCalls+repo.setTempUnschedCalls, "旧失败不能修改新身份的健康状态")
					require.Zero(t, blocker.calls, "旧失败不能阻断新身份的内存调度")
					require.Zero(t, repo.updateExtraCalls, "旧失败不能退休新身份的强制刷新标记")
				})
			}
		}
	}
}
