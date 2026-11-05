package app

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/user"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/audit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/plugin/postgres"
	"github.com/TokenFlux/TokenRouter/internal/plugin/provider"
)

// plugin_capabilities.go binds plugin financial and entitlement authority to existing billing,
// authentication-cache, and audit services. The ledger supplies a transaction
// context that the existing balance adjuster joins, so plugin credits neither
// increase cumulative recharge totals nor bypass the atomic insufficient-funds
// predicate. Subscription issuance locks a live recipient and retains the existing
// plan template and timeline rules. Adapters return only declared result fields.
//
// Cache invalidation and adjustment history follow a successful ledger commit.
// Every auxiliary operation is attempted, and errors are returned together for
// diagnostic logging without repeating the balance change. Audit observations
// arrive separately from the RPC boundary, including permission and rate failures;
// this adapter supplies the existing queue's actor, action, path, and bounded
// metadata fields while the ledger retains authoritative financial evidence.

// pluginWalletEffects adapts existing atomic funding and post-commit observation services.
type pluginWalletEffects struct {
	balances    billing.BalanceAdjuster      // transaction-aware balance adjustment capability
	records     *billing.RedeemAdmin         // completed balance adjustment history writer
	invalidator identity.UserAuthInvalidator // affected-user authentication cache invalidator
	cache       identity.UserBalanceCache    // user funding eligibility cache
}

// pluginSubscriptionEffects joins existing issuance to the ledger and exposes post-commit invalidation.
type pluginSubscriptionEffects struct {
	service     *billing.SubscriptionService // existing plan-template and timeline issuance
	invalidator identity.UserAuthInvalidator // affected-user authentication cache invalidator
}

// pluginCapabilityAudit maps bounded RPC observations into existing administrator audit records.
type pluginCapabilityAudit struct {
	service *audit.AuditLogService // shared best-effort audit queue
}

// providePluginGrants derives persistent caller authorization from the initialized JWT secret.
func providePluginGrants(cfg *config.Config) (*plugin.Grants, error) {
	return plugin.NewGrants(cfg.Auth.JWT.Secret, time.Now)
}

// providePluginWrites binds replay storage to the shared installation policy source.
func providePluginWrites(store *postgres.Store, source *plugin.DevSource) *plugin.Writes {
	return plugin.NewWrites(store, source, time.Now)
}

// providePluginWallet constructs financial capability validation with existing billing collaborators.
func providePluginWallet(writes *plugin.Writes, grants *plugin.Grants, balances billing.BalanceAdjuster, records *billing.RedeemAdmin, invalidator apikey.APIKeyAuthCacheInvalidator, cache *billing.Eligibility) *plugin.WalletService {
	effects := &pluginWalletEffects{balances: balances, records: records, invalidator: invalidator, cache: cache}
	return plugin.NewWalletService(writes, grants, effects, time.Local, slog.Default())
}

// providePluginSubscriptions binds direct entitlement grants to existing transactional billing rules.
func providePluginSubscriptions(writes *plugin.Writes, service *billing.SubscriptionService, invalidator apikey.APIKeyAuthCacheInvalidator) *plugin.Subscriptions {
	effects := &pluginSubscriptionEffects{service: service, invalidator: invalidator}
	return plugin.NewSubscriptions(writes, effects, time.Local)
}

// providePluginHostServices shares configured capabilities and auditing across authenticated process bindings.
func providePluginHostServices(wallet *plugin.WalletService, subscriptions *plugin.Subscriptions, queries *plugin.Queries, emails *plugin.Emails, captcha plugin.CaptchaVerifier, kv *plugin.KV, redeem *plugin.RedeemCodes, grants *plugin.Grants, auditor *audit.AuditLogService) *provider.HostServices {
	return provider.NewHostServices(provider.HostCapabilities{Wallet: wallet, Subscriptions: subscriptions, Queries: queries, Emails: emails, Captcha: captcha, KV: kv, Redeem: redeem}, grants, pluginCapabilityAudit{service: auditor})
}

