//go:build unit

package provider

import (
	"context"
	"errors"
	"log/slog"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// refreshAttemptFixture 只装配生产组件；重试、清理与熔断均由 account 的唯一实现执行。
type refreshAttemptFixture struct {
	Attempts accountcore.RefreshAttempts
	Post     *accountcore.RefreshPostActions
}

func newRefreshAttemptFixture(repo *tokenRefreshAccountRepo, tuning *accountcore.RefreshTuning, invalidator accountcore.TokenCacheInvalidator, scheduler *tokenRefreshSchedulerCache, cooldown accountcore.TempUnschedCache) *refreshAttemptFixture {
	post := &accountcore.RefreshPostActions{
		Now: time.Now, Info: slog.Info, Warn: slog.Warn, Debug: slog.Debug,
		RequestClearer: repo, NeedsReauth: accountcore.GrokNeedsReauth,
		ClearBlock: func(int64) {},
		ClearReauth: func(ctx context.Context, value *accountcore.Record) {
			accountcore.ClearGrokNeedsReauth(ctx, repo, value.ID)
		},
		ClearError: func(context.Context, *accountcore.Record) (bool, error) {
			return false, errors.New("refresh error conditional writer is not configured")
		},
		ClearCooldown: func(ctx context.Context, value *accountcore.Record) (bool, error) {
			return repo.ClearRefreshCooldownIfUnchanged(ctx, accountcore.ObserveRefreshCooldown(value))
		},
	}
	if invalidator != nil {
		post.Invalidate = invalidator.InvalidateToken
	}
	if scheduler != nil {
		post.SyncAccount = scheduler.SetAccount
	}
	if cooldown != nil {
		post.DeleteCooldown = cooldown.DeleteTempUnsched
	}
	return &refreshAttemptFixture{Post: post, Attempts: accountcore.RefreshAttempts{
		Tuning: tuning, Policy: accountcore.DefaultBackgroundRefreshPolicy(), AttemptTimeout: tuning.AttemptTimeout(0, 0, false),
		Now: time.Now, Info: slog.Info, Warn: slog.Warn, Error: slog.Error,
		NonRetryable: IsNonRetryableRefreshError, SharedProviderError: IsSharedProviderRefreshError,
		AmbiguousEntitlement: IsAmbiguousGrokEntitlementRefreshError,
		FailureWriter:        repo, GrokMutation: repo, Invalidate: post.Invalidate,
		PrepareFailure:      func(*accountcore.Record) func(time.Time, string) { return func(time.Time, string) {} },
		ClearRefreshRequest: post.ClearRefreshRequest, PostActions: post.Run, SyncCleanup: post.SyncWithCleanup,
		Persist: func(ctx context.Context, value *accountcore.Record, credentials map[string]any) error {
			_, err := accountcore.PersistCredentials(ctx, repo, value, credentials, slog.Warn)
			return err
		},
	}}
}

func newRefreshAPI(repo accountcore.RefreshRepository, cache accountcore.RefreshCache) *accountcore.OAuthRefreshAPI {
	return accountcore.NewOAuthRefreshAPI(repo, cache, accountcore.RefreshOptions{Platform: accountcore.AccountRefreshPlatformPolicy()})
}

// refreshRecordFixture 模拟原行读取，不构造任何旧账号服务或缓存。
type refreshRecordFixture struct{ accountsByID map[int64]*accountcore.Record }

func (r *refreshRecordFixture) GetByID(_ context.Context, id int64) (*accountcore.Record, error) {
	if value := r.accountsByID[id]; value != nil {
		return value, nil
	}
	return nil, errors.New("account not found")
}
