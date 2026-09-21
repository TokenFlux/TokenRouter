//go:build unit

package identity_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	identitytestkit "github.com/TokenFlux/TokenRouter/internal/identity/testkit"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/notification/smtp"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/stretchr/testify/require"
)

type settingRepoStub struct {
	mu               sync.Mutex
	values           map[string]string
	err              error
	getValueCalls    int
	getMultipleCalls int
}

func (s *settingRepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

func (s *settingRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getValueCalls++
	if s.err != nil {
		return "", s.err
	}
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", settingscore.ErrSettingNotFound
}

func (s *settingRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

func (s *settingRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getMultipleCalls++
	if s.err != nil {
		return nil, s.err
	}
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if v, ok := s.values[key]; ok {
			result[key] = v
		}
	}
	return result, nil
}

func (s *settingRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	panic("unexpected SetMultiple call")
}

func (s *settingRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *settingRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

type emailCacheStub struct {
	data *identity.VerificationCodeData
	err  error
}

type refreshTokenCacheStub struct{}

type userPlatformQuotaRepoStub struct {
	bulkInsertCalls [][]billing.UserPlatformQuotaRecord
	bulkInsertErr   error
}

func (s *userPlatformQuotaRepoStub) BulkInsertInitial(_ context.Context, records []billing.UserPlatformQuotaRecord) error {
	cloned := make([]billing.UserPlatformQuotaRecord, len(records))
	copy(cloned, records)
	s.bulkInsertCalls = append(s.bulkInsertCalls, cloned)
	return s.bulkInsertErr
}

func (s *userPlatformQuotaRepoStub) GetByUserPlatform(context.Context, int64, string) (*billing.UserPlatformQuotaRecord, error) {
	panic("unexpected GetByUserPlatform call")
}

func (s *userPlatformQuotaRepoStub) ListByUser(context.Context, int64) ([]billing.UserPlatformQuotaRecord, error) {
	panic("unexpected ListByUser call")
}

func (s *userPlatformQuotaRepoStub) IncrementUsageWithReset(context.Context, int64, string, float64, time.Time) error {
	panic("unexpected IncrementUsageWithReset call")
}

func (s *userPlatformQuotaRepoStub) UpsertForUser(context.Context, int64, []billing.UserPlatformQuotaRecord) error {
	panic("unexpected UpsertForUser call")
}

func (s *userPlatformQuotaRepoStub) ResetExpiredWindow(context.Context, int64, string, string, time.Time) error {
	panic("unexpected ResetExpiredWindow call")
}

func (s *userPlatformQuotaRepoStub) BatchSnapshotUsage(_ context.Context, _ []billing.UserPlatformQuotaSnapshot, _ time.Time) error {
	return nil
}

func (s *refreshTokenCacheStub) StoreRefreshToken(context.Context, string, *identity.RefreshTokenData, time.Duration) error {
	return nil
}

func (s *refreshTokenCacheStub) GetRefreshToken(context.Context, string) (*identity.RefreshTokenData, error) {
	return nil, identity.ErrRefreshTokenNotFound
}

func (s *refreshTokenCacheStub) DeleteRefreshToken(context.Context, string) error {
	return nil
}

func (s *refreshTokenCacheStub) DeleteUserRefreshTokens(context.Context, int64) error {
	return nil
}

func (s *refreshTokenCacheStub) DeleteTokenFamily(context.Context, string) error {
	return nil
}

func (s *refreshTokenCacheStub) AddToUserTokenSet(context.Context, int64, string, time.Duration) error {
	return nil
}

func (s *refreshTokenCacheStub) AddToFamilyTokenSet(context.Context, string, string, time.Duration) error {
	return nil
}

func (s *refreshTokenCacheStub) GetUserTokenHashes(context.Context, int64) ([]string, error) {
	return nil, nil
}

func (s *refreshTokenCacheStub) GetFamilyTokenHashes(context.Context, string) ([]string, error) {
	return nil, nil
}

func (s *refreshTokenCacheStub) IsTokenInFamily(context.Context, string, string) (bool, error) {
	return false, nil
}

func (s *emailCacheStub) GetVerificationCode(ctx context.Context, email string) (*identity.VerificationCodeData, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.data, nil
}

func (s *emailCacheStub) SetVerificationCode(ctx context.Context, email string, data *identity.VerificationCodeData, ttl time.Duration) error {
	return nil
}

func (s *emailCacheStub) DeleteVerificationCode(ctx context.Context, email string) error {
	return nil
}

func (s *emailCacheStub) GetNotifyVerifyCode(ctx context.Context, email string) (*identity.VerificationCodeData, error) {
	return nil, nil
}

func (s *emailCacheStub) SetNotifyVerifyCode(ctx context.Context, email string, data *identity.VerificationCodeData, ttl time.Duration) error {
	return nil
}

func (s *emailCacheStub) DeleteNotifyVerifyCode(ctx context.Context, email string) error {
	return nil
}

func (s *emailCacheStub) GetPasswordResetToken(ctx context.Context, email string) (*identity.PasswordResetTokenData, error) {
	return nil, nil
}

func (s *emailCacheStub) SetPasswordResetToken(ctx context.Context, email string, data *identity.PasswordResetTokenData, ttl time.Duration) error {
	return nil
}

func (s *emailCacheStub) DeletePasswordResetToken(ctx context.Context, email string) error {
	return nil
}

func (s *emailCacheStub) IsPasswordResetEmailInCooldown(ctx context.Context, email string) bool {
	return false
}

func (s *emailCacheStub) SetPasswordResetEmailCooldown(ctx context.Context, email string, ttl time.Duration) error {
	return nil
}

func (s *emailCacheStub) GetNotifyCodeUserRate(ctx context.Context, userID int64) (int64, error) {
	return 0, nil
}

func (s *emailCacheStub) IncrNotifyCodeUserRate(ctx context.Context, userID int64, window time.Duration) (int64, error) {
	return 0, nil
}

func newAuthService(repo *userRepoStub, settings map[string]string, emailCache identity.EmailCache, quotaRepo billing.UserPlatformQuotaRepository) *identity.AuthService {
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:     "test-secret",
			ExpireHour: 1,
		},
		Default: config.DefaultConfig{
			UserBalance:     3.5,
			UserConcurrency: 2,
		},
	}

	var settingService *authSettingsFixture
	if settings != nil {
		settingService = newAuthSettingsFixture(&settingRepoStub{values: settings}, cfg)
	}

	var emailService *identity.EmailChallenges
	if emailCache != nil {
		emailService = identity.NewEmailChallenges(emailCache, notification.NewMailer(&settingRepoStub{values: settings}, smtp.New()))
	}

	return identitytestkit.Auth(
		nil, &identity. // entClient
				AuthDependencies{Users: repo, Options: identitytestkit.
			// redeemRepo
			AuthOptions(

				// refreshTokenCache
				cfg), Settings: authSettingsPort(settingService), Email: identitytestkit.Email(emailService), Quotas:

		// promoService
		// defaultSubAssigner
		// affiliateService
		quotaRepo},
	)
}

