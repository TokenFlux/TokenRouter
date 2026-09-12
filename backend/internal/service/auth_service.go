// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
)

var ErrInvalidCredentials = identity.ErrInvalidCredentials

var ErrUserNotActive = identity.ErrUserNotActive

var ErrEmailExists = identity.ErrEmailExists

var ErrEmailReserved = identity.ErrEmailReserved

var ErrInvalidToken = identity.ErrInvalidToken

var ErrTokenExpired = identity.ErrTokenExpired

var ErrAccessTokenExpired = identity.ErrAccessTokenExpired

var ErrTokenTooLarge = identity.ErrTokenTooLarge

var ErrTokenRevoked = identity.ErrTokenRevoked

var ErrRefreshTokenInvalid = identity.ErrRefreshTokenInvalid

var ErrRefreshTokenExpired = identity.ErrRefreshTokenExpired

var ErrRefreshTokenReused = identity.ErrRefreshTokenReused

var ErrEmailVerifyRequired = identity.ErrEmailVerifyRequired

var ErrEmailSuffixNotAllowed = identity.ErrEmailSuffixNotAllowed

var ErrEmailDomainRegistrationLimit = identity.ErrEmailDomainRegistrationLimit

var ErrEmailChangeDisabled = identity.ErrEmailChangeDisabled

var ErrRegDisabled = identity.ErrRegDisabled

var ErrServiceUnavailable = identity.ErrServiceUnavailable

var ErrInvitationCodeRequired = identity.ErrInvitationCodeRequired

var ErrInvitationCodeInvalid = identity.ErrInvitationCodeInvalid

var ErrOAuthInvitationRequired = identity.ErrOAuthInvitationRequired

var ErrCaptchaProviderConflict = identity.ErrCaptchaProviderConflict

type JWTClaims = identity.JWTClaims

// AuthService 认证服务
type AuthService struct {
	core                  *identity.AuthService
	state                 *identitypostgres.AuthState
	entClient             *dbent.Client
	userRepo              UserRepository
	redeemRepo            RedeemCodeRepository
	refreshTokenCache     RefreshTokenCache
	cfg                   *config.Config
	settingService        *SettingService
	emailService          *EmailService
	turnstileService      *TurnstileService
	tencentCaptchaService *TencentCaptchaService
	aliyunCaptchaService  *AliyunCaptchaService
	emailQueueService     *EmailQueueService
	promoService          *PromoService
	affiliateService      *AffiliateService
	defaultSubAssigner    DefaultSubscriptionAssigner
	authCacheInvalidator  APIKeyAuthCacheInvalidator
	billingCache          BillingCache
	userPlatformQuotaRepo UserPlatformQuotaRepository
}

type CaptchaProof = identity.CaptchaProof

type DefaultSubscriptionAssigner = identity.DefaultSubscriptionAssigner

// NewAuthService 创建认证服务实例
func NewAuthService(
	entClient *dbent.Client,
	userRepo UserRepository,
	redeemRepo RedeemCodeRepository,
	refreshTokenCache RefreshTokenCache,
	cfg *config.Config,
	settingService *SettingService,
	emailService *EmailService,
	turnstileService *TurnstileService,
	emailQueueService *EmailQueueService,
	promoService *PromoService,
	defaultSubAssigner DefaultSubscriptionAssigner,
	affiliateService *AffiliateService,
	userPlatformQuotaRepo UserPlatformQuotaRepository,
) *AuthService {
	return &AuthService{
		entClient:             entClient,
		userRepo:              userRepo,
		redeemRepo:            redeemRepo,
		refreshTokenCache:     refreshTokenCache,
		cfg:                   cfg,
		settingService:        settingService,
		emailService:          emailService,
		turnstileService:      turnstileService,
		emailQueueService:     emailQueueService,
		promoService:          promoService,
		affiliateService:      affiliateService,
		defaultSubAssigner:    defaultSubAssigner,
		userPlatformQuotaRepo: userPlatformQuotaRepo,
	}
}

func (s *AuthService) SetRuntimeCaches(authCacheInvalidator APIKeyAuthCacheInvalidator, billingCache BillingCache) {
	s.authCacheInvalidator = authCacheInvalidator
	s.billingCache = billingCache

	if s.core != nil {
		s.core.SetRuntimeCaches(authCacheInvalidator, billingCache)
	}
}

func (s *AuthService) EntClient() *dbent.Client {
	if s == nil {
		return nil
	}
	return s.entClient
}

func (s *AuthService) SetTencentCaptchaService(tencentCaptchaService *TencentCaptchaService) {
	s.tencentCaptchaService = tencentCaptchaService

	if s.core != nil {
		s.core.SetTencentCaptchaService(tencentCaptchaService)
	}
}

func (s *AuthService) SetAliyunCaptchaService(aliyunCaptchaService *AliyunCaptchaService) {
	s.aliyunCaptchaService = aliyunCaptchaService

	if s.core != nil {
		s.core.SetAliyunCaptchaService(aliyunCaptchaService)
	}
}

