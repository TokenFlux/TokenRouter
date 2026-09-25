// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

type CaptchaProof struct {
	// TurnstileToken 承载 Cloudflare Turnstile token；阿里云验证码复用该字段承载 captchaVerifyParam
	TurnstileToken string
	TencentTicket  string
	TencentRandstr string
}

type DefaultSubscriptionAssigner interface {
	AssignOrExtendSubscription(ctx context.Context, input *AssignSubscriptionInput) (*UserSubscription, bool, error)
}

type AuthSignupGrantPlan struct {
	Balance        float64
	Concurrency    int
	Subscriptions  []DefaultSubscriptionSetting
	PlatformQuotas map[string]*DefaultPlatformQuotaSetting
}

func (s *AuthService) SetRuntimeCaches(authCacheInvalidator APIKeyAuthCacheInvalidator, billingCache BillingCache) {
	s.Invalidator = authCacheInvalidator
	s.BalanceCache = billingCache
}

func (s *AuthService) SetTencentCaptchaService(tencentCaptchaService *TencentCaptchaService) {
	s.Tencent = tencentCaptchaService
}

func (s *AuthService) SetAliyunCaptchaService(aliyunCaptchaService *AliyunCaptchaService) {
	s.Aliyun = aliyunCaptchaService
}

// Register 用户注册，返回token和用户
func (s *AuthService) Register(ctx context.Context, email, password string) (string, *User, error) {
	return s.RegisterWithVerification(ctx, email, password, "", "", "", "")
}

// RegisterWithVerification 用户注册（支持邮件验证、优惠码、邀请码和邀请返利码），返回token和用户。
func (s *AuthService) RegisterWithVerification(ctx context.Context, email, password, verifyCode, promoCode, invitationCode, affiliateCode string) (string, *User, error) {
	// 检查是否开放注册（默认关闭：settingService 未配置时不允许注册）
	if s.Settings == nil || !s.Settings.IsRegistrationEnabled(ctx) {
		return "", nil, ErrRegDisabled
	}

	// 防止用户注册 LinuxDo OAuth 合成邮箱，避免第三方登录与本地账号发生碰撞。
	if IsReservedEmail(email) {
		return "", nil, ErrEmailReserved
	}
	existsEmail, err := s.AuthRegistrationEmailExists(ctx, email)
	if err != nil {
		s.Observer.Printf("service.auth", "[Auth] Database error checking email exists: %v", err)
		return "", nil, ErrServiceUnavailable
	}
	if existsEmail {
		return "", nil, ErrEmailExists
	}
	if err := s.AuthValidateRegistrationEmailQuota(ctx, email); err != nil {
		return "", nil, err
	}

	artifacts, err := s.AuthResolveRegistrationArtifacts(ctx, invitationCode, ErrInvitationCodeRequired)
	if err != nil {
		return "", nil, err
	}
	artifacts.EnforceEmailDomainQuota = true

	// 检查是否需要邮件验证
	if s.Settings != nil && s.Settings.IsEmailVerifyEnabled(ctx) {
		// 如果邮件验证已开启但邮件服务未配置，拒绝注册
		// 这是一个配置错误，不应该允许绕过验证
		if s.Email == nil {
			s.Observer.Printf("service.auth", "%s", "[Auth] Email verification enabled but email service not configured, rejecting registration")
			return "", nil, ErrServiceUnavailable
		}
		if verifyCode == "" {
			return "", nil, ErrEmailVerifyRequired
		}
		// 验证邮箱验证码
		if err := s.Email.VerifyCode(ctx, email, verifyCode); err != nil {
			return "", nil, fmt.Errorf("verify code: %w", err)
		}
	}

	// 密码哈希
	hashedPassword, err := s.HashPassword(password)
	if err != nil {
		return "", nil, fmt.Errorf("hash password: %w", err)
	}

	grantPlan := s.AuthResolveSignupGrantPlan(ctx, "email")

	// 新用户默认 RPM（0 = 不限制）。注册时写入，后续作为用户级兜底。
	var defaultRPMLimit int
	if s.Settings != nil {
		defaultRPMLimit = s.Settings.GetDefaultUserRPMLimit(ctx)
	}

	// 创建用户
	user := &User{
		Email:        email,
		PasswordHash: hashedPassword,
		Role:         RoleUser,
		Balance:      grantPlan.Balance,
		Concurrency:  grantPlan.Concurrency,
		RPMLimit:     defaultRPMLimit,
		Status:       StatusActive,
	}
	if err := s.AuthCreateRegisteredUser(ctx, user, artifacts); err != nil {
		// 优先检查邮箱冲突错误（竞态条件下可能发生）
		switch {
		case errors.Is(err, ErrEmailExists):
			return "", nil, ErrEmailExists
		case errors.Is(err, ErrEmailDomainRegistrationLimit):
			return "", nil, ErrEmailDomainRegistrationLimit
		case errors.Is(err, ErrInvitationCodeInvalid):
			return "", nil, ErrInvitationCodeInvalid
		default:
			s.Observer.Printf("service.auth", "[Auth] Database error creating user: %v", err)
			return "", nil, ErrServiceUnavailable
		}
	}
	s.AuthPostAuthUserBootstrap(ctx, user, "email", true)
	s.AuthAssignSubscriptions(ctx, user.ID, grantPlan.Subscriptions, "auto assigned by signup defaults")
	// 平台限额快照失败不阻断注册，避免配置或 DB 短暂异常影响用户创建。
	_ = s.AuthSnapshotPlatformQuotaDefaults(ctx, user.ID, &grantPlan)
	s.AuthBindRegistrationAffiliate(ctx, user.ID, affiliateCode)

	// 应用优惠码（如果提供且功能已启用）
	if promoCode != "" && s.Promo != nil && s.Settings != nil && s.Settings.IsPromoCodeEnabled(ctx) {
		if err := s.Promo.ApplyPromoCode(ctx, user.ID, promoCode); err != nil {
			// 优惠码应用失败不影响注册，只记录日志
			s.Observer.Printf("service.auth", "[Auth] Failed to apply promo code for user %d: %v", user.ID, err)
		} else {
			// 重新获取用户信息以获取更新后的余额
			if updatedUser, err := s.Users.GetByID(ctx, user.ID); err == nil {
				user = updatedUser
			}
		}
	}

	// 生成token
	token, err := s.GenerateToken(ctx, user)
	if err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}

	return token, user, nil
}