func TestAuthService_Register_Disabled(t *testing.T) {
	repo := &userRepoStub{}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "false",
	}, nil, nil)

	_, _, err := service.Register(context.Background(), "user@test.com", "password")
	require.ErrorIs(t, err, identity.ErrRegDisabled)
}

func TestAuthService_Register_DisabledByDefault(t *testing.T) {
	// 当 settings 为 nil（设置项不存在）时，注册应该默认关闭
	repo := &userRepoStub{}
	service := newAuthService(repo, nil, nil, nil)

	_, _, err := service.Register(context.Background(), "user@test.com", "password")
	require.ErrorIs(t, err, identity.ErrRegDisabled)
}

func TestAuthService_Register_SnapshotsPlatformQuotaDefaults(t *testing.T) {
	repo := &userRepoStub{nextID: 77}
	quotaRepo := &userPlatformQuotaRepoStub{}

	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:  "true",
		billing.SettingKeyDefaultPlatformQuotas: `{"openai": {"weekly": 12.34}}`,
	}, nil, quotaRepo)

	_, user, err := service.Register(context.Background(), "newuser@test.com", "password")
	require.NoError(t, err)
	require.NotNil(t, user)

	require.Len(t, quotaRepo.bulkInsertCalls, 1)

	records := quotaRepo.bulkInsertCalls[0]
	var openaiRecord *billing.UserPlatformQuotaRecord
	for i := range records {
		if records[i].Platform == "openai" {
			openaiRecord = &records[i]
			break
		}
	}
	require.NotNil(t, openaiRecord, "expected openai platform record")
	require.Equal(t, int64(77), openaiRecord.UserID)
	require.NotNil(t, openaiRecord.WeeklyLimitUSD)
	require.InDelta(t, 12.34, *openaiRecord.WeeklyLimitUSD, 0.0001)
}

func TestAuthService_Register_DoesNotSnapshotOnDisabled(t *testing.T) {
	repo := &userRepoStub{}
	quotaRepo := &userPlatformQuotaRepoStub{}

	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "false",
	}, nil, quotaRepo)

	_, _, err := service.Register(context.Background(), "user@test.com", "password")
	require.ErrorIs(t, err, identity.ErrRegDisabled)

	require.Empty(t, quotaRepo.bulkInsertCalls, "registration rejected before user creation must not snapshot")
}

