//go:build unit

package identity_test

import (
	"context"
	"errors"
	"testing"
	"time"

	identitytestkit "github.com/TokenFlux/TokenRouter/internal/identity/testkit"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/notification/smtp"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/stretchr/testify/require"
)

type redeemCodeRepoStub struct {
	codesByCode map[string]*billing.RedeemCode
	useCalls    []struct {
		id     int64
		userID int64
	}
	updateCalls []*billing.RedeemCode
}

func (s *redeemCodeRepoStub) Create(context.Context, *billing.RedeemCode) error {
	panic("unexpected Create call")
}

func (s *redeemCodeRepoStub) CreateBatch(context.Context, []billing.RedeemCode) error {
	panic("unexpected CreateBatch call")
}

func (s *redeemCodeRepoStub) GetByID(context.Context, int64) (*billing.RedeemCode, error) {
	panic("unexpected GetByID call")
}

func (s *redeemCodeRepoStub) GetByIDForUpdate(context.Context, int64) (*billing.RedeemCode, error) {
	panic("unexpected GetByIDForUpdate call")
}

func (s *redeemCodeRepoStub) GetByCode(_ context.Context, code string) (*billing.RedeemCode, error) {
	if s.codesByCode == nil {
		return nil, billing.ErrRedeemCodeNotFound
	}
	redeemCode, ok := s.codesByCode[code]
	if !ok {
		return nil, billing.ErrRedeemCodeNotFound
	}
	cloned := *redeemCode
	return &cloned, nil
}

func (s *redeemCodeRepoStub) GetByCodeForUpdate(ctx context.Context, code string) (*billing.RedeemCode, error) {
	return s.GetByCode(ctx, code)
}

func (s *redeemCodeRepoStub) Update(_ context.Context, code *billing.RedeemCode) error {
	if code == nil {
		return nil
	}
	cloned := *code
	s.updateCalls = append(s.updateCalls, &cloned)
	if s.codesByCode == nil {
		s.codesByCode = make(map[string]*billing.RedeemCode)
	}
	s.codesByCode[cloned.Code] = &cloned
	return nil
}

func (s *redeemCodeRepoStub) BatchUpdate(context.Context, []int64, billing.RedeemCodeBatchUpdateFields) (int64, error) {
	panic("unexpected BatchUpdate call")
}

func (s *redeemCodeRepoStub) Delete(context.Context, int64) error {
	panic("unexpected Delete call")
}

func (s *redeemCodeRepoStub) Use(_ context.Context, id, userID int64) error {
	for code, redeemCode := range s.codesByCode {
		if redeemCode.ID != id {
			continue
		}
		now := time.Now().UTC()
		redeemCode.Status = billing.StatusUsed
		redeemCode.UsedCount = 1
		redeemCode.UsedBy = &userID
		redeemCode.UsedAt = &now
		s.codesByCode[code] = redeemCode
		s.useCalls = append(s.useCalls, struct {
			id     int64
			userID int64
		}{id: id, userID: userID})
		return nil
	}
	return billing.ErrRedeemCodeNotFound
}

func (s *redeemCodeRepoStub) List(context.Context, pagination.PaginationParams) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *redeemCodeRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *redeemCodeRepoStub) ListByUser(context.Context, int64, int) ([]billing.RedeemCode, error) {
	panic("unexpected ListByUser call")
}

func (s *redeemCodeRepoStub) ListByUserPaginated(context.Context, int64, pagination.PaginationParams, string) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected ListByUserPaginated call")
}

func (s *redeemCodeRepoStub) SumPositiveBalanceByUser(context.Context, int64) (float64, error) {
	panic("unexpected SumPositiveBalanceByUser call")
}

func (s *redeemCodeRepoStub) CreateUsage(context.Context, *billing.RedeemCodeUsage) error {
	panic("unexpected CreateUsage call")
}

func (s *redeemCodeRepoStub) GetUsageByRedeemCodeAndUser(context.Context, int64, int64) (*billing.RedeemCodeUsage, error) {
	return nil, nil
}

func newOAuthEmailFlowAuthService(
	userRepo identity.UserRepository,
	redeemRepo billing.RedeemCodeRepository,
	refreshTokenCache identity.RefreshTokenCache,
	settings map[string]string,
	emailCache identity.EmailCache,
	quotaRepo billing.UserPlatformQuotaRepository, // 新增
) *identity.AuthService {
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:                   "test-secret",
			ExpireHour:               1,
			AccessTokenExpireMinutes: 60,
			RefreshTokenExpireDays:   7,
		},
		Default: config.DefaultConfig{
			UserBalance:     3.5,
			UserConcurrency: 2,
		},
	}

	settingService := newAuthSettingsFixture(&settingRepoStub{values: settings}, cfg)
	emailService := identity.NewEmailChallenges(emailCache, notification.NewMailer(&settingRepoStub{values: settings}, smtp.New()))

	return identitytestkit.Auth(
		nil, &identity.AuthDependencies{Users: userRepo, Redeem: redeemRepo, RefreshTokens: refreshTokenCache, Options: identitytestkit.AuthOptions(cfg), Settings: authSettingsPort(settingService), Email: identitytestkit.Email(emailService), Quotas: quotaRepo}, // 替换原来的 nil
	)
}

