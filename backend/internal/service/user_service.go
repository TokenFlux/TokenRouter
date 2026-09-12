// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	fmt "fmt"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	_ "image/png"
	slog "log/slog"
	strconv "strconv"
	time "time"
)

var ErrUserNotFound = identity.ErrUserNotFound

var ErrPasswordIncorrect = identity.ErrPasswordIncorrect

var ErrBalanceNegative = identity.ErrBalanceNegative

var ErrInsufficientPerms = identity.ErrInsufficientPerms

var ErrNotifyCodeUserRateLimit = identity.ErrNotifyCodeUserRateLimit

var ErrAvatarInvalid = identity.ErrAvatarInvalid

var ErrAvatarTooLarge = identity.ErrAvatarTooLarge

var ErrAvatarNotImage = identity.ErrAvatarNotImage

var ErrProfileEmailChangeForbidden = identity.ErrProfileEmailChangeForbidden

var ErrIdentityProviderInvalid = identity.ErrIdentityProviderInvalid

var ErrIdentityRedirectInvalid = identity.ErrIdentityRedirectInvalid

var ErrUserAPIKeyLimitInvalid = identity.ErrUserAPIKeyLimitInvalid

var ErrIdentityUnbindLastMethod = identity.ErrIdentityUnbindLastMethod

// IsValidUserAPIKeyLimit 委托身份资料实现。
func IsValidUserAPIKeyLimit(limit int) bool { return identity.IsValidUserAPIKeyLimit(limit) }

type UserListFilters = identity.UserListFilters

type UserUpdateFields = identity.UserUpdateFields

type BalanceChange = identity.BalanceChange

type UserRepository interface {
	Create(ctx context.Context, user *User) error
	CreateWithNormalizedEmailGuard(ctx context.Context, user *User, normalizedEmail string) error
	GetByID(ctx context.Context, id int64) (*User, error)
	// GetByIDIncludeDeleted 绕过软删除过滤按 ID 取用户（含已删）。仅供管理员审计/usage 点击使用。
	GetByIDIncludeDeleted(ctx context.Context, id int64) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetFirstAdmin(ctx context.Context) (*User, error)
	// Update 只写 fields 中显式声明的列，其余列保持库中当前值。
	Update(ctx context.Context, user *User, fields UserUpdateFields) error
	// UpdateWithNormalizedEmailGuard 在相同字段掩码基础上增加 fork 的注册邮箱归一化互斥保护。
	UpdateWithNormalizedEmailGuard(ctx context.Context, user *User, normalizedEmail string, fields UserUpdateFields) error
	Delete(ctx context.Context, id int64) error
	GetUserAvatar(ctx context.Context, userID int64) (*UserAvatar, error)
	UpsertUserAvatar(ctx context.Context, userID int64, input UpsertUserAvatarInput) (*UserAvatar, error)
	DeleteUserAvatar(ctx context.Context, userID int64) error

	List(ctx context.Context, params pagination.PaginationParams) ([]User, *pagination.PaginationResult, error)
	ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters UserListFilters) ([]User, *pagination.PaginationResult, error)
	GetLatestUsedAtByUserIDs(ctx context.Context, userIDs []int64) (map[int64]*time.Time, error)
	GetLatestUsedAtByUserID(ctx context.Context, userID int64) (*time.Time, error)
	UpdateUserLastActiveAt(ctx context.Context, userID int64, activeAt time.Time) error

	AddBalance(ctx context.Context, id int64, amount float64) error
	UpdateBalance(ctx context.Context, id int64, amount float64) error
	DeductBalance(ctx context.Context, id int64, amount float64) (float64, error)
	// AdjustBalance 原子地把 delta 累加到余额上，并返回变更前后的值。结果为负时
	// 拒绝写入并返回 ErrBalanceNegative。管理员的加/扣款必须走这里而不是
	// "读余额→算新值→整行写回"，否则并发的计费扣款会被旧快照抹掉。
	AdjustBalance(ctx context.Context, id int64, delta float64) (BalanceChange, error)
	// SetBalance 原子地把余额置为 value（value 必须 >= 0），返回变更前后的值。
	SetBalance(ctx context.Context, id int64, value float64) (BalanceChange, error)
	UpdateConcurrency(ctx context.Context, id int64, amount int) error
	// BatchSetConcurrency 批量设置用户并发数，负数会按 0 处理。
	BatchSetConcurrency(ctx context.Context, userIDs []int64, value int) (int, error)
	// BatchAddConcurrency 批量增减用户并发数，结果不会低于 0。
	BatchAddConcurrency(ctx context.Context, userIDs []int64, delta int) (int, error)
	// BatchUpdateLimits 在一次写入中只覆盖非 nil 的用户限制字段。
	BatchUpdateLimits(ctx context.Context, userIDs []int64, concurrency, rpmLimit *int) (int, error)
	ExistsByEmail(ctx context.Context, email string) (bool, error)
	ExistsByNormalizedEmail(ctx context.Context, normalizedEmail string) (bool, error)
	LockRegistrationEmail(ctx context.Context, normalizedEmail string) error
	RemoveGroupFromAllowedGroups(ctx context.Context, groupID int64) (int64, error)
	// AddGroupToAllowedGroups 将指定分组增量添加到用户的 allowed_groups（幂等，冲突忽略）
	AddGroupToAllowedGroups(ctx context.Context, userID int64, groupID int64) error
	// RemoveGroupFromUserAllowedGroups 移除单个用户的指定分组权限
	RemoveGroupFromUserAllowedGroups(ctx context.Context, userID int64, groupID int64) error
	ListUserAuthIdentities(ctx context.Context, userID int64) ([]UserAuthIdentityRecord, error)
	UnbindUserAuthProvider(ctx context.Context, userID int64, provider string) error

	// TOTP 双因素认证
	UpdateTotpSecret(ctx context.Context, userID int64, encryptedSecret *string) error
	EnableTotp(ctx context.Context, userID int64) error
	DisableTotp(ctx context.Context, userID int64) error
}