// Register 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) Register(ctx context.Context, email, password string) (string, *User, error) {
	token, u, err := s.identityCore().Register(ctx, email, password)
	return token, UserFromIdentity(u), err
}

// RegisterWithVerification 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) RegisterWithVerification(ctx context.Context, email, password, verifyCode, promoCode, invitationCode, affiliateCode string) (string, *User, error) {
	token, u, err := s.identityCore().RegisterWithVerification(ctx, email, password, verifyCode, promoCode, invitationCode, affiliateCode)
	return token, UserFromIdentity(u), err
}

type SendVerifyCodeResult = identity.SendVerifyCodeResult

// SendVerifyCode 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) SendVerifyCode(ctx context.Context, email string, locale ...string) error {
	return s.identityCore().SendVerifyCode(ctx, email, locale...)
}

// SendVerifyCodeAsync 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) SendVerifyCodeAsync(ctx context.Context, email string, locale ...string) (*SendVerifyCodeResult, error) {
	return s.identityCore().SendVerifyCodeAsync(ctx, email, locale...)
}

// VerifyCaptchaForRegister 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) VerifyCaptchaForRegister(ctx context.Context, proof CaptchaProof, remoteIP, verifyCode string) error {
	return s.identityCore().VerifyCaptchaForRegister(ctx, proof, remoteIP, verifyCode)
}

// VerifyCaptcha 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) VerifyCaptcha(ctx context.Context, proof CaptchaProof, remoteIP string) error {
	return s.identityCore().VerifyCaptcha(ctx, proof, remoteIP)
}

// VerifyActionCaptchaIfEnabled 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) VerifyActionCaptchaIfEnabled(ctx context.Context, proof CaptchaProof, remoteIP string) error {
	return s.identityCore().VerifyActionCaptchaIfEnabled(ctx, proof, remoteIP)
}

// VerifyTurnstileForRegister 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) VerifyTurnstileForRegister(ctx context.Context, token, remoteIP, verifyCode string) error {
	return s.identityCore().VerifyTurnstileForRegister(ctx, token, remoteIP, verifyCode)
}

// VerifyTurnstile 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) VerifyTurnstile(ctx context.Context, token string, remoteIP string) error {
	return s.identityCore().VerifyTurnstile(ctx, token, remoteIP)
}

// IsTurnstileEnabled 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) IsTurnstileEnabled(ctx context.Context) bool {
	return s.identityCore().IsTurnstileEnabled(ctx)
}

// IsRegistrationEnabled 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) IsRegistrationEnabled(ctx context.Context) bool {
	return s.identityCore().IsRegistrationEnabled(ctx)
}

// IsEmailVerifyEnabled 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) IsEmailVerifyEnabled(ctx context.Context) bool {
	return s.identityCore().IsEmailVerifyEnabled(ctx)
}

// Login 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) Login(ctx context.Context, email, password string) (string, *User, error) {
	token, u, err := s.identityCore().Login(ctx, email, password)
	return token, UserFromIdentity(u), err
}

// LoginOrRegisterOAuth 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) LoginOrRegisterOAuth(ctx context.Context, email, username string) (string, *User, error) {
	token, u, err := s.identityCore().LoginOrRegisterOAuth(ctx, email, username)
	return token, UserFromIdentity(u), err
}

// LoginOrRegisterOAuthWithTokenPair 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) LoginOrRegisterOAuthWithTokenPair(ctx context.Context, email, username, invitationCode, affiliateCode, signupSource string) (*TokenPair, *User, error) {
	pair, u, err := s.identityCore().LoginOrRegisterOAuthWithTokenPair(ctx, email, username, invitationCode, affiliateCode, signupSource)
	return pair, UserFromIdentity(u), err
}

// LoginOrRegisterOAuthWithTokenPairForSource 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) LoginOrRegisterOAuthWithTokenPairForSource(ctx context.Context, email, username, invitationCode, affiliateCode, signupSource string) (*TokenPair, *User, error) {
	pair, u, err := s.identityCore().LoginOrRegisterOAuthWithTokenPairForSource(ctx, email, username, invitationCode, affiliateCode, signupSource)
	return pair, UserFromIdentity(u), err
}

// LoginOrRegisterOAuthWithTokenPairAndPromoCode 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) LoginOrRegisterOAuthWithTokenPairAndPromoCode(ctx context.Context, email, username, invitationCode, affiliateCode, promoCode, signupSource string) (*TokenPair, *User, error) {
	pair, u, err := s.identityCore().LoginOrRegisterOAuthWithTokenPairAndPromoCode(ctx, email, username, invitationCode, affiliateCode, promoCode, signupSource)
	return pair, UserFromIdentity(u), err
}

// ApplyOAuthSignupPromoCode 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) ApplyOAuthSignupPromoCode(ctx context.Context, userID int64, promoCode string) {
	s.identityCore().ApplyOAuthSignupPromoCode(ctx, userID, promoCode)
}