func TestAuthService_Register_EmailVerifyEnabledButServiceNotConfigured(t *testing.T) {
	repo := &userRepoStub{}
	// 邮件验证开启但 emailCache 为 nil（emailService 未配置）
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "true",
		identity.SettingKeyEmailVerifyEnabled:  "true",
	}, nil, nil)

	// 应返回服务不可用错误，而不是允许绕过验证
	_, _, err := service.RegisterWithVerification(context.Background(), "user@test.com", "password", "any-code", "", "", "")
	require.ErrorIs(t, err, identity.ErrServiceUnavailable)
}

func TestAuthService_Register_EmailVerifyRequired(t *testing.T) {
	repo := &userRepoStub{}
	cache := &emailCacheStub{} // 配置 emailService
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "true",
		identity.SettingKeyEmailVerifyEnabled:  "true",
	}, cache, nil)

	_, _, err := service.RegisterWithVerification(context.Background(), "user@test.com", "password", "", "", "", "")
	require.ErrorIs(t, err, identity.ErrEmailVerifyRequired)
}

func TestAuthService_Register_EmailVerifyInvalid(t *testing.T) {
	repo := &userRepoStub{}
	cache := &emailCacheStub{
		data: &identity.VerificationCodeData{Code: "expected", Attempts: 0},
	}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "true",
		identity.SettingKeyEmailVerifyEnabled:  "true",
	}, cache, nil)

	_, _, err := service.RegisterWithVerification(context.Background(), "user@test.com", "password", "wrong", "", "", "")
	require.ErrorIs(t, err, identity.ErrInvalidVerifyCode)
	require.ErrorContains(t, err, "verify code")
}

func TestAuthService_Register_EmailExists(t *testing.T) {
	repo := &userRepoStub{exists: true}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "true",
	}, nil, nil)

	_, _, err := service.Register(context.Background(), "user@test.com", "password")
	require.ErrorIs(t, err, identity.ErrEmailExists)
}

func TestAuthService_Register_CheckEmailError(t *testing.T) {
	repo := &userRepoStub{existsErr: errors.New("db down")}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "true",
	}, nil, nil)

	_, _, err := service.Register(context.Background(), "user@test.com", "password")
	require.ErrorIs(t, err, identity.ErrServiceUnavailable)
}

func TestAuthService_Register_ReservedEmail(t *testing.T) {
	repo := &userRepoStub{}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "true",
	}, nil, nil)

	_, _, err := service.Register(context.Background(), "linuxdo-123@linuxdo-connect.invalid", "password")
	require.ErrorIs(t, err, identity.ErrEmailReserved)
}

func TestAuthService_Register_EmailDomainRegistrationLimit(t *testing.T) {
	repo := &userRepoStub{domainCounts: map[string]int{"other.com": 1}}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		identity.SettingKeyRegistrationEmailSuffixWhitelist:    `["@example.com","@company.com"]`,
		identity.SettingKeyRegistrationEmailDomainQuotaEnabled: "true",
	}, nil, nil)

	_, _, err := service.Register(context.Background(), "user@other.com", "password")
	require.ErrorIs(t, err, identity.ErrEmailDomainRegistrationLimit)
	appErr := apperror.FromError(err)
	require.Equal(t, "EMAIL_DOMAIN_REGISTRATION_LIMIT", appErr.Reason)
}

func TestAuthService_Register_NonWhitelistDomainAllowsFirstAccount(t *testing.T) {
	repo := &userRepoStub{nextID: 9, domainCounts: map[string]int{"custom.example": 0}}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		identity.SettingKeyRegistrationEmailSuffixWhitelist:    `["@example.com"]`,
		identity.SettingKeyRegistrationEmailDomainQuotaEnabled: "true",
	}, nil, nil)

	_, user, err := service.Register(context.Background(), "first@sub.custom.example", "password")
	require.NoError(t, err)
	require.Equal(t, int64(9), user.ID)
	require.Equal(t, []string{"custom.example"}, repo.domainGuardCalls)
}

func TestAuthService_Register_NonWhitelistDomainRejectedWhenQuotaDisabledByDefault(t *testing.T) {
	repo := &userRepoStub{nextID: 9, domainCounts: map[string]int{"custom.example": 0}}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:              "true",
		identity.SettingKeyRegistrationEmailSuffixWhitelist: `["@example.com"]`,
	}, nil, nil)

	_, _, err := service.Register(context.Background(), "first@custom.example", "password")
	require.ErrorIs(t, err, identity.ErrEmailSuffixNotAllowed)
	require.Equal(t, "EMAIL_SUFFIX_NOT_ALLOWED", apperror.FromError(err).Reason)
	require.Empty(t, repo.created)
	require.Empty(t, repo.domainGuardCalls)
}