// RegistrationEmailDomainRepository 为非白名单域名单账户策略提供原子仓储能力。
// 独立成窄接口，避免注册专用方法扩散到所有 UserRepository 测试桩和消费者。
type RegistrationEmailDomainRepository interface {
	CountUsersByEmailDomain(ctx context.Context, domain string) (int, error)
	CreateWithRegistrationEmailGuards(ctx context.Context, user *User, normalizedEmail, domain string) error
}

type RedeemUserAdjustmentRepository = identity.RedeemUserAdjustmentRepository

type UserAuthIdentityRecord = identity.UserAuthIdentityRecord

type UserIdentitySummary = identity.UserIdentitySummary

type UserIdentitySummarySet = identity.UserIdentitySummarySet

type StartUserIdentityBindingRequest = identity.StartUserIdentityBindingRequest

type StartUserIdentityBindingResult = identity.StartUserIdentityBindingResult

type UpdateProfileRequest = identity.UpdateProfileRequest

type UserAvatar = identity.UserAvatar

type UpsertUserAvatarInput = identity.UpsertUserAvatarInput

type ChangePasswordRequest = identity.ChangePasswordRequest

type UserService struct {
	*identity.UserService
}

func NewUserService(userRepo UserRepository, settingRepo SettingRepository, authCacheInvalidator APIKeyAuthCacheInvalidator, billingCache BillingCache) *UserService {
	return &UserService{UserService: identity.NewUserService(IdentityRepository(userRepo), settingRepo, authCacheInvalidator, billingCache, RunBackgroundTask)}
}

// GetFirstAdmin 委托身份资料实现。
func (s *UserService) GetFirstAdmin(ctx context.Context) (*User, error) {
	u, err := s.UserService.GetFirstAdmin(ctx)
	return UserFromIdentity(u), err
}

// GetProfile 委托身份资料实现。
func (s *UserService) GetProfile(ctx context.Context, userID int64) (*User, error) {
	u, err := s.UserService.GetProfile(ctx, userID)
	return UserFromIdentity(u), err
}

