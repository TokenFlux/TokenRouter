//go:build unit

package apikey_test

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/team"
	"github.com/stretchr/testify/require"
)

// billingModeSubscriptionRepoStub 只实现 API Key 结算配置读取需要的订阅查询。
type billingModeSubscriptionRepoStub struct {
	billing.UserSubscriptionRepository
	subscriptions map[int64]*billing.UserSubscription
}

// billingModeUserRepoStub 按 ID 返回成员或 Owner，便于验证团队 Key 的付款主体隔离。
type billingModeUserRepoStub struct {
	identity.UserRepository

	users     map[int64]*identity.User
	requested []int64
}

func (s *billingModeUserRepoStub) GetByID(_ context.Context, id int64) (*identity.User, error) {
	s.requested = append(s.requested, id)
	user := s.users[id]
	if user == nil {
		return nil, identity.ErrUserNotFound
	}
	copyUser := *user
	return &copyUser, nil
}

func (s *billingModeSubscriptionRepoStub) GetByID(_ context.Context, id int64) (*billing.UserSubscription, error) {
	subscription := s.subscriptions[id]
	if subscription == nil {
		return nil, billing.ErrSubscriptionNotFound
	}
	copySubscription := *subscription
	return &copySubscription, nil
}

func (s *billingModeSubscriptionRepoStub) ListActiveByUserID(_ context.Context, userID int64) ([]billing.UserSubscription, error) {
	result := make([]billing.UserSubscription, 0)
	for _, subscription := range s.subscriptions {
		if subscription.UserID == userID {
			result = append(result, *subscription)
		}
	}
	return result, nil
}

func activeBillingModeSubscription(id, userID int64, groupIDs ...int64) *billing.UserSubscription {
	now := time.Now()
	return &billing.UserSubscription{
		ID:        id,
		UserID:    userID,
		PlanID:    id,
		Status:    billing.SubscriptionStatusActive,
		StartsAt:  now.Add(-time.Hour),
		ExpiresAt: now.Add(time.Hour),
		Plan: &billing.SubscriptionPlan{
			ID:       id,
			Name:     "restricted plan",
			GroupIDs: append([]int64(nil), groupIDs...),
		},
	}
}

func TestAPIKeyService_CreateBillingModeValidatesPreferredSubscriptionGroups(t *testing.T) {
	const userID int64 = 7
	allowedGroup := &routing.Group{ID: 11, Status: billing.StatusActive, IsExclusive: true}
	blockedGroup := &routing.Group{ID: 12, Status: billing.StatusActive, IsExclusive: true}
	preferredID := int64(101)
	repo := &apiKeyNameSanitizeRepoStub{}
	user := &identity.User{ID: userID, Status: billing.StatusActive, Role: identity.RoleUser, AllowedGroups: []int64{allowedGroup.ID, blockedGroup.ID}}
	svc := testkit.NewService(
		repo,
		&userRepoStub{user: user},
		&compositeGroupRepoStub{groups: map[int64]*routing.Group{allowedGroup.ID: allowedGroup, blockedGroup.ID: blockedGroup}},
		&billingModeSubscriptionRepoStub{subscriptions: map[int64]*billing.UserSubscription{
			preferredID: activeBillingModeSubscription(preferredID, userID, allowedGroup.ID),
		}},
		nil,
		nil,
		nil,
	)
	svc.Start()

	t.Run("指定订阅保留合法分组", func(t *testing.T) {
		customKey := "sk_billing_mode_allowed_group"
		created, err := svc.Create(context.Background(), userID, apikey.CreateAPIKeyRequest{
			Name:                    "subscription key",
			CustomKey:               &customKey,
			GroupID:                 &allowedGroup.ID,
			BillingMode:             apikey.APIKeyBillingModeSubscription,
			PreferredSubscriptionID: &preferredID,
		})

		require.NoError(t, err)
		require.Equal(t, apikey.APIKeyBillingModeSubscription, created.BillingMode)
		require.NotNil(t, created.PreferredSubscriptionID)
		require.Equal(t, preferredID, *created.PreferredSubscriptionID)
	})

	t.Run("指定订阅拒绝套餐外分组", func(t *testing.T) {
		customKey := "sk_billing_mode_blocked_group"
		_, err := svc.Create(context.Background(), userID, apikey.CreateAPIKeyRequest{
			Name:                    "blocked subscription key",
			CustomKey:               &customKey,
			GroupID:                 &blockedGroup.ID,
			BillingMode:             apikey.APIKeyBillingModeSubscription,
			PreferredSubscriptionID: &preferredID,
		})

		require.ErrorIs(t, err, apikey.ErrPreferredSubscriptionGroup)
	})

	t.Run("指定订阅拒绝无绑定分组", func(t *testing.T) {
		customKey := "sk_billing_mode_without_group"
		_, err := svc.Create(context.Background(), userID, apikey.CreateAPIKeyRequest{
			Name:                    "unbound subscription key",
			CustomKey:               &customKey,
			BillingMode:             apikey.APIKeyBillingModeSubscription,
			PreferredSubscriptionID: &preferredID,
		})

		require.ErrorIs(t, err, apikey.ErrPreferredSubscriptionGroup)
	})
}

