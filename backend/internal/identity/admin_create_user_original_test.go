//go:build unit

package identity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/settings"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestAdminService_CreateUser_Success(t *testing.T) {
	repo := &userRepoStub{nextID: 10}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})
	balance := 12.5

	input := &identity.CreateUserInput{
		Email:                "user@test.com",
		Password:             "strong-pass",
		Username:             "tester",
		Notes:                "note",
		Balance:              &balance,
		Concurrency:          7,
		AllowedGroups:        []int64{3, 5},
		DisabledPublicGroups: []int64{8},
	}

	user, err := svc.CreateUser(context.Background(), input)
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, int64(10), user.ID)
	require.Equal(t, input.Email, user.Email)
	require.Equal(t, input.Username, user.Username)
	require.Equal(t, input.Notes, user.Notes)
	require.Equal(t, balance, user.Balance)
	require.Equal(t, input.Concurrency, user.Concurrency)
	require.Equal(t, identity.DefaultUserAPIKeyLimit, user.APIKeyLimit)
	require.Equal(t, input.AllowedGroups, user.AllowedGroups)
	require.Equal(t, input.DisabledPublicGroups, user.DisabledPublicGroups)
	require.Equal(t, identity.RoleUser, user.Role)
	require.Equal(t, billing.StatusActive, user.Status)
	require.True(t, user.CheckPassword(input.Password))
	require.Len(t, repo.created, 1)
	require.Equal(t, user, repo.created[0])
}

func TestAdminService_CreateUser_APIKeyLimitDefaultsAndExplicitZero(t *testing.T) {
	repo := &userRepoStub{nextID: 13}
	settingService := newAdminCreationSettings(&adminCreationSettingsStore{authSourceDefaultsRepoStub: authSourceDefaultsRepoStub{values: map[string]string{
		identity.SettingKeyDefaultUserAPIKeyLimit: "45",
	}}}, identity.GrantSettingsOptions{})
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Settings: settingService})

	inherited, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:    "inherited-limit@test.com",
		Password: "strong-pass",
	})
	require.NoError(t, err)
	require.Equal(t, 45, inherited.APIKeyLimit)

	unlimited := 0
	explicit, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:       "unlimited-limit@test.com",
		Password:    "strong-pass",
		APIKeyLimit: &unlimited,
	})
	require.NoError(t, err)
	require.Equal(t, 0, explicit.APIKeyLimit)
}

func TestAdminService_CreateUser_RejectsNegativeAPIKeyLimit(t *testing.T) {
	repo := &userRepoStub{}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})
	negative := -1

	_, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:       "negative-limit@test.com",
		Password:    "strong-pass",
		APIKeyLimit: &negative,
	})

	require.ErrorIs(t, err, identity.ErrUserAPIKeyLimitInvalid)
	require.Empty(t, repo.created)
}

func TestAdminService_CreateUser_RejectsAPIKeyLimitAboveDatabaseRange(t *testing.T) {
	repo := &userRepoStub{}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})
	tooHigh := identity.MaxUserAPIKeyLimit + 1

	_, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:       "too-high-limit@test.com",
		Password:    "strong-pass",
		APIKeyLimit: &tooHigh,
	})

	require.ErrorIs(t, err, identity.ErrUserAPIKeyLimitInvalid)
	require.Empty(t, repo.created)
}

func TestAdminService_CreateUser_UsesDefaultBalanceWhenBalanceOmitted(t *testing.T) {
	repo := &userRepoStub{nextID: 11}
	defaults := identity.GrantSettingsOptions{DefaultBalance: 0}
	settingService := newAdminCreationSettings(&adminCreationSettingsStore{authSourceDefaultsRepoStub: authSourceDefaultsRepoStub{values: map[string]string{
		billing.SettingKeyDefaultBalance: "0.02",
	}}}, defaults)
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Settings: settingService})

	user, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:    "default-balance@test.com",
		Password: "strong-pass",
	})

	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, 0.02, user.Balance)
	require.Len(t, repo.created, 1)
	require.Equal(t, 0.02, repo.created[0].Balance)
}

