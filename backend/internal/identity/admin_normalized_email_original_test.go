//go:build unit

package identity_test

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type emailNormalizationRepoStub struct {
	user *identity.User

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
	createCalls             []*identity.User
	updateCalls             []*identity.User
	normalizedUpdateCalls   []string
	normalizedUpdateUsers   []*identity.User
}

func cloneEmailNormalizationUser(u *identity.User) *identity.User {
	if u == nil {
		return nil
	}
	cloned := *u
	if u.AllowedGroups != nil {
		cloned.AllowedGroups = append([]int64(nil), u.AllowedGroups...)
	}
	if u.BalanceNotifyExtraEmails != nil {
		cloned.BalanceNotifyExtraEmails = append([]billing.NotifyEmailSummary(nil), u.BalanceNotifyExtraEmails...)
	}
	if u.GroupRates != nil {
		cloned.GroupRates = make(map[int64]float64, len(u.GroupRates))
		for k, v := range u.GroupRates {
			cloned.GroupRates[k] = v
		}
	}
	return &cloned
}

func (s *emailNormalizationRepoStub) Create(_ context.Context, user *identity.User) error {
	if s.createErr != nil {
		return s.createErr
	}
	cloned := cloneEmailNormalizationUser(user)
	s.createCalls = append(s.createCalls, cloned)
	s.user = cloneEmailNormalizationUser(cloned)
	return nil
}

func (s *emailNormalizationRepoStub) CreateWithNormalizedEmailGuard(ctx context.Context, user *identity.User, normalizedEmail string) error {
	return s.Create(ctx, user)
}

func (s *emailNormalizationRepoStub) GetByID(context.Context, int64) (*identity.User, error) {
	if s.getByIDErr != nil {
		return nil, s.getByIDErr
	}
	return cloneEmailNormalizationUser(s.user), nil
}

func (s *emailNormalizationRepoStub) GetByIDIncludeDeleted(ctx context.Context, id int64) (*identity.User, error) {
	return s.GetByID(ctx, id)
}

func (s *emailNormalizationRepoStub) GetByEmail(context.Context, string) (*identity.User, error) {
	return nil, identity.ErrUserNotFound
}

func (s *emailNormalizationRepoStub) GetFirstAdmin(context.Context) (*identity.User, error) {
	return nil, identity.ErrUserNotFound
}

func (s *emailNormalizationRepoStub) Update(_ context.Context, user *identity.User, _ identity.UserUpdateFields) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	cloned := cloneEmailNormalizationUser(user)
	s.updateCalls = append(s.updateCalls, cloned)
	s.user = cloneEmailNormalizationUser(cloned)
	return nil
}

