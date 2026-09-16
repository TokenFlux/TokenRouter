// AlertDelivery 发送已经确定的余额/额度事件，不重新判断资金条件。
package notification

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/notification/contract"
)

type AlertDelivery struct {
	emailService             Sender
	notificationEmailService *NotificationEmailService
}

func NewAlertDelivery(email Sender, n *NotificationEmailService) *AlertDelivery {
	return &AlertDelivery{emailService: email, notificationEmailService: n}
}
func (s *AlertDelivery) SetNotificationEmailService(n *NotificationEmailService) {
	s.notificationEmailService = n
}

const emailSendTimeout = 30 * time.Second
const thresholdTypePercentage = "percentage"

// quotaDimLabels maps dimension names to display labels.
var quotaDimLabels = map[string]string{
	quotaDimDaily:  "日限额 / Daily",
	quotaDimWeekly: "周限额 / Weekly",
	quotaDimTotal:  "总限额 / Total",
}

// sendEmails sends an email to all recipients with shared timeout and error logging.
func (s *AlertDelivery) sendEmails(recipients []string, subject, body string, logAttrs ...any) {
	if len(recipients) == 0 {
		slog.Warn("sendEmails: no recipients", "subject", subject)
		return
	}
	for _, to := range recipients {
		ctx, cancel := context.WithTimeout(context.Background(), emailSendTimeout)
		if err := s.emailService.SendEmail(ctx, to, subject, body); err != nil {
			attrs := append([]any{"to", to, "error", err}, logAttrs...)
			slog.Error("failed to send notification", attrs...)
		} else {
			slog.Info("notification email sent successfully", "to", to, "subject", subject)
		}
		cancel()
	}
}

// sendBalanceLowEmails sends balance low notification to all recipients.
func (s *AlertDelivery) SendBalanceLowEmails(recipients []string, userID int64, userName, userEmail string, balance, threshold float64, siteName, rechargeURL string) {
	displayName := userName
	if displayName == "" {
		displayName = userEmail
	}
	if s.notificationEmailService != nil {
		fallbackRecipients := make([]string, 0, len(recipients))
		for _, to := range recipients {
			ctx, cancel := context.WithTimeout(context.Background(), emailSendTimeout)
			err := s.notificationEmailService.Send(ctx, NotificationEmailSendInput{
				Event:          NotificationEmailEventBalanceLow,
				RecipientEmail: to,
				RecipientName:  displayName,
				UserID:         userID,
				SourceType:     "balance_low",
				SourceID:       strconv.FormatInt(userID, 10),
				ReminderKey:    time.Now().UTC().Format("2006-01-02"),
				Variables: map[string]string{
					"current_balance": fmt.Sprintf("%.2f", balance),
					"threshold":       fmt.Sprintf("%.2f", threshold),
					"recharge_url":    rechargeURL,
				},
			})
			cancel()
			if err != nil {
				if ShouldFallbackNotificationEmail(err) {
					slog.Warn("template balance low notification failed; falling back to built-in body", "to", to, "err", err.Error())
					fallbackRecipients = append(fallbackRecipients, to)
				} else {
					slog.Warn("template balance low notification delivery failed; not sending fallback to avoid duplicates", "to", to, "err", err.Error())
				}
			}
		}
		if len(fallbackRecipients) == 0 {
			return
		}
		recipients = fallbackRecipients
	}
	subject := fmt.Sprintf("[%s] 余额不足提醒 / Balance Low Alert", SanitizeEmailHeader(siteName))
	body := s.BuildBalanceLowEmailBody(html.EscapeString(displayName), balance, threshold, html.EscapeString(siteName), rechargeURL)
	s.sendEmails(recipients, subject, body, "user_email", userEmail, "balance", balance)
}