// GetProfileIdentitySummaries 委托身份资料实现。
func (s *UserService) GetProfileIdentitySummaries(ctx context.Context, userID int64, user *User) (UserIdentitySummarySet, error) {
	return s.UserService.GetProfileIdentitySummaries(ctx, userID, IdentityUser(user))
}

// PrepareIdentityBindingStart 委托身份资料实现。
func (s *UserService) PrepareIdentityBindingStart(ctx context.Context, req StartUserIdentityBindingRequest) (*StartUserIdentityBindingResult, error) {
	return s.UserService.PrepareIdentityBindingStart(ctx, req)
}

// UnbindUserAuthProvider 委托身份资料实现。
func (s *UserService) UnbindUserAuthProvider(ctx context.Context, userID int64, provider string) (*User, error) {
	u, err := s.UserService.UnbindUserAuthProvider(ctx, userID, provider)
	return UserFromIdentity(u), err
}

// UnbindUserAuthProviderWithResult 委托身份资料实现。
func (s *UserService) UnbindUserAuthProviderWithResult(ctx context.Context, userID int64, provider string) (*User, bool, error) {
	u, changed, err := s.UserService.UnbindUserAuthProviderWithResult(ctx, userID, provider)
	return UserFromIdentity(u), changed, err
}

// UpdateProfile 委托身份资料实现。
func (s *UserService) UpdateProfile(ctx context.Context, userID int64, req UpdateProfileRequest) (*User, error) {
	u, err := s.UserService.UpdateProfile(ctx, userID, req)
	return UserFromIdentity(u), err
}

// SetAvatar 委托身份资料实现。
func (s *UserService) SetAvatar(ctx context.Context, userID int64, raw string) (*UserAvatar, error) {
	return s.UserService.SetAvatar(ctx, userID, raw)
}

// ValidateUserAvatar 委托身份资料实现。
func ValidateUserAvatar(raw string) error { return identity.ValidateUserAvatar(raw) }

// ChangePassword 委托身份资料实现。
func (s *UserService) ChangePassword(ctx context.Context, userID int64, req ChangePasswordRequest) error {
	return s.UserService.ChangePassword(ctx, userID, req)
}

// GetByID 委托身份资料实现。
func (s *UserService) GetByID(ctx context.Context, id int64) (*User, error) {
	u, err := s.UserService.GetByID(ctx, id)
	return UserFromIdentity(u), err
}

// TouchLastActive 委托身份资料实现。
func (s *UserService) TouchLastActive(ctx context.Context, userID int64) {
	s.UserService.TouchLastActive(ctx, userID)
}

// TouchLastActiveForUser 委托身份资料实现。
func (s *UserService) TouchLastActiveForUser(ctx context.Context, user *User) {
	s.UserService.TouchLastActiveForUser(ctx, IdentityUser(user))
}

// List 委托身份资料实现。
func (s *UserService) List(ctx context.Context, params pagination.PaginationParams) ([]User, *pagination.PaginationResult, error) {
	v, page, err := s.UserService.List(ctx, params)
	if v == nil {
		return nil, page, err
	}
	out := make([]User, len(v))
	for i := range v {
		out[i] = *UserFromIdentity(&v[i])
	}
	return out, page, err
}

// UpdateBalance 委托身份资料实现。
func (s *UserService) UpdateBalance(ctx context.Context, userID int64, amount float64) error {
	return s.UserService.UpdateBalance(ctx, userID, amount)
}

// UpdateConcurrency 委托身份资料实现。
func (s *UserService) UpdateConcurrency(ctx context.Context, userID int64, concurrency int) error {
	return s.UserService.UpdateConcurrency(ctx, userID, concurrency)
}

// UpdateStatus 委托身份资料实现。
func (s *UserService) UpdateStatus(ctx context.Context, userID int64, status string) error {
	return s.UserService.UpdateStatus(ctx, userID, status)
}

// Delete 委托身份资料实现。
func (s *UserService) Delete(ctx context.Context, userID int64) error {
	return s.UserService.Delete(ctx, userID)
}

