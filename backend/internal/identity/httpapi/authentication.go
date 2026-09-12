// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	gin "github.com/gin-gonic/gin"
)

// AuthenticationHandler 汇总同一组实例，不在每个请求重新创建用例、存储或 provider 客户端。
type AuthenticationHandler struct {
	Session  *SessionHandler
	Pending  *PendingHandler
	Bind     *OAuthBindHandler
	LinuxDo  *LinuxDoHandler
	OIDC     *OIDCHandler
	Email    *EmailOAuthHandler
	Google   *GoogleOneTapHandler
	WeChat   *WeChatHandler
	DingTalk *DingTalkHandler
}

func (h *AuthenticationHandler) BindDingTalkOAuthLogin(c *gin.Context) {
	h.DingTalk.BindDingTalkOAuthLogin(c)
}
func (h *AuthenticationHandler) BindLinuxDoOAuthLogin(c *gin.Context) {
	h.Pending.BindLinuxDoOAuthLogin(c)
}
func (h *AuthenticationHandler) BindOIDCOAuthLogin(c *gin.Context) { h.Pending.BindOIDCOAuthLogin(c) }
func (h *AuthenticationHandler) BindPendingOAuthLogin(c *gin.Context) {
	h.Pending.BindPendingOAuthLogin(c)
}
func (h *AuthenticationHandler) BindWeChatOAuthLogin(c *gin.Context) {
	h.Pending.BindWeChatOAuthLogin(c)
}
func (h *AuthenticationHandler) CompleteDingTalkOAuthRegistration(c *gin.Context) {
	h.DingTalk.CompleteDingTalkOAuthRegistration(c)
}
func (h *AuthenticationHandler) CompleteGitHubOAuthRegistration(c *gin.Context) {
	h.Email.CompleteGitHubOAuthRegistration(c)
}
func (h *AuthenticationHandler) CompleteGoogleOAuthRegistration(c *gin.Context) {
	h.Email.CompleteGoogleOAuthRegistration(c)
}
func (h *AuthenticationHandler) CompleteLinuxDoOAuthRegistration(c *gin.Context) {
	h.LinuxDo.CompleteLinuxDoOAuthRegistration(c)
}
func (h *AuthenticationHandler) CompleteOIDCOAuthRegistration(c *gin.Context) {
	h.OIDC.CompleteOIDCOAuthRegistration(c)
}
func (h *AuthenticationHandler) CompleteWeChatOAuthRegistration(c *gin.Context) {
	h.WeChat.CompleteWeChatOAuthRegistration(c)
}
func (h *AuthenticationHandler) CreateDingTalkOAuthAccount(c *gin.Context) {
	h.DingTalk.CreateDingTalkOAuthAccount(c)
}
func (h *AuthenticationHandler) CreateLinuxDoOAuthAccount(c *gin.Context) {
	h.Pending.CreateLinuxDoOAuthAccount(c)
}
func (h *AuthenticationHandler) CreateOIDCOAuthAccount(c *gin.Context) {
	h.Pending.CreateOIDCOAuthAccount(c)
}
func (h *AuthenticationHandler) CreatePendingOAuthAccount(c *gin.Context) {
	h.Pending.CreatePendingOAuthAccount(c)
}
func (h *AuthenticationHandler) CreateWeChatOAuthAccount(c *gin.Context) {
	h.Pending.CreateWeChatOAuthAccount(c)
}
func (h *AuthenticationHandler) DingTalkOAuthCallback(c *gin.Context) {
	h.DingTalk.DingTalkOAuthCallback(c)
}
func (h *AuthenticationHandler) DingTalkOAuthStart(c *gin.Context) { h.DingTalk.DingTalkOAuthStart(c) }
func (h *AuthenticationHandler) ExchangePendingOAuthCompletion(c *gin.Context) {
	h.Pending.ExchangePendingOAuthCompletion(c)
}
func (h *AuthenticationHandler) ForgotPassword(c *gin.Context)      { h.Session.ForgotPassword(c) }
func (h *AuthenticationHandler) GetCurrentUser(c *gin.Context)      { h.Session.GetCurrentUser(c) }
func (h *AuthenticationHandler) GitHubOAuthCallback(c *gin.Context) { h.Email.GitHubOAuthCallback(c) }
func (h *AuthenticationHandler) GitHubOAuthStart(c *gin.Context)    { h.Email.GitHubOAuthStart(c) }
func (h *AuthenticationHandler) GoogleOAuthCallback(c *gin.Context) { h.Email.GoogleOAuthCallback(c) }
func (h *AuthenticationHandler) GoogleOAuthStart(c *gin.Context)    { h.Email.GoogleOAuthStart(c) }
func (h *AuthenticationHandler) GoogleOneTap(c *gin.Context)        { h.Google.GoogleOneTap(c) }
func (h *AuthenticationHandler) LinuxDoOAuthCallback(c *gin.Context) {
	h.LinuxDo.LinuxDoOAuthCallback(c)
}
func (h *AuthenticationHandler) LinuxDoOAuthStart(c *gin.Context) { h.LinuxDo.LinuxDoOAuthStart(c) }
func (h *AuthenticationHandler) Login(c *gin.Context)             { h.Session.Login(c) }
func (h *AuthenticationHandler) Login2FA(c *gin.Context)          { h.Session.Login2FA(c) }
func (h *AuthenticationHandler) Logout(c *gin.Context)            { h.Session.Logout(c) }
func (h *AuthenticationHandler) OIDCOAuthCallback(c *gin.Context) { h.OIDC.OIDCOAuthCallback(c) }
func (h *AuthenticationHandler) OIDCOAuthStart(c *gin.Context)    { h.OIDC.OIDCOAuthStart(c) }
func (h *AuthenticationHandler) PrepareOAuthBindAccessTokenCookie(c *gin.Context) {
	h.Bind.PrepareOAuthBindAccessTokenCookie(c)
}
func (h *AuthenticationHandler) RefreshToken(c *gin.Context)      { h.Session.RefreshToken(c) }
func (h *AuthenticationHandler) Register(c *gin.Context)          { h.Session.Register(c) }
func (h *AuthenticationHandler) ResetPassword(c *gin.Context)     { h.Session.ResetPassword(c) }
func (h *AuthenticationHandler) RevokeAllSessions(c *gin.Context) { h.Session.RevokeAllSessions(c) }
func (h *AuthenticationHandler) SendPendingOAuthVerifyCode(c *gin.Context) {
	h.Pending.SendPendingOAuthVerifyCode(c)
}
func (h *AuthenticationHandler) SendVerifyCode(c *gin.Context) { h.Session.SendVerifyCode(c) }
func (h *AuthenticationHandler) ValidateInvitationCode(c *gin.Context) {
	h.Session.ValidateInvitationCode(c)
}
func (h *AuthenticationHandler) ValidatePromoCode(c *gin.Context)   { h.Session.ValidatePromoCode(c) }
func (h *AuthenticationHandler) WeChatOAuthCallback(c *gin.Context) { h.WeChat.WeChatOAuthCallback(c) }
func (h *AuthenticationHandler) WeChatOAuthStart(c *gin.Context)    { h.WeChat.WeChatOAuthStart(c) }