func TestAdminService_CreateUser_ExplicitZeroBalanceOverridesDefault(t *testing.T) {
	repo := &userRepoStub{nextID: 12}
	defaults := identity.GrantSettingsOptions{DefaultBalance: 0}
	settingService := newAdminCreationSettings(&adminCreationSettingsStore{authSourceDefaultsRepoStub: authSourceDefaultsRepoStub{values: map[string]string{
		billing.SettingKeyDefaultBalance: "0.02",
	}}}, defaults)
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Settings: settingService})
	balance := 0.0

	user, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:    "zero-balance@test.com",
		Password: "strong-pass",
		Balance:  &balance,
	})

	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, 0.0, user.Balance)
	require.Len(t, repo.created, 1)
	require.Equal(t, 0.0, repo.created[0].Balance)
}

func TestAdminService_CreateUser_EmailExists(t *testing.T) {
	repo := &userRepoStub{createErr: identity.ErrEmailExists}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})

	_, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:    "dup@test.com",
		Password: "password",
	})
	require.ErrorIs(t, err, identity.ErrEmailExists)
	require.Empty(t, repo.created)
}

func TestAdminService_CreateUser_CreateError(t *testing.T) {
	createErr := errors.New("db down")
	repo := &userRepoStub{createErr: createErr}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})

	_, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:    "user@test.com",
		Password: "password",
	})
	require.ErrorIs(t, err, createErr)
	require.Empty(t, repo.created)
}

func TestAdminService_CreateUser_AssignsDefaultSubscriptions(t *testing.T) {
	repo := &userRepoStub{nextID: 21}
	assigner := &defaultSubscriptionAssignerStub{}
	defaults := identity.GrantSettingsOptions{DefaultBalance: 0, DefaultConcurrency: 1}
	settingService := newAdminCreationSettings(&adminCreationSettingsStore{authSourceDefaultsRepoStub: authSourceDefaultsRepoStub{values: map[string]string{
		billing.SettingKeyDefaultSubscriptions: `[{"plan_id":5}]`,
	}}}, defaults)
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo, Settings: settingService, Subscriptions: assigner})

	_, err := svc.CreateUser(context.Background(), &identity.CreateUserInput{
		Email:    "new-user@test.com",
		Password: "password",
	})
	require.NoError(t, err)
	require.Len(t, assigner.calls, 1)
	require.Equal(t, int64(21), assigner.calls[0].UserID)
	require.Equal(t, int64(5), assigner.calls[0].PlanID)
}

// adminCreationSettingsStore 保留原设置替身的按键读取与缺键语义。
type adminCreationSettingsStore struct{ authSourceDefaultsRepoStub }

func (s *adminCreationSettingsStore) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", settings.ErrSettingNotFound
	}
	return value, nil
}

// adminCreationSettings 仅组合唯一配置解释器，不复制解析规则。
type adminCreationSettings struct {
	*identity.RuntimeSettings
	*identity.GrantSettings
}

func newAdminCreationSettings(repo *adminCreationSettingsStore, opts identity.GrantSettingsOptions) *adminCreationSettings {
	return &adminCreationSettings{identity.NewRuntimeSettings(repo, settings.ErrSettingNotFound), identity.NewGrantSettings(repo, opts)}
}
func (*adminCreationSettings) IsAffiliateAdminRechargeEnabled(context.Context) bool {
	panic("unexpected recharge policy read")
}

// defaultSubscriptionAssignerStub 记录原赠送调用与错误。
type defaultSubscriptionAssignerStub struct {
	calls []billing.AssignSubscriptionInput
	err   error
}

func (s *defaultSubscriptionAssignerStub) AssignOrExtendSubscription(_ context.Context, input *billing.AssignSubscriptionInput) (*billing.UserSubscription, bool, error) {
	if input != nil {
		s.calls = append(s.calls, *input)
	}
	if s.err != nil {
		return nil, false, s.err
	}
	return &billing.UserSubscription{UserID: input.UserID, PlanID: input.PlanID}, false, nil
}
