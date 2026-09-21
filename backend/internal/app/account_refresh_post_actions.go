package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"
)

// provideRefreshPostActions 只绑定存储和观察接口；后置规则由 account 执行。
func provideRefreshPostActions(store *postgres.AccountStore, privacy *account.PrivacyService, invalidator account.TokenCacheInvalidator, cache scheduler.SnapshotCache, cooldown account.TempUnschedCache, blocker account.RuntimeUnblocker) *account.RefreshPostActions {
	post := &account.RefreshPostActions{
		Now: time.Now, Info: slog.Info, Warn: slog.Warn, Debug: slog.Debug,
		Privacy: privacy, RequestClearer: store, NeedsReauth: account.GrokNeedsReauth,
		ClearReauth: func(ctx context.Context, value *account.Record) {
			account.ClearGrokNeedsReauth(ctx, store, value.ID)
		},
		ClearError: func(ctx context.Context, value *account.Record) (bool, error) {
			return store.ClearUsageErrorIfUnchanged(ctx, account.UsageRecoveryVersion{
				CredentialVersion: account.FailureVersion(value).CredentialVersion, ErrorMessage: value.ErrorMessage,
			})
		},
		ClearCooldown: func(ctx context.Context, value *account.Record) (bool, error) {
			return store.ClearRefreshCooldownIfUnchanged(ctx, account.ObserveRefreshCooldown(value))
		},
		ClearBlock: func(id int64) {
			if blocker != nil && id > 0 {
				blocker.ClearAccountSchedulingBlock(id)
			}
		},
	}
	if invalidator != nil {
		post.Invalidate = invalidator.InvalidateToken
	}
	if cache != nil {
		post.SyncAccount = func(ctx context.Context, value *account.Record) error {
			return cache.SetAccount(ctx, codec.WrapRecord(value))
		}
	}
	if cooldown != nil {
		post.DeleteCooldown = cooldown.DeleteTempUnsched
	}
	return post
}
