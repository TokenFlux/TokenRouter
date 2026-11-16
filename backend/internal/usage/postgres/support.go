package postgres

import (
	"context"

	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keypg "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpg "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	identitypg "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	infra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	groupPG "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// support.go maps database associations into usage display views and delegates
// shared SQL operations.
// Usage reports combine raw SQL records with current identity, key, group,
// provider, and subscription data. These conversions keep each owning module's
// mapping rules in one place while presenting the smaller usage views to
// association loaders.
//
// User and key views pass through querycache.Clone to copy supported mutable
// fields after conversion. Group and subscription conversion retain references
// defined by their owning mappers, so association consumers treat them as
// read-only. SQL and deadlock helpers delegate connection choice, row cleanup,
// and retries to infra; joining an Ent transaction changes the selected client
// while transaction completion remains the caller's responsibility.

// postgresDeadlockMaxAttempts labels the maximum complete-operation attempts in
// usage batch failure logs, using the retry limit owned by infra.
const postgresDeadlockMaxAttempts = infra.DeadlockMaxAttempts

type (
	// sqlExecutor accepts statements and queries on an infra-selected connection or
	// transaction.
	sqlExecutor = infra.Executor
	// sqlQueryer supplies SQL rows to the shared single-row scanning helper.
	sqlQueryer = infra.Queryer
)

// scanSingleRow scans the first SQL result through infra and closes the acquired
// rows. Empty results return sql.ErrNoRows; scan, iteration, and cleanup
// failures follow infra.ScanSingleRow's error handling.
func scanSingleRow(ctx context.Context, q sqlQueryer, query string, args []any, out ...any) error {
	return infra.ScanSingleRow(ctx, q, query, args, out...)
}

// paginationResultFromTotal constructs page metadata from the supplied row
// count.
func paginationResultFromTotal(total int64, p pagination.PaginationParams) *pagination.PaginationResult {
	return pagination.ResultFromTotal(total, p)
}

// groupEntityToService converts a nullable Ent group using routing's group
// mapper. The result retains the mapper's shared fields, including localization
// and routing collections, so usage readers treat those fields as read-only.
func groupEntityToService(m *ent.Group) *usage.GroupView {
	return (*accessview.GroupConfig)(groupPG.GroupFromEnt(m))
}

// providerEntityToService builds a provider display view from its recorded ID
// and name. Nil entities return nil, allowing association hydration to leave
// missing provider data absent.
func providerEntityToService(m *ent.Provider) *usage.ProviderView {
	if m == nil {
		return nil
	}
	return &usage.ProviderView{ID: m.ID, Name: m.Name}
}

// userSubscriptionEntityToService converts a nullable Ent grant through
// billing's mapper. Deleted rows receive its display status, and retained
// optional pointers remain shared under billing.SubscriptionFromEntity's
// ownership rules.
func userSubscriptionEntityToService(m *ent.UserSubscription) *billing.UserSubscription {
	return billingpg.SubscriptionFromEntity(m)
}

// userEntityToService copies the identity mapper's represented user fields into
// a usage view. Nil entities return nil; querycache.Clone copies supported
// exported mutable values before the view enters an association map.
func userEntityToService(m *ent.User) *usage.UserView {
	v := identitypg.UserFromEntity(m)
	if v == nil {
		return nil
	}
	out := &usage.UserView{ID: v.ID, Email: v.Email, Username: v.Username, Role: v.Role, Balance: v.Balance, FrozenBalance: v.FrozenBalance, Concurrency: v.Concurrency, Status: v.Status, AllowedGroups: v.AllowedGroups, DisabledPublicGroups: v.DisabledPublicGroups, LastActiveAt: v.LastActiveAt, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, DeletedAt: v.DeletedAt, BalanceNotifyEnabled: v.BalanceNotifyEnabled, BalanceNotifyThresholdType: v.BalanceNotifyThresholdType, BalanceNotifyThreshold: v.BalanceNotifyThreshold, BalanceNotifyExtraEmails: v.BalanceNotifyExtraEmails, TotalRecharged: v.TotalRecharged, RPMLimit: v.RPMLimit, APIKeyLimit: v.APIKeyLimit}
	return querycache.Clone(out)
}

// apiKeyEntityToService builds a usage key view from the key mapper and its
// loaded routing groups. Nil input returns nil; supported mutable fields are
// cloned after group and composite-binding conversion.
func apiKeyEntityToService(m *ent.APIKey) *usage.KeyView {
	v := keypg.KeyApiKeyEntityToService(m)
	if v == nil {
		return nil
	}
	out := &usage.KeyView{ID: v.ID, UserID: v.UserID, TeamID: v.TeamID, TeamOwnerDisabled: v.TeamOwnerDisabled, Key: v.Key, Name: v.Name, GroupID: v.GroupID, IsComposite: v.IsComposite, Status: v.Status, FastModePolicy: v.FastModePolicy, ForceClaude1hCache: v.ForceClaude1hCache, BillingMode: v.BillingMode, PreferredSubscriptionID: v.PreferredSubscriptionID, ModelMapping: v.ModelMapping, IPWhitelist: v.IPWhitelist, IPBlacklist: v.IPBlacklist, LastUsedAt: v.LastUsedAt, LastUsedIP: v.LastUsedIP, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, FallbackWhenGroupUnavailable: v.FallbackWhenGroupUnavailable, CurrentConcurrency: v.CurrentConcurrency, ManagedBy: v.ManagedBy, Quota: v.Quota, QuotaUsed: v.QuotaUsed, ExpiresAt: v.ExpiresAt, RateLimit5h: v.RateLimit5h, RateLimit1d: v.RateLimit1d, RateLimit7d: v.RateLimit7d, Usage5h: v.Usage5h, Usage1d: v.Usage1d, Usage7d: v.Usage7d, Window5hStart: v.Window5hStart, Window1dStart: v.Window1dStart, Window7dStart: v.Window7dStart}
	out.Group = (*accessview.GroupConfig)(apikey.RoutingGroup(v.Group))
	for _, g := range v.CompositeGroups {
		out.CompositeGroups = append(out.CompositeGroups, usage.KeyCompositeGroupView{ID: g.ID, APIKeyID: g.APIKeyID, GroupID: g.GroupID, Prefix: g.Prefix, NormalizedPrefix: g.NormalizedPrefix, SortOrder: g.SortOrder, UserGroupRPMOverride: g.UserGroupRPMOverride, Group: (*accessview.GroupConfig)(apikey.RoutingGroup(g.Group))})
	}
	return querycache.Clone(out)
}

// retryPostgresDeadlock delegates complete-operation retries for SQLSTATE 40P01
// to infra. The callback recreates its statement or transaction on each attempt;
// its context also bounds the shared retry waits.
func retryPostgresDeadlock[T any](ctx context.Context, operation string, size int, fn func() (T, error)) (T, error) {
	return infra.RetryDeadlock(ctx, operation, size, fn)
}

// postgresSQLState extracts a pq.Error code through infra's error-chain
// traversal. Nil errors or chains with no nonnil pq.Error produce empty text,
// allowing batch log callers to use their unknown-state label.
func postgresSQLState(err error) string { return infra.SQLState(err) }

// isPostgresDeadlock reports whether infra identifies a PostgreSQL deadlock
// error.
func isPostgresDeadlock(err error) bool { return infra.IsDeadlock(err) }

// clientFromContext selects an Ent transaction client when the context carries a
// transaction. Otherwise it returns fallback; the caller remains responsible for
// committing or rolling back any selected transaction.
func clientFromContext(ctx context.Context, fallback *ent.Client) *ent.Client {
	if tx := ent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return fallback
}