func TestAuthService_Register_NonWhitelistDomainRejectedWhenQuotaExplicitlyDisabled(t *testing.T) {
	repo := &userRepoStub{domainCounts: map[string]int{"custom.example": 0}}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		identity.SettingKeyRegistrationEmailSuffixWhitelist:    `["@example.com"]`,
		identity.SettingKeyRegistrationEmailDomainQuotaEnabled: "false",
	}, nil, nil)

	_, _, err := service.Register(context.Background(), "first@custom.example", "password")
	require.ErrorIs(t, err, identity.ErrEmailSuffixNotAllowed)
}

// 预检通过后管理员关闭开关时，最终创建阶段必须重新读取设置并恢复严格白名单。
func TestAuthService_CreateRegisteredUser_RechecksDomainQuotaSwitch(t *testing.T) {
	ctx := context.Background()
	settings := map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		identity.SettingKeyRegistrationEmailSuffixWhitelist:    `["@example.com"]`,
		identity.SettingKeyRegistrationEmailDomainQuotaEnabled: "true",
	}
	repo := &userRepoStub{nextID: 9, domainCounts: map[string]int{"custom.example": 0}}
	cfg := &config.Config{Default: config.DefaultConfig{UserConcurrency: 1}}
	settingService := newAuthSettingsFixture(&settingRepoStub{values: settings}, cfg)
	service := identitytestkit.Auth(
		nil, &identity.AuthDependencies{Users: repo, Options: identitytestkit.AuthOptions(cfg), Settings: authSettingsPort(settingService)},
	)

	require.NoError(t, service.AuthValidateRegistrationEmailQuota(ctx, "first@custom.example"))
	settings[identity.SettingKeyRegistrationEmailDomainQuotaEnabled] = "false"

	err := service.AuthCreateRegisteredUser(ctx, &identity.User{Email: "first@custom.example"}, &identity.AuthRegistrationArtifacts{
		EnforceEmailDomainQuota: true,
	})
	require.ErrorIs(t, err, identity.ErrEmailSuffixNotAllowed)
	require.Empty(t, repo.created)
	require.Empty(t, repo.domainGuardCalls)
}

func TestAuthService_Register_EmailSuffixAllowed(t *testing.T) {
	repo := &userRepoStub{nextID: 8}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:              "true",
		identity.SettingKeyRegistrationEmailSuffixWhitelist: `["example.com"]`,
	}, nil, nil)

	_, user, err := service.Register(context.Background(), "user@example.com", "password")
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, int64(8), user.ID)
}

func TestAuthService_SendVerifyCode_EmailDomainRegistrationLimit(t *testing.T) {
	repo := &userRepoStub{domainCounts: map[string]int{"other.com": 1}}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		identity.SettingKeyRegistrationEmailSuffixWhitelist:    `["@example.com","@company.com"]`,
		identity.SettingKeyRegistrationEmailDomainQuotaEnabled: "true",
	}, nil, nil)

	err := service.SendVerifyCode(context.Background(), "user@other.com")
	require.ErrorIs(t, err, identity.ErrEmailDomainRegistrationLimit)
	appErr := apperror.FromError(err)
	require.Equal(t, "EMAIL_DOMAIN_REGISTRATION_LIMIT", appErr.Reason)
}

func TestAuthService_SendVerifyCode_NonWhitelistDomainRejectedWhenQuotaDisabled(t *testing.T) {
	repo := &userRepoStub{domainCounts: map[string]int{"custom.example": 0}}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:              "true",
		identity.SettingKeyRegistrationEmailSuffixWhitelist: `["@example.com"]`,
	}, nil, nil)

	err := service.SendVerifyCode(context.Background(), "user@custom.example")
	require.ErrorIs(t, err, identity.ErrEmailSuffixNotAllowed)
}

func TestAuthService_SendVerifyCodeAsync_NonWhitelistDomainRejectedWhenQuotaDisabled(t *testing.T) {
	repo := &userRepoStub{domainCounts: map[string]int{"custom.example": 0}}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:              "true",
		identity.SettingKeyRegistrationEmailSuffixWhitelist: `["@example.com"]`,
	}, nil, nil)

	_, err := service.SendVerifyCodeAsync(context.Background(), "user@custom.example")
	require.ErrorIs(t, err, identity.ErrEmailSuffixNotAllowed)
}

