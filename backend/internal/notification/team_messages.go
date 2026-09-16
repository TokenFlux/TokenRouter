// 团队模板只接收已确认的收件人与事件参数。
package notification

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"time"
)

func (s *Mailer) RecipientName(email string) string { return EmailRecipientName(email) }
func (s *Mailer) SendTeamInvitation(ctx context.Context, email, recipientName string, recipientUserID int64, teamName, link string, expiresAt time.Time) error {
	// 团队邀请优先走统一模板系统，允许管理员在邮件设置中自定义主题和正文。
	if s.notificationEmailService != nil {
		err := s.notificationEmailService.Send(ctx, SendRequest{
			Event:          NotificationEmailEventTeamInvitation,
			RecipientEmail: email,
			RecipientName:  recipientName,
			UserID:         recipientUserID,
			Variables: map[string]string{
				"team_name":      teamName,
				"invitation_url": link,
				"expires_at":     expiresAt.Format(time.RFC3339),
			},
		})
		if err == nil {
			return nil
		}
		if !ShouldFallbackNotificationEmail(err) {
			return err
		}
		slog.Warn("failed to send templated team invitation email, falling back to legacy template", "recipient_hash", NotificationEmailHash(email), "error", err)
	}

	body := fmt.Sprintf("<p>你被邀请加入团队 <strong>%s</strong>。</p><p><a href=\"%s\">查看并处理邀请</a></p><p>邀请有效期至 %s。</p>", html.EscapeString(teamName), html.EscapeString(link), expiresAt.Format(time.RFC3339))
	return s.SendEmail(ctx, email, "团队邀请", body)
}
func (s Mailer) SendOwnershipTransfer(ctx context.Context, email, teamName, link string) error {
	body := fmt.Sprintf("<p>你收到团队 <strong>%s</strong> 的所有权转让请求。</p><p><a href=\"%s\">确认或拒绝转让</a></p>", html.EscapeString(teamName), html.EscapeString(link))
	return s.SendEmail(ctx, email, "团队所有权转让", body)
}
