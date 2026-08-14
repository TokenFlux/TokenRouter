//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

type teamContextErrorRepository struct {
	TeamRepository
	err error
}

func (r *teamContextErrorRepository) GetContextByUserID(context.Context, int64) (*TeamContext, error) {
	return nil, r.err
}

func validTeamAPIKeyForLifecycleTest() *APIKey {
	teamID := int64(11)
	createdAt := time.Now()
	return &APIKey{
		ID:        31,
		UserID:    2,
		TeamID:    &teamID,
		Status:    StatusAPIKeyActive,
		CreatedAt: createdAt,
		Team:      &Team{ID: teamID, Status: TeamStatusActive},
		TeamMembership: &TeamMembership{
			TeamID:   teamID,
			UserID:   2,
			Role:     TeamRoleMember,
			JoinedAt: createdAt.Add(-time.Minute),
		},
		ActorUser: &User{ID: 2, Status: StatusActive},
		User:      &User{ID: 1, Status: StatusActive},
	}
}

func TestAPIKeyOwnerLockAlwaysDisablesKey(t *testing.T) {
	key := validTeamAPIKeyForLifecycleTest()
	require.True(t, key.IsActive())

	key.TeamOwnerDisabled = true
	require.False(t, key.IsActive())
}

func TestValidateTeamKeyLifecycle(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*APIKey)
		cfg    *config.Config
		want   error
	}{
		{name: "valid", cfg: &config.Config{Team: config.TeamConfig{Enabled: true}}},
		{name: "feature_disabled", cfg: &config.Config{}, want: ErrTeamFeatureDisabled},
		{name: "membership_missing", cfg: &config.Config{Team: config.TeamConfig{Enabled: true}}, mutate: func(key *APIKey) { key.TeamMembership = nil }, want: ErrTeamMembershipRequired},
		{name: "membership_rejoined_after_key", cfg: &config.Config{Team: config.TeamConfig{Enabled: true}}, mutate: func(key *APIKey) { key.TeamMembership.JoinedAt = key.CreatedAt.Add(time.Second) }, want: ErrTeamMembershipRequired},
		{name: "team_suspended", cfg: &config.Config{Team: config.TeamConfig{Enabled: true}}, mutate: func(key *APIKey) { key.Team.Status = TeamStatusSuspended }, want: ErrTeamSuspended},
		{name: "actor_inactive", cfg: &config.Config{Team: config.TeamConfig{Enabled: true}}, mutate: func(key *APIKey) { key.ActorUser.Status = StatusDisabled }, want: ErrTeamActorInactive},
		{name: "owner_inactive", cfg: &config.Config{Team: config.TeamConfig{Enabled: true}}, mutate: func(key *APIKey) { key.User.Status = StatusDisabled }, want: ErrTeamBillingOwnerInactive},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := validTeamAPIKeyForLifecycleTest()
			if test.mutate != nil {
				test.mutate(key)
			}
			err := (&APIKeyService{cfg: test.cfg}).ValidateTeamKeyLifecycle(key)
			if test.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, test.want)
		})
	}
}

func TestHydrateTeamAPIKeyOnlyMapsMissingContextToMembershipError(t *testing.T) {
	repositoryFailure := errors.New("team repository unavailable")
	tests := []struct {
		name    string
		repoErr error
		want    error
	}{
		{name: "team_missing", repoErr: ErrTeamNotFound, want: ErrTeamMembershipRequired},
		{name: "repository_failure", repoErr: repositoryFailure, want: repositoryFailure},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := validTeamAPIKeyForLifecycleTest()
			key.Team = nil
			key.TeamMembership = nil
			key.ActorUser = nil
			key.User = nil
			service := &APIKeyService{
				teamRepo: &teamContextErrorRepository{err: test.repoErr},
				cfg:      &config.Config{Team: config.TeamConfig{Enabled: true}},
			}

			_, err := service.hydrateTeamAPIKey(context.Background(), key, nil)
			require.ErrorIs(t, err, test.want)
		})
	}
}