func TestAuthService_Register_CreateError(t *testing.T) {
	repo := &userRepoStub{createErr: errors.New("create failed")}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "true",
	}, nil, nil)

	_, _, err := service.Register(context.Background(), "user@test.com", "password")
	require.ErrorIs(t, err, identity.ErrServiceUnavailable)
}

func TestAuthService_Register_CreateEmailExistsRace(t *testing.T) {
	// 模拟竞态条件：ExistsByEmail 返回 false，但 Create 时因唯一约束失败
	repo := &userRepoStub{createErr: identity.ErrEmailExists}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "true",
	}, nil, nil)

	_, _, err := service.Register(context.Background(), "user@test.com", "password")
	require.ErrorIs(t, err, identity.ErrEmailExists)
}

func TestAuthService_Register_Success(t *testing.T) {
	repo := &userRepoStub{nextID: 5}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		identity.SettingKeyAuthSourceDefaultEmailGrantOnSignup: "false",
	}, nil, nil)

	token, user, err := service.Register(context.Background(), "user@test.com", "password")
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.NotNil(t, user)
	require.Equal(t, int64(5), user.ID)
	require.Equal(t, "user@test.com", user.Email)
	require.Equal(t, identity.RoleUser, user.Role)
	require.Equal(t, billing.StatusActive, user.Status)
	require.Equal(t, 3.5, user.Balance)
	require.Equal(t, 2, user.Concurrency)
	require.Len(t, repo.created, 1)
	require.True(t, user.CheckPassword("password"))
}

func TestAuthService_ValidateToken_ExpiredReturnsClaimsWithError(t *testing.T) {
	repo := &userRepoStub{}
	service := newAuthService(repo, nil, nil, nil)

	// 创建用户并生成 token
	user := &identity.User{
		ID:           1,
		Email:        "test@test.com",
		Role:         identity.RoleUser,
		Status:       billing.StatusActive,
		TokenVersion: 1,
	}
	token, err := service.GenerateToken(context.Background(), user)
	require.NoError(t, err)

	// 验证有效 token
	claims, err := service.ValidateToken(token)
	require.NoError(t, err)
	require.NotNil(t, claims)
	require.Equal(t, int64(1), claims.UserID)

	// 模拟过期 token（通过创建一个过期很久的 token）
	service.Options.JWT.ExpireHour = -1 // 设置为负数使 token 立即过期
	rebuildOriginalSession(service)
	expiredToken, err := service.GenerateToken(context.Background(), user)
	require.NoError(t, err)
	service.Options.JWT.ExpireHour = 1 // 恢复
	rebuildOriginalSession(service)

	// 验证过期 token 应返回 claims 和 ErrTokenExpired
	claims, err = service.ValidateToken(expiredToken)
	require.ErrorIs(t, err, identity.ErrTokenExpired)
	require.NotNil(t, claims, "claims should not be nil when token is expired")
	require.Equal(t, int64(1), claims.UserID)
	require.Equal(t, "test@test.com", claims.Email)
}

func TestAuthService_RefreshToken_ExpiredTokenNoPanic(t *testing.T) {
	user := &identity.User{
		ID:           1,
		Email:        "test@test.com",
		Role:         identity.RoleUser,
		Status:       billing.StatusActive,
		TokenVersion: 1,
	}
	repo := &userRepoStub{user: user}
	service := newAuthService(repo, nil, nil, nil)

	// 创建过期 token
	service.Options.JWT.ExpireHour = -1
	rebuildOriginalSession(service)
	expiredToken, err := service.GenerateToken(context.Background(), user)
	require.NoError(t, err)
	service.Options.JWT.ExpireHour = 1
	rebuildOriginalSession(service)

	// RefreshToken 使用过期 token 不应 panic
	require.NotPanics(t, func() {
		newToken, err := service.RefreshToken(context.Background(), expiredToken)
		require.NoError(t, err)
		require.NotEmpty(t, newToken)
	})
}

func TestAuthService_GetAccessTokenExpiresIn_FallbackToExpireHour(t *testing.T) {
	service := newAuthService(&userRepoStub{}, nil, nil, nil)
	service.Options.JWT.ExpireHour = 24
	rebuildOriginalSession(service)
	service.Options.JWT.AccessTokenExpireMinutes = 0
	rebuildOriginalSession(service)

	require.Equal(t, 24*3600, service.GetAccessTokenExpiresIn())
}