func TestRegisterOAuthEmailAccountRollsBackCreatedUserWhenTokenPairGenerationFails(t *testing.T) {
	userRepo := &userRepoStub{nextID: 42}
	redeemRepo := &redeemCodeRepoStub{
		codesByCode: map[string]*billing.RedeemCode{
			"INVITE123": {
				ID:     7,
				Code:   "INVITE123",
				Type:   billing.RedeemTypeInvitation,
				Status: billing.StatusUnused,
			},
		},
	}
	emailCache := &emailCacheStub{
		data: &identity.VerificationCodeData{
			Code:      "246810",
			Attempts:  0,
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
		},
	}
	authService := newOAuthEmailFlowAuthService(
		userRepo,
		redeemRepo,
		nil,
		map[string]string{
			identity.SettingKeyRegistrationEnabled:    "true",
			promotion.SettingKeyInvitationCodeEnabled: "true",
			identity.SettingKeyEmailVerifyEnabled:     "true",
		},
		emailCache,
		nil,
	)

	tokenPair, user, err := authService.RegisterOAuthEmailAccount(
		context.Background(),
		"fresh@example.com",
		"secret-123",
		"246810",
		"INVITE123",
		"oidc",
	)

	require.Nil(t, tokenPair)
	require.Nil(t, user)
	require.Error(t, err)
	require.Contains(t, err.Error(), "generate token pair")
	require.Equal(t, []int64{42}, userRepo.deletedIDs)
	require.Len(t, userRepo.created, 1)
	require.Empty(t, redeemRepo.useCalls)
	require.Empty(t, redeemRepo.updateCalls)
}

func TestRegisterOAuthEmailAccountRejectsExhaustedNonWhitelistDomain(t *testing.T) {
	userRepo := &userRepoStub{domainCounts: map[string]int{"custom.example": 1}}
	authService := newOAuthEmailFlowAuthService(
		userRepo,
		&redeemCodeRepoStub{},
		&refreshTokenCacheStub{},
		map[string]string{
			identity.SettingKeyRegistrationEnabled:                 "true",
			identity.SettingKeyRegistrationEmailSuffixWhitelist:    `["@example.com"]`,
			identity.SettingKeyRegistrationEmailDomainQuotaEnabled: "true",
		},
		&emailCacheStub{data: &identity.VerificationCodeData{
			Code:      "246810",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
		}},
		nil,
	)

	_, _, err := authService.RegisterOAuthEmailAccount(
		context.Background(),
		"second@sub.custom.example",
		"secret-123",
		"246810",
		"",
		"oidc",
	)

	require.ErrorIs(t, err, identity.ErrEmailDomainRegistrationLimit)
	require.Empty(t, userRepo.created)
}

func TestSendPendingOAuthVerifyCodeRejectsExhaustedNonWhitelistDomain(t *testing.T) {
	userRepo := &userRepoStub{domainCounts: map[string]int{"custom.example": 1}}
	authService := newOAuthEmailFlowAuthService(
		userRepo,
		nil,
		nil,
		map[string]string{
			identity.SettingKeyRegistrationEnabled:                 "true",
			identity.SettingKeyRegistrationEmailSuffixWhitelist:    `["@example.com"]`,
			identity.SettingKeyRegistrationEmailDomainQuotaEnabled: "true",
		},
		&emailCacheStub{},
		nil,
	)

	_, err := authService.SendPendingOAuthVerifyCode(context.Background(), "second@custom.example")
	require.ErrorIs(t, err, identity.ErrEmailDomainRegistrationLimit)
}

func TestSendPendingOAuthVerifyCodeRejectsNonWhitelistDomainWhenQuotaDisabled(t *testing.T) {
	userRepo := &userRepoStub{domainCounts: map[string]int{"custom.example": 0}}
	authService := newOAuthEmailFlowAuthService(
		userRepo,
		nil,
		nil,
		map[string]string{
			identity.SettingKeyRegistrationEnabled:              "true",
			identity.SettingKeyRegistrationEmailSuffixWhitelist: `["@example.com"]`,
		},
		&emailCacheStub{},
		nil,
	)

	_, err := authService.SendPendingOAuthVerifyCode(context.Background(), "first@custom.example")
	require.ErrorIs(t, err, identity.ErrEmailSuffixNotAllowed)
}

