// BalanceNotifyService 保留阈值判断、配置读取时点和账号回源顺序。
package billing

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/notification/contract"
)

type NotifySettings interface {
	GetMultiple(context.Context, []string) (map[string]string, error)
	GetValue(context.Context, string) (string, error)
}
type AlertSender interface {
	SendBalanceLowEmails([]string, int64, string, string, float64, float64, string, string)
	SendQuotaAlertEmails([]string, int64, string, string, contract.QuotaDimension, float64, string)
}
type QuotaNotifyAccount struct {
	ID             int64
	Name, Platform string
	Dimensions     []QuotaNotifyDimension
}
type QuotaNotifyReader interface {
	GetByID(context.Context, int64) (*QuotaNotifyAccount, error)
}
type BalanceNotifyService struct {
	emailService AlertSender
	settingRepo  NotifySettings
	accountRepo  QuotaNotifyReader
	background   func(string, func())
}

func NewBalanceNotifyService(sender AlertSender, settings NotifySettings, accounts QuotaNotifyReader, background func(string, func())) *BalanceNotifyService {
	return &BalanceNotifyService{emailService: sender, settingRepo: settings, accountRepo: accounts, background: background}
}

const defaultSiteName = "Sub2API"

// CheckBalanceAfterDeduction checks if balance crossed below threshold after deduction.
// Notification is sent only on first crossing: oldBalance >= threshold && newBalance < threshold.
func (s *BalanceNotifyService) CheckBalanceAfterDeduction(ctx context.Context, user *UserSummary, oldBalance, cost float64) {
	if !s.CanNotifyBalance(user) {
		return
	}
	effectiveThreshold, rechargeURL, ok := s.ResolveUserEffectiveThreshold(ctx, user)
	if !ok {
		return
	}
	newBalance := oldBalance - cost
	if !CrossedDownward(oldBalance, newBalance, effectiveThreshold) {
		return
	}
	s.DispatchBalanceLowEmail(ctx, user, newBalance, effectiveThreshold, rechargeURL)
}

// canNotifyBalance checks nil guards and user-level toggle.
func (s *BalanceNotifyService) CanNotifyBalance(user *UserSummary) bool {
	if user == nil || s.emailService == nil || s.settingRepo == nil {
		return false
	}
	return user.BalanceNotifyEnabled
}

// resolveUserEffectiveThreshold 委托 billing 的唯一阈值规则。
func (s *BalanceNotifyService) ResolveUserEffectiveThreshold(ctx context.Context, user *UserSummary) (effectiveThreshold float64, rechargeURL string, ok bool) {
	globalEnabled, globalThreshold, rechargeURL := s.GetBalanceNotifyConfig(ctx)
	effective, ok := EffectiveBalanceThreshold(globalEnabled, globalThreshold, user.BalanceNotifyThreshold, user.BalanceNotifyThresholdType, user.TotalRecharged)
	if !ok {
		return 0, "", false
	}
	return effective, rechargeURL, true
}

// dispatchBalanceLowEmail collects recipients and sends the alert in a goroutine.
func (s *BalanceNotifyService) DispatchBalanceLowEmail(ctx context.Context, user *UserSummary, newBalance, threshold float64, rechargeURL string) {
	siteName := s.GetSiteName(ctx)
	recipients := s.CollectBalanceNotifyRecipients(user)
	slog.Info("CheckBalanceAfterDeduction: sending notification",
		"user_id", user.ID, "recipients", recipients, "new_balance", newBalance, "threshold", threshold)
	s.background("service/balance_notify_service.go:dispatchBalanceLowEmail", func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in balance notification", "recover", r)
			}
		}()
		s.emailService.SendBalanceLowEmails(recipients, user.ID, user.Username, user.Email, newBalance, threshold, siteName, rechargeURL)
	})
}

// CheckAccountQuotaAfterIncrement checks if any quota dimension crossed above its notify threshold.
// When quotaState is non-nil (from DB transaction RETURNING), it is used directly for threshold
// checking, avoiding a separate DB read. Otherwise it falls back to fetching fresh account data.
func (s *BalanceNotifyService) CheckAccountQuotaAfterIncrement(ctx context.Context, account *QuotaNotifyAccount, cost float64, quotaState *AccountQuotaState) {
	if account == nil || s.emailService == nil || s.settingRepo == nil || cost <= 0 {
		return
	}
	if !s.IsAccountQuotaNotifyEnabled(ctx) {
		return
	}
	adminEmails := s.GetAccountQuotaNotifyEmails(ctx)
	if len(adminEmails) == 0 {
		return
	}

	siteName := s.GetSiteName(ctx)
	var dims []QuotaNotifyDimension
	if quotaState != nil {
		dims = quotaDimsFromCommitted(account, quotaState)
	} else {
		freshAccount := s.FetchFreshAccount(ctx, account)
		dims = append([]QuotaNotifyDimension(nil), freshAccount.Dimensions...)
		account = freshAccount // use fresh data for alert metadata
	}
	s.CheckQuotaDimCrossings(account, dims, cost, adminEmails, siteName)
}

