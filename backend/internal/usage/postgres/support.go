// 此文件只组合已有实体转换与用量显示字段，不含管理或计费规则。
package postgres

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"

	"github.com/TokenFlux/TokenRouter/ent"
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

type sqlExecutor = infra.Executor
type sqlQueryer = infra.Queryer

func scanSingleRow(ctx context.Context, q sqlQueryer, query string, args []any, out ...any) error {
	return infra.ScanSingleRow(ctx, q, query, args, out...)
}
func paginationResultFromTotal(total int64, p pagination.PaginationParams) *pagination.PaginationResult {
	return pagination.ResultFromTotal(total, p)
}

func groupEntityToService(m *ent.Group) *usage.GroupView {
	return (*accessview.GroupConfig)(groupPG.GroupFromEnt(m))
}
func accountEntityToService(m *ent.Account) *usage.AccountView {
	if m == nil {
		return nil
	}
	return &usage.AccountView{ID: m.ID, Name: m.Name}
}
func userSubscriptionEntityToService(m *ent.UserSubscription) *billing.UserSubscription {
	return billingpg.SubscriptionFromEntity(m)
}
func userEntityToService(m *ent.User) *usage.UserView {
	v := identitypg.UserFromEntity(m)
	if v == nil {
		return nil
	}
	out := &usage.UserView{ID: v.ID, Email: v.Email, Username: v.Username, Role: v.Role, Balance: v.Balance, FrozenBalance: v.FrozenBalance, Concurrency: v.Concurrency, Status: v.Status, AllowedGroups: v.AllowedGroups, DisabledPublicGroups: v.DisabledPublicGroups, LastActiveAt: v.LastActiveAt, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, DeletedAt: v.DeletedAt, BalanceNotifyEnabled: v.BalanceNotifyEnabled, BalanceNotifyThresholdType: v.BalanceNotifyThresholdType, BalanceNotifyThreshold: v.BalanceNotifyThreshold, BalanceNotifyExtraEmails: v.BalanceNotifyExtraEmails, TotalRecharged: v.TotalRecharged, RPMLimit: v.RPMLimit, APIKeyLimit: v.APIKeyLimit}
	return querycache.Clone(out)
}
func apiKeyEntityToService(m *ent.APIKey) *usage.KeyView {
	v := keypg.KeyApiKeyEntityToService(m)
	if v == nil {
		return nil
	}
	out := &usage.KeyView{ID: v.ID, UserID: v.UserID, TeamID: v.TeamID, TeamOwnerDisabled: v.TeamOwnerDisabled, Key: v.Key, Name: v.Name, GroupID: v.GroupID, IsComposite: v.IsComposite, Status: v.Status, FastModePolicy: v.FastModePolicy, BillingMode: v.BillingMode, PreferredSubscriptionID: v.PreferredSubscriptionID, ModelMapping: v.ModelMapping, IPWhitelist: v.IPWhitelist, IPBlacklist: v.IPBlacklist, LastUsedAt: v.LastUsedAt, LastUsedIP: v.LastUsedIP, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, FallbackToDefaultGroupWhenUnavailable: v.FallbackToDefaultGroupWhenUnavailable, CurrentConcurrency: v.CurrentConcurrency, ManagedBy: v.ManagedBy, Quota: v.Quota, QuotaUsed: v.QuotaUsed, ExpiresAt: v.ExpiresAt, RateLimit5h: v.RateLimit5h, RateLimit1d: v.RateLimit1d, RateLimit7d: v.RateLimit7d, Usage5h: v.Usage5h, Usage1d: v.Usage1d, Usage7d: v.Usage7d, Window5hStart: v.Window5hStart, Window1dStart: v.Window1dStart, Window7dStart: v.Window7dStart}
	out.Group = (*accessview.GroupConfig)(apikey.RoutingGroup(v.Group))
	for _, g := range v.CompositeGroups {
		out.CompositeGroups = append(out.CompositeGroups, usage.KeyCompositeGroupView{ID: g.ID, APIKeyID: g.APIKeyID, GroupID: g.GroupID, Prefix: g.Prefix, NormalizedPrefix: g.NormalizedPrefix, SortOrder: g.SortOrder, UserGroupRPMOverride: g.UserGroupRPMOverride, Group: (*accessview.GroupConfig)(apikey.RoutingGroup(g.Group))})
	}
	return querycache.Clone(out)
}

// 死锁重试始终复用 infra 的完整操作闭包机制。
const postgresDeadlockMaxAttempts = infra.DeadlockMaxAttempts

func retryPostgresDeadlock[T any](ctx context.Context, operation string, size int, fn func() (T, error)) (T, error) {
	return infra.RetryDeadlock(ctx, operation, size, fn)
}
func postgresSQLState(err error) string { return infra.SQLState(err) }
func isPostgresDeadlock(err error) bool { return infra.IsDeadlock(err) }

// clientFromContext 仅沿用既有 Ent 事务键，不创建或提交事务。
func clientFromContext(ctx context.Context, fallback *ent.Client) *ent.Client {
	if tx := ent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return fallback
}
