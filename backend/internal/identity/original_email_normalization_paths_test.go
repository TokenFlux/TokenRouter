//go:build unit

package identity_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitytestkit "github.com/TokenFlux/TokenRouter/internal/identity/testkit"
	"github.com/stretchr/testify/require"
)

func newEmailNormalizationAuthService(repo identity.UserRepository, settings map[string]string) *identity.AuthService {
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

	return identitytestkit.Auth(
		nil, &identity.AuthDependencies{Users: repo, Options: identitytestkit.AuthOptions(cfg), Settings: authSettingsPort(newAuthSettingsFixture(&settingRepoStub{values: settings}, cfg))},
	)
}

func TestAuthService_Register_UsesNormalizedEmailLookupWhenEnabled(t *testing.T) {
	repo := &emailNormalizationRepoStub{existsByNormalized: true}
	svc := newEmailNormalizationAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:            "true",
		identity.SettingKeyRegistrationEmailNormalization: "true",
	})

	_, _, err := svc.Register(context.Background(), "Y.o.u.r.N.a.m.e+abc@googlemail.com.", "password")
	require.ErrorIs(t, err, identity.ErrEmailExists)
	require.Equal(t, []string{"Y.o.u.r.N.a.m.e+abc@googlemail.com."}, repo.existsByEmailCalls)
	require.Equal(t, []string{"yourname@gmail.com"}, repo.existsByNormalizedCalls)
	require.Empty(t, repo.createCalls)
}

func TestAuthService_Register_SkipsNormalizedLookupWhenDisabled(t *testing.T) {
	repo := &emailNormalizationRepoStub{}
	svc := newEmailNormalizationAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "true",
	})

	_, user, err := svc.Register(context.Background(), "Y.o.u.r.N.a.m.e+abc@example.com", "password")
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, []string{"Y.o.u.r.N.a.m.e+abc@example.com"}, repo.existsByEmailCalls)
	require.Empty(t, repo.existsByNormalizedCalls)
}

func TestUserService_UpdateProfile_RejectsEmailWhenNormalizationEnabled(t *testing.T) {
	repo := &emailNormalizationRepoStub{
		user: &identity.User{
			ID:          7,
			Email:       "old@example.com",
			Username:    "old-name",
			Concurrency: 2,
		},
	}
	svc := identity.NewUserService(repo, &settingRepoStub{values: map[string]string{
		identity.SettingKeyRegistrationEmailNormalization: "true",
	}}, nil, nil, runProfileBackground)
	newEmail := "Y.o.u.r.N.a.m.e+promo@example.com"
	newUsername := "new-name"

	_, err := svc.UpdateProfile(context.Background(), 7, identity.UpdateProfileRequest{
		Email:    &newEmail,
		Username: &newUsername,
	})
	require.ErrorIs(t, err, identity.ErrProfileEmailChangeForbidden)
	require.Empty(t, repo.existsByEmailCalls)
	require.Empty(t, repo.normalizedUpdateCalls)
	require.Empty(t, repo.normalizedUpdateUsers)
	require.Empty(t, repo.updateCalls)
	require.Equal(t, "old@example.com", repo.user.Email)
	require.Equal(t, "old-name", repo.user.Username)
}

func TestUserService_UpdateProfile_RejectsEmailWhenNormalizationDisabled(t *testing.T) {
	repo := &emailNormalizationRepoStub{
		user: &identity.User{
			ID:    8,
			Email: "old@example.com",
		},
		existsByEmail: true,
	}
	svc := identity.NewUserService(repo, &settingRepoStub{values: map[string]string{}}, nil, nil, runProfileBackground)
	newEmail := "duplicate@example.com"

	_, err := svc.UpdateProfile(context.Background(), 8, identity.UpdateProfileRequest{Email: &newEmail})
	require.ErrorIs(t, err, identity.ErrProfileEmailChangeForbidden)
	require.Empty(t, repo.existsByEmailCalls)
	require.Empty(t, repo.normalizedUpdateCalls)
	require.Empty(t, repo.updateCalls)
}
