// Mailer 组合运行时 SMTP 参数与技术发送，不拥有身份凭据。
package notification

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/notification/contract"
)

type Mailer struct {
	settingRepo              SettingRepository
	transport                SMTPTransport
	notificationEmailService *NotificationEmailService
}

func NewMailer(repo SettingRepository, transport SMTPTransport) *Mailer {
	return &Mailer{settingRepo: repo, transport: transport}
}
func (s *Mailer) SetNotificationEmailService(n *NotificationEmailService) {
	s.notificationEmailService = n
}
func (s *Mailer) NotificationService() *NotificationEmailService { return s.notificationEmailService }
func (s *Mailer) SendEmail(ctx context.Context, to, subject, body string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cfg, err := s.GetSMTPConfig(ctx)
	if err != nil {
		return err
	}
	return s.transport.Send(ctx, cfg, to, subject, body)
}
func (s *Mailer) SendEmailWithConfigContext(ctx context.Context, cfg *SMTPConfig, to, subject, body string) error {
	return s.transport.Send(ctx, cfg, to, subject, body)
}
func (s *Mailer) SendEmailWithConfig(cfg *SMTPConfig, to, subject, body string) error {
	return s.SendEmailWithConfigContext(context.Background(), cfg, to, subject, body)
}
func (s *Mailer) TestSMTPConnectionContext(ctx context.Context, cfg *SMTPConfig) error {
	return s.transport.Test(ctx, cfg)
}
func (s *Mailer) TestSMTPConnectionWithConfig(cfg *SMTPConfig) error {
	return s.TestSMTPConnectionContext(context.Background(), cfg)
}

var ErrEmailNotConfigured = contract.ErrEmailNotConfigured

func EmailRecipientName(email string) string {
	trimmed := strings.TrimSpace(email)
	if trimmed == "" {
		return ""
	}
	if at := strings.Index(trimmed, "@"); at > 0 {
		return trimmed[:at]
	}
	return trimmed
}

// GetSMTPConfig 从数据库获取SMTP配置
func (s *Mailer) GetSMTPConfig(ctx context.Context) (*SMTPConfig, error) {
	keys := []string{
		SettingKeySMTPHost,
		SettingKeySMTPPort,
		SettingKeySMTPUsername,
		SettingKeySMTPPassword,
		SettingKeySMTPFrom,
		SettingKeySMTPFromName,
		SettingKeySMTPUseTLS,
	}

	settings, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("get smtp settings: %w", err)
	}

	host := strings.TrimSpace(settings[SettingKeySMTPHost])
	if host == "" {
		return nil, ErrEmailNotConfigured
	}

	port := 587 // 默认端口
	if portStr := settings[SettingKeySMTPPort]; portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			port = p
		}
	}

	useTLS := settings[SettingKeySMTPUseTLS] == "true"

	return &SMTPConfig{
		Host:     host,
		Port:     port,
		Username: strings.TrimSpace(settings[SettingKeySMTPUsername]),
		Password: strings.TrimSpace(settings[SettingKeySMTPPassword]),
		From:     strings.TrimSpace(settings[SettingKeySMTPFrom]),
		FromName: strings.TrimSpace(settings[SettingKeySMTPFromName]),
		UseTLS:   useTLS,
	}, nil
}

const SettingKeySMTPFrom = "smtp_from"
const SettingKeySMTPFromName = "smtp_from_name"
const SettingKeySMTPHost = "smtp_host"
const SettingKeySMTPPassword = "smtp_password"
const SettingKeySMTPPort = "smtp_port"
const SettingKeySMTPUseTLS = "smtp_use_tls"
const SettingKeySMTPUsername = "smtp_username"

// SanitizeEmailHeader 防止模板参数注入额外邮件头。
func SanitizeEmailHeader(s string) string { return strings.NewReplacer("\r", "", "\n", "").Replace(s) }

// CheckTransport 只检查运行参数，不建立连接或发送邮件。
func (s *NotificationEmailService) CheckTransport(ctx context.Context) error {
	if s == nil || s.emailService == nil {
		return ErrEmailNotConfigured
	}
	source, ok := s.emailService.(interface {
		GetSMTPConfig(context.Context) (*SMTPConfig, error)
	})
	if !ok {
		return ErrEmailNotConfigured
	}
	_, err := source.GetSMTPConfig(ctx)
	return err
}
