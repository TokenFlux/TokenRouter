//go:build unit

// 保留原标签测试的私有委托，生产代码不保留测试专用入口。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/notification"
)

// buildQuotaDimsFromState builds quota dimensions using DB transaction state instead of account snapshot.
// Notification settings (enabled, threshold, thresholdType) come from the account; usage values from quotaState.
func buildQuotaDimsFromState(account *Account, state *AccountQuotaState) []quotaDim {
	return []quotaDim{
		{quotaDimDaily, account.GetQuotaNotifyDailyEnabled(), account.GetQuotaNotifyDailyThreshold(), account.GetQuotaNotifyDailyThresholdType(), state.DailyUsed, state.DailyLimit},
		{quotaDimWeekly, account.GetQuotaNotifyWeeklyEnabled(), account.GetQuotaNotifyWeeklyThreshold(), account.GetQuotaNotifyWeeklyThresholdType(), state.WeeklyUsed, state.WeeklyLimit},
		{quotaDimTotal, account.GetQuotaNotifyTotalEnabled(), account.GetQuotaNotifyTotalThreshold(), account.GetQuotaNotifyTotalThresholdType(), state.TotalUsed, state.TotalLimit},
	}
}

const thresholdTypeFixed = "fixed"

const thresholdTypePercentage = "percentage"

const defaultSiteName = "Sub2API"

func (s *BalanceNotifyService) checkQuotaDimCrossings(account *Account, dims []quotaDim, cost float64, adminEmails []string, siteName string) {
	s.native().CheckQuotaDimCrossings(QuotaNotifyAccountView(account), quotaDimensions(dims), cost, adminEmails, siteName)
}

func (s *BalanceNotifyService) getBalanceNotifyConfig(ctx context.Context) (enabled bool, threshold float64, rechargeURL string) {
	return s.native().GetBalanceNotifyConfig(ctx)
}

func (s *BalanceNotifyService) isAccountQuotaNotifyEnabled(ctx context.Context) bool {
	return s.native().IsAccountQuotaNotifyEnabled(ctx)
}

func (s *BalanceNotifyService) getSiteName(ctx context.Context) string {
	return s.native().GetSiteName(ctx)
}

func (s *BalanceNotifyService) collectBalanceNotifyRecipients(user *User) []string {
	return s.native().CollectBalanceNotifyRecipients(BillingUserSummary(user))
}

func crossedDownward(oldV, newV, threshold float64) bool {
	return billing.CrossedDownward(oldV, newV, threshold)
}

func sanitizeEmailHeader(s string) string { return notification.SanitizeEmailHeader(s) }

func (s *BalanceNotifyService) buildBalanceLowEmailBody(userName string, balance, threshold float64, siteName, rechargeURL string) string {
	return (*notification.AlertDelivery)(nil).BuildBalanceLowEmailBody(userName, balance, threshold, siteName, rechargeURL)
}

func (s *BalanceNotifyService) buildQuotaAlertEmailBody(accountID int64, accountName, platform, dimLabel string, used, limit, remaining float64, thresholdDisplay, siteName string) string {
	return (*notification.AlertDelivery)(nil).BuildQuotaAlertEmailBody(accountID, accountName, platform, dimLabel, used, limit, remaining, thresholdDisplay, siteName)
}