// SendVerifyCodeResult 发送验证码返回结果
type SendVerifyCodeResult struct {
	Countdown int `json:"countdown"` // 倒计时秒数
}

// SendVerifyCode 发送邮箱验证码（同步方式）
func (s *AuthService) SendVerifyCode(ctx context.Context, email string, locale ...string) error {
	// 检查是否开放注册（默认关闭）
	if s.Settings == nil || !s.Settings.IsRegistrationEnabled(ctx) {
		return ErrRegDisabled
	}

	if IsReservedEmail(email) {
		return ErrEmailReserved
	}
	// 检查邮箱是否已存在
	existsEmail, err := s.AuthRegistrationEmailExists(ctx, email)
	if err != nil {
		s.Observer.Printf("service.auth", "[Auth] Database error checking email exists: %v", err)
		return ErrServiceUnavailable
	}
	if existsEmail {
		return ErrEmailExists
	}
	if err := s.AuthValidateRegistrationEmailQuota(ctx, email); err != nil {
		return err
	}

	// 发送验证码
	if s.Email == nil {
		return errors.New("email service not configured")
	}

	// 获取网站名称
	siteName := "Sub2API"
	if s.Settings != nil {
		siteName = s.Settings.GetSiteName(ctx)
	}

	return s.Email.SendVerifyCode(ctx, email, siteName, FirstEmailLocale(locale))
}

// SendVerifyCodeAsync 异步发送邮箱验证码并返回倒计时
func (s *AuthService) SendVerifyCodeAsync(ctx context.Context, email string, locale ...string) (*SendVerifyCodeResult, error) {
	s.Observer.Printf("service.auth", "[Auth] SendVerifyCodeAsync called for email: %s", email)

	// 检查是否开放注册（默认关闭）
	if s.Settings == nil || !s.Settings.IsRegistrationEnabled(ctx) {
		s.Observer.Printf("service.auth", "%s", "[Auth] Registration is disabled")
		return nil, ErrRegDisabled
	}

	if IsReservedEmail(email) {
		return nil, ErrEmailReserved
	}
	// 检查邮箱是否已存在
	existsEmail, err := s.AuthRegistrationEmailExists(ctx, email)
	if err != nil {
		s.Observer.Printf("service.auth", "[Auth] Database error checking email exists: %v", err)
		return nil, ErrServiceUnavailable
	}
	if existsEmail {
		s.Observer.Printf("service.auth", "[Auth] Email already exists: %s", email)
		return nil, ErrEmailExists
	}
	if err := s.AuthValidateRegistrationEmailQuota(ctx, email); err != nil {
		return nil, err
	}

	// 检查邮件队列服务是否配置
	if s.EmailQueue == nil {
		s.Observer.Printf("service.auth", "%s", "[Auth] Email queue service not configured")
		return nil, errors.New("email queue service not configured")
	}

	// 获取网站名称
	siteName := "Sub2API"
	if s.Settings != nil {
		siteName = s.Settings.GetSiteName(ctx)
	}

	// 异步发送
	s.Observer.Printf("service.auth", "[Auth] Enqueueing verify code for: %s", email)
	if err := s.EmailQueue.EnqueueVerifyCode(email, siteName, FirstEmailLocale(locale)); err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to enqueue: %v", err)
		return nil, fmt.Errorf("enqueue verify code: %w", err)
	}

	s.Observer.Printf("service.auth", "[Auth] Verify code enqueued successfully for: %s", email)
	return &SendVerifyCodeResult{
		Countdown: 60, // 60秒倒计时
	}, nil
}

// VerifyCaptchaForRegister 在注册场景下验证当前启用的验证码。
// 当邮箱验证开启且已提交验证码时，说明验证码发送阶段已完成验证码校验，
// 此处跳过二次校验，避免一次性 token 在注册提交时重复使用导致误报失败。
func (s *AuthService) VerifyCaptchaForRegister(ctx context.Context, proof CaptchaProof, remoteIP, verifyCode string) error {
	if s.IsEmailVerifyEnabled(ctx) && strings.TrimSpace(verifyCode) != "" {
		s.Observer.Printf("service.auth", "%s", "[Auth] Email verify flow detected, skip duplicate captcha check on register")
		return nil
	}
	return s.VerifyCaptcha(ctx, proof, remoteIP)
}

func (s *AuthService) VerifyCaptcha(ctx context.Context, proof CaptchaProof, remoteIP string) error {
	required := s.Options != nil && s.Options.Server.Mode == "release" && s.Options.Turnstile.Required
	if s.Settings == nil {
		if required {
			return ErrTurnstileNotConfigured
		}
		return nil
	}

	providerConfig, err := s.Settings.GetCaptchaProviderConfig(ctx)
	if err != nil {
		s.Observer.Printf("service.auth", "%s", "[Auth] Failed to read captcha provider settings")
		return ErrServiceUnavailable
	}
	turnstileEnabled := providerConfig.TurnstileEnabled
	tencentEnabled := providerConfig.Tencent.Enabled
	aliyunEnabled := providerConfig.Aliyun.Enabled
	if AuthCaptchaProvidersConflict(turnstileEnabled, tencentEnabled, aliyunEnabled) {
		return ErrCaptchaProviderConflict
	}
	if tencentEnabled {
		if s.Tencent == nil {
			return ErrTencentCaptchaNotConfigured
		}
		return s.Tencent.VerifyTicketWithConfig(ctx, providerConfig.Tencent, proof.TencentTicket, proof.TencentRandstr, remoteIP)
	}
	if aliyunEnabled {
		if s.Aliyun == nil {
			return ErrAliyunCaptchaNotConfigured
		}
		return s.Aliyun.VerifyParamWithConfig(ctx, providerConfig.Aliyun, proof.TurnstileToken)
	}
	if turnstileEnabled {
		if s.Turnstile == nil || strings.TrimSpace(providerConfig.TurnstileSecretKey) == "" {
			return ErrTurnstileNotConfigured
		}
		return s.Turnstile.VerifyTokenWithSecret(ctx, providerConfig.TurnstileSecretKey, proof.TurnstileToken, remoteIP)
	}
	if required {
		return ErrTurnstileNotConfigured
	}
	return nil
}

