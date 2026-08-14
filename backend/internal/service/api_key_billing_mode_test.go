//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

// billingModeSubscriptionRepoStub 只实现 API Key 结算配置读取需要的订阅查询。
type billingModeSubscriptionRepoStub struct {
	UserSubscriptionRepository
	subscriptions map[int64]*UserSubscription
}

// billingModeUserRepoStub 按 ID 返回成员或 Owner，便于验证团队 Key 的付款主体隔离。
type billingModeUserRepoStub struct {
	UserRepository
	users     map[int64]*User
	requested []int64
}

func (s *billingModeUserRepoStub) GetByID(_ context.Context, id int64) (*User, error) {
	s.requested = append(s.requested, id)
	user := s.users[id]
	if user == nil {
		return nil, ErrUserNotFound
	}
	copyUser := *user
	return &copyUser, nil
}

func (s *billingModeSubscriptionRepoStub) GetByID(_ context.Context, id int64) (*UserSubscription, error) {
	subscription := s.subscriptions[id]
	if subscription == nil {
		return nil, ErrSubscriptionNotFound
	}
	copySubscription := *subscription
	return &copySubscription, nil
}

func (s *billingModeSubscriptionRepoStub) ListActiveByUserID(_ context.Context, userID int64) ([]UserSubscription, error) {
	result := make([]UserSubscription, 0)
	for _, subscription := range s.subscriptions {
		if subscription.UserID == userID {
			result = append(result, *subscription)
		}
	}
	return result, nil
}

func activeBillingModeSubscription(id, userID int64, groupIDs ...int64) *UserSubscription {
	now := time.Now()
	return &UserSubscription{
		ID:        id,
		UserID:    userID,
		PlanID:    id,
		Status:    SubscriptionStatusActive,
		StartsAt:  now.Add(-time.Hour),
		ExpiresAt: now.Add(time.Hour),
		Plan: &SubscriptionPlan{
			ID:       id,
			Name:     "restricted plan",
			GroupIDs: append([]int64(nil), groupIDs...),
		},
	}
}

func TestAPIKeyService_CreateBillingModeValidatesPreferredSubscriptionGroups(t *testing.T) {
	const userID int64 = 7
	allowedGroup := &Group{ID: 11, Status: StatusActive, IsExclusive: true}
	blockedGroup := &Group{ID: 12, Status: StatusActive, IsExclusive: true}
	preferredID := int64(101)
	repo := &apiKeyNameSanitizeRepoStub{}
	user := &User{ID: userID, Status: StatusActive, Role: RoleUser, AllowedGroups: []int64{allowedGroup.ID, blockedGroup.ID}}
	svc := NewAPIKeyService(
		repo,
		&userRepoStub{user: user},
		&compositeGroupRepoStub{groups: map[int64]*Group{allowedGroup.ID: allowedGroup, blockedGroup.ID: blockedGroup}},
		&billingModeSubscriptionRepoStub{subscriptions: map[int64]*UserSubscription{
			preferredID: activeBillingModeSubscription(preferredID, userID, allowedGroup.ID),
		}},
		nil,
		nil,
		nil,
	)

	t.Run("指定订阅保留合法分组", func(t *testing.T) {
		customKey := "sk_billing_mode_allowed_group"
		created, err := svc.Create(context.Background(), userID, CreateAPIKeyRequest{
			Name:                    "subscription key",
			CustomKey:               &customKey,
			GroupID:                 &allowedGroup.ID,
			BillingMode:             APIKeyBillingModeSubscription,
			PreferredSubscriptionID: &preferredID,
		})

		require.NoError(t, err)
		require.Equal(t, APIKeyBillingModeSubscription, created.BillingMode)
		require.NotNil(t, created.PreferredSubscriptionID)
		require.Equal(t, preferredID, *created.PreferredSubscriptionID)
	})

	t.Run("指定订阅拒绝套餐外分组", func(t *testing.T) {
		customKey := "sk_billing_mode_blocked_group"
		_, err := svc.Create(context.Background(), userID, CreateAPIKeyRequest{
			Name:                    "blocked subscription key",
			CustomKey:               &customKey,
			GroupID:                 &blockedGroup.ID,
			BillingMode:             APIKeyBillingModeSubscription,
			PreferredSubscriptionID: &preferredID,
		})

		require.ErrorIs(t, err, ErrPreferredSubscriptionGroup)
	})

	t.Run("指定订阅拒绝无绑定分组", func(t *testing.T) {
		customKey := "sk_billing_mode_without_group"
		_, err := svc.Create(context.Background(), userID, CreateAPIKeyRequest{
			Name:                    "unbound subscription key",
			CustomKey:               &customKey,
			BillingMode:             APIKeyBillingModeSubscription,
			PreferredSubscriptionID: &preferredID,
		})

		require.ErrorIs(t, err, ErrPreferredSubscriptionGroup)
	})
}

