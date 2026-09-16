// RiskDelivery 只呈现并投递已确定的风险事件。
package notification

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/notification/contract"
)

type RiskDelivery struct {
	emailService *Mailer
	settingRepo  SettingRepository
}

func NewRiskDelivery(mail *Mailer) *RiskDelivery {
	return &RiskDelivery{emailService: mail, settingRepo: mail.settingRepo}
}
func (s *RiskDelivery) SendViolationEmail(ctx context.Context, cfg *contract.RiskPolicy, log *contract.RiskLog) error {
	siteName := s.siteName(ctx)
	if s.emailService.notificationEmailService != nil {
		if err := s.emailService.notificationEmailService.Send(ctx, NotificationEmailSendInput{
			Event:          NotificationEmailEventContentModerationViolation,
			RecipientEmail: log.UserEmail,
			RecipientName:  EmailRecipientName(log.UserEmail),
			UserID:         contentModerationEmailUserID(log),
			SourceType:     "content_moderation",
			SourceID:       contentModerationEmailSourceID(log),
			Variables:      contentModerationEmailVariables(log, cfg),
		}); err == nil {
			return nil
		} else {
			if !ShouldFallbackNotificationEmail(err) {
				return err
			}
			slog.Warn("template content moderation violation email failed; falling back to built-in body", "log_id", log.ID, "recipient_hash", NotificationEmailHash(log.UserEmail), "err", err.Error())
		}
	}
	subject := fmt.Sprintf("[%s] 账户风控提醒 / Risk Control Notice", SanitizeEmailHeader(siteName))
	body := BuildContentModerationViolationEmailBody(siteName, log, cfg)
	return s.emailService.SendEmail(ctx, log.UserEmail, subject, body)
}
func (s *RiskDelivery) SendAccountDisabledEmail(ctx context.Context, cfg *contract.RiskPolicy, log *contract.RiskLog) error {
	siteName := s.siteName(ctx)
	if s.emailService.notificationEmailService != nil {
		if err := s.emailService.notificationEmailService.Send(ctx, NotificationEmailSendInput{
			Event:          NotificationEmailEventContentModerationDisabled,
			RecipientEmail: log.UserEmail,
			RecipientName:  EmailRecipientName(log.UserEmail),
			UserID:         contentModerationEmailUserID(log),
			SourceType:     "content_moderation",
			SourceID:       contentModerationEmailSourceID(log),
			Variables:      contentModerationEmailVariables(log, cfg),
		}); err == nil {
			return nil
		} else {
			if !ShouldFallbackNotificationEmail(err) {
				return err
			}
			slog.Warn("template content moderation disabled email failed; falling back to built-in body", "log_id", log.ID, "recipient_hash", NotificationEmailHash(log.UserEmail), "err", err.Error())
		}
	}
	subject := fmt.Sprintf("[%s] 账户已被禁用 / Account Disabled", SanitizeEmailHeader(siteName))
	body := BuildContentModerationAccountDisabledEmailBody(siteName, log, cfg)
	return s.emailService.SendEmail(ctx, log.UserEmail, subject, body)
}
func (s *RiskDelivery) SendCyberAccountDisabledEmail(ctx context.Context, cfg *contract.RiskPolicy, warning *contract.RiskWarning) error {
	siteName := s.siteName(ctx)
	if s.emailService.notificationEmailService != nil {
		if err := s.emailService.notificationEmailService.Send(ctx, NotificationEmailSendInput{
			Event:          NotificationEmailEventContentModerationDisabled,
			RecipientEmail: warning.UserEmail,
			RecipientName:  EmailRecipientName(warning.UserEmail),
			UserID:         contentModerationCyberEmailUserID(warning),
			SourceType:     "content_moderation_cyber",
			SourceID:       contentModerationCyberEmailSourceID(warning),
			Variables:      contentModerationCyberEmailVariables(warning, cfg),
		}); err == nil {
			return nil
		} else {
			if !ShouldFallbackNotificationEmail(err) {
				return err
			}
			slog.Warn("template cyber content moderation disabled email failed; falling back to built-in body", "warning_id", warning.ID, "recipient_hash", NotificationEmailHash(warning.UserEmail), "err", err.Error())
		}
	}
	subject := fmt.Sprintf("[%s] 账户已被禁用 / Account Disabled", SanitizeEmailHeader(siteName))
	body := BuildContentModerationCyberAccountDisabledEmailBody(siteName, warning, cfg)
	return s.emailService.SendEmail(ctx, warning.UserEmail, subject, body)
}
func contentModerationEmailUserID(log *contract.RiskLog) int64 {
	if log == nil || log.UserID == nil {
		return 0
	}
	return *log.UserID
}
func contentModerationEmailSourceID(log *contract.RiskLog) string {
	if log == nil || log.ID <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", log.ID)
}
func contentModerationEmailVariables(log *contract.RiskLog, cfg *contract.RiskPolicy) map[string]string {
	variables := map[string]string{
		"triggered_at":        time.Now().UTC().Format(time.RFC3339),
		"group_name":          "-",
		"moderation_category": "-",
		"moderation_score":    "0.000",
		"violation_count":     "0",
		"ban_threshold":       "0",
	}
	if log != nil {
		if !log.CreatedAt.IsZero() {
			variables["triggered_at"] = log.CreatedAt.UTC().Format(time.RFC3339)
		}
		if strings.TrimSpace(log.GroupName) != "" {
			variables["group_name"] = strings.TrimSpace(log.GroupName)
		}
		if strings.TrimSpace(log.HighestCategory) != "" {
			variables["moderation_category"] = strings.TrimSpace(log.HighestCategory)
		}
		variables["moderation_score"] = fmt.Sprintf("%.3f", log.HighestScore)
		variables["violation_count"] = fmt.Sprintf("%d", log.ViolationCount)
	}
	if cfg != nil {
		variables["ban_threshold"] = fmt.Sprintf("%d", cfg.BanThreshold)
	}
	return variables
}
func contentModerationCyberEmailUserID(warning *contract.RiskWarning) int64 {
	if warning == nil || warning.UserID == nil {
		return 0
	}
	return *warning.UserID
}
func contentModerationCyberEmailSourceID(warning *contract.RiskWarning) string {
	if warning == nil || warning.ID <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", warning.ID)
}
func contentModerationCyberEmailVariables(warning *contract.RiskWarning, cfg *contract.RiskPolicy) map[string]string {
	variables := map[string]string{
		"triggered_at":        time.Now().UTC().Format(time.RFC3339),
		"group_name":          "-",
		"moderation_category": "cyber",
		"moderation_score":    "0.000",
		"violation_count":     "0",
		"ban_threshold":       "0",
	}
	if warning != nil {
		if !warning.CreatedAt.IsZero() {
			variables["triggered_at"] = warning.CreatedAt.UTC().Format(time.RFC3339)
		}
		if strings.TrimSpace(warning.GroupName) != "" {
			variables["group_name"] = strings.TrimSpace(warning.GroupName)
		}
	}
	if cfg != nil {
		variables["ban_threshold"] = fmt.Sprintf("%d", cfg.CyberBanThreshold)
	}
	return variables
}
func (s *RiskDelivery) siteName(ctx context.Context) string {
	if s == nil || s.settingRepo == nil {
		return "Sub2API"
	}
	name, err := s.settingRepo.GetValue(ctx, SettingKeySiteName)
	if err != nil || strings.TrimSpace(name) == "" {
		return "Sub2API"
	}
	return strings.TrimSpace(name)
}
func BuildContentModerationViolationEmailBody(siteName string, log *contract.RiskLog, cfg *contract.RiskPolicy) string {
	if log == nil {
		return ""
	}
	userName := strings.TrimSpace(log.UserEmail)
	if userName == "" && log.UserID != nil {
		userName = fmt.Sprintf("UID %d", *log.UserID)
	}
	threshold := cfg.BanThreshold
	if threshold <= 0 {
		threshold = defaultContentModerationBanThreshold
	}
	statusBlock := ""
	if log.AutoBanned {
		statusBlock = `<div style="margin-top:24px;padding:18px 20px;border-radius:10px;background:#ff3b30;color:#fff;font-size:18px;font-weight:700;text-align:center;line-height:1.6;">账户当前处于封禁状态，所有 API 请求将被拒绝</div>`
	}
	return fmt.Sprintf(`<!doctype html>
<html>
<body style="margin:0;padding:0;background:#f5f6fb;color:#222;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Arial,sans-serif;">
  <div style="max-width:680px;margin:0 auto;padding:32px 20px;">
    <div style="height:8px;background:#ef4444;border-radius:14px 14px 0 0;"></div>
    <div style="background:#fff;border-radius:0 0 14px 14px;padding:40px 48px;box-shadow:0 8px 28px rgba(15,23,42,.08);">
      <div style="letter-spacing:4px;color:#999;font-size:14px;text-transform:uppercase;">Risk Control / 风控提醒</div>
      <h1 style="margin:20px 0 28px;font-size:30px;line-height:1.25;">账户触发内容审计规则</h1>
      <p style="font-size:17px;line-height:1.9;margin:0 0 24px;">尊敬的用户 <strong>%s</strong>，您的 API 请求在内容审计中触发平台风控策略。详情如下。</p>
      <div style="background:#fff1f2;border:1px solid #fecdd3;border-radius:12px;padding:22px 28px;margin:28px 0;">
        <h2 style="margin:0 0 18px;color:#b91c1c;font-size:18px;">触发详情</h2>
        <table style="width:100%%;border-collapse:collapse;font-size:16px;">
          <tr><td style="padding:12px 0;color:#888;border-bottom:1px solid #fee2e2;">触发时间</td><td style="padding:12px 0;border-bottom:1px solid #fee2e2;">%s</td></tr>
          <tr><td style="padding:12px 0;color:#888;border-bottom:1px solid #fee2e2;">触发来源</td><td style="padding:12px 0;border-bottom:1px solid #fee2e2;">内容审核</td></tr>
          <tr><td style="padding:12px 0;color:#888;border-bottom:1px solid #fee2e2;">所属分组</td><td style="padding:12px 0;border-bottom:1px solid #fee2e2;">%s</td></tr>
          <tr><td style="padding:12px 0;color:#888;border-bottom:1px solid #fee2e2;">命中类别</td><td style="padding:12px 0;border-bottom:1px solid #fee2e2;">%s / %.3f</td></tr>
          <tr><td style="padding:12px 0;color:#888;">累计触发次数</td><td style="padding:12px 0;color:#dc2626;font-weight:700;">%d 次（阈值 %d）</td></tr>
        </table>
      </div>
      %s
      <p style="font-size:14px;line-height:1.8;color:#777;margin-top:28px;">此邮件由 %s 自动发送，请勿回复。</p>
    </div>
  </div>
</body>
</html>`,
		html.EscapeString(userName),
		html.EscapeString(time.Now().Format("2006-01-02 15:04:05")),
		html.EscapeString(defaultContentModerationString(log.GroupName, "-")),
		html.EscapeString(defaultContentModerationString(log.HighestCategory, "-")),
		log.HighestScore,
		log.ViolationCount,
		threshold,
		statusBlock,
		html.EscapeString(siteName),
	)
}
func BuildContentModerationAccountDisabledEmailBody(siteName string, log *contract.RiskLog, cfg *contract.RiskPolicy) string {
	if log == nil {
		return ""
	}
	userName := strings.TrimSpace(log.UserEmail)
	if userName == "" && log.UserID != nil {
		userName = fmt.Sprintf("UID %d", *log.UserID)
	}
	threshold := cfg.BanThreshold
	if threshold <= 0 {
		threshold = defaultContentModerationBanThreshold
	}
	return fmt.Sprintf(`<!doctype html>
<html>
<body style="margin:0;padding:0;background:#f5f6fb;color:#222;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Arial,sans-serif;">
  <div style="max-width:680px;margin:0 auto;padding:32px 20px;">
    <div style="height:8px;background:#ef4444;border-radius:14px 14px 0 0;"></div>
    <div style="background:#fff;border-radius:0 0 14px 14px;padding:40px 48px;box-shadow:0 8px 28px rgba(15,23,42,.08);">
      <div style="letter-spacing:4px;color:#999;font-size:14px;text-transform:uppercase;">Risk Control / 账户封禁</div>
      <h1 style="margin:20px 0 28px;font-size:30px;line-height:1.25;">账户已被自动禁用</h1>
      <p style="font-size:17px;line-height:1.9;margin:0 0 24px;">尊敬的用户 <strong>%s</strong>，您的账户在计数周期内多次触发平台风控策略，系统已自动禁用该账户。详情如下。</p>
      <div style="background:#fff1f2;border:1px solid #fecdd3;border-radius:12px;padding:22px 28px;margin:28px 0;">
        <h2 style="margin:0 0 18px;color:#b91c1c;font-size:18px;">封禁详情</h2>
        <table style="width:100%%;border-collapse:collapse;font-size:16px;">
          <tr><td style="padding:12px 0;color:#888;border-bottom:1px solid #fee2e2;">封禁时间</td><td style="padding:12px 0;border-bottom:1px solid #fee2e2;">%s</td></tr>
          <tr><td style="padding:12px 0;color:#888;border-bottom:1px solid #fee2e2;">触发来源</td><td style="padding:12px 0;border-bottom:1px solid #fee2e2;">内容审核</td></tr>
          <tr><td style="padding:12px 0;color:#888;border-bottom:1px solid #fee2e2;">所属分组</td><td style="padding:12px 0;border-bottom:1px solid #fee2e2;">%s</td></tr>
          <tr><td style="padding:12px 0;color:#888;border-bottom:1px solid #fee2e2;">命中类别</td><td style="padding:12px 0;border-bottom:1px solid #fee2e2;">%s / %.3f</td></tr>
          <tr><td style="padding:12px 0;color:#888;">累计触发次数</td><td style="padding:12px 0;color:#dc2626;font-weight:700;">%d 次（阈值 %d）</td></tr>
        </table>
      </div>
      <div style="margin-top:24px;padding:18px 20px;border-radius:10px;background:#ff3b30;color:#fff;font-size:18px;font-weight:700;text-align:center;line-height:1.6;">账户当前处于封禁状态，所有 API 请求将被拒绝</div>
      <p style="font-size:15px;line-height:1.8;color:#666;margin-top:24px;">如需申诉或恢复账号，请联系平台管理员处理。</p>
      <p style="font-size:14px;line-height:1.8;color:#777;margin-top:28px;">此邮件由 %s 自动发送，请勿回复。</p>
    </div>
  </div>
</body>
</html>`,
		html.EscapeString(userName),
		html.EscapeString(time.Now().Format("2006-01-02 15:04:05")),
		html.EscapeString(defaultContentModerationString(log.GroupName, "-")),
		html.EscapeString(defaultContentModerationString(log.HighestCategory, "-")),
		log.HighestScore,
		log.ViolationCount,
		threshold,
		html.EscapeString(siteName),
	)
}
func BuildContentModerationCyberAccountDisabledEmailBody(siteName string, warning *contract.RiskWarning, cfg *contract.RiskPolicy) string {
	if warning == nil {
		return ""
	}
	userName := strings.TrimSpace(warning.UserEmail)
	if userName == "" && warning.UserID != nil {
		userName = fmt.Sprintf("UID %d", *warning.UserID)
	}
	threshold := cfg.CyberBanThreshold
	if threshold <= 0 {
		threshold = defaultContentModerationCyberBanThreshold
	}
	return fmt.Sprintf(`<!doctype html>
<html>
<body style="margin:0;padding:0;background:#f5f6fb;color:#222;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Arial,sans-serif;">
  <div style="max-width:680px;margin:0 auto;padding:32px 20px;">
    <div style="height:8px;background:#ef4444;border-radius:14px 14px 0 0;"></div>
    <div style="background:#fff;border-radius:0 0 14px 14px;padding:40px 48px;box-shadow:0 8px 28px rgba(15,23,42,.08);">
      <div style="letter-spacing:4px;color:#999;font-size:14px;text-transform:uppercase;">Risk Control / 账户封禁</div>
      <h1 style="margin:20px 0 28px;font-size:30px;line-height:1.25;">账户已被自动禁用</h1>
      <p style="font-size:17px;line-height:1.9;margin:0 0 24px;">尊敬的用户 <strong>%s</strong>，您的账户在计数周期内多次触发 OpenAI cyber 风控拒绝，系统已自动禁用该账户。详情如下。</p>
      <div style="background:#fff1f2;border:1px solid #fecdd3;border-radius:12px;padding:22px 28px;margin:28px 0;">
        <h2 style="margin:0 0 18px;color:#b91c1c;font-size:18px;">封禁详情</h2>
        <table style="width:100%%;border-collapse:collapse;font-size:16px;">
          <tr><td style="padding:12px 0;color:#888;border-bottom:1px solid #fee2e2;">封禁时间</td><td style="padding:12px 0;border-bottom:1px solid #fee2e2;">%s</td></tr>
          <tr><td style="padding:12px 0;color:#888;border-bottom:1px solid #fee2e2;">触发来源</td><td style="padding:12px 0;border-bottom:1px solid #fee2e2;">OpenAI cyber 风控</td></tr>
          <tr><td style="padding:12px 0;color:#888;border-bottom:1px solid #fee2e2;">所属分组</td><td style="padding:12px 0;border-bottom:1px solid #fee2e2;">%s</td></tr>
          <tr><td style="padding:12px 0;color:#888;border-bottom:1px solid #fee2e2;">上游账号</td><td style="padding:12px 0;border-bottom:1px solid #fee2e2;">%s</td></tr>
          <tr><td style="padding:12px 0;color:#888;">累计触发次数</td><td style="padding:12px 0;color:#dc2626;font-weight:700;">%d 次（阈值 %d）</td></tr>
        </table>
      </div>
      <div style="margin-top:24px;padding:18px 20px;border-radius:10px;background:#ff3b30;color:#fff;font-size:18px;font-weight:700;text-align:center;line-height:1.6;">账户当前处于封禁状态，所有 API 请求将被拒绝</div>
      <p style="font-size:15px;line-height:1.8;color:#666;margin-top:24px;">如需申诉或恢复账号，请联系平台管理员处理。</p>
      <p style="font-size:14px;line-height:1.8;color:#777;margin-top:28px;">此邮件由 %s 自动发送，请勿回复。</p>
    </div>
  </div>
</body>
</html>`,
		html.EscapeString(userName),
		html.EscapeString(time.Now().Format("2006-01-02 15:04:05")),
		html.EscapeString(defaultContentModerationString(warning.GroupName, "-")),
		html.EscapeString(defaultContentModerationString(warning.AccountName, "-")),
		warning.ViolationCount,
		threshold,
		html.EscapeString(siteName),
	)
}
func defaultContentModerationString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

const defaultContentModerationBanThreshold = 10
const defaultContentModerationCyberBanThreshold = 10