// AuthCaptchaProvidersConflict 同一时间仅允许启用一家人机验证服务商
func AuthCaptchaProvidersConflict(enabled ...bool) bool {
	count := 0
	for _, e := range enabled {
		if e {
			count++
		}
	}
	return count > 1
}

// VerifyActionCaptchaIfEnabled 仅保护动作触发的扩展入口（OAuth 登录启动、passkey 登录），
// 腾讯天御与阿里云验证码启用时拦截；不扩大 Cloudflare Turnstile 的既有覆盖范围。
func (s *AuthService) VerifyActionCaptchaIfEnabled(ctx context.Context, proof CaptchaProof, remoteIP string) error {
	if s == nil || s.Settings == nil {
		return ErrServiceUnavailable
	}

	providerConfig, err := s.Settings.GetCaptchaProviderConfig(ctx)
	if err != nil {
		s.Observer.Printf("service.auth", "%s", "[Auth] Failed to read captcha provider settings")
		return ErrServiceUnavailable
	}
	tencentEnabled := providerConfig.Tencent.Enabled
	aliyunEnabled := providerConfig.Aliyun.Enabled
	if !tencentEnabled && !aliyunEnabled {
		return nil
	}
	if AuthCaptchaProvidersConflict(providerConfig.TurnstileEnabled, tencentEnabled, aliyunEnabled) {
		return ErrCaptchaProviderConflict
	}
	if aliyunEnabled {
		if s.Aliyun == nil {
			return ErrAliyunCaptchaNotConfigured
		}
		return s.Aliyun.VerifyParamWithConfig(ctx, providerConfig.Aliyun, proof.TurnstileToken)
	}
	if s.Tencent == nil {
		return ErrTencentCaptchaNotConfigured
	}
	return s.Tencent.VerifyTicketWithConfig(
		ctx,
		providerConfig.Tencent,
		proof.TencentTicket,
		proof.TencentRandstr,
		remoteIP,
	)
}

// VerifyTurnstileForRegister 保留旧内部接口，生产 handler 使用 VerifyCaptchaForRegister。
func (s *AuthService) VerifyTurnstileForRegister(ctx context.Context, token, remoteIP, verifyCode string) error {
	return s.VerifyCaptchaForRegister(ctx, CaptchaProof{TurnstileToken: token}, remoteIP, verifyCode)
}

// VerifyTurnstile 保留旧内部接口，生产 handler 使用 VerifyCaptcha。
func (s *AuthService) VerifyTurnstile(ctx context.Context, token string, remoteIP string) error {
	return s.VerifyCaptcha(ctx, CaptchaProof{TurnstileToken: token}, remoteIP)
}

// IsTurnstileEnabled 检查是否启用Turnstile验证
func (s *AuthService) IsTurnstileEnabled(ctx context.Context) bool {
	if s.Turnstile == nil {
		return false
	}
	return s.Turnstile.IsEnabled(ctx)
}

// IsRegistrationEnabled 检查是否开放注册
func (s *AuthService) IsRegistrationEnabled(ctx context.Context) bool {
	if s.Settings == nil {
		return false // 安全默认：settingService 未配置时关闭注册
	}
	return s.Settings.IsRegistrationEnabled(ctx)
}

// IsEmailVerifyEnabled 检查是否开启邮件验证
func (s *AuthService) IsEmailVerifyEnabled(ctx context.Context) bool {
	if s.Settings == nil {
		return false
	}
	return s.Settings.IsEmailVerifyEnabled(ctx)
}

// Login 用户登录，返回JWT token
func (s *AuthService) Login(ctx context.Context, email, password string) (string, *User, error) {
	// 查找用户
	user, err := s.Users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return "", nil, ErrInvalidCredentials
		}
		// 记录数据库错误但不暴露给用户
		s.Observer.Printf("service.auth", "[Auth] Database error during login: %v", err)
		return "", nil, ErrServiceUnavailable
	}

	// 验证密码
	if !s.CheckPassword(password, user.PasswordHash) {
		return "", nil, ErrInvalidCredentials
	}

	// 检查用户状态
	if !user.IsActive() {
		return "", nil, ErrUserNotActive
	}

	// 生成JWT token
	token, err := s.GenerateToken(ctx, user)
	if err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}

	return token, user, nil
}

