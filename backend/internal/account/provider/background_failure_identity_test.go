//go:build unit

package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"maps"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 返回旧交换失败之前，模拟管理员已经持久化一份新凭据。
type s06OldFailureRefresher struct {
	row     *accountcore.Record
	failure error
}

func (*s06OldFailureRefresher) CanRefresh(*accountcore.Record) bool                  { return true }
func (*s06OldFailureRefresher) NeedsRefresh(*accountcore.Record, time.Duration) bool { return true }
func (*s06OldFailureRefresher) CacheKey(*accountcore.Record) string                  { return "fixture:old-failure" }
func (r *s06OldFailureRefresher) Refresh(context.Context, *accountcore.Record) (map[string]any, error) {
	r.row.Credentials = map[string]any{"access_token": "fresh-admin-fixture", "refresh_token": "fresh-refresh-fixture"}
	return nil, r.failure
}

type s06FailureBlocker struct{ calls int }

func (b *s06FailureBlocker) PrepareRefreshFailure(int64) func(accountcore.RefreshFailureNotice) {
	return func(accountcore.RefreshFailureNotice) { b.calls++ }
}
func TestS06BackgroundFailureCannotBlockNewCredentials(t *testing.T) {
	for _, engine := range []string{"fallback", "unified"} {
		for _, platform := range []string{capability.PlatformAnthropic, capability.PlatformOpenAI, capability.PlatformGemini, capability.PlatformAntigravity, capability.PlatformQoder} {
			for _, mode := range []string{"permanent", "retry_exhausted"} {
				t.Run(platform+"/"+mode+"/"+engine, func(t *testing.T) {
					row := &accountcore.Record{ID: 1, Platform: platform, Type: capability.AccountTypeOAuth, Status: accountcore.StatusActive, Schedulable: true, Credentials: map[string]any{"access_token": "old-fixture", "refresh_token": "old-refresh-fixture"}}
					if platform == capability.PlatformQoder {
						row.Type = capability.AccountTypeCosy
					}
					if platform == capability.PlatformAntigravity {
						row.Extra = accountcore.AntigravityForceTokenRefreshExtra("fixture 401")
					}
					snapshot := *row
					snapshot.Credentials = maps.Clone(row.Credentials)
					repo := &tokenRefreshAccountRepo{}
					repo.accountsByID = map[int64]*accountcore.Record{1: row}
					blocker := &s06FailureBlocker{}
					s := newRefreshAttemptFixture(repo, &accountcore.RefreshTuning{MaxRetries: 1}, nil, nil, nil)
					s.Attempts.PrepareFailure = func(v *accountcore.Record) func(time.Time, string) {
						return accountcore.PrepareRefreshFailureNotice(blocker, v)
					}
					if engine == "unified" {
						s.Attempts.API = newRefreshAPI(repo, nil)
					}
					message := "invalid_grant fixture"
					if mode == "retry_exhausted" {
						message = "temporary upstream fixture"
					}
					refresher := &s06OldFailureRefresher{row: row, failure: errors.New(message)}
					require.Error(t, s.Attempts.Run(context.Background(), &snapshot, refresher, refresher, time.Hour, nil))
					require.Zero(t, repo.setErrorCalls+repo.setTempUnschedCalls, "旧失败不能修改新身份的健康状态")
					require.Zero(t, blocker.calls, "旧失败不能阻断新身份的内存调度")
					require.Zero(t, repo.updateExtraCalls, "旧失败不能退休新身份的强制刷新标记")
				})
			}
		}
	}
}
