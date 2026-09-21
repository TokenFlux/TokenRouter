package provider

import (
	"context"
	"log/slog"
	"reflect"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// backgroundAttemptOptions 只绑定替身端口，实际周期、尝试和成功清理由原生组件执行。
func backgroundAttemptOptions(repo accountcore.CredentialUpdateStore, tuning *accountcore.RefreshTuning) accountcore.RefreshAttempts {
	post := &accountcore.RefreshPostActions{
		Now: time.Now, Info: slog.Info, Warn: slog.Warn, Debug: slog.Debug,
		ClearBlock: func(int64) {}, NeedsReauth: accountcore.GrokNeedsReauth,
		ClearReauth: func(context.Context, *accountcore.Record) {},
	}
	failure, _ := repo.(accountcore.RefreshFailureWriter)
	grok, _ := repo.(accountcore.GrokRefreshMutationWriter)
	return accountcore.RefreshAttempts{
		Tuning: tuning, Policy: accountcore.DefaultBackgroundRefreshPolicy(),
		AttemptTimeout: tuning.AttemptTimeout(0, 0, false), Now: time.Now,
		Info: slog.Info, Warn: slog.Warn, Error: slog.Error,
		NonRetryable: IsNonRetryableRefreshError, SharedProviderError: IsSharedProviderRefreshError,
		AmbiguousEntitlement: IsAmbiguousGrokEntitlementRefreshError,
		FailureWriter:        failure, GrokMutation: grok,
		PrepareFailure:      func(*accountcore.Record) func(time.Time, string) { return func(time.Time, string) {} },
		ClearRefreshRequest: post.ClearRefreshRequest, PostActions: post.Run, SyncCleanup: post.SyncWithCleanup,
		Persist: func(ctx context.Context, value *accountcore.Record, credentials map[string]any) error {
			_, err := accountcore.PersistCredentials(ctx, repo, value, credentials, slog.Warn)
			return err
		},
	}
}

func refreshAPIForFixture(repo accountcore.RefreshRepository, cache accountcore.RefreshCache) *accountcore.OAuthRefreshAPI {
	return accountcore.NewOAuthRefreshAPI(repo, cache, accountcore.RefreshOptions{Platform: accountcore.AccountRefreshPlatformPolicy()})
}

// 测试写入保留字段计数；任何意外的整行更新仍通过同一替身可观测。
func (r *poolHealthAccountRepo) Update(ctx context.Context, value *accountcore.Record) error {
	return r.UpdateCredentials(ctx, value.ID, value.Credentials)
}
func (r *productionPathRateRepo) Update(ctx context.Context, value *accountcore.Record) error {
	return r.UpdateCredentials(ctx, value.ID, value.Credentials)
}
func (r *grokReconcileRepo) Update(ctx context.Context, value *accountcore.Record) error {
	return r.UpdateCredentials(ctx, value.ID, value.Credentials)
}
func (b *reconcileRuntimeBlocker) PrepareRefreshFailure(int64) func(accountcore.RefreshFailureNotice) {
	return func(value accountcore.RefreshFailureNotice) {
		b.BlockAccountScheduling(&accountcore.Record{ID: value.AccountID}, value.Until, value.Reason)
	}
}

func (r *tokenRefreshCandidateRepo) Update(ctx context.Context, value *accountcore.Record) error {
	return r.UpdateCredentials(ctx, value.ID, value.Credentials)
}

func candidatePostActions(repo *tokenRefreshCandidateRepo) *accountcore.RefreshPostActions {
	return &accountcore.RefreshPostActions{Now: time.Now, Info: slog.Info, Warn: slog.Warn, Debug: slog.Debug, ClearBlock: func(int64) {}, NeedsReauth: accountcore.GrokNeedsReauth,
		ClearCooldown: func(ctx context.Context, value *accountcore.Record) (bool, error) {
			return repo.ClearRefreshCooldownIfUnchanged(ctx, accountcore.ObserveRefreshCooldown(value))
		},
	}
}

func cooldownPostActions(repo *refreshSuccessCooldownRepo) *accountcore.RefreshPostActions {
	return &accountcore.RefreshPostActions{Now: time.Now, Info: slog.Info, Warn: slog.Warn, Debug: slog.Debug, ClearBlock: func(int64) {}, NeedsReauth: accountcore.GrokNeedsReauth,
		ClearCooldown: func(ctx context.Context, value *accountcore.Record) (bool, error) {
			return repo.ClearRefreshCooldownIfUnchanged(ctx, accountcore.ObserveRefreshCooldown(value))
		},
	}
}

// 竞争替身比较当前行身份，nil 凭据与原刷新快照一致。
func refreshFailureMatchesFixture(value *accountcore.Record, version accountcore.RefreshFailureVersion) bool {
	return value != nil && reflect.DeepEqual(accountcore.FailureVersion(value), version)
}

func (r *tokenRefreshCandidateRepo) ApplyOAuthRefreshFailure(ctx context.Context, version accountcore.RefreshFailureVersion, failure accountcore.RefreshFailure) (bool, error) {
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
	if failure.Kind == accountcore.RefreshFailurePermanent {
		r.setErrorCalls++
	} else {
		r.setTempUnschedCalls++
		r.lastTempUnschedReason = failure.Message
	}
	return true, nil
}
