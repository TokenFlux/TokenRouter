// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package handler

import (
	context "context"

	keydto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	identitydto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	provider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	gin "github.com/gin-gonic/gin"
)

// AuthHandler handles authentication-related requests
type AuthHandler struct {
	cfg                   *config.Config
	authService           *service.AuthService
	userService           *service.UserService
	settingSvc            *service.SettingService
	promoService          *service.PromoService
	redeemService         *service.RedeemService
	totpService           *service.TotpService
	userAttributeService  *service.UserAttributeService
	googleIDTokenVerifier googleIDTokenVerifier

	dingTalkClients provider.DingTalkClients
}

// NewAuthHandler creates a new AuthHandler
func NewAuthHandler(cfg *config.Config, authService *service.AuthService, userService *service.UserService, settingService *service.SettingService, promoService *service.PromoService, redeemService *service.RedeemService, totpService *service.TotpService, userAttributeService *service.UserAttributeService) *AuthHandler {
	return &AuthHandler{
		cfg:                  cfg,
		authService:          authService,
		userService:          userService,
		settingSvc:           settingService,
		promoService:         promoService,
		redeemService:        redeemService,
		totpService:          totpService,
		userAttributeService: userAttributeService,
	}
}

type RegisterRequest = identityhttp.RegisterRequest

type SendVerifyCodeRequest = identityhttp.SendVerifyCodeRequest

type SendVerifyCodeResponse = identityhttp.SendVerifyCodeResponse

type LoginRequest = identityhttp.LoginRequest

// AuthResponse 认证响应格式（匹配前端期望）
type AuthResponse struct {
	AccessToken  string                                             `json:"access_token"`
	RefreshToken string                                             `json:"refresh_token,omitempty"` // 新增：Refresh Token
	ExpiresIn    int                                                `json:"expires_in,omitempty"`    // 新增：Access Token有效期（秒）
	TokenType    string                                             `json:"token_type"`
	User         *identitydto.User[keydto.APIKey[routingdto.Group]] `json:"user"`
}

func (h *AuthHandler) isBackendModeEnabled(ctx context.Context) bool {
	if h == nil || h.settingSvc == nil {
		return false
	}
	settings, err := h.settingSvc.GetPublicSettings(ctx)
	if err == nil && settings != nil {
		return settings.BackendModeEnabled
	}
	return h.settingSvc.IsBackendModeEnabled(ctx)
}

func (h *AuthHandler) Register(c *gin.Context) { h.sessionHTTP().Register(c) }

func (h *AuthHandler) SendVerifyCode(c *gin.Context) { h.sessionHTTP().SendVerifyCode(c) }

func (h *AuthHandler) Login(c *gin.Context) { h.sessionHTTP().Login(c) }

type TotpLoginResponse = identityhttp.TotpLoginResponse

type Login2FARequest = identityhttp.Login2FARequest

func (h *AuthHandler) Login2FA(c *gin.Context) { h.sessionHTTP().Login2FA(c) }

func (h *AuthHandler) GetCurrentUser(c *gin.Context) { h.sessionHTTP().GetCurrentUser(c) }

type ValidatePromoCodeRequest = identityhttp.ValidatePromoCodeRequest

type ValidatePromoCodeResponse = identityhttp.ValidatePromoCodeResponse

func (h *AuthHandler) ValidatePromoCode(c *gin.Context) { h.sessionHTTP().ValidatePromoCode(c) }

type ValidateInvitationCodeRequest = identityhttp.ValidateInvitationCodeRequest

type ValidateInvitationCodeResponse = identityhttp.ValidateInvitationCodeResponse

func (h *AuthHandler) ValidateInvitationCode(c *gin.Context) {
	h.sessionHTTP().ValidateInvitationCode(c)
}

type ForgotPasswordRequest = identityhttp.ForgotPasswordRequest

type ForgotPasswordResponse = identityhttp.ForgotPasswordResponse

func (h *AuthHandler) ForgotPassword(c *gin.Context) { h.sessionHTTP().ForgotPassword(c) }

type ResetPasswordRequest = identityhttp.ResetPasswordRequest

type ResetPasswordResponse = identityhttp.ResetPasswordResponse

func (h *AuthHandler) ResetPassword(c *gin.Context) { h.sessionHTTP().ResetPassword(c) }

// ==================== Token Refresh Endpoints ====================

type RefreshTokenRequest = identityhttp.RefreshTokenRequest

type RefreshTokenResponse = identityhttp.RefreshTokenResponse

func (h *AuthHandler) RefreshToken(c *gin.Context) { h.sessionHTTP().RefreshToken(c) }

type LogoutRequest = identityhttp.LogoutRequest

type LogoutResponse = identityhttp.LogoutResponse

func (h *AuthHandler) Logout(c *gin.Context) { h.sessionHTTP().Logout(c) }

type RevokeAllSessionsResponse = identityhttp.RevokeAllSessionsResponse

func (h *AuthHandler) RevokeAllSessions(c *gin.Context) { h.sessionHTTP().RevokeAllSessions(c) }

// sessionHTTP 在兼容期投影依赖，认证和缓存始终复用原核心实例。
func (h *AuthHandler) sessionHTTP() *identityhttp.SessionHandler {
	if h == nil {
		return identityhttp.NewSessionHandler(nil, nil, nil, nil, nil, nil, identityhttp.SessionHTTPOptions{RunMode: config.RunModeStandard})
	}
	mode := config.RunModeStandard
	if h.cfg != nil {
		mode = h.cfg.RunMode
	}
	options := identityhttp.SessionHTTPOptions{RunMode: mode, BackendMode: h.isBackendModeEnabled, AuditActor: middleware2.SetAuditActor,
		ClearPendingCookies: func(c *gin.Context) {
			secure := isRequestHTTPS(c)
			clearOAuthPendingSessionCookie(c, secure)
			clearOAuthPendingBrowserCookie(c, secure)
		},
		LogoutPending: func(c *gin.Context) { h.consumePendingOAuthSessionOnLogout(c); clearOAuthLogoutCookies(c) },
		PreviewPromotion: func(ctx context.Context, code string) identityhttp.PromotionPreview {
			v := h.promoService.PreviewRegistrationPromotion(ctx, code)
			return identityhttp.PromotionPreview{Valid: v.Valid, BonusAmount: v.BonusAmount, ErrorCode: v.ErrorCode}
		}}

	var settings identityhttp.SessionHTTPSettings
	if h.settingSvc != nil {
		settings = h.settingSvc
	}
	return identityhttp.NewSessionHandler(h.authService.IdentityCore(), identityUserCore(h.userService), settings, h.redeemService, h.totpService, h.pendingFlow(), options)
}