func TestAuthService_GetAccessTokenExpiresIn_MinutesHasPriority(t *testing.T) {
	service := newAuthService(&userRepoStub{}, nil, nil, nil)
	service.Options.JWT.ExpireHour = 24
	rebuildOriginalSession(service)
	service.Options.JWT.AccessTokenExpireMinutes = 90
	rebuildOriginalSession(service)

	require.Equal(t, 90*60, service.GetAccessTokenExpiresIn())
}

func TestAuthService_GenerateToken_UsesExpireHourWhenMinutesZero(t *testing.T) {
	service := newAuthService(&userRepoStub{}, nil, nil, nil)
	service.Options.JWT.ExpireHour = 24
	rebuildOriginalSession(service)
	service.Options.JWT.AccessTokenExpireMinutes = 0
	rebuildOriginalSession(service)

	user := &identity.User{
		ID:           1,
		Email:        "test@test.com",
		Role:         identity.RoleUser,
		Status:       billing.StatusActive,
		TokenVersion: 1,
	}

	token, err := service.GenerateToken(context.Background(), user)
	require.NoError(t, err)

	claims, err := service.ValidateToken(token)
	require.NoError(t, err)
	require.NotNil(t, claims)
	require.NotNil(t, claims.IssuedAt)
	require.NotNil(t, claims.ExpiresAt)

	require.WithinDuration(t, claims.IssuedAt.Add(24*time.Hour), claims.ExpiresAt.Time, 2*time.Second)
}

func TestAuthService_GenerateToken_UsesMinutesWhenConfigured(t *testing.T) {
	service := newAuthService(&userRepoStub{}, nil, nil, nil)
	service.Options.JWT.ExpireHour = 24
	rebuildOriginalSession(service)
	service.Options.JWT.AccessTokenExpireMinutes = 90
	rebuildOriginalSession(service)

	user := &identity.User{
		ID:           2,
		Email:        "test2@test.com",
		Role:         identity.RoleUser,
		Status:       billing.StatusActive,
		TokenVersion: 1,
	}

	token, err := service.GenerateToken(context.Background(), user)
	require.NoError(t, err)

	claims, err := service.ValidateToken(token)
	require.NoError(t, err)
	require.NotNil(t, claims)
	require.NotNil(t, claims.IssuedAt)
	require.NotNil(t, claims.ExpiresAt)

	require.WithinDuration(t, claims.IssuedAt.Add(90*time.Minute), claims.ExpiresAt.Time, 2*time.Second)
}

func TestAuthService_Register_AssignsDefaultSubscriptions(t *testing.T) {
	repo := &userRepoStub{nextID: 42}
	assigner := &defaultSubscriptionAssignerStub{}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		billing.SettingKeyDefaultSubscriptions:                 `[{"plan_id":11},{"plan_id":12}]`,
		identity.SettingKeyAuthSourceDefaultEmailGrantOnSignup: "false",
	}, nil, nil)
	service.DefaultSubscriptions = assigner

	_, user, err := service.Register(context.Background(), "default-sub@test.com", "password")
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Len(t, assigner.calls, 2)
	require.Equal(t, int64(42), assigner.calls[0].UserID)
	require.Equal(t, int64(11), assigner.calls[0].PlanID)
	require.Equal(t, int64(12), assigner.calls[1].PlanID)
}

func TestAuthService_Register_UsesEmailAuthSourceDefaultsWhenGrantEnabled(t *testing.T) {
	repo := &userRepoStub{nextID: 52}
	assigner := &defaultSubscriptionAssignerStub{}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		billing.SettingKeyDefaultSubscriptions:                 `[{"plan_id":91}]`,
		identity.SettingKeyAuthSourceDefaultEmailBalance:       "12.5",
		identity.SettingKeyAuthSourceDefaultEmailConcurrency:   "7",
		identity.SettingKeyAuthSourceDefaultEmailSubscriptions: `[{"plan_id":11}]`,
		identity.SettingKeyAuthSourceDefaultEmailGrantOnSignup: "true",
	}, nil, nil)
	service.DefaultSubscriptions = assigner

	_, user, err := service.Register(context.Background(), "email-defaults@test.com", "password")
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, 12.5, user.Balance)
	require.Equal(t, 7, user.Concurrency)
	require.Len(t, assigner.calls, 1)
	require.Equal(t, int64(11), assigner.calls[0].PlanID)
}