// AdjustBalance applies the signed delta on the ledger's transaction-bound context.
// Existing billing errors map to stable plugin failures. Returned decimal strings
// use the billing store's balance observations at its eight-place display precision.
func (a *pluginWalletEffects) AdjustBalance(ctx context.Context, userID int64, delta float64) (plugin.WalletChange, error) {
	change, err := a.balances.AdjustBalance(ctx, userID, delta)
	if errors.Is(err, billing.ErrUserNotFound) {
		return plugin.WalletChange{}, plugin.ErrHostUserNotFound
	}
	if errors.Is(err, billing.ErrBalanceNegative) {
		return plugin.WalletChange{}, plugin.ErrInsufficientBalance
	}
	if err != nil {
		return plugin.WalletChange{}, err
	}
	return plugin.WalletChange{Before: strconv.FormatFloat(change.Old, 'f', 8, 64), After: strconv.FormatFloat(change.New, 'f', 8, 64)}, nil
}

// AfterWallet invalidates both user caches and records the completed plugin adjustment.
// The caller supplies an independent bounded context after financial commit.
// Cache or history failure never repeats the balance mutation or requests affiliate rebates.
func (a *pluginWalletEffects) AfterWallet(ctx context.Context, userID int64, delta float64, notes string) error {
	a.invalidator.InvalidateAuthCacheByUserID(ctx, userID)
	cacheErr := a.cache.InvalidateUserBalance(ctx, userID)
	historyErr := a.records.RecordAdjustment(ctx, userID, billing.AdjustmentTypePluginBalance, delta, notes)
	return errors.Join(cacheErr, historyErr)
}

// RecordCapability submits an immutable plugin observation through the existing audit queue.
// A missing verified caller is represented by an absent actor identifier. The
// record intentionally contains no grant bytes, configuration values, or email body.
func (a pluginCapabilityAudit) RecordCapability(observation plugin.CapabilityAudit) {
	var actor *int64
	if observation.ActorUserID > 0 {
		actor = &observation.ActorUserID
	}
	a.service.Record(&audit.AuditLog{
		ActorUserID: actor, ActorRole: "plugin", AuthMethod: "plugin", Action: "plugin." + observation.RPC, Path: "plugin:" + observation.PluginID,
		StatusCode: observation.StatusCode, LatencyMs: observation.LatencyMs,
		Extra: map[string]any{
			"plugin_id": observation.PluginID, "plugin_version": observation.Version, "user_id": observation.UserID,
			"idempotency_key": observation.Key, "replayed": observation.Replayed, "amount": observation.Amount,
			"redeem_type": observation.RedeemType, "quantity": observation.Quantity,
			"plan_id": observation.PlanID, "validity_days": observation.Days, "reason": observation.Reason, "error_reason": observation.ErrorReason,
		},
	})
}

// Assign locks a live recipient before delegating to existing subscription issuance.
// The ledger must already own the transaction. Existing billing repeats the user
// lock on that same connection, retaining its ordinary plan and timeline behavior.
//
// The explicit live-user predicate prevents a soft-deleted account from receiving
// new plugin benefits, including when deletion races with issuance. Missing plans
// map to the plugin plan error after recipient identity has been established.
// AssignedBy and SourceOrderID remain empty, and quota terms come from the live
// plan rather than caller-supplied templates or synthetic payment identities.
func (a *pluginSubscriptionEffects) Assign(ctx context.Context, userID, planID int64, days int32, notes string) (plugin.SubscriptionGrant, error) {
	tx := ent.TxFromContext(ctx)
	if tx == nil {
		return plugin.SubscriptionGrant{}, errors.New("plugin subscription requires its ledger transaction")
	}
	_, err := tx.User.Query().Where(user.IDEQ(userID), user.DeletedAtIsNil()).Select(user.FieldID).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return plugin.SubscriptionGrant{}, plugin.ErrHostUserNotFound
	}
	if err != nil {
		return plugin.SubscriptionGrant{}, err
	}
	granted, queued, err := a.service.AssignOrExtendSubscription(ctx, &billing.AssignSubscriptionInput{
		UserID: userID, PlanID: planID, ValidityDays: int(days), AssignedBy: 0, SourceOrderID: nil, UseProvidedTemplate: false, Notes: notes,
	})
	if ent.IsNotFound(err) {
		return plugin.SubscriptionGrant{}, plugin.ErrPlanNotAllowed
	}
	if err != nil {
		return plugin.SubscriptionGrant{}, err
	}
	return plugin.SubscriptionGrant{ID: granted.ID, ExpiresUnix: granted.ExpiresAt.Unix(), Queued: queued}, nil
}

// InvalidateUserAuth requests fresh authorization after a committed plugin subscription grant.
func (a *pluginSubscriptionEffects) InvalidateUserAuth(ctx context.Context, userID int64) {
	a.invalidator.InvalidateAuthCacheByUserID(ctx, userID)
}
