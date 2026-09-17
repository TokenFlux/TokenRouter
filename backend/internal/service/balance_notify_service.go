// 旧网关通知入口只投影，阈值与投递各自使用唯一实现。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/notification"
)

type AccountQuotaReader interface {
	GetByID(context.Context, int64) (*Account, error)
}
type BalanceNotifyService struct {
	core     *billing.BalanceNotifyService
	delivery *notification.AlertDelivery
}

func WrapBalanceNotifyService(core *billing.BalanceNotifyService, delivery *notification.AlertDelivery) *BalanceNotifyService {
	return &BalanceNotifyService{core: core, delivery: delivery}
}
func NewBalanceNotifyService(email *EmailService, settings SettingRepository, accounts AccountQuotaReader) *BalanceNotifyService {
	var sender billing.AlertSender
	delivery := notification.NewAlertDelivery(email, nil)
	if email != nil {
		sender = delivery
	}
	var reader billing.QuotaNotifyReader
	if accounts != nil {
		reader = legacyQuotaNotifyReader{accounts}
	}
	return WrapBalanceNotifyService(billing.NewBalanceNotifyService(sender, settings, reader, func(name string, fn func()) { RunBackgroundTask(name, BackgroundCall0(fn)) }), delivery)
}
func (s *BalanceNotifyService) native() *billing.BalanceNotifyService {
	if s.core != nil {
		return s.core
	}
	return billing.NewBalanceNotifyService(nil, nil, nil, nil)
}
func (s *BalanceNotifyService) SetNotificationEmailService(n *NotificationEmailService) {
	if s.delivery != nil {
		s.delivery.SetNotificationEmailService(n)
	}
}

type legacyQuotaNotifyReader struct{ reader AccountQuotaReader }

func (r legacyQuotaNotifyReader) GetByID(ctx context.Context, id int64) (*billing.QuotaNotifyAccount, error) {
	v, e := r.reader.GetByID(ctx, id)
	return QuotaNotifyAccountView(v), e
}
func QuotaNotifyAccountView(a *Account) *billing.QuotaNotifyAccount {
	if a == nil {
		return nil
	}
	return &billing.QuotaNotifyAccount{ID: a.ID, Name: a.Name, Platform: a.Platform, Dimensions: quotaDimensions(buildQuotaDims(a))}
}
func quotaDimensions(dims []quotaDim) []billing.QuotaNotifyDimension {
	out := make([]billing.QuotaNotifyDimension, len(dims))
	for i, d := range dims {
		out[i] = d.billingDimension()
	}
	return out
}

// quotaDim describes one quota dimension for notification checking.
type quotaDim struct {
	name          string
	enabled       bool
	threshold     float64
	thresholdType string // "fixed" (default) or "percentage"
	currentUsed   float64
	limit         float64
}

// buildQuotaDims returns the three quota dimensions for notification checking.
func buildQuotaDims(account *Account) []quotaDim {
	return []quotaDim{
		{quotaDimDaily, account.GetQuotaNotifyDailyEnabled(), account.GetQuotaNotifyDailyThreshold(), account.GetQuotaNotifyDailyThresholdType(), account.GetQuotaDailyUsed(), account.GetQuotaDailyLimit()},
		{quotaDimWeekly, account.GetQuotaNotifyWeeklyEnabled(), account.GetQuotaNotifyWeeklyThreshold(), account.GetQuotaNotifyWeeklyThresholdType(), account.GetQuotaWeeklyUsed(), account.GetQuotaWeeklyLimit()},
		{quotaDimTotal, account.GetQuotaNotifyTotalEnabled(), account.GetQuotaNotifyTotalThreshold(), account.GetQuotaNotifyTotalThresholdType(), account.GetQuotaUsed(), account.GetQuotaLimit()},
	}
}

// billingDimension 只投影通知配置和已经取得的事务状态。
func (d quotaDim) billingDimension() billing.QuotaNotifyDimension {
	return billing.QuotaNotifyDimension{Name: d.name, Enabled: d.enabled, Threshold: d.threshold, ThresholdType: d.thresholdType, CurrentUsed: d.currentUsed, Limit: d.limit}
}

const quotaDimDaily = "daily"
const quotaDimWeekly = "weekly"
const quotaDimTotal = "total"

func (s *BalanceNotifyService) CheckBalanceAfterDeduction(ctx context.Context, user *User, oldBalance, cost float64) {
	s.native().CheckBalanceAfterDeduction(ctx, BillingUserSummary(user), oldBalance, cost)
}

func (s *BalanceNotifyService) CheckAccountQuotaAfterIncrement(ctx context.Context, account *Account, cost float64, quotaState *AccountQuotaState) {
	s.native().CheckAccountQuotaAfterIncrement(ctx, QuotaNotifyAccountView(account), cost, quotaState)
}