func TestAuthService_Register_GrantOnSignupFalseFallsBackToGlobalDefaults(t *testing.T) {
	repo := &userRepoStub{nextID: 53}
	assigner := &defaultSubscriptionAssignerStub{}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		billing.SettingKeyDefaultSubscriptions:                 `[{"plan_id":31}]`,
		identity.SettingKeyAuthSourceDefaultEmailBalance:       "99",
		identity.SettingKeyAuthSourceDefaultEmailConcurrency:   "88",
		identity.SettingKeyAuthSourceDefaultEmailSubscriptions: `[{"plan_id":32}]`,
		identity.SettingKeyAuthSourceDefaultEmailGrantOnSignup: "false",
	}, nil, nil)
	service.DefaultSubscriptions = assigner

	_, user, err := service.Register(context.Background(), "email-global@test.com", "password")
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, 3.5, user.Balance)
	require.Equal(t, 2, user.Concurrency)
	require.Len(t, assigner.calls, 1)
	require.Equal(t, int64(31), assigner.calls[0].PlanID)
}

func TestAuthService_Register_GrantOnSignupMergesSourceOverridesWithGlobalDefaults(t *testing.T) {
	repo := &userRepoStub{nextID: 54}
	assigner := &defaultSubscriptionAssignerStub{}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		billing.SettingKeyDefaultSubscriptions:                 `[{"plan_id":31}]`,
		identity.SettingKeyAuthSourceDefaultEmailBalance:       "9.5",
		identity.SettingKeyAuthSourceDefaultEmailConcurrency:   "5",
		identity.SettingKeyAuthSourceDefaultEmailSubscriptions: `[]`,
		identity.SettingKeyAuthSourceDefaultEmailGrantOnSignup: "true",
	}, nil, nil)
	service.DefaultSubscriptions = assigner

	_, user, err := service.Register(context.Background(), "email-merged@test.com", "password")
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, 9.5, user.Balance)
	require.Equal(t, 5, user.Concurrency)
	require.Len(t, assigner.calls, 1)
	require.Equal(t, int64(31), assigner.calls[0].PlanID)
}

func TestAuthService_LoginOrRegisterOAuthWithTokenPair_UsesLinuxDoAuthSourceDefaultsOnSignup(t *testing.T) {
	repo := &userRepoStub{nextID: 61}
	assigner := &defaultSubscriptionAssignerStub{}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                   "true",
		billing.SettingKeyDefaultSubscriptions:                   `[{"plan_id":81}]`,
		identity.SettingKeyAuthSourceDefaultLinuxDoBalance:       "21.75",
		identity.SettingKeyAuthSourceDefaultLinuxDoConcurrency:   "9",
		identity.SettingKeyAuthSourceDefaultLinuxDoSubscriptions: `[{"plan_id":22}]`,
		identity.SettingKeyAuthSourceDefaultLinuxDoGrantOnSignup: "true",
	}, nil, nil)
	service.DefaultSubscriptions = assigner
	service.RefreshTokens = &refreshTokenCacheStub{}
	rebuildOriginalSession(service)

	tokenPair, user, err := service.LoginOrRegisterOAuthWithTokenPairForSource(context.Background(), "linuxdo-123@linuxdo-connect.invalid", "linuxdo_user", "", "", "linuxdo")
	require.NoError(t, err)
	require.NotNil(t, tokenPair)
	require.NotNil(t, user)
	require.Equal(t, int64(61), user.ID)
	require.Equal(t, 21.75, user.Balance)
	require.Equal(t, 9, user.Concurrency)
	require.Len(t, repo.created, 1)
	require.Len(t, assigner.calls, 1)
	require.Equal(t, int64(22), assigner.calls[0].PlanID)
}

func TestAuthService_LoginOrRegisterOAuthWithTokenPair_ExistingUserDoesNotGrantAgain(t *testing.T) {
	existing := &identity.User{
		ID:           88,
		Email:        "linuxdo-123@linuxdo-connect.invalid",
		Username:     "existing-linuxdo",
		Role:         identity.RoleUser,
		Status:       billing.StatusActive,
		Balance:      4,
		Concurrency:  1,
		TokenVersion: 2,
	}
	repo := &userRepoStub{user: existing}
	assigner := &defaultSubscriptionAssignerStub{}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                   "true",
		identity.SettingKeyAuthSourceDefaultLinuxDoBalance:       "21.75",
		identity.SettingKeyAuthSourceDefaultLinuxDoConcurrency:   "9",
		identity.SettingKeyAuthSourceDefaultLinuxDoSubscriptions: `[{"plan_id":22}]`,
		identity.SettingKeyAuthSourceDefaultLinuxDoGrantOnSignup: "true",
	}, nil, nil)
	service.DefaultSubscriptions = assigner
	service.RefreshTokens = &refreshTokenCacheStub{}
	rebuildOriginalSession(service)

	tokenPair, user, err := service.LoginOrRegisterOAuthWithTokenPairForSource(context.Background(), existing.Email, "linuxdo_user", "", "", "linuxdo")
	require.NoError(t, err)
	require.NotNil(t, tokenPair)
	require.Equal(t, existing.ID, user.ID)
	require.Equal(t, 4.0, user.Balance)
	require.Equal(t, 1, user.Concurrency)
	require.Empty(t, repo.created)
	require.Empty(t, assigner.calls)
}

