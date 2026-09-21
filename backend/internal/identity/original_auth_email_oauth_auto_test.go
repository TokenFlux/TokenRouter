//go:build unit

package identity_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitytestkit "github.com/TokenFlux/TokenRouter/internal/identity/testkit"
	"github.com/stretchr/testify/require"
)

func newEmailOAuthAutoAuthService(
	userRepo identity.UserRepository,
	settings map[string]string,
	quotaRepo billing.UserPlatformQuotaRepository,
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

	return identitytestkit.Auth(
		nil, &identity. // entClient — nil, updateUserSignupSource early return
				AuthDependencies{Users: userRepo, RefreshTokens:
		// redeemRepo — invitationCode="" 时不触发
		&refreshTokenCacheStub{}, Options: identitytestkit.AuthOptions(cfg), Settings: authSettingsPort(settingService), Quotas:
		// emailService
		// turnstileService
		// emailQueueService
		// promoService
		// defaultSubAssigner — nil, assignSubscriptions early return
		// affiliateService — nil, bindOAuthAffiliate early return
		quotaRepo},
	)
}

func TestEmailOAuthAuto_SnapshotsPlatformQuotaDefaults(t *testing.T) {
	userRepo := &userRepoStub{nextID: 88}
	quotaRepo := &userPlatformQuotaRepoStub{}

	svc := newEmailOAuthAutoAuthService(
		userRepo,
		map[string]string{
			identity.SettingKeyRegistrationEnabled:  "true",
			billing.SettingKeyDefaultPlatformQuotas: `{"gemini": {"monthly": 100.0}}`,
		},
		quotaRepo,
	)

	user, err := svc.AuthCreateEmailOAuthUser(
		context.Background(),
		"newoauth@example.com",
		"newoauth",
		"github",
		"", // invitationCode
		"", // affiliateCode
	)
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, int64(88), user.ID)

	require.Len(t, quotaRepo.bulkInsertCalls, 1, "createEmailOAuthUser must snapshot platform quotas via BulkInsertInitial")

	records := quotaRepo.bulkInsertCalls[0]
	var geminiRecord *billing.UserPlatformQuotaRecord
	for i := range records {
		if records[i].Platform == "gemini" {
			geminiRecord = &records[i]
			break
		}
	}
	require.NotNil(t, geminiRecord, "expected gemini platform record")
	require.NotNil(t, geminiRecord.MonthlyLimitUSD)
	require.InDelta(t, 100.0, *geminiRecord.MonthlyLimitUSD, 0.0001)
}