func TestRegisterOAuthEmailAccountRejectsExpiredInvitation(t *testing.T) {
	userRepo := &userRepoStub{nextID: 42}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	redeemRepo := &redeemCodeRepoStub{
		codesByCode: map[string]*billing.RedeemCode{
			"INVITE-EXPIRED": {
				ID:        7,
				Code:      "INVITE-EXPIRED",
				Type:      billing.RedeemTypeInvitation,
				Status:    billing.StatusUnused,
				MaxUses:   1,
				ExpiresAt: &expiredAt,
			},
		},
	}
	emailCache := &emailCacheStub{
		data: &identity.VerificationCodeData{
			Code:      "246810",
			Attempts:  0,
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
		},
	}
	authService := newOAuthEmailFlowAuthService(
		userRepo,
		redeemRepo,
		&refreshTokenCacheStub{},
		map[string]string{
			identity.SettingKeyRegistrationEnabled:    "true",
			promotion.SettingKeyInvitationCodeEnabled: "true",
			identity.SettingKeyEmailVerifyEnabled:     "true",
		},
		emailCache,
		nil,
	)

	tokenPair, user, err := authService.RegisterOAuthEmailAccount(
		context.Background(),
		"fresh@example.com",
		"secret-123",
		"246810",
		"INVITE-EXPIRED",
		"oidc",
	)

	require.Nil(t, tokenPair)
	require.Nil(t, user)
	require.ErrorIs(t, err, identity.ErrInvitationCodeInvalid)
	require.Empty(t, userRepo.created)
}

func TestRegisterOAuthEmailAccountSetsNormalizedSignupSourceOnCreatedUser(t *testing.T) {
	userRepo := &userRepoStub{nextID: 42}
	emailCache := &emailCacheStub{
		data: &identity.VerificationCodeData{
			Code:      "246810",
			Attempts:  0,
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
		},
	}
	authService := newOAuthEmailFlowAuthService(
		userRepo,
		&redeemCodeRepoStub{},
		&refreshTokenCacheStub{},
		map[string]string{
			identity.SettingKeyRegistrationEnabled: "true",
			identity.SettingKeyEmailVerifyEnabled:  "true",
		},
		emailCache,
		nil,
	)

	tokenPair, user, err := authService.RegisterOAuthEmailAccount(
		context.Background(),
		"fresh@example.com",
		"secret-123",
		"246810",
		"",
		" OIDC ",
	)

	require.NoError(t, err)
	require.NotNil(t, tokenPair)
	require.NotNil(t, user)
	require.Len(t, userRepo.created, 1)
	require.Equal(t, "oidc", userRepo.created[0].SignupSource)
}

func TestRegisterOAuthEmailAccountKeepsGitHubAndGoogleSignupSource(t *testing.T) {
	tests := []struct {
		name         string
		email        string
		signupSource string
		want         string
	}{
		{
			name:         "github",
			email:        "github@example.com",
			signupSource: " GitHub ",
			want:         "github",
		},
		{
			name:         "google",
			email:        "google@example.com",
			signupSource: " Google ",
			want:         "google",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userRepo := &userRepoStub{nextID: 43}
			emailCache := &emailCacheStub{
				data: &identity.VerificationCodeData{
					Code:      "246810",
					Attempts:  0,
					CreatedAt: time.Now().UTC(),
					ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
				},
			}
			authService := newOAuthEmailFlowAuthService(
				userRepo,
				&redeemCodeRepoStub{},
				&refreshTokenCacheStub{},
				map[string]string{
					identity.SettingKeyRegistrationEnabled: "true",
					identity.SettingKeyEmailVerifyEnabled:  "true",
				},
				emailCache,
				nil,
			)

			tokenPair, user, err := authService.RegisterOAuthEmailAccount(
				context.Background(),
				tt.email,
				"secret-123",
				"246810",
				"",
				tt.signupSource,
			)

			require.NoError(t, err)
			require.NotNil(t, tokenPair)
			require.NotNil(t, user)
			require.Len(t, userRepo.created, 1)
			require.Equal(t, tt.want, userRepo.created[0].SignupSource)
		})
	}
}