// LoginOrRegisterOAuth 用于第三方 OAuth/SSO 登录：
// - 如果邮箱已存在：直接登录（不需要本地密码）
// - 如果邮箱不存在：创建新用户并登录
//
// 注意：该函数用于 LinuxDo OAuth 登录场景（不同于上游账号的 OAuth，例如 Claude/OpenAI/Gemini）。
// 为了满足现有数据库约束（需要密码哈希），新用户会生成随机密码并进行哈希保存。
func (s *AuthService) LoginOrRegisterOAuth(ctx context.Context, email, username string) (string, *User, error) {
	email = strings.TrimSpace(email)
	if email == "" || len(email) > 255 {
		return "", nil, infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return "", nil, infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}

	username = strings.TrimSpace(username)
	if len([]rune(username)) > 100 {
		username = string([]rune(username)[:100])
	}

	user, err := s.Users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// OAuth 首次登录视为注册（fail-close：settingService 未配置时不允许注册）
			if s.Settings == nil || !s.Settings.IsRegistrationEnabled(ctx) {
				return "", nil, ErrRegDisabled
			}

			randomPassword, err := RandomHexString(32)
			if err != nil {
				s.Observer.Printf("service.auth", "[Auth] Failed to generate random password for oauth signup: %v", err)
				return "", nil, ErrServiceUnavailable
			}
			hashedPassword, err := s.HashPassword(randomPassword)
			if err != nil {
				return "", nil, fmt.Errorf("hash password: %w", err)
			}

			signupSource := AuthInferLegacySignupSource(email)
			grantPlan := s.AuthResolveSignupGrantPlan(ctx, signupSource)
			var defaultRPMLimit int
			if s.Settings != nil {
				defaultRPMLimit = s.Settings.GetDefaultUserRPMLimit(ctx)
			}

			newUser := &User{
				Email:        email,
				Username:     username,
				PasswordHash: hashedPassword,
				Role:         RoleUser,
				Balance:      grantPlan.Balance,
				Concurrency:  grantPlan.Concurrency,
				RPMLimit:     defaultRPMLimit,
				Status:       StatusActive,
				SignupSource: signupSource,
			}

			if err := s.AuthCreateRegisteredUser(ctx, newUser, nil); err != nil {
				if errors.Is(err, ErrEmailExists) {
					// 并发场景：GetByEmail 与 Create 之间用户被创建。
					user, err = s.Users.GetByEmail(ctx, email)
					if err != nil {
						if errors.Is(err, ErrUserNotFound) {
							return "", nil, ErrEmailExists
						}
						s.Observer.Printf("service.auth", "[Auth] Database error getting user after conflict: %v", err)
						return "", nil, ErrServiceUnavailable
					}
				} else {
					s.Observer.Printf("service.auth", "[Auth] Database error creating oauth user: %v", err)
					return "", nil, ErrServiceUnavailable
				}
			} else {
				user = newUser
				s.AuthPostAuthUserBootstrap(ctx, user, signupSource, false)
				s.AuthAssignSubscriptions(ctx, user.ID, grantPlan.Subscriptions, "auto assigned by signup defaults")
				// snapshot user × platform quota（fail-open）
				_ = s.AuthSnapshotPlatformQuotaDefaults(ctx, user.ID, &grantPlan)
			}
		} else {
			s.Observer.Printf("service.auth", "[Auth] Database error during oauth login: %v", err)
			return "", nil, ErrServiceUnavailable
		}
	}

	if !user.IsActive() {
		return "", nil, ErrUserNotActive
	}

	// 尽力补全：当用户名为空时，使用第三方返回的用户名回填。
	if user.Username == "" && username != "" {
		user.Username = username
		if err := s.Users.Update(ctx, user, UserUpdateFields{Username: true}); err != nil {
			s.Observer.Printf("service.auth", "[Auth] Failed to update username after oauth login: %v", err)
		}
	}
	token, err := s.GenerateToken(ctx, user)
	if err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}
	return token, user, nil
}

// AuthCanBypassRegistrationDisabledForOAuth 在钉钉企业模式（internal_only）且
// dingtalk_connect_bypass_registration=true 时，允许跳过全局 registration_enabled 检查。
func (s *AuthService) AuthCanBypassRegistrationDisabledForOAuth(ctx context.Context, signupSource string) bool {
	if signupSource != "dingtalk" {
		return false
	}
	cfg, err := s.Settings.GetDingTalkConnectOAuthConfig(ctx)
	if err != nil || !cfg.Enabled || !cfg.BypassRegistration {
		return false
	}
	return cfg.CorpRestrictionPolicy == "internal_only"
}

// LoginOrRegisterOAuthWithTokenPair 用于第三方 OAuth/SSO 登录，返回完整的 TokenPair。
// 与 LoginOrRegisterOAuth 功能相同，但返回 TokenPair 而非单个 token。
// invitationCode 仅在邀请码注册模式下新用户注册时使用；已有账号登录时忽略。
// affiliateCode 是邀请返利码，仅在新用户注册时生效；signupSource 用于渠道默认授权和钉钉注册豁免。
func (s *AuthService) LoginOrRegisterOAuthWithTokenPair(ctx context.Context, email, username, invitationCode, affiliateCode, signupSource string) (*TokenPair, *User, error) {
	return s.AuthLoginOrRegisterOAuthWithTokenPair(ctx, email, username, invitationCode, affiliateCode, "", signupSource)
}

// LoginOrRegisterOAuthWithTokenPairForSource 用于需要显式记录 OAuth 来源的第三方登录。
// affiliateCode 是邀请返利码，仅在新用户注册时生效；signupSource 用于渠道默认授权和钉钉注册豁免。
func (s *AuthService) LoginOrRegisterOAuthWithTokenPairForSource(ctx context.Context, email, username, invitationCode, affiliateCode, signupSource string) (*TokenPair, *User, error) {
	return s.LoginOrRegisterOAuthWithTokenPair(ctx, email, username, invitationCode, affiliateCode, signupSource)
}

// LoginOrRegisterOAuthWithTokenPairAndPromoCode 用于 OAuth 新用户注册时额外应用优惠码。
// promoCode 只在本次确实创建新用户时生效，已有账号登录不会重复应用。
func (s *AuthService) LoginOrRegisterOAuthWithTokenPairAndPromoCode(ctx context.Context, email, username, invitationCode, affiliateCode, promoCode, signupSource string) (*TokenPair, *User, error) {
	return s.AuthLoginOrRegisterOAuthWithTokenPair(ctx, email, username, invitationCode, affiliateCode, promoCode, signupSource)
}