// SendNotifyEmailCode 委托身份资料实现。
func (s *UserService) SendNotifyEmailCode(ctx context.Context, userID int64, email string, emailService *EmailService, cache EmailCache, locale ...string) error {
	return s.UserService.SendNotifyEmailCode(ctx, userID, email, identityNotifySender{emailService}, cache, locale...)
}

// VerifyAndAddNotifyEmail 委托身份资料实现。
func (s *UserService) VerifyAndAddNotifyEmail(ctx context.Context, userID int64, email, code string, cache EmailCache) error {
	return s.UserService.VerifyAndAddNotifyEmail(ctx, userID, email, code, cache)
}

// RemoveNotifyEmail 委托身份资料实现。
func (s *UserService) RemoveNotifyEmail(ctx context.Context, userID int64, email string) error {
	return s.UserService.RemoveNotifyEmail(ctx, userID, email)
}

// ToggleNotifyEmail 委托身份资料实现。
func (s *UserService) ToggleNotifyEmail(ctx context.Context, userID int64, email string, disabled bool) error {
	return s.UserService.ToggleNotifyEmail(ctx, userID, email, disabled)
}

// notifyVerifyEmailTemplate is the HTML template for notify email verification.
// Format args: siteName, code.
const notifyVerifyEmailTemplate = `<!DOCTYPE html>
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
            <p style="font-size: 18px; color: #333;">通知邮箱验证码 / Notification Email Verification</p>
            <div class="code">%s</div>
            <div class="info">
                <p>您正在添加额外的通知邮箱，请输入此验证码完成验证。</p>
                <p>You are adding an extra notification email. Please enter this code to verify.</p>
                <p>此验证码将在 <strong>15 分钟</strong>后失效。</p>
                <p>This code will expire in <strong>15 minutes</strong>.</p>
                <p>如果您没有请求此验证码，请忽略此邮件。</p>
                <p>If you did not request this code, please ignore this email.</p>
            </div>
        </div>
        <div class="footer">
            <p>此邮件由系统自动发送，请勿回复。/ This is an automated message, please do not reply.</p>
        </div>
    </div>
</body>
</html>`

// buildNotifyVerifyEmailBody builds the HTML email body for notify email verification.
func buildNotifyVerifyEmailBody(code, siteName string) string {
	return fmt.Sprintf(notifyVerifyEmailTemplate, siteName, code)
}

// identityNotifySender 保留旧邮件模板与失败回退，S10 改绑通知模块。
type identityNotifySender struct{ Service *EmailService }

func (s identityNotifySender) GenerateVerifyCode() (string, error) {
	return s.Service.GenerateVerifyCode()
}
func (s identityNotifySender) SendNotifyVerification(ctx context.Context, n identity.NotifyVerificationNotice) error {
	emailService := s.Service
	userID, email, code, locale, siteName := n.UserID, n.Email, n.Code, n.Locale, n.SiteName

	if emailService.notificationEmailService != nil {
		if err := emailService.notificationEmailService.Send(ctx, NotificationEmailSendInput{
			Event:          NotificationEmailEventNotificationEmailVerifyCode,
			Locale:         locale,
			RecipientEmail: email,
			RecipientName:  emailRecipientName(email),
			UserID:         userID,
			Variables: map[string]string{
				"verification_code":  code,
				"expires_in_minutes": strconv.Itoa(int(verifyCodeTTL / time.Minute)),
			},
		}); err == nil {
			return nil
		} else {
			if !shouldFallbackNotificationEmail(err) {
				return err
			}
			slog.Warn("template notification email verification failed; falling back to built-in body", "recipient_hash", notificationEmailHash(email), "err", err.Error())
		}
	}
	subject := fmt.Sprintf("[%s] 通知邮箱验证码 / Notification Email Verification", siteName)
	body := buildNotifyVerifyEmailBody(code, siteName)
	return emailService.SendEmail(ctx, email, subject, body)

}

// IdentityNotifySender 暴露现有通知投影，邮件模板仍由通知能力拥有。
func IdentityNotifySender(email *EmailService) identity.NotifyVerificationSender {
	return identityNotifySender{email}
}