// sendQuotaAlertEmails sends quota alert notification to admin emails.
func (s *AlertDelivery) SendQuotaAlertEmails(adminEmails []string, accountID int64, accountName, platform string, dim contract.QuotaDimension, used float64, siteName string) {
	dimLabel := quotaDimLabels[dim.Name]
	if dimLabel == "" {
		dimLabel = dim.Name
	}

	// Format the remaining-based threshold for display
	thresholdDisplay := fmt.Sprintf("$%.2f", dim.Threshold)
	if dim.ThresholdType == thresholdTypePercentage {
		thresholdDisplay = fmt.Sprintf("%.0f%%", dim.Threshold)
	}
	remaining := dim.Limit - used
	if remaining < 0 {
		remaining = 0
	}

	if s.notificationEmailService != nil {
		fallbackRecipients := make([]string, 0, len(adminEmails))
		for _, to := range adminEmails {
			ctx, cancel := context.WithTimeout(context.Background(), emailSendTimeout)
			err := s.notificationEmailService.Send(ctx, NotificationEmailSendInput{
				Event:          NotificationEmailEventAccountQuotaAlert,
				RecipientEmail: to,
				RecipientName:  EmailRecipientName(to),
				SourceType:     "account_quota",
				SourceID:       fmt.Sprintf("%d-%s", accountID, dim.Name),
				ReminderKey:    time.Now().UTC().Format("2006-01-02"),
				Variables: map[string]string{
					"account_id":      strconv.FormatInt(accountID, 10),
					"account_name":    accountName,
					"platform":        platform,
					"quota_dimension": dimLabel,
					"quota_used":      fmt.Sprintf("%.2f", used),
					"quota_limit":     fmt.Sprintf("%.2f", dim.Limit),
					"quota_remaining": fmt.Sprintf("%.2f", remaining),
					"quota_threshold": thresholdDisplay,
				},
			})
			cancel()
			if err != nil {
				if ShouldFallbackNotificationEmail(err) {
					slog.Warn("template account quota alert failed; falling back to built-in body", "to", to, "account_id", accountID, "dimension", dim.Name, "err", err.Error())
					fallbackRecipients = append(fallbackRecipients, to)
				} else {
					slog.Warn("template account quota alert delivery failed; not sending fallback to avoid duplicates", "to", to, "account_id", accountID, "dimension", dim.Name, "err", err.Error())
				}
			}
		}
		if len(fallbackRecipients) == 0 {
			return
		}
		adminEmails = fallbackRecipients
	}

	subject := fmt.Sprintf("[%s] 账号限额告警 / Account Quota Alert - %s", SanitizeEmailHeader(siteName), SanitizeEmailHeader(accountName))
	body := s.BuildQuotaAlertEmailBody(accountID, html.EscapeString(accountName), html.EscapeString(platform), html.EscapeString(dimLabel), used, dim.Limit, remaining, thresholdDisplay, html.EscapeString(siteName))
	s.sendEmails(adminEmails, subject, body, "account", accountName, "dimension", dim.Name)
}

// balanceLowEmailTemplate is the HTML template for balance low notifications.
// Format args: siteName, userName, userName, balance, threshold, threshold.
// The recharge button is appended dynamically when rechargeURL is set.
const balanceLowEmailTemplate = `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background-color: #f5f5f5; margin: 0; padding: 20px; }
        .container { max-width: 600px; margin: 0 auto; background-color: #fff; border-radius: 8px; overflow: hidden; box-shadow: 0 2px 8px rgba(0,0,0,0.1); }
        .header { background: linear-gradient(135deg, #f59e0b 0%%, #d97706 100%%); color: white; padding: 30px; text-align: center; }
        .header h1 { margin: 0; font-size: 24px; }
        .content { padding: 40px 30px; text-align: center; }
        .balance { font-size: 36px; font-weight: bold; color: #dc2626; margin: 20px 0; }
        .info { color: #666; font-size: 14px; line-height: 1.6; margin-top: 20px; }
        .recharge-btn { display: inline-block; margin-top: 24px; padding: 12px 32px; background: linear-gradient(135deg, #f59e0b 0%%, #d97706 100%%); color: #fff; text-decoration: none; border-radius: 6px; font-size: 16px; font-weight: bold; }
        .footer { background-color: #f8f9fa; padding: 20px; text-align: center; color: #999; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header"><h1>%s</h1></div>
        <div class="content">
            <p style="font-size: 18px; color: #333;">%s，您的余额不足</p>
            <p style="color: #666;">Dear %s, your balance is running low</p>
            <div class="balance">$%.2f</div>
            <div class="info">
                <p>您的账户余额已低于提醒阈值 <strong>$%.2f</strong>。</p>
                <p>Your account balance has fallen below the alert threshold of <strong>$%.2f</strong>.</p>
                <p>请及时充值以免服务中断。</p>
                <p>Please top up to avoid service interruption.</p>
            </div>
            %s
        </div>
        <div class="footer"><p>此邮件由系统自动发送，请勿回复。</p></div>
    </div>
</body>
</html>`