func (s *AuthService) AuthLoginOrRegisterOAuthWithTokenPair(ctx context.Context, email, username, invitationCode, affiliateCode, promoCode, signupSource string) (*TokenPair, *User, error) {
	// 检查 refreshTokenCache 是否可用
	if s.RefreshTokens == nil {
		return nil, nil, errors.New("refresh token cache not configured")
	}

	email = strings.TrimSpace(email)
	if email == "" || len(email) > 255 {
		return nil, nil, infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, nil, infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}

	username = strings.TrimSpace(username)
	if len([]rune(username)) > 100 {
		username = string([]rune(username)[:100])
	}

	user, err := s.Users.GetByEmail(ctx, email)
	created := false
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// OAuth 首次登录视为注册
			if s.Settings == nil || (!s.Settings.IsRegistrationEnabled(ctx) && !s.AuthCanBypassRegistrationDisabledForOAuth(ctx, signupSource)) {
				return nil, nil, ErrRegDisabled
			}

			artifacts, err := s.AuthResolveRegistrationArtifacts(ctx, invitationCode, ErrOAuthInvitationRequired)
			if err != nil {
				return nil, nil, err
			}

			randomPassword, err := RandomHexString(32)
			if err != nil {
				s.Observer.Printf("service.auth", "[Auth] Failed to generate random password for oauth signup: %v", err)
				return nil, nil, ErrServiceUnavailable
			}
			hashedPassword, err := s.HashPassword(randomPassword)
			if err != nil {
				return nil, nil, fmt.Errorf("hash password: %w", err)
			}

			// 优先用 caller 显式传入的 signupSource（如 "dingtalk" / "linuxdo" / "oidc" / "wechat"），
			// 否则才按邮箱后缀推断——避免有真实邮箱的 OAuth 用户被推断为 "email" 渠道，导致渠道授权错读。
			if strings.TrimSpace(signupSource) == "" {
				signupSource = AuthInferLegacySignupSource(email)
			}
			grantPlan := s.AuthResolveSignupGrantPlan(ctx, signupSource)
			var defaultRPMLimit int
			if s.Settings != nil {
				defaultRPMLimit = s.Settings.GetDefaultUserRPMLimit(ctx)
			}

			newUser := &User{
				Email:        email,
				Username:     username,
				PasswordHash: hashedPassword,
				Role:         RoleUser,
				Balance:      grantPlan.Balance,
				Concurrency:  grantPlan.Concurrency,
				RPMLimit:     defaultRPMLimit,
				Status:       StatusActive,
				SignupSource: signupSource,
			}
			if err := s.AuthCreateRegisteredUser(ctx, newUser, artifacts); err != nil {
				if errors.Is(err, ErrEmailExists) {
					user, err = s.Users.GetByEmail(ctx, email)
					if err != nil {
						if errors.Is(err, ErrUserNotFound) {
							return nil, nil, ErrEmailExists
						}
						s.Observer.Printf("service.auth", "[Auth] Database error getting user after conflict: %v", err)
						return nil, nil, ErrServiceUnavailable
					}
				} else if errors.Is(err, ErrInvitationCodeInvalid) {
					return nil, nil, ErrInvitationCodeInvalid
				} else {
					s.Observer.Printf("service.auth", "[Auth] Database error creating oauth user: %v", err)
					return nil, nil, ErrServiceUnavailable
				}
			} else {
				user = newUser
				created = true
				s.AuthPostAuthUserBootstrap(ctx, user, signupSource, false)
				s.AuthAssignSubscriptions(ctx, user.ID, grantPlan.Subscriptions, "auto assigned by signup defaults")
				// 平台限额快照失败不阻断 OAuth 注册，避免三方登录流程被默认限额配置拖垮。
				_ = s.AuthSnapshotPlatformQuotaDefaults(ctx, user.ID, &grantPlan)
				s.AuthBindRegistrationAffiliate(ctx, user.ID, affiliateCode)
			}
		} else {
			s.Observer.Printf("service.auth", "[Auth] Database error during oauth login: %v", err)
			return nil, nil, ErrServiceUnavailable
		}
	}

	if !user.IsActive() {
		return nil, nil, ErrUserNotActive
	}

	if user.Username == "" && username != "" {
		user.Username = username
		if err := s.Users.Update(ctx, user, UserUpdateFields{Username: true}); err != nil {
			s.Observer.Printf("service.auth", "[Auth] Failed to update username after oauth login: %v", err)
		}
	}
	if created {
		user = s.AuthApplyOAuthSignupPromoCode(ctx, user, promoCode)
	}
	tokenPair, err := s.GenerateTokenPair(ctx, user, "")
	if err != nil {
		return nil, nil, fmt.Errorf("generate token pair: %w", err)
	}
	return tokenPair, user, nil
}

func (s *AuthService) ApplyOAuthSignupPromoCode(ctx context.Context, userID int64, promoCode string) {
	if userID <= 0 {
		return
	}
	s.AuthApplyOAuthSignupPromoCode(ctx, &User{ID: userID}, promoCode)
}

func (s *AuthService) AuthApplyOAuthSignupPromoCode(ctx context.Context, user *User, promoCode string) *User {
	promoCode = strings.TrimSpace(promoCode)
	if user == nil || user.ID <= 0 || promoCode == "" || s.Promo == nil || s.Settings == nil || !s.Settings.IsPromoCodeEnabled(ctx) {
		return user
	}
	if err := s.Promo.ApplyPromoCode(ctx, user.ID, promoCode); err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to apply promo code for oauth user %d: %v", user.ID, err)
		return user
	}
	if updatedUser, err := s.Users.GetByID(ctx, user.ID); err == nil {
		return updatedUser
	}
	return user
}