// CreatePendingOAuthToken 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) CreatePendingOAuthToken(email, username string) (string, error) {
	return s.identityCore().CreatePendingOAuthToken(email, username)
}

// CreatePendingOAuthTokenWithAffiliate 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) CreatePendingOAuthTokenWithAffiliate(email, username, affiliateCode string) (string, error) {
	return s.identityCore().CreatePendingOAuthTokenWithAffiliate(email, username, affiliateCode)
}

type PendingOAuthIdentity = identity.PendingOAuthIdentity

// VerifyPendingOAuthToken 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) VerifyPendingOAuthToken(tokenStr string) (email, username string, err error) {
	return s.identityCore().VerifyPendingOAuthToken(tokenStr)
}

// VerifyPendingOAuthTokenDetails 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) VerifyPendingOAuthTokenDetails(tokenStr string) (*PendingOAuthIdentity, error) {
	return s.identityCore().VerifyPendingOAuthTokenDetails(tokenStr)
}

// authSourceSignupSettings 委托身份模块，旧入口仅保留类型投影。
func authSourceSignupSettings(defaults *AuthSourceDefaultSettings, signupSource string) (ProviderDefaultGrantSettings, bool) {
	return identity.AuthAuthSourceSignupSettings(defaults, signupSource)
}

// ValidateToken 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) ValidateToken(tokenString string) (*JWTClaims, error) {
	return s.identityCore().ValidateToken(tokenString)
}

// isReservedEmail 委托身份模块，旧入口仅保留类型投影。
func isReservedEmail(email string) bool { ; return identity.IsReservedEmail(email) }

// GenerateToken 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) GenerateToken(ctx context.Context, user *User) (string, error) {
	userProjection := IdentityUser(user)
	result, err := s.identityCore().GenerateToken(ctx, userProjection)
	ApplyIdentityUser(user, userProjection)
	return result, err
}

// GetAccessTokenExpiresIn 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) GetAccessTokenExpiresIn() int {
	return s.identityCore().GetAccessTokenExpiresIn()
}

// HashPassword 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) HashPassword(password string) (string, error) {
	return s.identityCore().HashPassword(password)
}

// CheckPassword 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) CheckPassword(password, hashedPassword string) bool {
	return s.identityCore().CheckPassword(password, hashedPassword)
}

// RefreshToken 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) RefreshToken(ctx context.Context, oldTokenString string) (string, error) {
	return s.identityCore().RefreshToken(ctx, oldTokenString)
}

// IsPasswordResetEnabled 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) IsPasswordResetEnabled(ctx context.Context) bool {
	return s.identityCore().IsPasswordResetEnabled(ctx)
}

// RequestPasswordReset 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) RequestPasswordReset(ctx context.Context, email, frontendBaseURL string, locale ...string) error {
	return s.identityCore().RequestPasswordReset(ctx, email, frontendBaseURL, locale...)
}

// RequestPasswordResetAsync 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) RequestPasswordResetAsync(ctx context.Context, email, frontendBaseURL string, locale ...string) error {
	return s.identityCore().RequestPasswordResetAsync(ctx, email, frontendBaseURL, locale...)
}

// ResetPassword 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) ResetPassword(ctx context.Context, email, token, newPassword string) error {
	return s.identityCore().ResetPassword(ctx, email, token, newPassword)
}

type TokenPair = identity.TokenPair

type TokenPairWithUser = identity.TokenPairWithUser

// GenerateTokenPair 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) GenerateTokenPair(ctx context.Context, user *User, familyID string) (*TokenPair, error) {
	userProjection := IdentityUser(user)
	result, err := s.identityCore().GenerateTokenPair(ctx, userProjection, familyID)
	ApplyIdentityUser(user, userProjection)
	return result, err
}

// RefreshTokenPair 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) RefreshTokenPair(ctx context.Context, refreshToken string) (*TokenPairWithUser, error) {
	return s.identityCore().RefreshTokenPair(ctx, refreshToken)
}

// RevokeRefreshToken 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) RevokeRefreshToken(ctx context.Context, refreshToken string) error {
	return s.identityCore().RevokeRefreshToken(ctx, refreshToken)
}

// RevokeSessionFamily 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) RevokeSessionFamily(ctx context.Context, familyID string) error {
	return s.identityCore().RevokeSessionFamily(ctx, familyID)
}

// RevokeAllUserSessions 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) RevokeAllUserSessions(ctx context.Context, userID int64) error {
	return s.identityCore().RevokeAllUserSessions(ctx, userID)
}

// RevokeAllUserTokens 委托身份模块，旧入口仅保留类型投影。
func (s *AuthService) RevokeAllUserTokens(ctx context.Context, userID int64) error {
	return s.identityCore().RevokeAllUserTokens(ctx, userID)
}

// WrapIdentityAuth 只为旧消费者保留字段形状，不重新装配身份核心或缓存。
func WrapIdentityAuth(client *dbent.Client, core *identity.AuthService, state *identitypostgres.AuthState) *AuthService {
	return &AuthService{entClient: client, core: core, state: state}
}
