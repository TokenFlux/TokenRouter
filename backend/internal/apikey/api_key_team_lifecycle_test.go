//go:build unit

package apikey_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/team"
	"github.com/stretchr/testify/require"
)

type teamContextErrorRepository struct {
	team.TeamRepository
	err error
}

func (r *teamContextErrorRepository) GetContextByUserID(context.Context, int64) (*team.TeamContext, error) {
	return nil, r.err
}

func validTeamAPIKeyForLifecycleTest() *apikey.APIKey {
	teamID := int64(11)
	createdAt := time.Now()
	return &apikey.APIKey{
		ID:        31,
		UserID:    2,
		TeamID:    &teamID,
		Status:    apikey.StatusAPIKeyActive,
		CreatedAt: createdAt,
		Team:      &team.Team{ID: teamID, Status: team.TeamStatusActive},
		TeamMembership: &team.TeamMembership{
			TeamID:   teamID,
			UserID:   2,
			Role:     team.TeamRoleMember,
			JoinedAt: createdAt.Add(-time.Minute),
		},
		ActorUser: &identity.User{ID: 2, Status: billing.StatusActive},
		User:      &identity.User{ID: 1, Status: billing.StatusActive},
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
		mutate func(*apikey.APIKey)
		cfg    *config.Config
		want   error
	}{
		{name: "valid", cfg: &config.Config{Team: config.TeamConfig{Enabled: true}}},
		{name: "feature_disabled", cfg: &config.Config{}, want: team.ErrTeamFeatureDisabled},
		{name: "membership_missing", cfg: &config.Config{Team: config.TeamConfig{Enabled: true}}, mutate: func(key *apikey.APIKey) { key.TeamMembership = nil }, want: team.ErrTeamMembershipRequired},
		{name: "membership_rejoined_after_key", cfg: &config.Config{Team: config.TeamConfig{Enabled: true}}, mutate: func(key *apikey.APIKey) { key.TeamMembership.JoinedAt = key.CreatedAt.Add(time.Second) }, want: team.ErrTeamMembershipRequired},
		{name: "team_suspended", cfg: &config.Config{Team: config.TeamConfig{Enabled: true}}, mutate: func(key *apikey.APIKey) { key.Team.Status = team.TeamStatusSuspended }, want: team.ErrTeamSuspended},
		{name: "actor_inactive", cfg: &config.Config{Team: config.TeamConfig{Enabled: true}}, mutate: func(key *apikey.APIKey) { key.ActorUser.Status = billing.StatusDisabled }, want: apikey.ErrTeamActorInactive},
		{name: "owner_inactive", cfg: &config.Config{Team: config.TeamConfig{Enabled: true}}, mutate: func(key *apikey.APIKey) { key.User.Status = billing.StatusDisabled }, want: apikey.ErrTeamBillingOwnerInactive},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := validTeamAPIKeyForLifecycleTest()
			if test.mutate != nil {
				test.mutate(key)
			}
			err := (newAPIKeyTestService(apiKeyTestDependencies{cfg: test.cfg})).ValidateTeamKeyLifecycle(key)
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
		{name: "team_missing", repoErr: team.ErrTeamNotFound, want: team.ErrTeamMembershipRequired},
		{name: "repository_failure", repoErr: repositoryFailure, want: repositoryFailure},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := validTeamAPIKeyForLifecycleTest()
			key.Team = nil
			key.TeamMembership = nil
			key.ActorUser = nil
			key.User = nil
			service := newAPIKeyTestService(apiKeyTestDependencies{
				teamRepo: &teamContextErrorRepository{err: test.repoErr},
				cfg:      &config.Config{Team: config.TeamConfig{Enabled: true}},
			})

			_, err := service.KeyHydrateTeamAPIKey(context.Background(), key, nil)
			require.ErrorIs(t, err, test.want)
		})
	}
}