// newAuthServiceWithDingTalkCfg 构建一个含完整 DingTalk config 的 AuthService，
// 用于测试 canBypassRegistrationDisabledForOAuth。
func newAuthServiceWithDingTalkCfg(settings map[string]string, dtCfg config.DingTalkConnectConfig) *identity.AuthService {
	cfg := &config.Config{
		JWT:      config.JWTConfig{Secret: "test-secret", ExpireHour: 1},
		Default:  config.DefaultConfig{UserBalance: 3.5, UserConcurrency: 2},
		DingTalk: dtCfg,
	}
	settingService := newAuthSettingsFixture(&settingRepoStub{values: settings}, cfg)
	return identitytestkit.Auth(nil, &identity.AuthDependencies{Options: identitytestkit.AuthOptions(cfg), Settings: authSettingsPort(settingService)})
}

// minDingTalkURLs 返回一个包含必填字段的基础 DingTalkConnectConfig（不设 Enabled/BypassRegistration/Policy）。
func minDingTalkURLs() config.DingTalkConnectConfig {
	return config.DingTalkConnectConfig{
		ClientID:            "test-client",
		ClientSecret:        "test-secret",
		AuthorizeURL:        "https://example.com/oauth2/auth",
		TokenURL:            "https://example.com/oauth2/token",
		UserInfoURL:         "https://example.com/oauth2/userinfo",
		RedirectURL:         "https://example.com/callback",
		FrontendRedirectURL: "https://example.com/auth/callback",
		DingTalkAppKind:     "internal_app",
		AppType:             "internal",
	}
}

func TestCanBypassRegistrationDisabledForOAuth(t *testing.T) {
	cases := []struct {
		name         string
		signupSource string
		settings     map[string]string
		dtCfg        config.DingTalkConnectConfig
		want         bool
	}{
		{
			name:         "non-dingtalk source → false",
			signupSource: "linuxdo",
			settings:     map[string]string{},
			dtCfg:        minDingTalkURLs(),
			want:         false,
		},
		{
			name:         "dingtalk but cfg.Enabled=false → false",
			signupSource: "dingtalk",
			settings: map[string]string{
				identity.SettingKeyDingTalkConnectEnabled:               "false",
				identity.SettingKeyDingTalkConnectBypassRegistration:    "true",
				identity.SettingKeyDingTalkConnectCorpRestrictionPolicy: "internal_only",
			},
			dtCfg: minDingTalkURLs(),
			want:  false,
		},
		{
			name:         "dingtalk enabled but BypassRegistration=false → false",
			signupSource: "dingtalk",
			settings: map[string]string{
				identity.SettingKeyDingTalkConnectEnabled:               "true",
				identity.SettingKeyDingTalkConnectBypassRegistration:    "false",
				identity.SettingKeyDingTalkConnectCorpRestrictionPolicy: "internal_only",
			},
			dtCfg: minDingTalkURLs(),
			want:  false,
		},
		{
			name:         "dingtalk enabled + bypass=true but policy=none → false",
			signupSource: "dingtalk",
			settings: map[string]string{
				identity.SettingKeyDingTalkConnectEnabled:               "true",
				identity.SettingKeyDingTalkConnectBypassRegistration:    "true",
				identity.SettingKeyDingTalkConnectCorpRestrictionPolicy: "none",
			},
			dtCfg: minDingTalkURLs(),
			want:  false,
		},
		{
			name:         "dingtalk enabled + bypass=true + policy=internal_only → true",
			signupSource: "dingtalk",
			settings: map[string]string{
				identity.SettingKeyDingTalkConnectEnabled:               "true",
				identity.SettingKeyDingTalkConnectBypassRegistration:    "true",
				identity.SettingKeyDingTalkConnectCorpRestrictionPolicy: "internal_only",
			},
			dtCfg: minDingTalkURLs(),
			want:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newAuthServiceWithDingTalkCfg(tc.settings, tc.dtCfg)
			got := svc.AuthCanBypassRegistrationDisabledForOAuth(context.Background(), tc.signupSource)
			require.Equal(t, tc.want, got)
		})
	}
}

// ConsumeRefreshToken 与此桩始终未找到凭据的读取行为一致。
func (s *refreshTokenCacheStub) ConsumeRefreshToken(context.Context, string) (bool, error) {
	return false, nil
}
