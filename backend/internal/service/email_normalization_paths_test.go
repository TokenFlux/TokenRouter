//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type emailNormalizationRepoStub struct {
	user *User

	existsByEmail           bool
	existsByEmailErr        error
	existsByNormalized      bool
	existsByNormalizedErr   error
	createErr               error
	getByIDErr              error
	updateErr               error
	normalizedUpdateErr     error
	existsByEmailCalls      []string
	existsByNormalizedCalls []string
	createCalls             []*User
	updateCalls             []*User
	normalizedUpdateCalls   []string
	normalizedUpdateUsers   []*User
}

func cloneEmailNormalizationUser(u *User) *User {
	if u == nil {
		return nil
	}
	cloned := *u
	if u.AllowedGroups != nil {
		cloned.AllowedGroups = append([]int64(nil), u.AllowedGroups...)
	}
	if u.BalanceNotifyExtraEmails != nil {
		cloned.BalanceNotifyExtraEmails = append([]NotifyEmailEntry(nil), u.BalanceNotifyExtraEmails...)
	}
	if u.GroupRates != nil {
		cloned.GroupRates = make(map[int64]float64, len(u.GroupRates))
		for k, v := range u.GroupRates {
			cloned.GroupRates[k] = v
		}
	}
	return &cloned
}

func (s *emailNormalizationRepoStub) Create(_ context.Context, user *User) error {
	if s.createErr != nil {
		return s.createErr
	}
	cloned := cloneEmailNormalizationUser(user)
	s.createCalls = append(s.createCalls, cloned)
	s.user = cloneEmailNormalizationUser(cloned)
	return nil
}

func (s *emailNormalizationRepoStub) GetByID(context.Context, int64) (*User, error) {
	if s.getByIDErr != nil {
		return nil, s.getByIDErr
	}
	return cloneEmailNormalizationUser(s.user), nil
}

func (s *emailNormalizationRepoStub) GetByEmail(context.Context, string) (*User, error) {
	return nil, ErrUserNotFound
}

func (s *emailNormalizationRepoStub) GetFirstAdmin(context.Context) (*User, error) {
	return nil, ErrUserNotFound
}

func (s *emailNormalizationRepoStub) Update(_ context.Context, user *User) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	cloned := cloneEmailNormalizationUser(user)
	s.updateCalls = append(s.updateCalls, cloned)
	s.user = cloneEmailNormalizationUser(cloned)
	return nil
}

func (s *emailNormalizationRepoStub) UpdateWithNormalizedEmailGuard(_ context.Context, user *User, normalizedEmail string) error {
	if s.normalizedUpdateErr != nil {
		return s.normalizedUpdateErr
	}
	cloned := cloneEmailNormalizationUser(user)
	s.normalizedUpdateCalls = append(s.normalizedUpdateCalls, normalizedEmail)
	s.normalizedUpdateUsers = append(s.normalizedUpdateUsers, cloned)
	s.user = cloneEmailNormalizationUser(cloned)
	return nil
}

func (s *emailNormalizationRepoStub) Delete(context.Context, int64) error {
	panic("unexpected Delete call")
}

func (s *emailNormalizationRepoStub) List(context.Context, pagination.PaginationParams) ([]User, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *emailNormalizationRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, UserListFilters) ([]User, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *emailNormalizationRepoStub) AddBalance(context.Context, int64, float64) error {
	panic("unexpected AddBalance call")
}

func (s *emailNormalizationRepoStub) UpdateBalance(context.Context, int64, float64) error {
	panic("unexpected UpdateBalance call")
}

func (s *emailNormalizationRepoStub) DeductBalance(context.Context, int64, float64) error {
	panic("unexpected DeductBalance call")
}

func (s *emailNormalizationRepoStub) UpdateConcurrency(context.Context, int64, int) error {
	panic("unexpected UpdateConcurrency call")
}

func (s *emailNormalizationRepoStub) ExistsByEmail(_ context.Context, email string) (bool, error) {
	s.existsByEmailCalls = append(s.existsByEmailCalls, email)
	if s.existsByEmailErr != nil {
		return false, s.existsByEmailErr
	}
	return s.existsByEmail, nil
}

func (s *emailNormalizationRepoStub) ExistsByNormalizedEmail(_ context.Context, normalizedEmail string) (bool, error) {
	s.existsByNormalizedCalls = append(s.existsByNormalizedCalls, normalizedEmail)
	if s.existsByNormalizedErr != nil {
		return false, s.existsByNormalizedErr
	}
	return s.existsByNormalized, nil
}

func (s *emailNormalizationRepoStub) LockRegistrationEmail(context.Context, string) error {
	panic("unexpected LockRegistrationEmail call")
}

func (s *emailNormalizationRepoStub) GetByReferralCode(context.Context, string) (*User, error) {
	return nil, ErrUserNotFound
}

func (s *emailNormalizationRepoStub) EnsureReferralCode(context.Context, int64) (string, error) {
	return "referral-code", nil
}

func (s *emailNormalizationRepoStub) CountReferredUsers(context.Context, int64) (int, error) {
	return 0, nil
}

func (s *emailNormalizationRepoStub) SumReferralRewardsByInviter(context.Context, int64) (float64, error) {
	return 0, nil
}

func (s *emailNormalizationRepoStub) RemoveGroupFromAllowedGroups(context.Context, int64) (int64, error) {
	panic("unexpected RemoveGroupFromAllowedGroups call")
}

func (s *emailNormalizationRepoStub) AddGroupToAllowedGroups(context.Context, int64, int64) error {
	panic("unexpected AddGroupToAllowedGroups call")
}