func (s *emailNormalizationRepoStub) UpdateWithNormalizedEmailGuard(_ context.Context, user *identity.User, normalizedEmail string, _ identity.UserUpdateFields) error {
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

func (s *emailNormalizationRepoStub) GetUserAvatar(context.Context, int64) (*identity.UserAvatar, error) {
	panic("unexpected GetUserAvatar call")
}

func (s *emailNormalizationRepoStub) UpsertUserAvatar(context.Context, int64, identity.UpsertUserAvatarInput) (*identity.UserAvatar, error) {
	panic("unexpected UpsertUserAvatar call")
}

func (s *emailNormalizationRepoStub) DeleteUserAvatar(context.Context, int64) error {
	panic("unexpected DeleteUserAvatar call")
}

func (s *emailNormalizationRepoStub) List(context.Context, pagination.PaginationParams) ([]identity.User, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *emailNormalizationRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, identity.UserListFilters) ([]identity.User, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *emailNormalizationRepoStub) GetLatestUsedAtByUserIDs(context.Context, []int64) (map[int64]*time.Time, error) {
	return map[int64]*time.Time{}, nil
}

func (s *emailNormalizationRepoStub) GetLatestUsedAtByUserID(context.Context, int64) (*time.Time, error) {
	return nil, nil
}

func (s *emailNormalizationRepoStub) UpdateUserLastActiveAt(context.Context, int64, time.Time) error {
	return nil
}

func (s *emailNormalizationRepoStub) AddBalance(context.Context, int64, float64) error {
	panic("unexpected AddBalance call")
}

func (s *emailNormalizationRepoStub) UpdateBalance(context.Context, int64, float64) error {
	panic("unexpected UpdateBalance call")
}

func (s *emailNormalizationRepoStub) DeductBalance(context.Context, int64, float64) (float64, error) {
	panic("unexpected DeductBalance call")
}

func (s *emailNormalizationRepoStub) AdjustBalance(context.Context, int64, float64) (identity.BalanceChange, error) {
	panic("unexpected AdjustBalance call")
}

func (s *emailNormalizationRepoStub) SetBalance(context.Context, int64, float64) (identity.BalanceChange, error) {
	panic("unexpected SetBalance call")
}

func (s *emailNormalizationRepoStub) UpdateConcurrency(context.Context, int64, int) error {
	panic("unexpected UpdateConcurrency call")
}

func (s *emailNormalizationRepoStub) BatchSetConcurrency(context.Context, []int64, int) (int, error) {
	panic("unexpected BatchSetConcurrency call")
}

func (s *emailNormalizationRepoStub) BatchAddConcurrency(context.Context, []int64, int) (int, error) {
	panic("unexpected BatchAddConcurrency call")
}

func (s *emailNormalizationRepoStub) BatchUpdateLimits(context.Context, []int64, *int, *int) (int, error) {
	panic("unexpected BatchUpdateLimits call")
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

func (s *emailNormalizationRepoStub) RemoveGroupFromAllowedGroups(context.Context, int64) (int64, error) {
	panic("unexpected RemoveGroupFromAllowedGroups call")
}

func (s *emailNormalizationRepoStub) AddGroupToAllowedGroups(context.Context, int64, int64) error {
	panic("unexpected AddGroupToAllowedGroups call")
}

func (s *emailNormalizationRepoStub) RemoveGroupFromUserAllowedGroups(context.Context, int64, int64) error {
	panic("unexpected RemoveGroupFromUserAllowedGroups call")
}

func (s *emailNormalizationRepoStub) ListUserAuthIdentities(context.Context, int64) ([]identity.UserAuthIdentityRecord, error) {
	return nil, nil
}

func (s *emailNormalizationRepoStub) UnbindUserAuthProvider(context.Context, int64, string) error {
	panic("unexpected UnbindUserAuthProvider call")
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

func TestAdminService_UpdateUser_UsesNormalizedEmailGuardWhenEnabled(t *testing.T) {
	repo := &emailNormalizationRepoStub{
		user: &identity.User{
			ID:       11,
			Email:    "old@example.com",
			Role:     identity.RoleUser,
			Status:   billing.StatusActive,
			Username: "tester",
		},
	}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Settings: newAdminCreationSettings(&adminCreationSettingsStore{authSourceDefaultsRepoStub: authSourceDefaultsRepoStub{values: map[string]string{identity.SettingKeyRegistrationEmailNormalization: "true"}}}, identity.GrantSettingsOptions{})})

	updated, err := svc.UpdateUser(context.Background(), 11, &identity.UpdateUserInput{
		Email: "Y.o.u.r.N.a.m.e+alias@googlemail.com.",
	})
	require.NoError(t, err)
	require.Equal(t, "Y.o.u.r.N.a.m.e+alias@googlemail.com.", updated.Email)
	require.Empty(t, repo.existsByEmailCalls)
	require.Equal(t, []string{"yourname@gmail.com"}, repo.normalizedUpdateCalls)
	require.Len(t, repo.normalizedUpdateUsers, 1)
	require.Equal(t, "Y.o.u.r.N.a.m.e+alias@googlemail.com.", repo.normalizedUpdateUsers[0].Email)
}