func TestAPIKeyService_UpdateBillingModeRejectsRestrictedSubscriptionWithoutGroup(t *testing.T) {
	const userID int64 = 7
	preferredID := int64(101)
	apiKey := &apikey.APIKey{
		ID:     1,
		UserID: userID,
		Key:    "sk_billing_mode_without_group_update",
		Status: apikey.StatusAPIKeyActive,
		User:   &identity.User{ID: userID, Status: billing.StatusActive, Role: identity.RoleUser},
	}
	repo := &apiKeyNameSanitizeRepoStub{apiKey: apiKey}
	svc := testkit.NewService(
		repo,
		&userRepoStub{user: apiKey.User},
		nil,
		&billingModeSubscriptionRepoStub{subscriptions: map[int64]*billing.UserSubscription{
			preferredID: activeBillingModeSubscription(preferredID, userID, 11),
		}},
		nil,
		nil,
		nil,
	)
	svc.Start()
	billingMode := apikey.APIKeyBillingModeSubscription

	_, err := svc.Update(context.Background(), apiKey.ID, userID, apikey.UpdateAPIKeyRequest{
		BillingMode:             &billingMode,
		PreferredSubscriptionID: &preferredID,
	})

	require.ErrorIs(t, err, apikey.ErrPreferredSubscriptionGroup)
	require.Empty(t, repo.updated)
}

func TestAPIKeyService_UpdateBillingModeClearsPreferredSubscription(t *testing.T) {
	const userID int64 = 7
	preferredID := int64(101)
	groupID := int64(11)
	repo := &apiKeyNameSanitizeRepoStub{apiKey: &apikey.APIKey{
		ID:                      1,
		UserID:                  userID,
		Key:                     "sk_billing_mode_update",
		Status:                  apikey.StatusAPIKeyActive,
		GroupID:                 &groupID,
		Group:                   &routing.Group{ID: groupID, Status: billing.StatusActive, IsExclusive: true},
		User:                    &identity.User{ID: userID, Status: billing.StatusActive, Role: identity.RoleUser, AllowedGroups: []int64{groupID}},
		BillingMode:             apikey.APIKeyBillingModeSubscription,
		PreferredSubscriptionID: &preferredID,
	}}
	svc := testkit.NewService(
		repo,
		&userRepoStub{user: repo.apiKey.User},
		&compositeGroupRepoStub{groups: map[int64]*routing.Group{groupID: repo.apiKey.Group}},
		&billingModeSubscriptionRepoStub{subscriptions: map[int64]*billing.UserSubscription{
			preferredID: activeBillingModeSubscription(preferredID, userID, groupID),
		}},
		nil,
		nil,
		nil,
	)
	svc.Start()
	billingMode := apikey.APIKeyBillingModeBalance

	updated, err := svc.Update(context.Background(), repo.apiKey.ID, userID, apikey.UpdateAPIKeyRequest{BillingMode: &billingMode})

	require.NoError(t, err)
	require.Equal(t, apikey.APIKeyBillingModeBalance, updated.BillingMode)
	require.Nil(t, updated.PreferredSubscriptionID)
	require.Len(t, repo.updated, 1)
	require.Nil(t, repo.updated[0].PreferredSubscriptionID)
}

func TestAPIKeyService_ListBillingSubscriptionsUsesTeamOwner(t *testing.T) {
	const (
		memberID int64 = 7
		ownerID  int64 = 8
	)
	ownerSubscriptionID := int64(101)
	memberSubscriptionID := int64(102)
	svc := testkit.NewService(
		nil,
		nil,
		nil,
		&billingModeSubscriptionRepoStub{subscriptions: map[int64]*billing.UserSubscription{
			ownerSubscriptionID:  activeBillingModeSubscription(ownerSubscriptionID, ownerID),
			memberSubscriptionID: activeBillingModeSubscription(memberSubscriptionID, memberID),
		}},
		nil,
		nil,
		&config.Config{Team: config.TeamConfig{Enabled: true}},
	)
	svc.Start()
	svc.SetTeamRepository(&fakeTeamRepository{teamContext: &team.TeamContext{
		Team:       &team.Team{ID: 11, Status: team.TeamStatusActive},
		Membership: &team.TeamMembership{TeamID: 11, UserID: memberID, Role: team.TeamRoleMember},
		Owner:      &team.TeamMembership{TeamID: 11, UserID: ownerID, Role: team.TeamRoleOwner},
	}})

	options, err := svc.ListBillingSubscriptionsForScope(context.Background(), memberID, "team")

	require.NoError(t, err)
	require.Len(t, options, 1)
	require.Equal(t, ownerSubscriptionID, options[0].ID)
}

