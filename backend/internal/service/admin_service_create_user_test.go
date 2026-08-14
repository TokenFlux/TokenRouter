//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAdminService_CreateUser_Success(t *testing.T) {
	repo := &userRepoStub{nextID: 10}
	svc := &adminServiceImpl{userRepo: repo}
	balance := 12.5

	input := &CreateUserInput{
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
	require.Equal(t, DefaultUserAPIKeyLimit, user.APIKeyLimit)
	require.Equal(t, input.AllowedGroups, user.AllowedGroups)
	require.Equal(t, input.DisabledPublicGroups, user.DisabledPublicGroups)
	require.Equal(t, RoleUser, user.Role)
	require.Equal(t, StatusActive, user.Status)
	require.True(t, user.CheckPassword(input.Password))
	require.Len(t, repo.created, 1)
	require.Equal(t, user, repo.created[0])
}

func TestAdminService_CreateUser_APIKeyLimitDefaultsAndExplicitZero(t *testing.T) {
	repo := &userRepoStub{nextID: 13}
	settingService := NewSettingService(&settingRepoStub{values: map[string]string{
		SettingKeyDefaultUserAPIKeyLimit: "45",
	}}, &config.Config{})
	svc := &adminServiceImpl{userRepo: repo, settingService: settingService}

	inherited, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:    "inherited-limit@test.com",
		Password: "strong-pass",
	})
	require.NoError(t, err)
	require.Equal(t, 45, inherited.APIKeyLimit)

	unlimited := 0
	explicit, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:       "unlimited-limit@test.com",
		Password:    "strong-pass",
		APIKeyLimit: &unlimited,
	})
	require.NoError(t, err)
	require.Equal(t, 0, explicit.APIKeyLimit)
}

func TestAdminService_CreateUser_RejectsNegativeAPIKeyLimit(t *testing.T) {
	repo := &userRepoStub{}
	svc := &adminServiceImpl{userRepo: repo}
	negative := -1

	_, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:       "negative-limit@test.com",
		Password:    "strong-pass",
		APIKeyLimit: &negative,
	})

	require.ErrorIs(t, err, ErrUserAPIKeyLimitInvalid)
	require.Empty(t, repo.created)
}

func TestAdminService_CreateUser_RejectsAPIKeyLimitAboveDatabaseRange(t *testing.T) {
	repo := &userRepoStub{}
	svc := &adminServiceImpl{userRepo: repo}
	tooHigh := MaxUserAPIKeyLimit + 1

	_, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:       "too-high-limit@test.com",
		Password:    "strong-pass",
		APIKeyLimit: &tooHigh,
	})

	require.ErrorIs(t, err, ErrUserAPIKeyLimitInvalid)
	require.Empty(t, repo.created)
}

func TestAdminService_CreateUser_UsesDefaultBalanceWhenBalanceOmitted(t *testing.T) {
	repo := &userRepoStub{nextID: 11}
	cfg := &config.Config{
		Default: config.DefaultConfig{
			UserBalance: 0,
		},
	}
	settingService := NewSettingService(&settingRepoStub{values: map[string]string{
		SettingKeyDefaultBalance: "0.02",
	}}, cfg)
	svc := &adminServiceImpl{userRepo: repo, settingService: settingService}

	user, err := svc.CreateUser(context.Background(), &CreateUserInput{
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
	cfg := &config.Config{
		Default: config.DefaultConfig{
			UserBalance: 0,
		},
	}
	settingService := NewSettingService(&settingRepoStub{values: map[string]string{
		SettingKeyDefaultBalance: "0.02",
	}}, cfg)
	svc := &adminServiceImpl{userRepo: repo, settingService: settingService}
	balance := 0.0

	user, err := svc.CreateUser(context.Background(), &CreateUserInput{
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
	repo := &userRepoStub{createErr: ErrEmailExists}
	svc := &adminServiceImpl{userRepo: repo}

	_, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:    "dup@test.com",
		Password: "password",
	})
	require.ErrorIs(t, err, ErrEmailExists)
	require.Empty(t, repo.created)
}

func TestAdminService_CreateUser_CreateError(t *testing.T) {
	createErr := errors.New("db down")
	repo := &userRepoStub{createErr: createErr}
	svc := &adminServiceImpl{userRepo: repo}

	_, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:    "user@test.com",
		Password: "password",
	})
	require.ErrorIs(t, err, createErr)
	require.Empty(t, repo.created)
}

func TestAdminService_CreateUser_AssignsDefaultSubscriptions(t *testing.T) {
	repo := &userRepoStub{nextID: 21}
	assigner := &defaultSubscriptionAssignerStub{}
	cfg := &config.Config{
		Default: config.DefaultConfig{
			UserBalance:     0,
			UserConcurrency: 1,
		},
	}
	settingService := NewSettingService(&settingRepoStub{values: map[string]string{
		SettingKeyDefaultSubscriptions: `[{"plan_id":5}]`,
	}}, cfg)
	svc := &adminServiceImpl{
		userRepo:           repo,
		settingService:     settingService,
		defaultSubAssigner: assigner,
	}

	_, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:    "new-user@test.com",
		Password: "password",
	})
	require.NoError(t, err)
	require.Len(t, assigner.calls, 1)
	require.Equal(t, int64(21), assigner.calls[0].UserID)
	require.Equal(t, int64(5), assigner.calls[0].PlanID)
}
