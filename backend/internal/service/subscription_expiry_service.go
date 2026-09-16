package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/google/uuid"
)

// SubscriptionExpiryService 兼容旧类型名，维护实现由 billing 唯一持有。
type SubscriptionExpiryService = billing.SubscriptionExpiryService

// LegacyExpiryNotifier 只做通知投影；生产装配通过 app/legacybridge 接入。
type LegacyExpiryNotifier struct{ Service *NotificationEmailService }

func (n LegacyExpiryNotifier) Ready(ctx context.Context) error {
	if n.Service == nil {
		return billing.ErrReminderTransportUnconfigured
	}
	err := n.Service.CheckTransport(ctx)
	if errors.Is(err, ErrEmailNotConfigured) {
		return billing.ErrReminderTransportUnconfigured
	}
	return err
}
func (n LegacyExpiryNotifier) Send(ctx context.Context, r billing.ExpiryReminder) error {
	return n.Service.Send(ctx, NotificationEmailSendInput{
		Event:          NotificationEmailEventSubscriptionExpiryReminder,
		RecipientEmail: r.RecipientEmail, RecipientName: r.RecipientName, UserID: r.UserID,
		SourceType: "user_subscription", SourceID: strconv.FormatInt(r.SubscriptionID, 10),
		ReminderKey: fmt.Sprintf("%dd", r.DaysRemaining),
		Variables:   map[string]string{"subscription_group": r.PlanName, "expiry_time": r.ExpiresAt.Format("2006-01-02 15:04"), "days_remaining": strconv.Itoa(r.DaysRemaining)},
	})
}

// NewSubscriptionExpiryService 保留旧构造签名，不隐式启动。
func NewSubscriptionExpiryService(repo UserSubscriptionRepository, interval time.Duration) *SubscriptionExpiryService {
	return billing.NewSubscriptionExpiryService(repo, billing.ExpiryOptions{Interval: interval, Owner: uuid.NewString(), Observe: log.Printf})
}

// AcquireLegacySingletonLease 保留既有 Redis 失败回退数据库及无后端语义，S15 删除转接。
func AcquireLegacySingletonLease(ctx context.Context, cache LeaderLockCache, db *sql.DB, key, owner string, ttl time.Duration) (func(), bool) {
	return tryAcquireSingletonLeaderLock(ctx, cache, db, key, owner, ttl)
}