// fetchFreshAccount loads the latest account from DB; falls back to the snapshot on error.
func (s *BalanceNotifyService) FetchFreshAccount(ctx context.Context, snapshot *QuotaNotifyAccount) *QuotaNotifyAccount {
	if s.accountRepo == nil {
		return snapshot
	}
	fresh, err := s.accountRepo.GetByID(ctx, snapshot.ID)
	if err != nil {
		slog.Warn("failed to fetch fresh account for quota notify, using snapshot",
			"account_id", snapshot.ID, "error", err)
		return snapshot
	}
	return fresh
}

// checkQuotaDimCrossings 委托 billing 的唯一阈值规则。
func (s *BalanceNotifyService) CheckQuotaDimCrossings(account *QuotaNotifyAccount, dims []QuotaNotifyDimension, cost float64, adminEmails []string, siteName string) {
	for _, dim := range dims {
		if threshold, ok := dim.Crossing(cost); ok {
			s.AsyncSendQuotaAlert(adminEmails, account.ID, account.Name, account.Platform, dim, dim.CurrentUsed, threshold, siteName)
		}
	}
}

// asyncSendQuotaAlert sends quota alert email in a goroutine with panic recovery.
func (s *BalanceNotifyService) AsyncSendQuotaAlert(adminEmails []string, accountID int64, accountName, platform string, dim QuotaNotifyDimension, newUsed, effectiveThreshold float64, siteName string) {
	s.background("service/balance_notify_service.go:asyncSendQuotaAlert", func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in quota notification", "recover", r)
			}
		}()
		s.emailService.SendQuotaAlertEmails(adminEmails, accountID, accountName, platform, contract.QuotaDimension(dim), newUsed, siteName)
	})
}

// getBalanceNotifyConfig reads global balance notification settings.
func (s *BalanceNotifyService) GetBalanceNotifyConfig(ctx context.Context) (enabled bool, threshold float64, rechargeURL string) {
	keys := []string{SettingKeyBalanceLowNotifyEnabled, SettingKeyBalanceLowNotifyThreshold, SettingKeyBalanceLowNotifyRechargeURL}
	settings, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return false, 0, ""
	}
	enabled = settings[SettingKeyBalanceLowNotifyEnabled] == "true"
	if v := settings[SettingKeyBalanceLowNotifyThreshold]; v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			threshold = f
		}
	}
	rechargeURL = settings[SettingKeyBalanceLowNotifyRechargeURL]
	return
}

// isAccountQuotaNotifyEnabled checks the global account quota notification toggle.
func (s *BalanceNotifyService) IsAccountQuotaNotifyEnabled(ctx context.Context) bool {
	val, err := s.settingRepo.GetValue(ctx, SettingKeyAccountQuotaNotifyEnabled)
	if err != nil {
		return false
	}
	return val == "true"
}

// getAccountQuotaNotifyEmails reads admin notification emails from settings,
// filtering out disabled and unverified entries.
func (s *BalanceNotifyService) GetAccountQuotaNotifyEmails(ctx context.Context) []string {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyAccountQuotaNotifyEmails)
	if err != nil || strings.TrimSpace(raw) == "" || raw == "[]" {
		return nil
	}

	entries := ParseNotifyEmails(raw)
	if len(entries) == 0 {
		return nil
	}

	return FilterVerifiedEmails(entries)
}

// getSiteName reads site name from settings with fallback.
func (s *BalanceNotifyService) GetSiteName(ctx context.Context) string {
	name, err := s.settingRepo.GetValue(ctx, SettingKeySiteName)
	if err != nil || name == "" {
		return defaultSiteName
	}
	return name
}

// filterVerifiedEmails returns deduplicated, non-disabled, verified emails.
func FilterVerifiedEmails(entries []NotifyEmailSummary) []string {
	var recipients []string
	seen := make(map[string]bool)
	for _, entry := range entries {
		if entry.Disabled || !entry.Verified {
			continue
		}
		email := strings.TrimSpace(entry.Email)
		if email == "" {
			continue
		}
		lower := strings.ToLower(email)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		recipients = append(recipients, email)
	}
	return recipients
}

// collectBalanceNotifyRecipients returns verified, non-disabled email recipients.
// Only emails with verified=true and disabled=false are included.
func (s *BalanceNotifyService) CollectBalanceNotifyRecipients(user *UserSummary) []string {
	return FilterVerifiedEmails(user.BalanceNotifyExtraEmails)
}

func quotaDimsFromCommitted(account *QuotaNotifyAccount, state *AccountQuotaState) []QuotaNotifyDimension {
	dims := append([]QuotaNotifyDimension(nil), account.Dimensions...)
	for i := range dims {
		switch dims[i].Name {
		case "daily":
			dims[i].CurrentUsed, dims[i].Limit = state.DailyUsed, state.DailyLimit
		case "weekly":
			dims[i].CurrentUsed, dims[i].Limit = state.WeeklyUsed, state.WeeklyLimit
		case "total":
			dims[i].CurrentUsed, dims[i].Limit = state.TotalUsed, state.TotalLimit
		}
	}
	return dims
}

const SettingKeyAccountQuotaNotifyEmails = "account_quota_notify_emails"
const SettingKeyAccountQuotaNotifyEnabled = "account_quota_notify_enabled"
const SettingKeyBalanceLowNotifyEnabled = "balance_low_notify_enabled"
const SettingKeyBalanceLowNotifyRechargeURL = "balance_low_notify_recharge_url"
const SettingKeyBalanceLowNotifyThreshold = "balance_low_notify_threshold"
const SettingKeySiteName = "site_name"
