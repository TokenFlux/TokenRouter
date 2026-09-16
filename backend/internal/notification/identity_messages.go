// 身份邮件只接收已确定的挑战与链接，不读取或保存身份凭据。
package notification

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"time"
)

const verifyCodeTTL = 15 * time.Minute
const passwordResetTokenTTL = 30 * time.Minute

func (s *Mailer) SendVerifyCodeMessage(ctx context.Context, email, siteName, code, locale string) error {
	if s.notificationEmailService != nil {
		err := s.notificationEmailService.Send(ctx, NotificationEmailSendInput{
			Event:          NotificationEmailEventAuthVerifyCode,
			Locale:         locale,
			RecipientEmail: email,
			RecipientName:  EmailRecipientName(email),
			Variables: map[string]string{
				"verification_code":  code,
				"expires_in_minutes": strconv.Itoa(int(verifyCodeTTL / time.Minute)),
			},
		})
		if err == nil {
			return nil
		}
		if !ShouldFallbackNotificationEmail(err) {
			return err
		}
		slog.Warn("failed to send templated verification email, falling back to legacy template", "recipient_hash", NotificationEmailHash(email), "error", err)
	}

	// 构建邮件内容
	subject := fmt.Sprintf("[%s] Email Verification Code", siteName)
	body := s.buildVerifyCodeEmailBody(code, siteName)

	// 发送邮件
	if err := s.SendEmail(ctx, email, subject, body); err != nil {
		return fmt.Errorf("send email: %w", err)
	}

	return nil
}
func (s *Mailer) SendPasswordResetMessage(ctx context.Context, email, siteName, fullResetURL, locale string) error {
	if s.notificationEmailService != nil {
		err := s.notificationEmailService.Send(ctx, NotificationEmailSendInput{
			Event:          NotificationEmailEventAuthPasswordReset,
			Locale:         locale,
			RecipientEmail: email,
			RecipientName:  EmailRecipientName(email),
			Variables: map[string]string{
				"reset_url":          fullResetURL,
				"expires_in_minutes": strconv.Itoa(int(passwordResetTokenTTL / time.Minute)),
			},
		})
		if err == nil {
			return nil
		}
		if !ShouldFallbackNotificationEmail(err) {
			return err
		}
		slog.Warn("failed to send templated password reset email, falling back to legacy template", "recipient_hash", NotificationEmailHash(email), "error", err)
	}

	// Build email content
	subject := fmt.Sprintf("[%s] 密码重置请求", siteName)
	body := s.buildPasswordResetEmailBody(fullResetURL, siteName)

	// Send email
	if err := s.SendEmail(ctx, email, subject, body); err != nil {
		return fmt.Errorf("send email: %w", err)
	}

	return nil
}

// buildVerifyCodeEmailBody 构建验证码邮件HTML内容
func (s *Mailer) buildVerifyCodeEmailBody(code, siteName string) string {
	return fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif; background-color: #f5f5f5; margin: 0; padding: 20px; }
        .container { max-width: 600px; margin: 0 auto; background-color: #ffffff; border-radius: 8px; overflow: hidden; box-shadow: 0 2px 8px rgba(0,0,0,0.1); }
        .header { background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%); color: white; padding: 30px; text-align: center; }
        .header h1 { margin: 0; font-size: 24px; }
        .content { padding: 40px 30px; text-align: center; }
        .code { font-size: 36px; font-weight: bold; letter-spacing: 8px; color: #333; background-color: #f8f9fa; padding: 20px 30px; border-radius: 8px; display: inline-block; margin: 20px 0; font-family: monospace; }
        .info { color: #666; font-size: 14px; line-height: 1.6; margin-top: 20px; }
        .footer { background-color: #f8f9fa; padding: 20px; text-align: center; color: #999; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>%s</h1>
        </div>
        <div class="content">
            <p style="font-size: 18px; color: #333;">Your verification code is:</p>
            <div class="code">%s</div>
            <div class="info">
                <p>This code will expire in <strong>15 minutes</strong>.</p>
                <p>If you did not request this code, please ignore this email.</p>
            </div>
        </div>
        <div class="footer">
            <p>This is an automated message, please do not reply.</p>
        </div>
    </div>
</body>
</html>
`, html.EscapeString(siteName), code)
}

// buildPasswordResetEmailBody builds the HTML content for password reset email
func (s *Mailer) buildPasswordResetEmailBody(resetURL, siteName string) string {
	return fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif; background-color: #f5f5f5; margin: 0; padding: 20px; }
        .container { max-width: 600px; margin: 0 auto; background-color: #ffffff; border-radius: 8px; overflow: hidden; box-shadow: 0 2px 8px rgba(0,0,0,0.1); }
        .header { background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%); color: white; padding: 30px; text-align: center; }
        .header h1 { margin: 0; font-size: 24px; }
        .content { padding: 40px 30px; text-align: center; }
        .button { display: inline-block; background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%); color: white; padding: 14px 32px; text-decoration: none; border-radius: 8px; font-size: 16px; font-weight: 600; margin: 20px 0; }
        .button:hover { opacity: 0.9; }
        .info { color: #666; font-size: 14px; line-height: 1.6; margin-top: 20px; }
        .link-fallback { color: #666; font-size: 12px; word-break: break-all; margin-top: 20px; padding: 15px; background-color: #f8f9fa; border-radius: 4px; }
        .footer { background-color: #f8f9fa; padding: 20px; text-align: center; color: #999; font-size: 12px; }
        .warning { color: #e74c3c; font-weight: 500; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>%s</h1>
        </div>
        <div class="content">
            <p style="font-size: 18px; color: #333;">密码重置请求</p>
            <p style="color: #666;">您已请求重置密码。请点击下方按钮设置新密码：</p>
            <a href="%s" class="button">重置密码</a>
            <div class="info">
                <p>此链接将在 <strong>30 分钟</strong>后失效。</p>
                <p class="warning">如果您没有请求重置密码，请忽略此邮件。您的密码将保持不变。</p>
            </div>
            <div class="link-fallback">
                <p>如果按钮无法点击，请复制以下链接到浏览器中打开：</p>
                <p>%s</p>
            </div>
        </div>
        <div class="footer">
            <p>这是一封自动发送的邮件，请勿回复。</p>
        </div>
    </div>
</body>
</html>
`, html.EscapeString(siteName), html.EscapeString(resetURL), html.EscapeString(resetURL))
}

// VerifyCodeBody 与 PasswordResetBody 供旧正文兼容入口委托。
func VerifyCodeBody(code, site string) string {
	return (*Mailer)(nil).buildVerifyCodeEmailBody(code, site)
}
func PasswordResetBody(url, site string) string {
	return (*Mailer)(nil).buildPasswordResetEmailBody(url, site)
}
