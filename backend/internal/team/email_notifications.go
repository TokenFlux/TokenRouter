// 团队通知先取得用户展示投影，再投递已确定的事件。
package team

import (
	"context"
	"strings"
	"time"
)

type InvitationRecipientReader interface {
	GetByEmail(context.Context, string) (*UserSnapshot, error)
}
type TeamMailSender interface {
	RecipientName(string) string
	SendTeamInvitation(context.Context, string, string, int64, string, string, time.Time) error
	SendOwnershipTransfer(context.Context, string, string, string) error
}
type EmailNotifications struct {
	sender TeamMailSender
	users  InvitationRecipientReader
}

func NewEmailNotifications(sender TeamMailSender, users InvitationRecipientReader) *EmailNotifications {
	return &EmailNotifications{sender: sender, users: users}
}
func (s EmailNotifications) SendInvitation(ctx context.Context, email, teamName, link string, expiresAt time.Time) error {
	if s.sender == nil {
		return nil
	}
	if strings.TrimSpace(link) == "" {
		return ErrTeamFrontendURLUnavailable
	}
	recipientName := s.sender.RecipientName(email)
	var recipientUserID int64
	if s.users != nil {
		if user, err := s.users.GetByEmail(ctx, email); err == nil && user != nil {
			recipientUserID = user.ID
			if strings.TrimSpace(user.Username) != "" {
				recipientName = strings.TrimSpace(user.Username)
			}
		}
	}

	return s.sender.SendTeamInvitation(ctx, email, recipientName, recipientUserID, teamName, link, expiresAt)
}
func (s EmailNotifications) SendOwnershipTransfer(ctx context.Context, email, name, link string) error {
	return s.sender.SendOwnershipTransfer(ctx, email, name, link)
}