func (s *AuthService) AuthAssignSubscriptions(ctx context.Context, userID int64, items []DefaultSubscriptionSetting, notes string) {
	if s.Settings == nil || s.DefaultSubscriptions == nil || userID <= 0 {
		return
	}
	for _, item := range items {
		err := s.AuthRunFailOpenDBStep(ctx, "auth_default_subscription", func(stepCtx context.Context) error {
			_, _, err := s.DefaultSubscriptions.AssignOrExtendSubscription(stepCtx, &AssignSubscriptionInput{
				UserID: userID,
				PlanID: item.PlanID,
				Notes:  notes,
			})
			return err
		})
		if err != nil {
			s.Observer.Printf("service.auth", "[Auth] Failed to assign default subscription: user_id=%d plan_id=%d err=%v", userID, item.PlanID, err)
		}
	}
}

type AuthRegistrationArtifacts struct {
	InvitationRedeemCode *RedeemCode
	// EnforceEmailDomainQuota 仅为普通注册和 OAuth 邮箱补全开启非白名单域名额度。
	EnforceEmailDomainQuota bool
}

func (s *AuthService) AuthResolveRegistrationArtifacts(ctx context.Context, invitationCode string, missingInvitationErr error) (*AuthRegistrationArtifacts, error) {
	artifacts := &AuthRegistrationArtifacts{}

	if s.Settings != nil && s.Settings.IsInvitationCodeEnabled(ctx) {
		if invitationCode == "" {
			return nil, missingInvitationErr
		}
		redeemCode, err := s.Redeem.GetByCode(ctx, invitationCode)
		if err != nil {
			s.Observer.Printf("service.auth", "[Auth] Invalid invitation code: %s, error: %v", invitationCode, err)
			return nil, ErrInvitationCodeInvalid
		}
		if redeemCode.Type != RedeemTypeInvitation || !redeemCode.CanUse() {
			s.Observer.Printf("service.auth", "[Auth] Invitation code invalid: type=%s, status=%s", redeemCode.Type, redeemCode.Status)
			return nil, ErrInvitationCodeInvalid
		}
		artifacts.InvitationRedeemCode = redeemCode
	}

	return artifacts, nil
}

func (s *AuthService) AuthBindRegistrationAffiliate(ctx context.Context, userID int64, affiliateCode string) {
	if s == nil || s.Affiliate == nil || userID <= 0 {
		return
	}
	if err := s.AuthRunFailOpenDBStep(ctx, "auth_affiliate_profile", func(stepCtx context.Context) error {
		err := s.Affiliate.EnsureUserAffiliate(stepCtx, userID)
		return err
	}); err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to initialize affiliate profile for user %d: %v", userID, err)
		return
	}
	if code := strings.TrimSpace(affiliateCode); code != "" {
		if err := s.AuthRunFailOpenDBStep(ctx, "auth_affiliate_bind_inviter", func(stepCtx context.Context) error {
			return s.Affiliate.BindInviterByCode(stepCtx, userID, code)
		}); err != nil {
			// 邀请返利码绑定失败不影响注册结果，只记录日志便于排查。
			s.Observer.Printf("service.auth", "[Auth] Failed to bind affiliate inviter for user %d: %v", userID, err)
		}
	}
}

func (s *AuthService) AuthResolveSignupGrantPlan(ctx context.Context, signupSource string) AuthSignupGrantPlan {
	plan := AuthSignupGrantPlan{}
	if s != nil && s.Options != nil {
		plan.Balance = s.Options.Default.UserBalance
		plan.Concurrency = s.Options.Default.UserConcurrency
	}
	if s == nil || s.Settings == nil {
		return plan
	}

	plan.Balance = s.Settings.GetDefaultBalance(ctx)
	plan.Concurrency = s.Settings.GetDefaultConcurrency(ctx)
	plan.Subscriptions = s.Settings.GetDefaultSubscriptions(ctx)

	// ============ 全局 quota 装载（必须在 ResolveAuthSourceGrantSettings 之前） ============
	// 无论 auth source 是否 enabled，全局层都要先装载，确保 !enabled 早退路径也携带全局 quota。
	if quotas, err := s.Settings.GetDefaultPlatformQuotas(ctx); err == nil {
		plan.PlatformQuotas = quotas
	} else {
		s.Observer.Printf("service.auth", "[Auth] Warning: load default platform quotas failed: %v (fail-open)", err)
	}
	// ============================================================================================

	resolved, enabled, err := s.Settings.ResolveAuthSourceGrantSettings(ctx, signupSource, false)
	if err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to load auth source signup defaults for %s: %v", signupSource, err)
		return plan
	}
	if !enabled {
		return plan // plan.PlatformQuotas 已含全局层
	}

	plan.Balance = resolved.Balance
	plan.Concurrency = resolved.Concurrency
	plan.Subscriptions = resolved.Subscriptions

	// ============ auth source quota merge（仅在 enabled 分支内） ============
	asQuotas := s.Settings.GetAuthSourcePlatformQuotas(ctx, signupSource)
	if plan.PlatformQuotas != nil {
		for platform, patch := range asQuotas {
			if dst := plan.PlatformQuotas[platform]; dst != nil {
				MergePlatformQuotaDefaults(dst, patch)
			}
		}
	}
	// ==============================================================================

	return plan
}

func AuthAuthSourceSignupSettings(defaults *AuthSourceDefaultSettings, signupSource string) (ProviderDefaultGrantSettings, bool) {
	if defaults == nil {
		return ProviderDefaultGrantSettings{}, false
	}

	switch strings.ToLower(strings.TrimSpace(signupSource)) {
	case "email":
		return defaults.Email, true
	case "linuxdo":
		return defaults.LinuxDo, true
	case "oidc":
		return defaults.OIDC, true
	case "wechat":
		return defaults.WeChat, true
	case "github":
		return defaults.GitHub, true
	case "google":
		return defaults.Google, true
	case "dingtalk":
		return defaults.DingTalk, true
	default:
		return ProviderDefaultGrantSettings{}, false
	}
}