func TestAPIKeyService_UpdateBillingModeRejectsRestrictedSubscriptionWithoutGroup(t *testing.T) {
	const userID int64 = 7
	preferredID := int64(101)
	apiKey := &APIKey{
		ID:     1,
		UserID: userID,
		Key:    "sk_billing_mode_without_group_update",
		Status: StatusAPIKeyActive,
		User:   &User{ID: userID, Status: StatusActive, Role: RoleUser},
	}
	repo := &apiKeyNameSanitizeRepoStub{apiKey: apiKey}
	svc := NewAPIKeyService(
		repo,
		&userRepoStub{user: apiKey.User},
		nil,
		&billingModeSubscriptionRepoStub{subscriptions: map[int64]*UserSubscription{
			preferredID: activeBillingModeSubscription(preferredID, userID, 11),
		}},
		nil,
		nil,
		nil,
	)
	billingMode := APIKeyBillingModeSubscription

	_, err := svc.Update(context.Background(), apiKey.ID, userID, UpdateAPIKeyRequest{
		BillingMode:             &billingMode,
		PreferredSubscriptionID: &preferredID,
	})

	require.ErrorIs(t, err, ErrPreferredSubscriptionGroup)
	require.Empty(t, repo.updated)
}

func TestAPIKeyService_UpdateBillingModeClearsPreferredSubscription(t *testing.T) {
	const userID int64 = 7
	preferredID := int64(101)
	groupID := int64(11)
	repo := &apiKeyNameSanitizeRepoStub{apiKey: &APIKey{
		ID:                      1,
		UserID:                  userID,
		Key:                     "sk_billing_mode_update",
		Status:                  StatusAPIKeyActive,
		GroupID:                 &groupID,
		Group:                   &Group{ID: groupID, Status: StatusActive, IsExclusive: true},
		User:                    &User{ID: userID, Status: StatusActive, Role: RoleUser, AllowedGroups: []int64{groupID}},
		BillingMode:             APIKeyBillingModeSubscription,
		PreferredSubscriptionID: &preferredID,
	}}
	svc := NewAPIKeyService(
		repo,
		&userRepoStub{user: repo.apiKey.User},
		&compositeGroupRepoStub{groups: map[int64]*Group{groupID: repo.apiKey.Group}},
		&billingModeSubscriptionRepoStub{subscriptions: map[int64]*UserSubscription{
			preferredID: activeBillingModeSubscription(preferredID, userID, groupID),
		}},
		nil,
		nil,
		nil,
	)
	billingMode := APIKeyBillingModeBalance

	updated, err := svc.Update(context.Background(), repo.apiKey.ID, userID, UpdateAPIKeyRequest{BillingMode: &billingMode})

	require.NoError(t, err)
	require.Equal(t, APIKeyBillingModeBalance, updated.BillingMode)
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
	svc := NewAPIKeyService(
		nil,
		nil,
		nil,
		&billingModeSubscriptionRepoStub{subscriptions: map[int64]*UserSubscription{
			ownerSubscriptionID:  activeBillingModeSubscription(ownerSubscriptionID, ownerID),
			memberSubscriptionID: activeBillingModeSubscription(memberSubscriptionID, memberID),
		}},
		nil,
		nil,
		&config.Config{Team: config.TeamConfig{Enabled: true}},
	)
	svc.SetTeamRepository(&fakeTeamRepository{teamContext: &TeamContext{
		Team:       &Team{ID: 11, Status: TeamStatusActive},
		Membership: &TeamMembership{TeamID: 11, UserID: memberID, Role: TeamRoleMember},
		Owner:      &TeamMembership{TeamID: 11, UserID: ownerID, Role: TeamRoleOwner},
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
	member := &User{ID: memberID, Status: StatusActive, Role: RoleUser}
	owner := &User{ID: ownerID, Status: StatusActive, Role: RoleUser, AllowedGroups: []int64{groupID}}
	group := &Group{ID: groupID, Status: StatusActive, IsExclusive: true}
	apiKey := &APIKey{
		ID:        1,
		UserID:    memberID,
		TeamID:    func() *int64 { value := teamID; return &value }(),
		Key:       "sk_inactive_team_billing_mode",
		Status:    StatusAPIKeyDisabled,
		CreatedAt: createdAt,
		GroupID:   &group.ID,
		Group:     group,
		User:      member,
	}
	keyRepo := &apiKeyNameSanitizeRepoStub{apiKey: apiKey}
	userRepo := &billingModeUserRepoStub{users: map[int64]*User{memberID: member, ownerID: owner}}
	svc := NewAPIKeyService(
		keyRepo,
		userRepo,
		&compositeGroupRepoStub{groups: map[int64]*Group{groupID: group}},
		&billingModeSubscriptionRepoStub{subscriptions: map[int64]*UserSubscription{
			preferredID: activeBillingModeSubscription(preferredID, ownerID, groupID),
		}},
		nil,
		nil,
		&config.Config{Team: config.TeamConfig{Enabled: true}},
	)
	svc.SetTeamRepository(&fakeTeamRepository{teamContext: &TeamContext{
		Team:       &Team{ID: teamID, Status: TeamStatusActive},
		Membership: &TeamMembership{TeamID: teamID, UserID: memberID, Role: TeamRoleMember, JoinedAt: createdAt.Add(-time.Minute)},
		Owner:      &TeamMembership{TeamID: teamID, UserID: ownerID, Role: TeamRoleOwner},
	}})
	billingMode := APIKeyBillingModeSubscription

	updated, err := svc.Update(context.Background(), apiKey.ID, memberID, UpdateAPIKeyRequest{
		BillingMode:             &billingMode,
		PreferredSubscriptionID: &preferredID,
	})

	require.NoError(t, err)
	require.Equal(t, []int64{ownerID}, userRepo.requested)
	require.NotNil(t, updated.User)
	require.Equal(t, ownerID, updated.User.ID)
	require.Equal(t, APIKeyBillingModeSubscription, updated.BillingMode)
	require.Equal(t, preferredID, *updated.PreferredSubscriptionID)
}

func TestAPIKeyAuthSnapshotRoundTripPreservesBillingMode(t *testing.T) {
	preferredID := int64(101)
	key := &APIKey{
		ID:                      1,
		UserID:                  7,
		Key:                     "sk_billing_mode_snapshot",
		Status:                  StatusAPIKeyActive,
		BillingMode:             APIKeyBillingModeSubscription,
		PreferredSubscriptionID: &preferredID,
		User:                    &User{ID: 7, Status: StatusActive, Role: RoleUser},
	}
	svc := NewAPIKeyService(nil, nil, nil, nil, nil, nil, nil)

	snapshot := svc.snapshotFromAPIKey(context.Background(), key)
	restored := svc.snapshotToAPIKey(key.Key, snapshot)

	require.Equal(t, APIKeyBillingModeSubscription, restored.BillingMode)
	require.NotNil(t, restored.PreferredSubscriptionID)
	require.Equal(t, preferredID, *restored.PreferredSubscriptionID)
}