// quotaAlertEmailTemplate is the HTML template for account quota alert notifications.
// Format args: siteName, accountID, accountName, platform, dimLabel, used, limitStr, remaining, thresholdDisplay.
const quotaAlertEmailTemplate = `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background-color: #f5f5f5; margin: 0; padding: 20px; }
        .container { max-width: 600px; margin: 0 auto; background-color: #fff; border-radius: 8px; overflow: hidden; box-shadow: 0 2px 8px rgba(0,0,0,0.1); }
        .header { background: linear-gradient(135deg, #ef4444 0%%, #dc2626 100%%); color: white; padding: 30px; text-align: center; }
        .header h1 { margin: 0; font-size: 24px; }
        .content { padding: 40px 30px; }
        .metric { display: flex; justify-content: space-between; padding: 12px 0; border-bottom: 1px solid #eee; }
        .metric-label { color: #666; }
        .metric-value { font-weight: bold; color: #333; }
        .info { color: #666; font-size: 14px; line-height: 1.6; margin-top: 20px; text-align: center; }
        .footer { background-color: #f8f9fa; padding: 20px; text-align: center; color: #999; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header"><h1>%s</h1></div>
        <div class="content">
            <p style="font-size: 18px; color: #333; text-align: center;">账号限额告警 / Account Quota Alert</p>
            <div class="metric"><span class="metric-label">账号 ID / Account ID</span><span class="metric-value">#%d</span></div>
            <div class="metric"><span class="metric-label">账号 / Account</span><span class="metric-value">%s</span></div>
            <div class="metric"><span class="metric-label">平台 / Platform</span><span class="metric-value">%s</span></div>
            <div class="metric"><span class="metric-label">维度 / Dimension</span><span class="metric-value">%s</span></div>
            <div class="metric"><span class="metric-label">已使用 / Used</span><span class="metric-value">$%.2f</span></div>
            <div class="metric"><span class="metric-label">限额 / Limit</span><span class="metric-value">%s</span></div>
            <div class="metric"><span class="metric-label">剩余额度 / Remaining</span><span class="metric-value">$%.2f</span></div>
            <div class="metric"><span class="metric-label">提醒阈值 / Alert Threshold</span><span class="metric-value">%s</span></div>
            <div class="info">
                <p>账号剩余额度已低于提醒阈值，请及时关注。</p>
                <p>Account remaining quota has fallen below the alert threshold.</p>
            </div>
        </div>
        <div class="footer"><p>此邮件由系统自动发送，请勿回复。</p></div>
    </div>
</body>
</html>`

// buildBalanceLowEmailBody builds HTML email for balance low notification.
func (s *AlertDelivery) BuildBalanceLowEmailBody(userName string, balance, threshold float64, siteName, rechargeURL string) string {
	rechargeBlock := ""
	if rechargeURL != "" {
		rechargeBlock = fmt.Sprintf(`<a href="%s" class="recharge-btn">立即充值 / Top Up Now</a>`, html.EscapeString(rechargeURL))
	}
	return fmt.Sprintf(balanceLowEmailTemplate, siteName, userName, userName, balance, threshold, threshold, rechargeBlock)
}

// buildQuotaAlertEmailBody builds HTML email for account quota alert.
func (s *AlertDelivery) BuildQuotaAlertEmailBody(accountID int64, accountName, platform, dimLabel string, used, limit, remaining float64, thresholdDisplay, siteName string) string {
	limitStr := fmt.Sprintf("$%.2f", limit)
	if limit <= 0 {
		limitStr = "无限制 / Unlimited"
	}
	return fmt.Sprintf(quotaAlertEmailTemplate, siteName, accountID, accountName, platform, dimLabel, used, limitStr, remaining, thresholdDisplay)
}

const quotaDimDaily = "daily"
const quotaDimWeekly = "weekly"
const quotaDimTotal = "total"