func (s *AuthService) AuthPostAuthUserBootstrap(ctx context.Context, user *User, signupSource string, touchLogin bool) {
	if user == nil || user.ID <= 0 {
		return
	}

	if strings.TrimSpace(signupSource) == "" {
		signupSource = "email"
	}
	s.AuthUpdateUserSignupSource(ctx, user.ID, signupSource)

	if touchLogin {
		s.AuthTouchUserLogin(ctx, user.ID)
	}
}

func (s *AuthService) AuthBackfillEmailIdentityOnSuccessfulLogin(ctx context.Context, user *User) {
	if s == nil || user == nil || user.ID <= 0 {
		return
	}
	identity, created := s.AuthEnsureEmailAuthIdentity(ctx, user, "auth_service_login_backfill")
	if s.AuthShouldApplyEmailFirstBindDefaults(ctx, user.ID, identity, created) {
		if err := s.ApplyProviderDefaultSettingsOnFirstBind(ctx, user.ID, "email"); err != nil {
			s.Observer.Printf("service.auth", "[Auth] Failed to apply email first bind defaults: user_id=%d err=%v", user.ID, err)
		}
	}
}

func AuthEmailAuthIdentitySource(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	raw, ok := metadata["source"]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}

func AuthInferLegacySignupSource(email string) string {
	normalized := strings.ToLower(strings.TrimSpace(email))
	switch {
	case strings.HasSuffix(normalized, DingTalkConnectSyntheticEmailDomain):
		return "dingtalk"
	case strings.HasSuffix(normalized, LinuxDoConnectSyntheticEmailDomain):
		return "linuxdo"
	case strings.HasSuffix(normalized, OIDCConnectSyntheticEmailDomain):
		return "oidc"
	case strings.HasSuffix(normalized, WeChatConnectSyntheticEmailDomain):
		return "wechat"
	default:
		return "email"
	}
}

func (s *AuthService) AuthValidateRegistrationEmailPolicy(ctx context.Context, email string) error {
	if s.Settings == nil {
		return nil
	}
	whitelist := s.Settings.GetRegistrationEmailSuffixWhitelist(ctx)
	if !IsRegistrationEmailSuffixAllowed(email, whitelist) {
		return AuthBuildEmailSuffixNotAllowedError(whitelist)
	}
	return nil
}

// AuthValidateRegistrationEmailQuota 保留白名单为空时的全放行行为；配置白名单后，
// 默认严格拒绝非白名单邮箱，仅在域名额度开关开启时按主域名共享一个名额。
func (s *AuthService) AuthValidateRegistrationEmailQuota(ctx context.Context, email string) error {
	if s == nil || s.Settings == nil {
		return nil
	}
	whitelist := s.Settings.GetRegistrationEmailSuffixWhitelist(ctx)
	if !IsRegistrationEmailSuffixLimited(email, whitelist) {
		return nil
	}
	if !s.Settings.IsRegistrationEmailDomainQuotaEnabled(ctx) {
		return AuthBuildEmailSuffixNotAllowedError(whitelist)
	}
	domain, err := s.AuthRegistrationEmailDomainLimit(ctx, email)
	if err != nil || domain == "" {
		return err
	}
	quotaRepo := s.DomainRegistration
	ok := quotaRepo != nil
	if !ok {
		// 无数据库的单元测试桩保持兼容；生产装配必须具备原子仓储能力。
		if s.HasDatabase() {
			return ErrServiceUnavailable
		}
		return nil
	}
	count, err := quotaRepo.CountUsersByEmailDomain(ctx, domain)
	if err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to count registration email domain %s: %v", domain, err)
		return ErrServiceUnavailable
	}
	if count > 0 {
		return ErrEmailDomainRegistrationLimit
	}
	return nil
}

func (s *AuthService) AuthRegistrationEmailDomainLimit(ctx context.Context, email string) (string, error) {
	if s == nil || s.Settings == nil {
		return "", nil
	}
	whitelist := s.Settings.GetRegistrationEmailSuffixWhitelist(ctx)
	if !IsRegistrationEmailSuffixLimited(email, whitelist) {
		return "", nil
	}
	// 事务内再次读取开关，避免预检后设置被关闭仍按额度放行。
	if !s.Settings.IsRegistrationEmailDomainQuotaEnabled(ctx) {
		return "", AuthBuildEmailSuffixNotAllowedError(whitelist)
	}
	domain := RegistrationEmailDomain(email)
	if domain == "" {
		return "", AuthBuildEmailSuffixNotAllowedError(whitelist)
	}
	return domain, nil
}

func (s *AuthService) AuthRegistrationEmailExists(ctx context.Context, email string) (bool, error) {
	exists, err := s.Users.ExistsByEmail(ctx, email)
	if err != nil || exists {
		return exists, err
	}
	if s.Settings != nil && s.Settings.IsRegistrationEmailNormalizationEnabled(ctx) {
		normalizedEmail := NormalizeRegistrationEmailAddress(email)
		if normalizedEmail != "" {
			return s.Users.ExistsByNormalizedEmail(ctx, normalizedEmail)
		}
	}
	return false, nil
}

func AuthBuildEmailSuffixNotAllowedError(whitelist []string) error {
	if len(whitelist) == 0 {
		return ErrEmailSuffixNotAllowed
	}

	allowed := strings.Join(whitelist, ", ")
	return infraerrors.BadRequest(
		"EMAIL_SUFFIX_NOT_ALLOWED",
		fmt.Sprintf("email suffix is not allowed, allowed suffixes: %s", allowed),
	).WithMetadata(map[string]string{
		"allowed_suffixes":     strings.Join(whitelist, ","),
		"allowed_suffix_count": strconv.Itoa(len(whitelist)),
	})
}

