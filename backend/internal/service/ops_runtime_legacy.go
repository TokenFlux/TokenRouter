package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/ops"
)

// LegacyOpsEmail 把旧通知能力投影为调用端口，邮件规则仍由通知拥有者处理。
func LegacyOpsEmail(s *EmailService) *ops.EmailDelivery {
	if s == nil {
		return nil
	}
	out := &ops.EmailDelivery{SendEmail: s.SendEmail, RecipientName: emailRecipientName, ShouldFallback: shouldFallbackNotificationEmail}
	if n := s.notificationEmailService; n != nil {
		out.TemplatesEnabled = true
		out.ResolveRecipientLocale = n.ResolveRecipientLocale
		out.SendTemplate = func(ctx context.Context, v ops.Notification) error {
			return n.Send(ctx, NotificationEmailSendInput{Event: v.Event, Locale: v.Locale, RecipientEmail: v.RecipientEmail, RecipientName: v.RecipientName, SourceType: v.SourceType, SourceID: v.SourceID, ReminderKey: v.ReminderKey, Variables: v.Variables, RawHTMLVariables: v.RawHTMLVariables})
		}
	}
	return out
}

type legacyOpsAdmin struct{ s *UserService }

func (a legacyOpsAdmin) GetFirstAdmin(ctx context.Context) (*ops.UserObservation, error) {
	u, e := a.s.GetFirstAdmin(ctx)
	if u == nil {
		return nil, e
	}
	return &ops.UserObservation{ID: u.ID, Email: u.Email, Username: u.Username}, e
}
