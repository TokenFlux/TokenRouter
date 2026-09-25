package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	promotionhttp "github.com/TokenFlux/TokenRouter/internal/promotion/httpapi"
)

// identityVerificationDelivery 只投影已生成的验证邮件事件。
type identityVerificationDelivery struct {
	mail       *notification.Mailer
	challenges *identity.EmailChallenges
}

func (d identityVerificationDelivery) SendNotifyVerification(ctx context.Context, n identity.NotifyVerificationNotice) error {
	return d.mail.SendNotifyVerification(ctx, n.UserID, n.Email, n.Code, n.Locale, n.SiteName)
}
func providePanelUserHTTP(users *identity.UserService, g *identityAuthGraph, mail *notification.Mailer, cache identity.EmailCache, challenges *identity.EmailChallenges) *identityhttp.UserHandler {
	return identityhttp.NewUserHandler(users, g.Core, identityVerificationDelivery{mail: mail, challenges: challenges}, cache)
}
func providePromotionUserHTTP(s *promotion.AffiliateService) *promotionhttp.UserHandler {
	return promotionhttp.NewUserHandler(s)
}

// GenerateVerifyCode 复用身份模块的验证码生成，保持原投递顺序。
func (d identityVerificationDelivery) GenerateVerifyCode() (string, error) {
	return d.challenges.GenerateVerifyCode()
}