func TestAPIKeyService_UpdateInactiveTeamKeyBillingModeUsesTeamOwner(t *testing.T) {
	const (
		memberID int64 = 7
		ownerID  int64 = 8
		teamID   int64 = 11
		groupID  int64 = 12
	)
	preferredID := int64(101)
	createdAt := time.Now()
	member := &identity.User{ID: memberID, Status: billing.StatusActive, Role: identity.RoleUser}
	owner := &identity.User{ID: ownerID, Status: billing.StatusActive, Role: identity.RoleUser, AllowedGroups: []int64{groupID}}
	group := &routing.Group{ID: groupID, Status: billing.StatusActive, IsExclusive: true}
	apiKey := &apikey.APIKey{
		ID:        1,
		UserID:    memberID,
		TeamID:    func() *int64 { value := teamID; return &value }(),
		Key:       "sk_inactive_team_billing_mode",
		Status:    apikey.StatusAPIKeyDisabled,
		CreatedAt: createdAt,
		GroupID:   &group.ID,
		Group:     group,
		User:      member,
	}
	keyRepo := &apiKeyNameSanitizeRepoStub{apiKey: apiKey}
	userRepo := &billingModeUserRepoStub{users: map[int64]*identity.User{memberID: member, ownerID: owner}}
	svc := testkit.NewService(
		keyRepo,
		userRepo,
		&compositeGroupRepoStub{groups: map[int64]*routing.Group{groupID: group}},
		&billingModeSubscriptionRepoStub{subscriptions: map[int64]*billing.UserSubscription{
			preferredID: activeBillingModeSubscription(preferredID, ownerID, groupID),
		}},
		nil,
		nil,
		&config.Config{Team: config.TeamConfig{Enabled: true}},
	)
	svc.Start()
	svc.SetTeamRepository(&fakeTeamRepository{teamContext: &team.TeamContext{
		Team:       &team.Team{ID: teamID, Status: team.TeamStatusActive},
		Membership: &team.TeamMembership{TeamID: teamID, UserID: memberID, Role: team.TeamRoleMember, JoinedAt: createdAt.Add(-time.Minute)},
		Owner:      &team.TeamMembership{TeamID: teamID, UserID: ownerID, Role: team.TeamRoleOwner},
	}})
	billingMode := apikey.APIKeyBillingModeSubscription

	updated, err := svc.Update(context.Background(), apiKey.ID, memberID, apikey.UpdateAPIKeyRequest{
		BillingMode:             &billingMode,
		PreferredSubscriptionID: &preferredID,
	})

	require.NoError(t, err)
	require.Equal(t, []int64{ownerID}, userRepo.requested)
	require.NotNil(t, updated.User)
	require.Equal(t, ownerID, updated.User.ID)
	require.Equal(t, apikey.APIKeyBillingModeSubscription, updated.BillingMode)
	require.Equal(t, preferredID, *updated.PreferredSubscriptionID)
}

func TestAPIKeyAuthSnapshotRoundTripPreservesBillingMode(t *testing.T) {
	preferredID := int64(101)
	key := &apikey.APIKey{
		ID:                      1,
		UserID:                  7,
		Key:                     "sk_billing_mode_snapshot",
		Status:                  apikey.StatusAPIKeyActive,
		BillingMode:             apikey.APIKeyBillingModeSubscription,
		PreferredSubscriptionID: &preferredID,
		User:                    &identity.User{ID: 7, Status: billing.StatusActive, Role: identity.RoleUser},
	}
	svc := testkit.NewService(nil, nil, nil, nil, nil, nil, nil)
	svc.Start()

	snapshot := svc.KeySnapshotFromAPIKey(context.Background(), key)
	restored := svc.KeySnapshotToAPIKey(key.Key, snapshot)

	require.Equal(t, apikey.APIKeyBillingModeSubscription, restored.BillingMode)
	require.NotNil(t, restored.PreferredSubscriptionID)
	require.Equal(t, preferredID, *restored.PreferredSubscriptionID)
}