func (s *emailNormalizationRepoStub) RemoveGroupFromUserAllowedGroups(context.Context, int64, int64) error {
	panic("unexpected RemoveGroupFromUserAllowedGroups call")
}

func (s *emailNormalizationRepoStub) UpdateTotpSecret(context.Context, int64, *string) error {
	panic("unexpected UpdateTotpSecret call")
}

func (s *emailNormalizationRepoStub) EnableTotp(context.Context, int64) error {
	panic("unexpected EnableTotp call")
}

func (s *emailNormalizationRepoStub) DisableTotp(context.Context, int64) error {
	panic("unexpected DisableTotp call")
}

func newEmailNormalizationAuthService(repo UserRepository, settings map[string]string) *AuthService {
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

	return NewAuthService(
		nil,
		repo,
		nil,
		nil,
		cfg,
		NewSettingService(&settingRepoStub{values: settings}, cfg),
		nil,
		nil,
		nil,
		nil,
		nil,
	)
}

func TestAuthService_Register_UsesNormalizedEmailLookupWhenEnabled(t *testing.T) {
	repo := &emailNormalizationRepoStub{existsByNormalized: true}
	svc := newEmailNormalizationAuthService(repo, map[string]string{
		SettingKeyRegistrationEnabled:            "true",
		SettingKeyRegistrationEmailNormalization: "true",
	})

	_, _, err := svc.Register(context.Background(), "Y.o.u.r.N.a.m.e+abc@example.com", "password")
	require.ErrorIs(t, err, ErrEmailExists)
	require.Equal(t, []string{"Y.o.u.r.N.a.m.e+abc@example.com"}, repo.existsByEmailCalls)
	require.Equal(t, []string{"yourname@example.com"}, repo.existsByNormalizedCalls)
	require.Empty(t, repo.createCalls)
}

func TestAuthService_Register_SkipsNormalizedLookupWhenDisabled(t *testing.T) {
	repo := &emailNormalizationRepoStub{}
	svc := newEmailNormalizationAuthService(repo, map[string]string{
		SettingKeyRegistrationEnabled: "true",
	})

	_, user, err := svc.Register(context.Background(), "Y.o.u.r.N.a.m.e+abc@example.com", "password")
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, []string{"Y.o.u.r.N.a.m.e+abc@example.com"}, repo.existsByEmailCalls)
	require.Empty(t, repo.existsByNormalizedCalls)
}

func TestUserService_UpdateProfile_UsesNormalizedEmailGuardWhenEnabled(t *testing.T) {
	repo := &emailNormalizationRepoStub{
		user: &User{
			ID:          7,
			Email:       "old@example.com",
			Username:    "old-name",
			Concurrency: 2,
		},
	}
	svc := NewUserService(repo, &settingRepoStub{values: map[string]string{
		SettingKeyRegistrationEmailNormalization: "true",
	}}, nil, nil)
	newEmail := "Y.o.u.r.N.a.m.e+promo@example.com"
	newUsername := "new-name"

	updated, err := svc.UpdateProfile(context.Background(), 7, UpdateProfileRequest{
		Email:    &newEmail,
		Username: &newUsername,
	})
	require.NoError(t, err)
	require.Equal(t, newEmail, updated.Email)
	require.Empty(t, repo.existsByEmailCalls)
	require.Equal(t, []string{"yourname@example.com"}, repo.normalizedUpdateCalls)
	require.Len(t, repo.normalizedUpdateUsers, 1)
	require.Equal(t, newEmail, repo.normalizedUpdateUsers[0].Email)
	require.Equal(t, newUsername, repo.normalizedUpdateUsers[0].Username)
	require.Empty(t, repo.updateCalls)
}

func TestUserService_UpdateProfile_UsesExactEmailCheckWhenNormalizationDisabled(t *testing.T) {
	repo := &emailNormalizationRepoStub{
		user: &User{
			ID:    8,
			Email: "old@example.com",
		},
		existsByEmail: true,
	}
	svc := NewUserService(repo, &settingRepoStub{values: map[string]string{}}, nil, nil)
	newEmail := "duplicate@example.com"

	_, err := svc.UpdateProfile(context.Background(), 8, UpdateProfileRequest{Email: &newEmail})
	require.ErrorIs(t, err, ErrEmailExists)
	require.Equal(t, []string{"duplicate@example.com"}, repo.existsByEmailCalls)
	require.Empty(t, repo.normalizedUpdateCalls)
	require.Empty(t, repo.updateCalls)
}

func TestAdminService_UpdateUser_UsesNormalizedEmailGuardWhenEnabled(t *testing.T) {
	repo := &emailNormalizationRepoStub{
		user: &User{
			ID:       11,
			Email:    "old@example.com",
			Role:     RoleUser,
			Status:   StatusActive,
			Username: "tester",
		},
	}
	cfg := &config.Config{}
	svc := &adminServiceImpl{
		userRepo: repo,
		settingService: NewSettingService(&settingRepoStub{values: map[string]string{
			SettingKeyRegistrationEmailNormalization: "true",
		}}, cfg),
	}

	updated, err := svc.UpdateUser(context.Background(), 11, &UpdateUserInput{
		Email: "Y.o.u.r.N.a.m.e+alias@example.com",
	})
	require.NoError(t, err)
	require.Equal(t, "Y.o.u.r.N.a.m.e+alias@example.com", updated.Email)
	require.Empty(t, repo.existsByEmailCalls)
	require.Equal(t, []string{"yourname@example.com"}, repo.normalizedUpdateCalls)
	require.Len(t, repo.normalizedUpdateUsers, 1)
	require.Equal(t, "Y.o.u.r.N.a.m.e+alias@example.com", repo.normalizedUpdateUsers[0].Email)
}