// IsPasswordResetEnabled 检查是否启用密码重置功能
// 要求：必须同时开启邮件验证且 SMTP 配置正确
func (s *AuthService) IsPasswordResetEnabled(ctx context.Context) bool {
	if s.Settings == nil {
		return false
	}
	// Must have email verification enabled and SMTP configured
	if !s.Settings.IsEmailVerifyEnabled(ctx) {
		return false
	}
	return s.Settings.IsPasswordResetEnabled(ctx)
}

// AuthPreparePasswordReset validates the password reset request and returns necessary data
// Returns (siteName, resetURL, shouldProceed)
// shouldProceed is false when we should silently return success (to prevent enumeration)
func (s *AuthService) AuthPreparePasswordReset(ctx context.Context, email, frontendBaseURL string) (string, string, bool) {
	// Check if user exists (but don't reveal this to the caller)
	user, err := s.Users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// Security: Log but don't reveal that user doesn't exist
			s.Observer.Printf("service.auth", "[Auth] Password reset requested for non-existent email: %s", email)
			return "", "", false
		}
		s.Observer.Printf("service.auth", "[Auth] Database error checking email for password reset: %v", err)
		return "", "", false
	}

	// Check if user is active
	if !user.IsActive() {
		s.Observer.Printf("service.auth", "[Auth] Password reset requested for inactive user: %s", email)
		return "", "", false
	}

	// Get site name
	siteName := "Sub2API"
	if s.Settings != nil {
		siteName = s.Settings.GetSiteName(ctx)
	}

	// Build reset URL base
	resetURL := fmt.Sprintf("%s/reset-password", strings.TrimSuffix(frontendBaseURL, "/"))

	return siteName, resetURL, true
}

// RequestPasswordReset 请求密码重置（同步发送）
// Security: Returns the same response regardless of whether the email exists (prevent user enumeration)
func (s *AuthService) RequestPasswordReset(ctx context.Context, email, frontendBaseURL string, locale ...string) error {
	if !s.IsPasswordResetEnabled(ctx) {
		return infraerrors.Forbidden("PASSWORD_RESET_DISABLED", "password reset is not enabled")
	}
	if s.Email == nil {
		return ErrServiceUnavailable
	}

	siteName, resetURL, shouldProceed := s.AuthPreparePasswordReset(ctx, email, frontendBaseURL)
	if !shouldProceed {
		return nil // Silent success to prevent enumeration
	}

	if err := s.Email.SendPasswordResetEmail(ctx, email, siteName, resetURL, FirstEmailLocale(locale)); err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to send password reset email to %s: %v", email, err)
		return nil // Silent success to prevent enumeration
	}

	s.Observer.Printf("service.auth", "[Auth] Password reset email sent to: %s", email)
	return nil
}

// RequestPasswordResetAsync 异步请求密码重置（队列发送）
// Security: Returns the same response regardless of whether the email exists (prevent user enumeration)
func (s *AuthService) RequestPasswordResetAsync(ctx context.Context, email, frontendBaseURL string, locale ...string) error {
	if !s.IsPasswordResetEnabled(ctx) {
		return infraerrors.Forbidden("PASSWORD_RESET_DISABLED", "password reset is not enabled")
	}
	if s.EmailQueue == nil {
		return ErrServiceUnavailable
	}

	siteName, resetURL, shouldProceed := s.AuthPreparePasswordReset(ctx, email, frontendBaseURL)
	if !shouldProceed {
		return nil // Silent success to prevent enumeration
	}

	if err := s.EmailQueue.EnqueuePasswordReset(email, siteName, resetURL, FirstEmailLocale(locale)); err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to enqueue password reset email for %s: %v", email, err)
		return nil // Silent success to prevent enumeration
	}

	s.Observer.Printf("service.auth", "[Auth] Password reset email enqueued for: %s", email)
	return nil
}

// ResetPassword 重置密码
// Security: Increments TokenVersion to invalidate all existing JWT tokens
func (s *AuthService) ResetPassword(ctx context.Context, email, token, newPassword string) error {
	// Check if password reset is enabled
	if !s.IsPasswordResetEnabled(ctx) {
		return infraerrors.Forbidden("PASSWORD_RESET_DISABLED", "password reset is not enabled")
	}

	if s.Email == nil {
		return ErrServiceUnavailable
	}

	// Verify and consume the reset token (one-time use)
	if err := s.Email.ConsumePasswordResetToken(ctx, email, token); err != nil {
		return err
	}

	// Get user
	user, err := s.Users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return ErrInvalidResetToken // Token was valid but user was deleted
		}
		s.Observer.Printf("service.auth", "[Auth] Database error getting user for password reset: %v", err)
		return ErrServiceUnavailable
	}

	// Check if user is active
	if !user.IsActive() {
		return ErrUserNotActive
	}

	// Hash new password
	hashedPassword, err := s.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	// Update password and increment TokenVersion
	user.PasswordHash = hashedPassword
	user.TokenVersion++ // Invalidate all existing tokens

	// TokenVersion 无对应数据库列（见 ResolvedTokenVersion：由 email+password_hash 指纹推导），
	// 写回 password_hash 本身即可让旧 token 失效。
	if err := s.Users.Update(ctx, user, UserUpdateFields{PasswordHash: true}); err != nil {
		s.Observer.Printf("service.auth", "[Auth] Database error updating password for user %d: %v", user.ID, err)
		return ErrServiceUnavailable
	}

	// Also revoke all refresh tokens for this user
	if err := s.RevokeAllUserSessions(ctx, user.ID); err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to revoke refresh tokens for user %d: %v", user.ID, err)
		// Don't return error - password was already changed successfully
	}

	s.Observer.Printf("service.auth", "[Auth] Password reset successful for user: %s", email)
	return nil
}