func TestRegisterOAuthEmailAccountFallsBackUnknownSignupSourceToEmail(t *testing.T) {
	userRepo := &userRepoStub{nextID: 43}
	emailCache := &emailCacheStub{
		data: &identity.VerificationCodeData{
			Code:      "246810",
			Attempts:  0,
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
		},
	}
	authService := newOAuthEmailFlowAuthService(
		userRepo,
		&redeemCodeRepoStub{},
		&refreshTokenCacheStub{},
		map[string]string{
			identity.SettingKeyRegistrationEnabled: "true",
			identity.SettingKeyEmailVerifyEnabled:  "true",
		},
		emailCache,
		nil,
	)

	tokenPair, user, err := authService.RegisterOAuthEmailAccount(
		context.Background(),
		"fallback@example.com",
		"secret-123",
		"246810",
		"",
		"twitter",
	)

	require.NoError(t, err)
	require.NotNil(t, tokenPair)
	require.NotNil(t, user)
	require.Len(t, userRepo.created, 1)
	require.Equal(t, "email", userRepo.created[0].SignupSource)
}

func TestRollbackOAuthEmailAccountCreationRestoresInvitationUsage(t *testing.T) {
	userRepo := &userRepoStub{}
	redeemRepo := &redeemCodeRepoStub{
		codesByCode: map[string]*billing.RedeemCode{
			"INVITE123": {
				ID:     7,
				Code:   "INVITE123",
				Type:   billing.RedeemTypeInvitation,
				Status: billing.StatusUsed,
				UsedBy: func() *int64 {
					v := int64(42)
					return &v
				}(),
				UsedAt: func() *time.Time {
					v := time.Now().UTC()
					return &v
				}(),
			},
		},
	}
	authService := newOAuthEmailFlowAuthService(
		userRepo,
		redeemRepo,
		&refreshTokenCacheStub{},
		map[string]string{
			identity.SettingKeyRegistrationEnabled:    "true",
			promotion.SettingKeyInvitationCodeEnabled: "true",
		},
		&emailCacheStub{},
		nil,
	)

	err := authService.RollbackOAuthEmailAccountCreation(context.Background(), 42, "INVITE123")

	require.NoError(t, err)
	require.Equal(t, []int64{42}, userRepo.deletedIDs)
	require.Len(t, redeemRepo.updateCalls, 1)
	require.Equal(t, billing.StatusUnused, redeemRepo.updateCalls[0].Status)
	require.Nil(t, redeemRepo.updateCalls[0].UsedBy)
	require.Nil(t, redeemRepo.updateCalls[0].UsedAt)
}

func TestRollbackOAuthEmailAccountCreationPropagatesDeleteError(t *testing.T) {
	userRepo := &userRepoStub{deleteErr: errors.New("delete failed")}
	authService := newOAuthEmailFlowAuthService(
		userRepo,
		&redeemCodeRepoStub{},
		&refreshTokenCacheStub{},
		map[string]string{
			identity.SettingKeyRegistrationEnabled: "true",
		},
		&emailCacheStub{},
		nil,
	)

	err := authService.RollbackOAuthEmailAccountCreation(context.Background(), 42, "")

	require.Error(t, err)
	require.Contains(t, err.Error(), "delete created oauth user")
}

func TestFinalizeOAuthEmailAccount_SnapshotsPlatformQuotaDefaults(t *testing.T) {
	userRepo := &userRepoStub{nextID: 99}
	quotaRepo := &userPlatformQuotaRepoStub{}

	authService := newOAuthEmailFlowAuthService(
		userRepo,
		nil,
		&refreshTokenCacheStub{},
		map[string]string{
			identity.SettingKeyRegistrationEnabled:  "true",
			identity.SettingKeyEmailVerifyEnabled:   "true",
			billing.SettingKeyDefaultPlatformQuotas: `{"anthropic": {"daily": 5.5}}`,
		},
		&emailCacheStub{},
		quotaRepo,
	)

	user := &identity.User{
		ID:           99,
		Email:        "newuser@example.com",
		Role:         identity.RoleUser,
		Status:       billing.StatusActive,
		SignupSource: "oidc",
	}

	err := authService.FinalizeOAuthEmailAccount(
		context.Background(),
		user,
		"",
		"oidc",
		"",
	)

	require.NoError(t, err)

	require.Len(t, quotaRepo.bulkInsertCalls, 1, "snapshotPlatformQuotaDefaults must call BulkInsertInitial once on successful OAuth signup")

	records := quotaRepo.bulkInsertCalls[0]
	var anthropicRecord *billing.UserPlatformQuotaRecord
	for i := range records {
		if records[i].Platform == "anthropic" {
			anthropicRecord = &records[i]
			break
		}
	}
	require.NotNil(t, anthropicRecord, "expected anthropic platform record")
	require.Equal(t, int64(99), anthropicRecord.UserID)
	require.NotNil(t, anthropicRecord.DailyLimitUSD)
	require.InDelta(t, 5.5, *anthropicRecord.DailyLimitUSD, 0.0001)
}
