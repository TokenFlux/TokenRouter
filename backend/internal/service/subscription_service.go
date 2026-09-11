// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package service

import (
	context "context"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	time "time"
)

var MaxExpiresAt = billing.MaxExpiresAt

const MaxValidityDays = billing.MaxValidityDays

var ErrSubscriptionNotFound = billing.ErrSubscriptionNotFound

var ErrSubscriptionExpired = billing.ErrSubscriptionExpired

var ErrSubscriptionSuspended = billing.ErrSubscriptionSuspended

var ErrSubscriptionAlreadyExists = billing.ErrSubscriptionAlreadyExists

var ErrSubscriptionNotRevoked = billing.ErrSubscriptionNotRevoked

var ErrSubscriptionRestoreConflict = billing.ErrSubscriptionRestoreConflict

var ErrSubscriptionNotActive = billing.ErrSubscriptionNotActive

var ErrSubscriptionQuotaAvailable = billing.ErrSubscriptionQuotaAvailable

var ErrInvalidInput = billing.ErrInvalidInput

var ErrDailyLimitExceeded = billing.ErrDailyLimitExceeded

var ErrWeeklyLimitExceeded = billing.ErrWeeklyLimitExceeded

var ErrMonthlyLimitExceeded = billing.ErrMonthlyLimitExceeded

var ErrSubscriptionNilInput = billing.ErrSubscriptionNilInput

var ErrAdjustWouldExpire = billing.ErrAdjustWouldExpire

type SubscriptionService = billing.SubscriptionService

type SelfRevokeSubscriptionResult = billing.SelfRevokeSubscriptionResult

type AssignSubscriptionInput = billing.AssignSubscriptionInput

type BulkAssignSubscriptionInput = billing.BulkAssignSubscriptionInput

type BulkAssignResult = billing.BulkAssignResult

func normalizeAssignValidityDays(days int) int { return billing.NormalizeAssignValidityDays(days) }

func revokeChainDelta(sub *UserSubscription, now time.Time) time.Duration {
	return billing.RevokeChainDelta(sub, now)
}

func targetSubscriptionExpiresAt(sub *UserSubscription, now time.Time, validityDays int) time.Time {
	return billing.TargetSubscriptionExpiresAt(sub, now, validityDays)
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

type SubscriptionProgress = billing.SubscriptionProgress

type UsageWindowProgress = billing.UsageWindowProgress

// subscriptionGroupProjection 只为旧构造入口投影分组名称；生产装配在 app 绑定。
type subscriptionGroupProjection struct{ groups GroupRepository }

func (p subscriptionGroupProjection) GetByIDLite(ctx context.Context, id int64) (*billing.SubscriptionPlanGroup, error) {
	if p.groups == nil {
		return nil, nil
	}
	g, err := p.groups.GetByIDLite(ctx, id)
	if err != nil || g == nil {
		return nil, err
	}
	return &billing.SubscriptionPlanGroup{ID: g.ID, Name: g.Name}, nil
}

// NewSubscriptionService 保留旧构造器，所有规则由 billing 唯一执行。
func NewSubscriptionService(groups GroupRepository, repo UserSubscriptionRepository, _ *BillingCacheService, client *dbent.Client, _ *config.Config) *SubscriptionService {
	return billing.NewSubscriptionService(subscriptionGroupProjection{groups}, repo, billingpostgres.NewSubscriptionMutations(client))
}
