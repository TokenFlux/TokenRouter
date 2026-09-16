package service

import (
	"context"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	nativesmtp "github.com/TokenFlux/TokenRouter/internal/notification/smtp"
)

var ErrEmailNotConfigured = identity.ErrEmailNotConfigured
var ErrInvalidVerifyCode = identity.ErrInvalidVerifyCode
var ErrVerifyCodeTooFrequent = identity.ErrVerifyCodeTooFrequent
var ErrVerifyCodeMaxAttempts = identity.ErrVerifyCodeMaxAttempts
var ErrInvalidResetToken = identity.ErrInvalidResetToken

type EmailCache = identity.EmailCache

type VerificationCodeData = identity.VerificationCodeData

type PasswordResetTokenData = identity.PasswordResetTokenData

// EmailService 邮件服务
type EmailService struct {
	*notification.Mailer
	*identity.EmailChallenges
	notificationEmailService *NotificationEmailService
}

// NewEmailService 创建邮件服务实例
func NewEmailService(settingRepo SettingRepository, cache EmailCache) *EmailService {
	mailer := notification.NewMailer(settingRepo, nativesmtp.New())
	return WrapEmailService(mailer, identity.NewEmailChallenges(cache, mailer))
}

// WrapEmailService 生产装配传入同一通知和身份实例。
func WrapEmailService(mailer *notification.Mailer, challenges *identity.EmailChallenges) *EmailService {
	return &EmailService{Mailer: mailer, EmailChallenges: challenges}
}

func (s *EmailService) SetNotificationEmailService(notificationEmailService *NotificationEmailService) {
	s.notificationEmailService = notificationEmailService
	s.Mailer.SetNotificationEmailService(notificationEmailService)
}

func emailRecipientName(email string) string { return notification.EmailRecipientName(email) }

// SendEmail 发送邮件（使用数据库中保存的配置）
func (s *EmailService) SendEmail(ctx context.Context, to, subject, body string) error {
	return s.Mailer.SendEmail(ctx, to, subject, body)
}

type SMTPConfig = notification.SMTPConfig
