package apikey_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/team"
)

// apiKeyLimitUpperBound reuses the service's exclusive database amount bound
// so numeric fixtures exercise the same accepted interval as production validation.
const apiKeyLimitUpperBound = apikey.KeyApiKeyLimitUpperBound

// billingModeSubscriptionRepoStub returns configured shallow subscription views
// for funding selection tests.
type billingModeSubscriptionRepoStub struct {
	billing.UserSubscriptionRepository // unused subscription methods require setup
	// subscriptions supplies shallow results whose Plan pointers remain shared.
	// Tests configure entries before funding reads and provide nonnil values for
	// fixture list iteration.
	subscriptions map[int64]*billing.UserSubscription // configured payer subscription records
}

// billingModeUserRepoStub records payer reads and returns configured shallow
// identity views.
type billingModeUserRepoStub struct {
	identity.UserRepository // unused identity methods require setup
	// users supplies shallow identity results with shared nested fields; requested
	// captures attempted payer reads. Scenarios populate records and inspect the
	// unsynchronized list sequentially.
	users     map[int64]*identity.User // configured payer identity records
	requested []int64                  // captured payer lookup identifiers
}

// authGroupRepoStub returns configured target and platform group values for
// explicit selection tests.
type authGroupRepoStub struct {
	// groupsByPlatform and groupsByID provide shallow group values with retained
	// nested references. Lite ID reads require a configured target map; platform
	// reads copy its selected slice.
	groupsByPlatform map[string][]routing.Group // configured platform candidate slices
	groupsByID       map[int64]routing.Group    // configured target group values
}

// authUserGroupRateRepoStub records group-specific override reads and returns
// configured pointer values.
type authUserGroupRateRepoStub struct {
	// overrides returns retained RPM pointers, including nil for a missing group;
	// calls captures queried group IDs. Tests inspect these unsynchronized
	// observations after sequential service reads.
	overrides map[int64]*int // configured group RPM pointers
	calls     []int64        // captured group override requests
}

// apiKeyRepoStub supplies credential or page results and captures
// configuration, deletion and touch attempts.
type apiKeyRepoStub struct {
	// apiKey supplies shallow lookup copies; nested references remain shared.
	// getByIDErr takes precedence over the retained view, and an absent successful
	// GetByID record panics.
	apiKey     *apikey.APIKey // configured credential lookup view
	getByIDErr error          // configured credential lookup failure
	deleteErr  error          // configured credential deletion failure
	updateErr  error          // configured configuration write failure
	deletedIDs []int64        // captured credential deletion identifiers
	// updatedKeys records shallow values even when updateErr rejects the write.
	// Scenarios inspect attempts sequentially because capture slices have no
	// synchronization.
	updatedKeys []apikey.APIKey // captured configuration write attempts
	// allowListByUserID enables list calls before use; disabled calls panic.
	// Enabled calls capture input before listByUserIDErr is returned or configured
	// page values are copied.
	allowListByUserID   bool                          // creator page fixture enablement
	listByUserIDKeys    []apikey.APIKey               // configured creator page records
	listByUserIDErr     error                         // configured creator page failure
	listByUserIDCalls   []int64                       // captured creator page identifiers
	listByUserIDParams  []pagination.PaginationParams // captured creator page parameters
	listByUserIDFilters []apikey.APIKeyListFilters    // captured creator page filters
	// updateLastUsed is installed before a touch scenario and supplies its result
	// after ID and time capture. touchedIDs and touchedUsedAts are unsynchronized;
	// the concurrent singleflight test uses a separate locked recorder.
	updateLastUsed func(ctx context.Context, id int64, usedAt time.Time) error // optional touch result callback
	touchedIDs     []int64                                                     // captured touched credential identifiers
	touchedUsedAts []time.Time                                                 // captured touch write timestamps
}

// apiKeyCacheStub records creator-counter and authentication deletion attempts
// while acknowledging other cache operations.
type apiKeyCacheStub struct {
	// invalidated and deleteAuthKeys record optional cleanup intent for sequential
	// service scenarios. Captures remain in local slices and do not change shared
	// storage.
	invalidated    []int64  // captured creator counter cleanup identifiers
	deleteAuthKeys []string // captured hashed credential deletion identities
}

// apiKeyNameSanitizeRepoStub captures shallow creation and update records for
// name and funding tests.
type apiKeyNameSanitizeRepoStub struct {
	// apiKey is replaced after a captured update; created and updated retain
	// shallow records with shared nested data. These fixture operations are
	// sequenced by their scenarios.
	apiKey  *apikey.APIKey   // configured credential lookup record
	created []*apikey.APIKey // captured successful creation records
	updated []*apikey.APIKey // captured configuration update records
}

// quotaStateRepoStub exposes atomic quota observations while retaining count
// and selected failure state.
type quotaStateRepoStub struct {
	quotaBaseAPIKeyRepoStub // embedded unsupported fallback fixture
	// stateCalls counts each attempt before stateErr takes precedence over state.
	// Successful state reads copy the record, while nil state yields nil data with
	// no error.
	stateCalls int                           // counted atomic quota capability calls
	state      *apikey.APIKeyQuotaUsageState // configured quota result observation
	stateErr   error                         // configured quota capability failure
}

// quotaStateCacheStub captures authentication deletion intent after observed
// quota exhaustion.
type quotaStateCacheStub struct {
	deleteAuthKeys []string // captured exhaustion invalidation identities
}

// quotaBaseAPIKeyRepoStub counts follow-up Key reads while rejecting
// unsupported persistence paths.
type quotaBaseAPIKeyRepoStub struct {
	getByIDCalls int // counted fallback credential reads
}

// updateFieldsAPIKeyRepoStub mutates fixture quota and captures selected
// configuration masks.
type updateFieldsAPIKeyRepoStub struct {
	quotaBaseAPIKeyRepoStub // embedded unsupported fallback fixture
	// key remains shared with the scenario while quota increments change its
	// scalar usage. updateFields captures status or configuration masks
	// independently and does not replace that record.
	key          *apikey.APIKey              // retained quota and mask record
	updateFields []apikey.APIKeyUpdateFields // captured selected persistence masks
}

// userRepoStub supplies one retained user pointer for Key permission scenarios.
type userRepoStub struct {
	identity.UserRepository // unused identity methods require setup
	// user is returned by reference for every fixture identity read; scenarios
	// configure the requested permission data before using the service.
	user *identity.User // retained creator permission view
}

// fakeTeamRepository returns one retained context for both creator and team
// identity lookup.
type fakeTeamRepository struct {
	team.TeamRepository // unused team methods require setup
	// teamContext is returned by reference for both creator and team queries.
	// Scenarios provide complete member and owner records before successful
	// service use.
	teamContext *team.TeamContext // retained current membership fixture
}

// stubConcurrencyCacheForTest returns configured live Key observations through
// the scheduler cache interface.
type stubConcurrencyCacheForTest struct {
	scheduler.ConcurrencyCache // unused concurrency methods require setup
	// apiKeyConcurrency supplies observations only; track and release calls leave
	// its values unchanged. Batch reads allocate a separate result map with zero
	// for absent IDs.
	apiKeyConcurrency map[int64]int // configured active Key observations
}

// TestAPIKeyService_CreateBillingModeValidatesPreferredSubscriptionGroups
// creates specified-subscription credentials against permitted, plan-excluded
// and absent group bindings. It checks accepted funding selection, rejected
// plan coverage and the ordinary group presence required by creation.
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

		require.Error(t, err)
		require.Contains(t, err.Error(), "GROUP_REQUIRED")
	})
}

// TestAPIKeyService_UpdateBillingModeRejectsRestrictedSubscriptionWithoutGroup
// selects a restricted plan for an existing unbound credential. It checks the
// specified-subscription group error and absence of a captured configuration
// write.
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

// TestAPIKeyService_UpdateBillingModeClearsPreferredSubscription changes a
// specified-subscription credential to wallet funding. It checks the selected
// subscription pointer clears in both the returned view and the captured
// successful update.
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

// TestAPIKeyService_ListBillingSubscriptionsUsesTeamOwner offers separate
// member and owner subscriptions for team scope. It checks only the current
// owner's eligible subscription appears in the returned configuration options.
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

// TestAPIKeyService_UpdateInactiveTeamKeyBillingModeUsesTeamOwner changes
// funding on a disabled team credential retaining its creator view. It checks
// the service reads owner identity and validates the owner's subscription for
// the resulting Key.
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

// TestAPIKeyServiceManagedKeyHidden loads one hidden managed record through
// user read, update and deletion paths. It checks each reports Key-not-found
// and deletion is rejected before the fixture receives a lifecycle write.
func TestAPIKeyServiceManagedKeyHidden(t *testing.T) {
	ctx := context.Background()
	managedBy := creative.CreativeManagedBy
	managed := &apikey.APIKey{
		ID:        42,
		UserID:    7,
		Key:       "sk-hidden-managed-key",
		Name:      "creative-studio:12",
		Status:    billing.StatusActive,
		ManagedBy: &managedBy,
	}
	repo := &apiKeyRepoStub{apiKey: managed}
	svc := testkit.NewService(repo, nil, nil, nil, nil, nil, nil)
	svc.Start()

	// Reading a managed key returns no key and ErrAPIKeyNotFound.
	got, err := svc.GetByID(ctx, 42)
	require.Nil(t, got)
	require.Error(t, err)
	require.True(t, errors.Is(err, apikey.ErrAPIKeyNotFound), "managed key GetByID 必须返回 ErrAPIKeyNotFound")

	// Updating a managed key must also hide its existence.
	hackedName := "hacked"
	updated, err := svc.Update(ctx, 42, 7, apikey.UpdateAPIKeyRequest{Name: &hackedName})
	require.Nil(t, updated)
	require.True(t, errors.Is(err, apikey.ErrAPIKeyNotFound), "managed key Update 必须返回 ErrAPIKeyNotFound")

	// Reject deletion before calling DeleteWithAudit.
	err = svc.Delete(ctx, 42, 7)
	require.True(t, errors.Is(err, apikey.ErrAPIKeyNotFound), "managed key Delete 必须返回 ErrAPIKeyNotFound")
	require.Empty(t, repo.deletedIDs, "managed key 不得进入删除流程")
}

// TestAPIKeyService_GetByKey_UsesL2Cache supplies a current positive shared
// snapshot while storage reads fail. It checks reconstruction returns Key and
// payer identities plus the supplied group model-candidate map.
func TestAPIKeyService_GetByKey_UsesL2Cache(t *testing.T) {
	cache := &authCacheStub{}
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return nil, errors.New("unexpected repo call")
		},
	}
	cfg := &config.Config{APIKey: config.APIKeyConfig{AuthCache: config.APIKeyAuthCacheConfig{
		L2TTLSeconds:       60,
		NegativeTTLSeconds: 30,
	}}}
	svc := testkit.NewService(repo, nil, nil, nil, nil, cache, cfg)
	svc.Start()

	groupID := int64(9)
	cacheEntry := &apikey.APIKeyAuthCacheEntry{
		Snapshot: &apikey.APIKeyAuthSnapshot{
			Version:  apikey.KeyApiKeyAuthSnapshotVersion,
			APIKeyID: 1,
			UserID:   2,
			GroupID:  &groupID,
			Status:   billing.StatusActive,
			User: apikey.APIKeyAuthUserSnapshot{
				ID:          2,
				Status:      billing.StatusActive,
				Role:        identity.RoleUser,
				Balance:     10,
				Concurrency: 3,
			},
			Group: &apikey.APIKeyAuthGroupSnapshot{
				ID:   groupID,
				Name: "g",

				Status:              billing.StatusActive,
				RateMultiplier:      1,
				ModelRoutingEnabled: true,
				ModelRouting: map[string][]int64{
					"claude-opus-*": {1, 2},
				},
			},
		},
	}
	cache.getAuthCache = func(ctx context.Context, key string) (*apikey.APIKeyAuthCacheEntry, error) {
		return cacheEntry, nil
	}

	apiKey, err := svc.GetByKey(context.Background(), "k1")
	require.NoError(t, err)
	require.Equal(t, int64(1), apiKey.ID)
	require.Equal(t, int64(2), apiKey.User.ID)
	require.Equal(t, groupID, apiKey.Group.ID)
	require.True(t, apiKey.Group.ModelRoutingEnabled)
	require.Equal(t, map[string][]int64{"claude-opus-*": {1, 2}}, apiKey.Group.ModelRouting)
}

// TestAPIKeyService_GetByKey_KeepsDisabledGroupWithoutConfiguredFallbackFromRepo
// loads a disabled bound group with no saved unavailable target. It checks the
// original binding and state remain, and no target override is prefetched
// despite another candidate group.
func TestAPIKeyService_GetByKey_KeepsDisabledGroupWithoutConfiguredFallbackFromRepo(t *testing.T) {
	disabledGroupID := int64(9)
	defaultGroupID := int64(10)
	defaultRPM := 77
	oldRPM := 3
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return &apikey.APIKey{
				ID:                           1,
				UserID:                       2,
				GroupID:                      &disabledGroupID,
				Key:                          key,
				Status:                       billing.StatusActive,
				FallbackWhenGroupUnavailable: true,
				User: &identity.User{
					ID:                   2,
					Status:               billing.StatusActive,
					Role:                 identity.RoleUser,
					Balance:              10,
					Concurrency:          3,
					UserGroupRPMOverride: &oldRPM,
				},
				Group: &routing.Group{
					ID:   disabledGroupID,
					Name: "openai-disabled",

					Status:         billing.StatusDisabled,
					Hydrated:       true,
					RateMultiplier: 9,
				},
			}, nil
		},
	}
	groupRepo := &authGroupRepoStub{
		groupsByPlatform: map[string][]routing.Group{
			capability.PlatformOpenAI: {
				{
					ID:   defaultGroupID,
					Name: "openai-default",

					Status:   billing.StatusActive,
					Hydrated: true,

					RateMultiplier: 1.5,
				},
			},
		},
	}
	rateRepo := &authUserGroupRateRepoStub{overrides: map[int64]*int{defaultGroupID: &defaultRPM}}
	svc := testkit.NewService(repo, nil, groupRepo, nil, rateRepo, nil, &config.Config{})
	svc.Start()

	apiKey, err := svc.GetByKey(context.Background(), "k-disabled")
	require.NoError(t, err)
	require.NotNil(t, apiKey.GroupID)
	require.Equal(t, disabledGroupID, *apiKey.GroupID)
	require.NotNil(t, apiKey.Group)
	require.Equal(t, disabledGroupID, apiKey.Group.ID)
	require.Equal(t, billing.StatusDisabled, apiKey.Group.Status)
	require.Nil(t, apiKey.User.UserGroupRPMOverride)
	require.Empty(t, rateRepo.calls)
}

// TestAPIKeyService_GetByKey_FallsBackDisabledBoundGroupToConfiguredGroup loads
// a disabled group carrying one saved unavailable target. It checks the
// effective Key uses that target and its loaded group data instead of another
// unrelated candidate.
func TestAPIKeyService_GetByKey_FallsBackDisabledBoundGroupToConfiguredGroup(t *testing.T) {
	disabledGroupID := int64(9)
	configuredFallbackID := int64(11)
	defaultGroupID := int64(10)
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return &apikey.APIKey{
				ID:                           1,
				UserID:                       2,
				GroupID:                      &disabledGroupID,
				Key:                          key,
				Status:                       billing.StatusActive,
				FallbackWhenGroupUnavailable: true,
				User: &identity.User{
					ID:          2,
					Status:      billing.StatusActive,
					Role:        identity.RoleUser,
					Balance:     10,
					Concurrency: 3,
				},
				Group: &routing.Group{
					ID:   disabledGroupID,
					Name: "openai-disabled",

					Status:                     billing.StatusDisabled,
					Hydrated:                   true,
					UnavailableFallbackGroupID: &configuredFallbackID,
				},
			}, nil
		},
	}
	groupRepo := &authGroupRepoStub{
		groupsByID: map[int64]routing.Group{
			configuredFallbackID: {
				ID:   configuredFallbackID,
				Name: "openai-configured-fallback",

				Status:         billing.StatusActive,
				Hydrated:       true,
				RateMultiplier: 1.2,
			},
		},
		groupsByPlatform: map[string][]routing.Group{
			capability.PlatformOpenAI: {
				{
					ID:   defaultGroupID,
					Name: "openai-default",

					Status:   billing.StatusActive,
					Hydrated: true,

					RateMultiplier: 1,
				},
			},
		},
	}
	svc := testkit.NewService(repo, nil, groupRepo, nil, nil, nil, &config.Config{})
	svc.Start()

	apiKey, err := svc.GetByKey(context.Background(), "k-disabled")
	require.NoError(t, err)
	require.NotNil(t, apiKey.GroupID)
	require.Equal(t, configuredFallbackID, *apiKey.GroupID)
	require.NotNil(t, apiKey.Group)
	require.Equal(t, configuredFallbackID, apiKey.Group.ID)
	require.Equal(t, "openai-configured-fallback", apiKey.Group.Name)
}

// TestAPIKeyService_GetByKey_UsesConfiguredFallbackWithoutPlatformConstraint
// loads a configured target while other platform-labelled candidates exist. It
// checks target selection follows the stored identifier and returns its group
// view.
func TestAPIKeyService_GetByKey_UsesConfiguredFallbackWithoutPlatformConstraint(t *testing.T) {
	disabledGroupID := int64(9)
	configuredFallbackID := int64(11)
	defaultGroupID := int64(10)
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return &apikey.APIKey{
				ID:                           1,
				UserID:                       2,
				GroupID:                      &disabledGroupID,
				Key:                          key,
				Status:                       billing.StatusActive,
				FallbackWhenGroupUnavailable: true,
				User: &identity.User{
					ID:          2,
					Status:      billing.StatusActive,
					Role:        identity.RoleUser,
					Balance:     10,
					Concurrency: 3,
				},
				Group: &routing.Group{
					ID:   disabledGroupID,
					Name: "openai-disabled",

					Status:                     billing.StatusDisabled,
					Hydrated:                   true,
					UnavailableFallbackGroupID: &configuredFallbackID,
				},
			}, nil
		},
	}
	groupRepo := &authGroupRepoStub{
		groupsByID: map[int64]routing.Group{
			configuredFallbackID: {
				ID:   configuredFallbackID,
				Name: "mixed-fallback",

				Status:   billing.StatusActive,
				Hydrated: true,
			},
		},
		groupsByPlatform: map[string][]routing.Group{
			capability.PlatformOpenAI: {
				{
					ID:   defaultGroupID,
					Name: "openai-default",

					Status:   billing.StatusActive,
					Hydrated: true,
				},
			},
		},
	}
	svc := testkit.NewService(repo, nil, groupRepo, nil, nil, nil, &config.Config{})
	svc.Start()

	apiKey, err := svc.GetByKey(context.Background(), "k-disabled")
	require.NoError(t, err)
	require.NotNil(t, apiKey.GroupID)
	require.Equal(t, configuredFallbackID, *apiKey.GroupID)
	require.NotNil(t, apiKey.Group)
	require.Equal(t, configuredFallbackID, apiKey.Group.ID)
}

// TestAPIKeyService_GetByKey_KeepsDisabledGroupWithoutConfiguredFallbackFromAuthCache
// reconstructs a disabled-group snapshot carrying a payer override but no
// target. It checks the same disabled binding and saved override value remain
// available for later gateway admission.
func TestAPIKeyService_GetByKey_KeepsDisabledGroupWithoutConfiguredFallbackFromAuthCache(t *testing.T) {
	cache := &authCacheStub{}
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return nil, errors.New("unexpected repo call")
		},
	}
	disabledGroupID := int64(9)
	defaultGroupID := int64(10)
	groupRepo := &authGroupRepoStub{
		groupsByPlatform: map[string][]routing.Group{
			capability.PlatformGemini: {
				{
					ID:   defaultGroupID,
					Name: "gemini-default",

					Status:   billing.StatusActive,
					Hydrated: true,

					RateMultiplier: 2,
				},
			},
		},
	}
	oldRPM := 3
	cache.getAuthCache = func(ctx context.Context, key string) (*apikey.APIKeyAuthCacheEntry, error) {
		return &apikey.APIKeyAuthCacheEntry{
			Snapshot: &apikey.APIKeyAuthSnapshot{
				Version:                      apikey.KeyApiKeyAuthSnapshotVersion,
				APIKeyID:                     1,
				UserID:                       2,
				GroupID:                      &disabledGroupID,
				Status:                       billing.StatusActive,
				FallbackWhenGroupUnavailable: true,
				User: apikey.APIKeyAuthUserSnapshot{
					ID:                   2,
					Status:               billing.StatusActive,
					Role:                 identity.RoleUser,
					Balance:              10,
					Concurrency:          3,
					UserGroupRPMOverride: &oldRPM,
				},
				Group: &apikey.APIKeyAuthGroupSnapshot{
					ID:   disabledGroupID,
					Name: "gemini-disabled",

					Status:         billing.StatusDisabled,
					RateMultiplier: 8,
				},
			},
		}, nil
	}
	cfg := &config.Config{APIKey: config.APIKeyConfig{AuthCache: config.APIKeyAuthCacheConfig{L2TTLSeconds: 60}}}
	svc := testkit.NewService(repo, nil, groupRepo, nil, nil, cache, cfg)
	svc.Start()

	apiKey, err := svc.GetByKey(context.Background(), "k-cached-disabled")
	require.NoError(t, err)
	require.NotNil(t, apiKey.GroupID)
	require.Equal(t, disabledGroupID, *apiKey.GroupID)
	require.NotNil(t, apiKey.Group)
	require.Equal(t, disabledGroupID, apiKey.Group.ID)
	require.NotNil(t, apiKey.User.UserGroupRPMOverride)
	require.Equal(t, oldRPM, *apiKey.User.UserGroupRPMOverride)
}

// TestAPIKeyService_GetByKey_DoesNotFallbackDeletedOrMissingBoundGroup loads a
// deleted-state group or an absent edge with the fallback preference unset.
// It checks the saved Key and group identifiers stay unchanged, and an absent
// group edge remains absent after loading.
func TestAPIKeyService_GetByKey_DoesNotFallbackDeletedOrMissingBoundGroup(t *testing.T) {
	t.Run("deleted status", func(t *testing.T) {
		groupID := int64(9)
		repo := &authRepoStub{
			getByKeyForAuth: func(ctx context.Context, key string) (*apikey.APIKey, error) {
				return &apikey.APIKey{
					ID:      1,
					UserID:  2,
					GroupID: &groupID,
					Key:     key,
					Status:  billing.StatusActive,
					User: &identity.User{
						ID:          2,
						Status:      billing.StatusActive,
						Role:        identity.RoleUser,
						Balance:     10,
						Concurrency: 3,
					},
					Group: &routing.Group{
						ID:   groupID,
						Name: "deleted",

						Status:   "deleted",
						Hydrated: true,
					},
				}, nil
			},
		}
		groupRepo := &authGroupRepoStub{
			groupsByPlatform: map[string][]routing.Group{
				capability.PlatformOpenAI: {{ID: 10, Name: "openai-default", Status: billing.StatusActive, Hydrated: true}},
			},
		}
		ctx := apikey.WithInboundEndpoint(context.Background(), "/v1/images/generations")
		svc := testkit.NewService(repo, nil, groupRepo, nil, nil, nil, &config.Config{})
		svc.Start()

		apiKey, err := svc.GetByKey(ctx, "k-deleted")
		require.NoError(t, err)
		require.NotNil(t, apiKey.GroupID)
		require.Equal(t, groupID, *apiKey.GroupID)
		require.Equal(t, groupID, apiKey.Group.ID)
	})

	t.Run("missing edge", func(t *testing.T) {
		groupID := int64(9)
		repo := &authRepoStub{
			getByKeyForAuth: func(ctx context.Context, key string) (*apikey.APIKey, error) {
				return &apikey.APIKey{
					ID:      1,
					UserID:  2,
					GroupID: &groupID,
					Key:     key,
					Status:  billing.StatusActive,
					User: &identity.User{
						ID:          2,
						Status:      billing.StatusActive,
						Role:        identity.RoleUser,
						Balance:     10,
						Concurrency: 3,
					},
				}, nil
			},
		}
		groupRepo := &authGroupRepoStub{
			groupsByPlatform: map[string][]routing.Group{
				capability.PlatformAnthropic: {{ID: 10, Name: "default", Status: billing.StatusActive, Hydrated: true}},
			},
		}
		ctx := apikey.WithInboundEndpoint(context.Background(), "/v1/messages")
		svc := testkit.NewService(repo, nil, groupRepo, nil, nil, nil, &config.Config{})
		svc.Start()

		apiKey, err := svc.GetByKey(ctx, "k-missing-group")
		require.NoError(t, err)
		require.NotNil(t, apiKey.GroupID)
		require.Equal(t, groupID, *apiKey.GroupID)
		require.Nil(t, apiKey.Group)
	})
}

// TestAPIKeyService_GetByKey_NegativeCache supplies a shared negative entry
// while storage lookup would fail independently. It checks the service returns
// the Key-not-found identity from that accepted lookup result.
func TestAPIKeyService_GetByKey_NegativeCache(t *testing.T) {
	cache := &authCacheStub{}
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return nil, errors.New("unexpected repo call")
		},
	}
	cfg := &config.Config{APIKey: config.APIKeyConfig{AuthCache: config.APIKeyAuthCacheConfig{
		L2TTLSeconds:       60,
		NegativeTTLSeconds: 30,
	}}}
	svc := testkit.NewService(repo, nil, nil, nil, nil, cache, cfg)
	svc.Start()
	cache.getAuthCache = func(ctx context.Context, key string) (*apikey.APIKeyAuthCacheEntry, error) {
		return &apikey.APIKeyAuthCacheEntry{NotFound: true}, nil
	}

	_, err := svc.GetByKey(context.Background(), "missing")
	require.ErrorIs(t, err, apikey.ErrAPIKeyNotFound)
}

// TestAPIKeyService_GetByKey_CacheMissStoresL2 misses shared caching before
// loading a personal credential with a payer. It checks the returned Key
// identity and one captured shared publication attempt.
func TestAPIKeyService_GetByKey_CacheMissStoresL2(t *testing.T) {
	cache := &authCacheStub{}
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return &apikey.APIKey{
				ID:     5,
				UserID: 7,
				Status: billing.StatusActive,
				User: &identity.User{
					ID:          7,
					Status:      billing.StatusActive,
					Role:        identity.RoleUser,
					Balance:     12,
					Concurrency: 2,
				},
			}, nil
		},
	}
	cfg := &config.Config{APIKey: config.APIKeyConfig{AuthCache: config.APIKeyAuthCacheConfig{
		L2TTLSeconds:       60,
		NegativeTTLSeconds: 30,
	}}}
	svc := testkit.NewService(repo, nil, nil, nil, nil, cache, cfg)
	svc.Start()
	cache.getAuthCache = func(ctx context.Context, key string) (*apikey.APIKeyAuthCacheEntry, error) {
		return nil, errAuthCacheMiss
	}

	apiKey, err := svc.GetByKey(context.Background(), "k2")
	require.NoError(t, err)
	require.Equal(t, int64(5), apiKey.ID)
	require.Len(t, cache.setAuthKeys, 1)
}

// TestAPIKeyService_GetByKeyRejectsInvalidLengthBeforeCaches supplies empty or
// oversized credential text with observed cache and repository callbacks. It
// checks Key-not-found is returned before either callback can perform lookup
// work.
func TestAPIKeyService_GetByKeyRejectsInvalidLengthBeforeCaches(t *testing.T) {
	var cacheCalls atomic.Int32
	cache := &authCacheStub{getAuthCache: func(context.Context, string) (*apikey.APIKeyAuthCacheEntry, error) {
		cacheCalls.Add(1)
		return nil, errAuthCacheMiss
	}}
	repo := &authRepoStub{getByKeyForAuth: func(context.Context, string) (*apikey.APIKey, error) {
		t.Fatal("invalid credential reached repository")
		return nil, nil
	}}
	svc := testkit.NewService(repo, nil, nil, nil, nil, cache, &config.Config{APIKey: config.APIKeyConfig{AuthCache: config.APIKeyAuthCacheConfig{L2TTLSeconds: 60}}})
	svc.Start()

	for _, key := range []string{"", strings.Repeat("x", apikey.MaxAPIKeyCredentialBytes+1)} {
		_, err := svc.GetByKey(context.Background(), key)
		require.ErrorIs(t, err, apikey.ErrAPIKeyNotFound)
	}
	require.Zero(t, cacheCalls.Load())
}

// TestAPIKeyService_GetByKeyAllowsMaximumLength supplies credential text
// exactly at the accepted byte bound. It checks the repository receives that
// text once and its missing-Key result remains the returned identity.
func TestAPIKeyService_GetByKeyAllowsMaximumLength(t *testing.T) {
	key := strings.Repeat("x", apikey.MaxAPIKeyCredentialBytes)
	var repoCalls atomic.Int32
	repo := &authRepoStub{getByKeyForAuth: func(_ context.Context, got string) (*apikey.APIKey, error) {
		repoCalls.Add(1)
		require.Equal(t, key, got)
		return nil, apikey.ErrAPIKeyNotFound
	}}
	svc := testkit.NewService(repo, nil, nil, nil, nil, nil, &config.Config{})
	svc.Start()
	_, err := svc.GetByKey(context.Background(), key)
	require.ErrorIs(t, err, apikey.ErrAPIKeyNotFound)
	require.Equal(t, int32(1), repoCalls.Load())
}

// TestAPIKeyService_AuthLookupBulkheadRejectsExcessMisses holds one repository
// miss while a one-slot service receives another credential. It checks
// immediate overload, load metrics and completion of the first miss after its
// barrier is released.
func TestAPIKeyService_AuthLookupBulkheadRejectsExcessMisses(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	repo := &authRepoStub{getByKeyForAuth: func(context.Context, string) (*apikey.APIKey, error) {
		// Signal entry before holding the first load so the main test can observe
		// occupied admission capacity before issuing another credential lookup.
		close(entered)
		<-release
		return nil, apikey.ErrAPIKeyNotFound
	}}
	svc := testkit.NewService(repo, nil, nil, nil, nil, nil, &config.Config{APIKey: config.APIKeyConfig{LookupConcurrency: 1}})
	svc.Start()

	// Run the held lookup separately and return its error through a buffered
	// channel; entry acknowledgement orders the subsequent overload assertion.
	done := make(chan error, 1)
	go func() {
		_, err := svc.GetByKey(context.Background(), "first")
		done <- err
	}()
	<-entered

	_, err := svc.GetByKey(context.Background(), "second")
	require.ErrorIs(t, err, apikey.ErrAPIKeyAuthOverloaded)
	metrics := svc.AuthLookupMetrics()
	require.Equal(t, uint64(2), metrics.Total)
	require.Equal(t, uint64(1), metrics.Rejected)
	require.Equal(t, int64(1), metrics.InFlight)
	require.Equal(t, 1, metrics.Capacity)

	// Release the held repository read after overload metrics are observed
	// so the first caller can return without changing the rejected admission outcome.
	close(release)
	require.ErrorIs(t, <-done, apikey.ErrAPIKeyNotFound)
}

// TestAPIKeyService_GetByKey_SingleflightCollapses starts overlapping reads of
// one credential against a delayed repository callback. It checks all callers
// return successfully and the held overlap produces one captured storage read.
func TestAPIKeyService_GetByKey_SingleflightCollapses(t *testing.T) {
	var calls int32
	cache := &authCacheStub{}
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			atomic.AddInt32(&calls, 1)
			// Delay completion so callers released by the shared start barrier can join
			// the same singleflight lookup before its storage result is available.
			time.Sleep(50 * time.Millisecond)
			return &apikey.APIKey{
				ID:     11,
				UserID: 2,
				Status: billing.StatusActive,
				User: &identity.User{
					ID:          2,
					Status:      billing.StatusActive,
					Role:        identity.RoleUser,
					Balance:     1,
					Concurrency: 1,
				},
			}, nil
		},
	}
	cfg := &config.Config{APIKey: config.APIKeyConfig{AuthCache: config.APIKeyAuthCacheConfig{Singleflight: true}}}
	svc := testkit.NewService(repo, nil, nil, nil, nil, cache, cfg)
	svc.Start()

	start := make(chan struct{})
	wg := sync.WaitGroup{}
	errs := make([]error, 5)
	for i := range 5 {
		wg.Add(1)
		// Launch overlapping reads together before the delayed callback completes;
		// joining callers lets the test inspect one shared result and storage-read count.
		go func(idx int) {
			defer wg.Done()
			<-start
			_, err := svc.GetByKey(context.Background(), "k1")
			errs[idx] = err
		}(i)
	}
	close(start)
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), atomic.LoadInt32(&calls))
}

// TestAPIKeyServiceZeroValueLookup uses an unconfigured zero-valued service to
// read a nonempty credential. It checks the returned Key is nil and the
// missing-Key error retains its lookup context.
func TestAPIKeyServiceZeroValueLookup(t *testing.T) {
	var svc apikey.APIKeyService
	key, err := svc.GetByKey(context.Background(), "sk-zero-value")
	require.Nil(t, key)
	require.ErrorIs(t, err, apikey.ErrAPIKeyNotFound)
	require.Equal(t, "get api key: "+apikey.ErrAPIKeyNotFound.Error(), err.Error())
}

// TestApiKeyService_Delete_OwnerMismatch attempts user deletion of a credential
// owned by another creator. It checks permission rejection before lifecycle
// deletion or creation-counter and authentication cache notifications.
func TestApiKeyService_Delete_OwnerMismatch(t *testing.T) {
	repo := &apiKeyRepoStub{
		apiKey: &apikey.APIKey{ID: 10, UserID: 1, Key: "k"},
	}
	cache := &apiKeyCacheStub{}
	svc := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo, cache: cache})

	err := svc.Delete(context.Background(), 10, 2) // Key 10 belongs to user 1; user 2 attempts deletion.
	require.ErrorIs(t, err, identity.ErrInsufficientPerms)
	require.Empty(t, repo.deletedIDs)   // No deletion attempted
	require.Empty(t, cache.invalidated) // No creation-counter invalidation
	require.Empty(t, cache.deleteAuthKeys)
}

// TestApiKeyService_Delete_NotFound supplies a missing-Key read result before
// user deletion. It checks the error propagates while deletion and both cache
// cleanup capture lists remain empty.
func TestApiKeyService_Delete_NotFound(t *testing.T) {
	repo := &apiKeyRepoStub{getByIDErr: apikey.ErrAPIKeyNotFound}
	cache := &apiKeyCacheStub{}
	svc := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo, cache: cache})

	err := svc.Delete(context.Background(), 99, 1)
	require.ErrorIs(t, err, apikey.ErrAPIKeyNotFound)
	require.Empty(t, repo.deletedIDs)
	require.Empty(t, cache.invalidated)
	require.Empty(t, cache.deleteAuthKeys)
}

// TestAPIKeyService_List_FillsCurrentConcurrency returns a two-Key page with
// optional live counts supplied through the scheduler reader. It checks the
// returned records carry the configured active count and zero observation.
func TestAPIKeyService_List_FillsCurrentConcurrency(t *testing.T) {
	repo := &apiKeyRepoStub{
		allowListByUserID: true,
		listByUserIDKeys: []apikey.APIKey{
			{ID: 10, UserID: 7, Key: "sk-10", Name: "key-10"},
			{ID: 11, UserID: 7, Key: "sk-11", Name: "key-11"},
		},
	}
	concurrency := scheduler.NewConcurrencyService(&stubConcurrencyCacheForTest{
		apiKeyConcurrency: map[int64]int{10: 2, 11: 0},
	})
	svc := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo, concurrencyService: concurrency})

	keys, _, err := svc.List(context.Background(), 7, pagination.PaginationParams{Page: 1, PageSize: 20}, apikey.APIKeyListFilters{})
	require.NoError(t, err)
	require.Len(t, keys, 2)
	require.Equal(t, 2, keys[0].CurrentConcurrency)
	require.Equal(t, 0, keys[1].CurrentConcurrency)
}

// TestAPIKeyService_GetByID_FillsCurrentConcurrency loads one Key beside a
// configured scheduler-backed active count. It checks detail presentation
// receives that live observation for the configured credential.
func TestAPIKeyService_GetByID_FillsCurrentConcurrency(t *testing.T) {
	repo := &apiKeyRepoStub{
		apiKey: &apikey.APIKey{ID: 10, UserID: 7, Key: "sk-10", Name: "key-10"},
	}
	concurrency := scheduler.NewConcurrencyService(&stubConcurrencyCacheForTest{
		apiKeyConcurrency: map[int64]int{10: 4},
	})
	svc := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo, concurrencyService: concurrency})

	key, err := svc.GetByID(context.Background(), 10)
	require.NoError(t, err)
	require.Equal(t, 4, key.CurrentConcurrency)
}

// TestApiKeyService_Delete_DeleteFails fails the lifecycle write for a
// creator-owned credential fixture. It checks the attempted deletion ID is
// captured and cache notifications remain absent after the returned persistence
// failure.
func TestApiKeyService_Delete_DeleteFails(t *testing.T) {
	repo := &apiKeyRepoStub{
		apiKey:    &apikey.APIKey{ID: 42, UserID: 3, Key: "k"},
		deleteErr: errors.New("delete failed"),
	}
	cache := &apiKeyCacheStub{}
	svc := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo, cache: cache})

	err := svc.Delete(context.Background(), 3, 3) // The fixture supplies user 3 as owner.
	require.Error(t, err)
	require.ErrorContains(t, err, "delete api key")
	require.Equal(t, []int64{3}, repo.deletedIDs) // DeleteWithAudit was attempted
	require.Empty(t, cache.invalidated)           // No creation-counter cleanup attempt
	require.Empty(t, cache.deleteAuthKeys)        // No authentication deletion attempt
}

// TestAPIKeyService_Create_EscapesNameBeforePersist creates a permitted custom
// credential with HTML-like display text. It checks the returned name and
// stored creation capture contain the expected escaped representation.
func TestAPIKeyService_Create_EscapesNameBeforePersist(t *testing.T) {
	repo := &apiKeyNameSanitizeRepoStub{}
	svc := newAPIKeyTestService(apiKeyTestDependencies{
		apiKeyRepo: repo,
		groupRepo:  &compositeGroupRepoStub{groups: map[int64]*routing.Group{1: {ID: 1, Status: billing.StatusActive, Hydrated: true}}},
		userRepo:   &userRepoStub{user: &identity.User{ID: 7, Status: billing.StatusActive, Role: identity.RoleUser}},
	})
	customKey := "sk_valid_xss_key_1"

	created, err := svc.Create(context.Background(), 7, apikey.CreateAPIKeyRequest{
		GroupID:   sanitizeFixtureGroupID(),
		Name:      `<img src=x onerror=alert(1)>`,
		CustomKey: &customKey,
	})

	require.NoError(t, err)
	require.Equal(t, "&lt;img src=x onerror=alert(1)&gt;", created.Name)
	require.Len(t, repo.created, 1)
	require.Equal(t, "&lt;img src=x onerror=alert(1)&gt;", repo.created[0].Name)
}

// TestAPIKeyService_Create_DefaultsConfiguredGroupFallbackEnabled creates a
// permitted custom credential without a selection preference or Fast policy. It
// checks saved and returned unavailable-target preference is enabled and the
// Key policy follows the request.
func TestAPIKeyService_Create_DefaultsConfiguredGroupFallbackEnabled(t *testing.T) {
	repo := &apiKeyNameSanitizeRepoStub{}
	svc := newAPIKeyTestService(apiKeyTestDependencies{
		apiKeyRepo: repo,
		groupRepo:  &compositeGroupRepoStub{groups: map[int64]*routing.Group{1: {ID: 1, Status: billing.StatusActive, Hydrated: true}}},
		userRepo:   &userRepoStub{user: &identity.User{ID: 7, Status: billing.StatusActive, Role: identity.RoleUser}},
	})
	customKey := "sk_valid_default_fallback"

	created, err := svc.Create(context.Background(), 7, apikey.CreateAPIKeyRequest{
		GroupID:   sanitizeFixtureGroupID(),
		Name:      "default fallback",
		CustomKey: &customKey,
	})

	require.NoError(t, err)
	require.True(t, created.FallbackWhenGroupUnavailable)
	require.Len(t, repo.created, 1)
	require.True(t, repo.created[0].FallbackWhenGroupUnavailable)
	require.Equal(t, apikey.APIKeyFastModePolicyFollowRequest, created.FastModePolicy)
}

// TestAPIKeyService_CreateRejectsInvalidFastModePolicy submits unsupported Key
// acceleration text with no repository dependencies. It checks the policy error
// is returned before any unconfigured storage path can run.
func TestAPIKeyService_CreateRejectsInvalidFastModePolicy(t *testing.T) {
	svc := newAPIKeyTestService(apiKeyTestDependencies{})

	_, err := svc.Create(context.Background(), 7, apikey.CreateAPIKeyRequest{GroupID: sanitizeFixtureGroupID(), FastModePolicy: "invalid"})
	require.ErrorIs(t, err, apikey.ErrInvalidAPIKeyFastModePolicy)
}

// TestAPIKeyService_Create_AllowsDisablingGroupFallback creates a permitted Key
// with an explicitly false unavailable-target preference. It checks the
// returned view and successful storage capture retain that disabled selection
// choice.
func TestAPIKeyService_Create_AllowsDisablingGroupFallback(t *testing.T) {
	repo := &apiKeyNameSanitizeRepoStub{}
	svc := newAPIKeyTestService(apiKeyTestDependencies{
		apiKeyRepo: repo,
		groupRepo:  &compositeGroupRepoStub{groups: map[int64]*routing.Group{1: {ID: 1, Status: billing.StatusActive, Hydrated: true}}},
		userRepo:   &userRepoStub{user: &identity.User{ID: 7, Status: billing.StatusActive, Role: identity.RoleUser}},
	})
	customKey := "sk_valid_disabled_fallback"
	fallback := false

	created, err := svc.Create(context.Background(), 7, apikey.CreateAPIKeyRequest{
		GroupID:                      sanitizeFixtureGroupID(),
		Name:                         "disabled fallback",
		CustomKey:                    &customKey,
		FallbackWhenGroupUnavailable: &fallback,
	})

	require.NoError(t, err)
	require.False(t, created.FallbackWhenGroupUnavailable)
	require.Len(t, repo.created, 1)
	require.False(t, repo.created[0].FallbackWhenGroupUnavailable)
}

// TestAPIKeyService_Update_EscapesNameBeforePersist renames an owned credential
// using script-like display text. It checks the returned and captured name use
// the expected escaped representation in the submitted configuration record.
func TestAPIKeyService_Update_EscapesNameBeforePersist(t *testing.T) {
	repo := &apiKeyNameSanitizeRepoStub{
		apiKey: &apikey.APIKey{ID: 11, UserID: 7, Key: "sk_existing_key_01", Name: "old", Status: billing.StatusActive},
	}
	svc := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo})
	name := `<script>alert("x")</script>`

	updated, err := svc.Update(context.Background(), 11, 7, apikey.UpdateAPIKeyRequest{Name: &name})

	require.NoError(t, err)
	require.Equal(t, "&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;", updated.Name)
	require.Len(t, repo.updated, 1)
	require.Equal(t, "&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;", repo.updated[0].Name)
}

// TestAPIKeyService_UpdatePersistsFastModePolicy changes an owned credential
// from request-following acceleration to forced off. It checks both the
// returned view and successful write capture carry that selected policy.
func TestAPIKeyService_UpdatePersistsFastModePolicy(t *testing.T) {
	repo := &apiKeyNameSanitizeRepoStub{
		apiKey: &apikey.APIKey{ID: 11, UserID: 7, Key: "sk_existing_key_02", Name: "old", Status: billing.StatusActive, FastModePolicy: apikey.APIKeyFastModePolicyFollowRequest},
	}
	svc := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo})
	policy := apikey.APIKeyFastModePolicyForceOff

	updated, err := svc.Update(context.Background(), 11, 7, apikey.UpdateAPIKeyRequest{FastModePolicy: &policy})
	require.NoError(t, err)
	require.Equal(t, apikey.APIKeyFastModePolicyForceOff, updated.FastModePolicy)
	require.Equal(t, apikey.APIKeyFastModePolicyForceOff, repo.updated[0].FastModePolicy)
}

// TestAPIKeyService_UpdatePreservesOmittedIPRestrictions updates an owned
// credential while omitting both IP pattern fields. It checks the returned Key
// retains its saved allowed and denied pattern lists.
func TestAPIKeyService_UpdatePreservesOmittedIPRestrictions(t *testing.T) {
	repo := &apiKeyNameSanitizeRepoStub{
		apiKey: &apikey.APIKey{
			ID:          11,
			UserID:      7,
			Key:         "sk_existing_ip_key_01",
			Status:      billing.StatusActive,
			IPWhitelist: []string{"192.0.2.10"},
			IPBlacklist: []string{"198.51.100.0/24"},
		},
	}
	svc := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo})

	updated, err := svc.Update(context.Background(), 11, 7, apikey.UpdateAPIKeyRequest{})

	require.NoError(t, err)
	require.Equal(t, []string{"192.0.2.10"}, updated.IPWhitelist)
	require.Equal(t, []string{"198.51.100.0/24"}, updated.IPBlacklist)
}

// TestAPIKeyService_UpdateClearsExplicitEmptyIPRestriction supplies an empty
// allowed-pattern replacement while omitting saved denial input. It checks the
// allowlist clears and the existing denylist remains in the returned view.
func TestAPIKeyService_UpdateClearsExplicitEmptyIPRestriction(t *testing.T) {
	repo := &apiKeyNameSanitizeRepoStub{
		apiKey: &apikey.APIKey{
			ID:          11,
			UserID:      7,
			Key:         "sk_existing_ip_key_02",
			Status:      billing.StatusActive,
			IPWhitelist: []string{"192.0.2.10"},
			IPBlacklist: []string{"198.51.100.0/24"},
		},
	}
	svc := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo})
	emptyWhitelist := []string{}

	updated, err := svc.Update(context.Background(), 11, 7, apikey.UpdateAPIKeyRequest{IPWhitelist: &emptyWhitelist})

	require.NoError(t, err)
	require.Empty(t, updated.IPWhitelist)
	require.Equal(t, []string{"198.51.100.0/24"}, updated.IPBlacklist)
}

// TestAPIKeyService_UpdateRejectsInvalidIPRestriction supplies a malformed
// denied-address replacement for an owned credential. It checks the IP-pattern
// error is returned before the fixture captures a configuration write.
func TestAPIKeyService_UpdateRejectsInvalidIPRestriction(t *testing.T) {
	repo := &apiKeyNameSanitizeRepoStub{
		apiKey: &apikey.APIKey{ID: 11, UserID: 7, Key: "sk_existing_ip_key_03", Status: billing.StatusActive},
	}
	svc := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo})
	invalidBlacklist := []string{"not-an-ip"}

	_, err := svc.Update(context.Background(), 11, 7, apikey.UpdateAPIKeyRequest{IPBlacklist: &invalidBlacklist})

	require.ErrorIs(t, err, apikey.ErrInvalidIPPattern)
	require.Empty(t, repo.updated)
}

// TestAPIKeyService_UpdateQuotaUsed_UsesAtomicStatePath provides the optional
// atomic quota state capability reporting exhaustion. It checks one capability
// call, no follow-up Key read and notification of the returned stored
// credential.
func TestAPIKeyService_UpdateQuotaUsed_UsesAtomicStatePath(t *testing.T) {
	repo := &quotaStateRepoStub{
		state: &apikey.APIKeyQuotaUsageState{
			QuotaUsed: 12,
			Quota:     10,
			Key:       "sk-test-quota",
			Status:    apikey.StatusAPIKeyQuotaExhausted,
		},
	}
	cache := &quotaStateCacheStub{}
	svc := newAPIKeyTestService(apiKeyTestDependencies{
		apiKeyRepo: repo,
		cache:      cache,
	})

	err := svc.UpdateQuotaUsed(context.Background(), 101, 2)
	require.NoError(t, err)
	require.Equal(t, 1, repo.stateCalls)
	require.Equal(t, 0, repo.getByIDCalls, "fast path should not re-read API key by id")
	require.Equal(t, []string{svc.KeyAuthCacheKey("sk-test-quota")}, cache.deleteAuthKeys)
}

// TestAPIKeyService_Update_ReactivatesQuotaExhaustedWhenQuotaUnlimited sets a
// quota-exhausted owned credential's allowance to zero. It checks active status
// and unbounded quota appear in the returned view and captured configuration
// write.
func TestAPIKeyService_Update_ReactivatesQuotaExhaustedWhenQuotaUnlimited(t *testing.T) {
	repo := &apiKeyRepoStub{
		apiKey: &apikey.APIKey{
			ID:        10,
			UserID:    7,
			Key:       "sk-test-unlimited",
			Status:    apikey.StatusAPIKeyQuotaExhausted,
			Quota:     10,
			QuotaUsed: 12,
		},
	}
	svc := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo})
	quota := 0.0

	updated, err := svc.Update(context.Background(), 10, 7, apikey.UpdateAPIKeyRequest{Quota: &quota})

	require.NoError(t, err)
	require.Equal(t, billing.StatusActive, updated.Status)
	require.Equal(t, 0.0, updated.Quota)
	require.Len(t, repo.updatedKeys, 1)
	require.Equal(t, billing.StatusActive, repo.updatedKeys[0].Status)
	require.Equal(t, 0.0, repo.updatedKeys[0].Quota)
}

// TestAPIKeyUpdate_OnlyDeclaresRequestedColumns varies isolated configuration
// replacements including zero request allowances. It checks each service update
// supplies only the expected persistence mask so unselected billing counters
// remain outside the write.
func TestAPIKeyUpdate_OnlyDeclaresRequestedColumns(t *testing.T) {
	name := "renamed"
	zero := 0
	quota := 500.0
	rateLimit := 42.0
	whitelist := []string{"10.0.0.1"}
	fastModePolicy := apikey.APIKeyFastModePolicyForceOff
	fallbackToDefaultGroup := false
	modelMapping := map[string]string{"review": "gpt-5.6-luna"}

	tests := []struct {
		name string                     // configuration mask scenario label
		req  apikey.UpdateAPIKeyRequest // submitted configuration replacement inputs
		want apikey.APIKeyUpdateFields  // expected selected persistence columns
	}{
		{
			name: "clear concurrency limit",
			req:  apikey.UpdateAPIKeyRequest{ConcurrencyLimit: &zero},
			want: apikey.APIKeyUpdateFields{ConcurrencyLimit: true},
		},
		{
			name: "clear rpm limit",
			req:  apikey.UpdateAPIKeyRequest{RPMLimit: &zero},
			want: apikey.APIKeyUpdateFields{RPMLimit: true},
		},
		{
			name: "model mapping only",
			req:  apikey.UpdateAPIKeyRequest{ModelMapping: &modelMapping},
			want: apikey.APIKeyUpdateFields{ModelMapping: true},
		},
		{
			name: "name only",
			req:  apikey.UpdateAPIKeyRequest{Name: &name},
			want: apikey.APIKeyUpdateFields{Name: true},
		},
		{
			name: "quota only",
			req:  apikey.UpdateAPIKeyRequest{Quota: &quota},
			want: apikey.APIKeyUpdateFields{Quota: true},
		},
		{
			name: "rate limit threshold only",
			req:  apikey.UpdateAPIKeyRequest{RateLimit5h: &rateLimit},
			want: apikey.APIKeyUpdateFields{RateLimits: true},
		},
		{
			name: "ip whitelist only",
			req:  apikey.UpdateAPIKeyRequest{IPWhitelist: &whitelist},
			want: apikey.APIKeyUpdateFields{IPRules: true},
		},
		{
			name: "fork fast mode policy only",
			req:  apikey.UpdateAPIKeyRequest{FastModePolicy: &fastModePolicy},
			want: apikey.APIKeyUpdateFields{FastModePolicy: true},
		},
		{
			name: "fork group fallback policy only",
			req:  apikey.UpdateAPIKeyRequest{FallbackWhenGroupUnavailable: &fallbackToDefaultGroup},
			want: apikey.APIKeyUpdateFields{FallbackWhenGroupUnavailable: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo := newUpdateFieldsAPIKeyService(&apikey.APIKey{
				ID:        1,
				UserID:    7,
				Key:       "sk-test",
				Name:      "before",
				Status:    billing.StatusActive,
				Quota:     100,
				QuotaUsed: 30,
				Usage5h:   12,
			})

			_, err := svc.Update(context.Background(), 1, 7, tt.req)
			require.NoError(t, err)
			require.Equal(t, []apikey.APIKeyUpdateFields{tt.want}, repo.updateFields)
		})
	}
}

// TestAPIKeyUpdateClearsModelMappingWithEmptyObject supplies an empty
// model-rule object for a credential with an alias. It checks the returned map
// clears and the persistence mask selects the model mapping column.
func TestAPIKeyUpdateClearsModelMappingWithEmptyObject(t *testing.T) {
	emptyMapping := map[string]string{}
	svc, repo := newUpdateFieldsAPIKeyService(&apikey.APIKey{
		ID: 1, UserID: 7, Key: "sk-test", Status: billing.StatusActive,
		ModelMapping: map[string]string{"review": "gpt-5.6-luna"},
	})

	updated, err := svc.Update(context.Background(), 1, 7, apikey.UpdateAPIKeyRequest{ModelMapping: &emptyMapping})
	require.NoError(t, err)
	require.Empty(t, updated.ModelMapping)
	require.Equal(t, []apikey.APIKeyUpdateFields{{ModelMapping: true}}, repo.updateFields)
}

// TestAPIKeyUpdate_DeclaresUsageColumnsOnExplicitReset requests both total and
// spending-window usage resets on an owned record. It checks the mask selects
// the two corresponding usage groups rather than unrelated configuration
// columns.
func TestAPIKeyUpdate_DeclaresUsageColumnsOnExplicitReset(t *testing.T) {
	reset := true
	svc, repo := newUpdateFieldsAPIKeyService(&apikey.APIKey{
		ID: 1, UserID: 7, Key: "sk-test", Status: billing.StatusActive, Quota: 100, QuotaUsed: 30, Usage5h: 12,
	})

	_, err := svc.Update(context.Background(), 1, 7, apikey.UpdateAPIKeyRequest{
		ResetQuota:          &reset,
		ResetRateLimitUsage: &reset,
	})
	require.NoError(t, err)
	require.Equal(t, []apikey.APIKeyUpdateFields{{QuotaUsed: true, RateLimitUsage: true}}, repo.updateFields)
}

// TestAPIKeyUpdate_DeclaresStatusWhenReactivated increases a quota-exhausted
// credential's allowance above its saved usage. It checks persistence selects
// the quota and status columns together for that allowance replacement.
func TestAPIKeyUpdate_DeclaresStatusWhenReactivated(t *testing.T) {
	quota := 500.0
	svc, repo := newUpdateFieldsAPIKeyService(&apikey.APIKey{
		ID: 1, UserID: 7, Key: "sk-test", Status: apikey.StatusAPIKeyQuotaExhausted, Quota: 100, QuotaUsed: 100,
	})

	_, err := svc.Update(context.Background(), 1, 7, apikey.UpdateAPIKeyRequest{Quota: &quota})
	require.NoError(t, err)
	require.Equal(t, []apikey.APIKeyUpdateFields{{Quota: true, Status: true}}, repo.updateFields)
}

// TestUpdateQuotaUsed_ExhaustedMarkOnlyDeclaresStatus increments quota through
// the repository path lacking atomic state return until the saved allowance is
// exhausted. It checks the follow-up configuration mask selects status alone
// while leaving quota and other usage columns unselected.
func TestUpdateQuotaUsed_ExhaustedMarkOnlyDeclaresStatus(t *testing.T) {
	repo := &updateFieldsAPIKeyRepoStub{key: &apikey.APIKey{
		ID: 1, UserID: 7, Key: "sk-test", Status: billing.StatusActive, Quota: 10, QuotaUsed: 10,
	}}
	svc := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo})

	require.NoError(t, svc.UpdateQuotaUsed(context.Background(), 1, 5))
	require.Equal(t, []apikey.APIKeyUpdateFields{{Status: true}}, repo.updateFields)
}

// TestValidateAPIKeyLimit supplies zero, finite bounds, negative and nonfinite
// monetary values to the shared validator. It checks accepted inputs and the
// application error identity for every rejected allowance.
func TestValidateAPIKeyLimit(t *testing.T) {
	t.Parallel()

	// Use the nearest representable value below the exclusive decimal bound to
	// exercise its final valid numeric neighbor.
	for _, value := range []float64{0, 1, math.Nextafter(apiKeyLimitUpperBound, 0)} {
		require.NoError(t, apikey.ValidateAPIKeyLimit("quota", value))
	}

	for _, value := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1), apiKeyLimitUpperBound, 1e100} {
		err := apikey.ValidateAPIKeyLimit("rate_limit_7d", value)
		require.ErrorIs(t, err, apikey.ErrAPIKeyLimitInvalid)
		require.Equal(t, "API_KEY_LIMIT_INVALID", apperror.Reason(err))
	}
}

// TestValidateCreateAPIKeyRequestNumericLimits combines allowed creation
// defaults and positive validity with invalid amounts or expiry periods. It
// checks the creation validator accepts current bounds and rejects each
// independently invalid field.
func TestValidateCreateAPIKeyRequestNumericLimits(t *testing.T) {
	t.Parallel()

	positiveExpiry := 1
	// The predecessor of the exclusive bound tests that creation accepts the
	// closest representable allowance still inside its stored interval.
	require.NoError(t, apikey.KeyValidateCreateAPIKeyRequest(apikey.CreateAPIKeyRequest{
		Quota:         math.Nextafter(apiKeyLimitUpperBound, 0),
		RateLimit5h:   10,
		RateLimit1d:   20,
		RateLimit7d:   30,
		ExpiresInDays: &positiveExpiry,
	}))
	require.NoError(t, apikey.KeyValidateCreateAPIKeyRequest(apikey.CreateAPIKeyRequest{}))

	zeroExpiry, negativeExpiry := 0, -1
	invalid := []apikey.CreateAPIKeyRequest{
		{Quota: -1},
		{Quota: math.NaN()},
		{Quota: apiKeyLimitUpperBound},
		{RateLimit5h: math.Inf(1)},
		{RateLimit1d: -1},
		{RateLimit7d: 1e100},
		{ExpiresInDays: &zeroExpiry},
		{ExpiresInDays: &negativeExpiry},
	}
	for _, request := range invalid {
		require.Error(t, apikey.KeyValidateCreateAPIKeyRequest(request))
	}
}

// TestValidateUpdateAPIKeyRequestNumericLimits supplies omitted or zero
// monetary replacements alongside invalid numeric update values. It checks
// accepted replacement input and the shared allowance error identity on
// rejection.
func TestValidateUpdateAPIKeyRequestNumericLimits(t *testing.T) {
	t.Parallel()

	zero := 0.0
	// The predecessor float exercises the exclusive upper limit's accepted side.
	largeValid := math.Nextafter(apiKeyLimitUpperBound, 0)
	require.NoError(t, apikey.KeyValidateUpdateAPIKeyRequest(apikey.UpdateAPIKeyRequest{
		Quota:       &zero,
		RateLimit7d: &largeValid,
		RateLimit1d: nil,
		RateLimit5h: nil,
	}))

	negative, nan, inf, tooLarge := -1.0, math.NaN(), math.Inf(1), float64(apiKeyLimitUpperBound)
	invalid := []apikey.UpdateAPIKeyRequest{
		{Quota: &negative},
		{RateLimit5h: &nan},
		{RateLimit1d: &inf},
		{RateLimit7d: &tooLarge},
	}
	for _, request := range invalid {
		require.ErrorIs(t, apikey.KeyValidateUpdateAPIKeyRequest(request), apikey.ErrAPIKeyLimitInvalid)
	}
}

// TestAPIKeyServiceRejectsInvalidLimitsBeforeRepositoryAccess submits invalid
// create and update allowances with absent fixture repositories. It checks
// numeric validation returns the expected error before an unconfigured storage
// lookup can execute.
func TestAPIKeyServiceRejectsInvalidLimitsBeforeRepositoryAccess(t *testing.T) {
	t.Parallel()

	service := newAPIKeyTestService(apiKeyTestDependencies{})
	// Nil repository dependencies expose accidental lookup before validation.
	// Supply invalid amounts so both public write methods must return their numeric error first.
	var requestContext context.Context
	_, createErr := service.Create(requestContext, 1, apikey.CreateAPIKeyRequest{GroupID: sanitizeFixtureGroupID(), Quota: -1})
	require.ErrorIs(t, createErr, apikey.ErrAPIKeyLimitInvalid)

	invalid := math.Inf(1)
	_, updateErr := service.Update(requestContext, 1, 1, apikey.UpdateAPIKeyRequest{RateLimit5h: &invalid})
	require.ErrorIs(t, updateErr, apikey.ErrAPIKeyLimitInvalid)
}

// TestValidateTeamKeyLifecycle mutates one active team fixture through disabled
// support, absent membership, rejoin and inactive identity cases. It checks
// each ordered lifecycle rejection retains the corresponding team or user
// failure identity.
func TestValidateTeamKeyLifecycle(t *testing.T) {
	tests := []struct {
		name   string               // team lifecycle scenario label
		mutate func(*apikey.APIKey) // optional lifecycle rejection mutation
		cfg    *config.Config       // configured team feature support
		want   error                // expected lifecycle error identity
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
			err := newAPIKeyTestService(apiKeyTestDependencies{cfg: test.cfg}).ValidateTeamKeyLifecycle(key)
			if test.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, test.want)
		})
	}
}

// TestAPIKeyCreateRequiresExplicitPositiveGroup submits ordinary creation with
// absent, zero or negative group pointers. It checks the service returns
// GROUP_REQUIRED before any unconfigured repository can be queried.
func TestAPIKeyCreateRequiresExplicitPositiveGroup(t *testing.T) {
	zero, negative := int64(0), int64(-1)
	for _, groupID := range []*int64{nil, &zero, &negative} {
		service := newAPIKeyTestService(apiKeyTestDependencies{})
		_, err := service.Create(context.Background(), 7, apikey.CreateAPIKeyRequest{Name: "missing group", GroupID: groupID})
		require.Error(t, err)
		require.Contains(t, err.Error(), "GROUP_REQUIRED")
	}
}

// TestAPIKeyLoadingPreservesUnboundLegacyKey loads a saved personal credential
// that has no group binding. It checks the original secret and absent group
// fields remain available after loading rather than selecting an unrelated
// group.
func TestAPIKeyLoadingPreservesUnboundLegacyKey(t *testing.T) {
	repo := &authRepoStub{getByKeyForAuth: func(_ context.Context, key string) (*apikey.APIKey, error) {
		return &apikey.APIKey{ID: 19, UserID: 7, Key: key, Status: billing.StatusActive, User: &identity.User{ID: 7, Status: billing.StatusActive, Role: identity.RoleUser}}, nil
	}}
	service := newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo})
	loaded, err := service.GetByKey(context.Background(), "unchanged-legacy-key")
	require.NoError(t, err)
	require.Equal(t, "unchanged-legacy-key", loaded.Key)
	require.Nil(t, loaded.GroupID)
	require.Nil(t, loaded.Group)
}

// GetByID returns a shallow configured subscription copy for id or
// subscription-not-found when absent. Plan and timestamp references remain
// shared; the fixture ignores context and supplies no independent effectiveness
// filtering.
func (s *billingModeSubscriptionRepoStub) GetByID(_ context.Context, id int64) (*billing.UserSubscription, error) {
	subscription := s.subscriptions[id]
	if subscription == nil {
		return nil, billing.ErrSubscriptionNotFound
	}
	copySubscription := *subscription
	return &copySubscription, nil
}

// ListActiveByUserID copies configured subscription values whose UserID matches
// the supplied payer. Despite its name, the fixture does not filter current
// status or expiry; service option listing validates effectiveness afterward.
func (s *billingModeSubscriptionRepoStub) ListActiveByUserID(_ context.Context, userID int64) ([]billing.UserSubscription, error) {
	result := make([]billing.UserSubscription, 0)
	for _, subscription := range s.subscriptions {
		if subscription.UserID == userID {
			result = append(result, *subscription)
		}
	}
	return result, nil
}

// GetByID records the requested payer identifier before returning a shallow
// configured user copy. Missing map entries report user-not-found; nested
// identity data remains shared for the supplied scenario.
func (s *billingModeUserRepoStub) GetByID(_ context.Context, id int64) (*identity.User, error) {
	s.requested = append(s.requested, id)
	user := s.users[id]
	if user == nil {
		return nil, identity.ErrUserNotFound
	}
	copyUser := *user
	return &copyUser, nil
}

// activeBillingModeSubscription allocates an active subscription starting one
// hour before now and expiring one hour afterward. Its plan receives copied
// group IDs; callers choose matching record and payer values, then can adjust
// eligibility on their returned fixture.
func activeBillingModeSubscription(id, userID int64, groupIDs ...int64) *billing.UserSubscription {
	// Start before now and expire afterward so ordinary funding validation sees an
	// effective subscription during the surrounding scenario.
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

// Create panics when the service repository fixture receives an unsupported
// Create call.
func (s *authGroupRepoStub) Create(ctx context.Context, group *routing.Group) error {
	panic("unexpected Create call")
}

// GetByID panics when the service repository fixture receives an unsupported
// GetByID call.
func (s *authGroupRepoStub) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	panic("unexpected GetByID call")
}

// GetByIDLite returns a shallow configured target group value for id. An absent
// target map panics, while a missing entry reports group-not-found; nested
// collections and pointers retain fixture storage.
func (s *authGroupRepoStub) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	if s.groupsByID == nil {
		panic("unexpected GetByIDLite call")
	}
	group, ok := s.groupsByID[id]
	if !ok {
		return nil, routing.ErrGroupNotFound
	}
	return &group, nil
}

// Update panics when the service repository fixture receives an unsupported
// Update call.
func (s *authGroupRepoStub) Update(ctx context.Context, group *routing.Group) error {
	panic("unexpected Update call")
}

// Delete panics when the service repository fixture receives an unsupported
// Delete call.
func (s *authGroupRepoStub) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

// DeleteCascade panics when the service repository fixture receives an
// unsupported DeleteCascade call.
func (s *authGroupRepoStub) DeleteCascade(ctx context.Context, id int64) ([]int64, error) {
	panic("unexpected DeleteCascade call")
}

// List panics when the service repository fixture receives an unsupported List
// call.
func (s *authGroupRepoStub) List(ctx context.Context, params pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

// ListWithFilters panics when the service repository fixture receives an
// unsupported ListWithFilters call.
func (s *authGroupRepoStub) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]routing.Group, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

// ListActive panics when the service repository fixture receives an unsupported
// ListActive call.
func (s *authGroupRepoStub) ListActive(ctx context.Context) ([]routing.Group, error) {
	panic("unexpected ListActive call")
}

// ListActiveByPlatform delegates platform reads to the fixture's lite copier
// and returns its group values. Missing platform data becomes an empty slice,
// and nested references remain shared with configured entries.
func (s *authGroupRepoStub) ListActiveByPlatform(ctx context.Context, platform string) ([]routing.Group, error) {
	return s.ListActiveByPlatformLite(ctx, platform)
}

// ListActiveByPlatformLite allocates a slice of configured platform group
// values. Missing platform entries return an empty allocated slice, and nested
// group references remain shared; stored status is supplied as configured.
func (s *authGroupRepoStub) ListActiveByPlatformLite(ctx context.Context, platform string) ([]routing.Group, error) {
	groups := s.groupsByPlatform[platform]
	out := make([]routing.Group, len(groups))
	copy(out, groups)
	return out, nil
}

// ExistsByName panics when the service repository fixture receives an
// unsupported ExistsByName call.
func (s *authGroupRepoStub) ExistsByName(ctx context.Context, name string) (bool, error) {
	panic("unexpected ExistsByName call")
}

// GetProviderCount panics when the service repository fixture receives an
// unsupported GetProviderCount call.
func (s *authGroupRepoStub) GetProviderCount(ctx context.Context, groupID int64) (int64, int64, error) {
	panic("unexpected GetProviderCount call")
}

// DeleteProviderGroupsByGroupID panics when the service repository fixture
// receives an unsupported DeleteProviderGroupsByGroupID call.
func (s *authGroupRepoStub) DeleteProviderGroupsByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected DeleteProviderGroupsByGroupID call")
}

// GetProviderIDsByGroupIDs panics when the service repository fixture receives
// an unsupported GetProviderIDsByGroupIDs call.
func (s *authGroupRepoStub) GetProviderIDsByGroupIDs(ctx context.Context, groupIDs []int64) ([]int64, error) {
	panic("unexpected GetProviderIDsByGroupIDs call")
}

// BindProvidersToGroup panics when the service repository fixture receives an
// unsupported BindProvidersToGroup call.
func (s *authGroupRepoStub) BindProvidersToGroup(ctx context.Context, groupID int64, providerIDs []int64) error {
	panic("unexpected BindProvidersToGroup call")
}

// UpdateSortOrders panics when the service repository fixture receives an
// unsupported UpdateSortOrders call.
func (s *authGroupRepoStub) UpdateSortOrders(ctx context.Context, updates []routing.GroupSortOrderUpdate) error {
	panic("unexpected UpdateSortOrders call")
}

// GetByUserID panics when the service repository fixture receives an
// unsupported GetByUserID call.
func (s *authUserGroupRateRepoStub) GetByUserID(ctx context.Context, userID int64) (map[int64]float64, error) {
	panic("unexpected GetByUserID call")
}

// GetByUserAndGroup panics when the service repository fixture receives an
// unsupported GetByUserAndGroup call.
func (s *authUserGroupRateRepoStub) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error) {
	panic("unexpected GetByUserAndGroup call")
}

// GetRPMOverrideByUserAndGroup records the requested group and returns its
// configured override pointer. It ignores payer and context; missing entries
// return nil, allowing service scenarios to observe the later default path.
func (s *authUserGroupRateRepoStub) GetRPMOverrideByUserAndGroup(ctx context.Context, userID, groupID int64) (*int, error) {
	s.calls = append(s.calls, groupID)
	return s.overrides[groupID], nil
}

// GetByGroupID panics when the service repository fixture receives an
// unsupported GetByGroupID call.
func (s *authUserGroupRateRepoStub) GetByGroupID(ctx context.Context, groupID int64) ([]billing.UserGroupRateEntry, error) {
	panic("unexpected GetByGroupID call")
}

// SyncUserGroupRates panics when the service repository fixture receives an
// unsupported SyncUserGroupRates call.
func (s *authUserGroupRateRepoStub) SyncUserGroupRates(ctx context.Context, userID int64, rates map[int64]*float64) error {
	panic("unexpected SyncUserGroupRates call")
}

// SyncGroupRateMultipliers panics when the service repository fixture receives
// an unsupported SyncGroupRateMultipliers call.
func (s *authUserGroupRateRepoStub) SyncGroupRateMultipliers(ctx context.Context, groupID int64, entries []billing.GroupRateMultiplierInput) error {
	panic("unexpected SyncGroupRateMultipliers call")
}

// SyncGroupRPMOverrides panics when the service repository fixture receives an
// unsupported SyncGroupRPMOverrides call.
func (s *authUserGroupRateRepoStub) SyncGroupRPMOverrides(ctx context.Context, groupID int64, entries []billing.GroupRPMOverrideInput) error {
	panic("unexpected SyncGroupRPMOverrides call")
}

// ClearGroupRPMOverrides panics when the service repository fixture receives an
// unsupported ClearGroupRPMOverrides call.
func (s *authUserGroupRateRepoStub) ClearGroupRPMOverrides(ctx context.Context, groupID int64) error {
	panic("unexpected ClearGroupRPMOverrides call")
}

// DeleteByGroupID panics when the service repository fixture receives an
// unsupported DeleteByGroupID call.
func (s *authUserGroupRateRepoStub) DeleteByGroupID(ctx context.Context, groupID int64) error {
	panic("unexpected DeleteByGroupID call")
}

// DeleteByUserID panics when the service repository fixture receives an
// unsupported DeleteByUserID call.
func (s *authUserGroupRateRepoStub) DeleteByUserID(ctx context.Context, userID int64) error {
	panic("unexpected DeleteByUserID call")
}

// Create panics when the service repository fixture receives an unsupported
// Create call.
func (s *apiKeyRepoStub) Create(ctx context.Context, key *apikey.APIKey) error {
	panic("unexpected Create call")
}

// GetByID returns a configured error or shallow credential copy, ignoring the
// requested ID. Missing configured success data panics; nested fields remain
// shared with the retained record for these service scenarios.
func (s *apiKeyRepoStub) GetByID(ctx context.Context, id int64) (*apikey.APIKey, error) {
	if s.getByIDErr != nil {
		return nil, s.getByIDErr
	}
	if s.apiKey != nil {
		clone := *s.apiKey
		return &clone, nil
	}
	panic("unexpected GetByID call")
}

// GetKeyAndOwnerID returns the configured secret and creator, or the selected
// lookup error. An absent record reports Key-not-found; this fixture ignores
// the requested ID and uses its retained record for every lookup.
func (s *apiKeyRepoStub) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	if s.getByIDErr != nil {
		return "", 0, s.getByIDErr
	}
	if s.apiKey != nil {
		return s.apiKey.Key, s.apiKey.UserID, nil
	}
	return "", 0, apikey.ErrAPIKeyNotFound
}

// GetByKey panics when the service repository fixture receives an unsupported
// GetByKey call.
func (s *apiKeyRepoStub) GetByKey(ctx context.Context, key string) (*apikey.APIKey, error) {
	panic("unexpected GetByKey call")
}

// GetByKeyForAuth panics when the service repository fixture receives an
// unsupported GetByKeyForAuth call.
func (s *apiKeyRepoStub) GetByKeyForAuth(ctx context.Context, key string) (*apikey.APIKey, error) {
	panic("unexpected GetByKeyForAuth call")
}

// RotateCredential panics when the service repository fixture receives an
// unsupported RotateCredential call.
func (s *apiKeyRepoStub) RotateCredential(context.Context, *apikey.APIKey, string) error {
	panic("unexpected RotateCredential call")
}

// Update records a shallow nonnil Key value before returning its configured
// write error. The mask is ignored, so updatedKeys counts attempts including
// failures; input collections and pointers remain shared with captured records.
func (s *apiKeyRepoStub) Update(ctx context.Context, key *apikey.APIKey, _ apikey.APIKeyUpdateFields) error {
	if key != nil {
		s.updatedKeys = append(s.updatedKeys, *key)
	}
	return s.updateErr
}

// Delete records the requested identifier before returning the selected fixture
// deletion error. A failed attempt remains captured; scenarios sequence calls
// before inspecting this unsynchronized observation slice.
func (s *apiKeyRepoStub) Delete(ctx context.Context, id int64) error {
	s.deletedIDs = append(s.deletedIDs, id)
	return s.deleteErr
}

// DeleteWithAudit records lifecycle deletion intent before returning the
// configured persistence result. Capture includes failed attempts, and the
// fixture imposes no transaction or tombstone behavior on its observation list.
func (s *apiKeyRepoStub) DeleteWithAudit(ctx context.Context, id int64) error {
	s.deletedIDs = append(s.deletedIDs, id)
	return s.deleteErr
}

// ListByUserID records creator, page and filter input when explicitly enabled.
// Disabled use panics; configured failure returns after capture. Successful
// reads copy the record slice shallowly and report its length with one fixture
// page, ignoring actual pagination or filtering.
func (s *apiKeyRepoStub) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters apikey.APIKeyListFilters) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	if !s.allowListByUserID {
		panic("unexpected ListByUserID call")
	}
	s.listByUserIDCalls = append(s.listByUserIDCalls, userID)
	s.listByUserIDParams = append(s.listByUserIDParams, params)
	s.listByUserIDFilters = append(s.listByUserIDFilters, filters)
	if s.listByUserIDErr != nil {
		return nil, nil, s.listByUserIDErr
	}
	keys := append([]apikey.APIKey(nil), s.listByUserIDKeys...)
	return keys, &pagination.PaginationResult{
		Total:    int64(len(keys)),
		Page:     params.Page,
		PageSize: params.PageSize,
		Pages:    1,
	}, nil
}

// VerifyOwnership panics when the service repository fixture receives an
// unsupported VerifyOwnership call.
func (s *apiKeyRepoStub) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	panic("unexpected VerifyOwnership call")
}

// CountByUserID panics when the service repository fixture receives an
// unsupported CountByUserID call.
func (s *apiKeyRepoStub) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	panic("unexpected CountByUserID call")
}

// ExistsByKey panics when the service repository fixture receives an
// unsupported ExistsByKey call.
func (s *apiKeyRepoStub) ExistsByKey(ctx context.Context, key string) (bool, error) {
	panic("unexpected ExistsByKey call")
}

// ListByGroupID panics when the service repository fixture receives an
// unsupported ListByGroupID call.
func (s *apiKeyRepoStub) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	panic("unexpected ListByGroupID call")
}

// SearchAPIKeys panics when the service repository fixture receives an
// unsupported SearchAPIKeys call.
func (s *apiKeyRepoStub) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]apikey.APIKey, error) {
	panic("unexpected SearchAPIKeys call")
}

// ClearGroupIDByGroupID panics when the service repository fixture receives an
// unsupported ClearGroupIDByGroupID call.
func (s *apiKeyRepoStub) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected ClearGroupIDByGroupID call")
}

// UpdateGroupIDByUserAndGroup panics when the service repository fixture
// receives an unsupported UpdateGroupIDByUserAndGroup call.
func (s *apiKeyRepoStub) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	panic("unexpected UpdateGroupIDByUserAndGroup call")
}

// CountByGroupID panics when the service repository fixture receives an
// unsupported CountByGroupID call.
func (s *apiKeyRepoStub) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected CountByGroupID call")
}

// ListKeysByUserID panics when the service repository fixture receives an
// unsupported ListKeysByUserID call.
func (s *apiKeyRepoStub) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	panic("unexpected ListKeysByUserID call")
}

// ListKeysByGroupID panics when the service repository fixture receives an
// unsupported ListKeysByGroupID call.
func (s *apiKeyRepoStub) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	panic("unexpected ListKeysByGroupID call")
}

// IncrementQuotaUsed panics when the service repository fixture receives an
// unsupported IncrementQuotaUsed call.
func (s *apiKeyRepoStub) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error) {
	panic("unexpected IncrementQuotaUsed call")
}

// UpdateLastUsed records the supplied Key ID and timestamp before invoking its
// optional result callback. Missing callback succeeds; captures are
// unsynchronized, and concurrent touch scenarios use their separately locked
// fixture instead.
func (s *apiKeyRepoStub) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	s.touchedIDs = append(s.touchedIDs, id)
	s.touchedUsedAts = append(s.touchedUsedAts, usedAt)
	if s.updateLastUsed != nil {
		return s.updateLastUsed(ctx, id, usedAt)
	}
	return nil
}

// IncrementRateLimitUsage panics when the service repository fixture receives
// an unsupported IncrementRateLimitUsage call.
func (s *apiKeyRepoStub) IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error {
	panic("unexpected IncrementRateLimitUsage call")
}

// ResetRateLimitWindows panics when the service repository fixture receives an
// unsupported ResetRateLimitWindows call.
func (s *apiKeyRepoStub) ResetRateLimitWindows(ctx context.Context, id int64) error {
	panic("unexpected ResetRateLimitWindows call")
}

// GetRateLimitData panics when the service repository fixture receives an
// unsupported GetRateLimitData call.
func (s *apiKeyRepoStub) GetRateLimitData(ctx context.Context, id int64) (*apikey.APIKeyRateLimitData, error) {
	panic("unexpected GetRateLimitData call")
}

// GetCreateAttemptCount returns zero for creator-counter reads in this service
// fixture.
func (s *apiKeyCacheStub) GetCreateAttemptCount(ctx context.Context, userID int64) (int, error) {
	return 0, nil
}

// IncrementCreateAttemptCount acknowledges fixture failure-count requests
// without retaining a counter value.
func (s *apiKeyCacheStub) IncrementCreateAttemptCount(ctx context.Context, userID int64) error {
	return nil
}

// DeleteCreateAttemptCount records the creator identifier requested for counter
// cleanup.
func (s *apiKeyCacheStub) DeleteCreateAttemptCount(ctx context.Context, userID int64) error {
	s.invalidated = append(s.invalidated, userID)
	return nil
}

// IncrementDailyUsage acknowledges daily-counter requests while retaining no
// usage observation.
func (s *apiKeyCacheStub) IncrementDailyUsage(ctx context.Context, apiKey string) error {
	return nil
}

// SetDailyUsageExpiry accepts lifetime requests while retaining no
// daily-counter expiration.
func (s *apiKeyCacheStub) SetDailyUsageExpiry(ctx context.Context, apiKey string, ttl time.Duration) error {
	return nil
}

// GetAuthCache returns nil data with no error for every cache identity. The
// service must still treat the unusable entry as requiring load; this fixture
// stores no positive or negative state.
func (s *apiKeyCacheStub) GetAuthCache(ctx context.Context, key string) (*apikey.APIKeyAuthCacheEntry, error) {
	return nil, nil
}

// SetAuthCache acknowledges shared publication intent while retaining no entry
// or lifetime.
func (s *apiKeyCacheStub) SetAuthCache(ctx context.Context, key string, entry *apikey.APIKeyAuthCacheEntry, ttl time.Duration) error {
	return nil
}

// DeleteAuthCache records the requested hashed identity for service
// invalidation assertions.
func (s *apiKeyCacheStub) DeleteAuthCache(ctx context.Context, key string) error {
	s.deleteAuthKeys = append(s.deleteAuthKeys, key)
	return nil
}

// PublishAuthCacheInvalidation acknowledges service notification intent while
// sending no real message.
func (s *apiKeyCacheStub) PublishAuthCacheInvalidation(ctx context.Context, cacheKey string) error {
	return nil
}

// SubscribeAuthCacheInvalidation returns immediately with nil and invokes no
// invalidation callback. The service wrapper can interpret it as a closed
// subscription; this fixture supplies no held subscriber lifetime.
func (s *apiKeyCacheStub) SubscribeAuthCacheInvalidation(ctx context.Context, handler func(cacheKey string)) error {
	return nil
}

// Create captures a shallow record, assigns a fixture ID when absent and
// updates the caller with that captured value. Its creation list retains
// pointers and nested shared data; this fixture imposes no real persistence
// limit.
func (s *apiKeyNameSanitizeRepoStub) Create(ctx context.Context, key *apikey.APIKey) error {
	clone := *key
	if clone.ID == 0 {
		clone.ID = int64(len(s.created) + 1)
	}
	s.created = append(s.created, &clone)
	*key = clone
	return nil
}

// GetByID returns a shallow copy when the configured record matches id. Missing
// or mismatched records report Key-not-found; pointer and collection fields
// remain shared with the fixture lookup record.
func (s *apiKeyNameSanitizeRepoStub) GetByID(ctx context.Context, id int64) (*apikey.APIKey, error) {
	if s.apiKey == nil || s.apiKey.ID != id {
		return nil, apikey.ErrAPIKeyNotFound
	}
	clone := *s.apiKey
	return &clone, nil
}

// GetKeyAndOwnerID panics when the service repository fixture receives an
// unsupported GetKeyAndOwnerID call.
func (s *apiKeyNameSanitizeRepoStub) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	panic("unexpected GetKeyAndOwnerID call")
}

// GetByKey panics when the service repository fixture receives an unsupported
// GetByKey call.
func (s *apiKeyNameSanitizeRepoStub) GetByKey(ctx context.Context, key string) (*apikey.APIKey, error) {
	panic("unexpected GetByKey call")
}

// GetByKeyForAuth panics when the service repository fixture receives an
// unsupported GetByKeyForAuth call.
func (s *apiKeyNameSanitizeRepoStub) GetByKeyForAuth(ctx context.Context, key string) (*apikey.APIKey, error) {
	panic("unexpected GetByKeyForAuth call")
}

// RotateCredential panics when the service repository fixture receives an
// unsupported RotateCredential call.
func (s *apiKeyNameSanitizeRepoStub) RotateCredential(context.Context, *apikey.APIKey, string) error {
	panic("unexpected RotateCredential call")
}

// Update captures a shallow configuration record and replaces the fixture
// lookup pointer with it. The supplied mask is ignored, and nested data remains
// shared; separate mask fixtures protect column selection assertions.
func (s *apiKeyNameSanitizeRepoStub) Update(ctx context.Context, key *apikey.APIKey, _ apikey.APIKeyUpdateFields) error {
	clone := *key
	s.updated = append(s.updated, &clone)
	s.apiKey = &clone
	return nil
}

// Delete panics when the service repository fixture receives an unsupported
// Delete call.
func (s *apiKeyNameSanitizeRepoStub) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

// DeleteWithAudit panics when the service repository fixture receives an
// unsupported DeleteWithAudit call.
func (s *apiKeyNameSanitizeRepoStub) DeleteWithAudit(ctx context.Context, id int64) error {
	panic("unexpected DeleteWithAudit call")
}

// ListByUserID panics when the service repository fixture receives an
// unsupported ListByUserID call.
func (s *apiKeyNameSanitizeRepoStub) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters apikey.APIKeyListFilters) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	panic("unexpected ListByUserID call")
}

// VerifyOwnership panics when the service repository fixture receives an
// unsupported VerifyOwnership call.
func (s *apiKeyNameSanitizeRepoStub) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	panic("unexpected VerifyOwnership call")
}

// CountByUserID panics when the service repository fixture receives an
// unsupported CountByUserID call.
func (s *apiKeyNameSanitizeRepoStub) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	panic("unexpected CountByUserID call")
}

// ExistsByKey always reports that the supplied secret is unused in this
// creation fixture. It ignores context and text, so duplicate-secret validation
// belongs to a separate repository scenario rather than this name and
// configuration capture.
func (s *apiKeyNameSanitizeRepoStub) ExistsByKey(ctx context.Context, key string) (bool, error) {
	return false, nil
}

// ListByGroupID panics when the service repository fixture receives an
// unsupported ListByGroupID call.
func (s *apiKeyNameSanitizeRepoStub) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	panic("unexpected ListByGroupID call")
}

// SearchAPIKeys panics when the service repository fixture receives an
// unsupported SearchAPIKeys call.
func (s *apiKeyNameSanitizeRepoStub) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]apikey.APIKey, error) {
	panic("unexpected SearchAPIKeys call")
}

// ClearGroupIDByGroupID panics when the service repository fixture receives an
// unsupported ClearGroupIDByGroupID call.
func (s *apiKeyNameSanitizeRepoStub) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected ClearGroupIDByGroupID call")
}

// UpdateGroupIDByUserAndGroup panics when the service repository fixture
// receives an unsupported UpdateGroupIDByUserAndGroup call.
func (s *apiKeyNameSanitizeRepoStub) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	panic("unexpected UpdateGroupIDByUserAndGroup call")
}

// CountByGroupID panics when the service repository fixture receives an
// unsupported CountByGroupID call.
func (s *apiKeyNameSanitizeRepoStub) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected CountByGroupID call")
}

// ListKeysByUserID panics when the service repository fixture receives an
// unsupported ListKeysByUserID call.
func (s *apiKeyNameSanitizeRepoStub) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	panic("unexpected ListKeysByUserID call")
}

// ListKeysByGroupID panics when the service repository fixture receives an
// unsupported ListKeysByGroupID call.
func (s *apiKeyNameSanitizeRepoStub) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	panic("unexpected ListKeysByGroupID call")
}

// IncrementQuotaUsed panics when the service repository fixture receives an
// unsupported IncrementQuotaUsed call.
func (s *apiKeyNameSanitizeRepoStub) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error) {
	panic("unexpected IncrementQuotaUsed call")
}

// UpdateLastUsed panics when the service repository fixture receives an
// unsupported UpdateLastUsed call.
func (s *apiKeyNameSanitizeRepoStub) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	panic("unexpected UpdateLastUsed call")
}

// IncrementRateLimitUsage panics when the service repository fixture receives
// an unsupported IncrementRateLimitUsage call.
func (s *apiKeyNameSanitizeRepoStub) IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error {
	panic("unexpected IncrementRateLimitUsage call")
}

// ResetRateLimitWindows panics when the service repository fixture receives an
// unsupported ResetRateLimitWindows call.
func (s *apiKeyNameSanitizeRepoStub) ResetRateLimitWindows(ctx context.Context, id int64) error {
	panic("unexpected ResetRateLimitWindows call")
}

// GetRateLimitData panics when the service repository fixture receives an
// unsupported GetRateLimitData call.
func (s *apiKeyNameSanitizeRepoStub) GetRateLimitData(ctx context.Context, id int64) (*apikey.APIKeyRateLimitData, error) {
	panic("unexpected GetRateLimitData call")
}

// sanitizeFixtureGroupID allocates the ordinary target identifier used by
// configuration fixture tests.
func sanitizeFixtureGroupID() *int64 { id := int64(1); return &id }

// IncrementQuotaUsedAndGetState counts the capability call and returns selected
// error or copied quota state. Nil state succeeds with nil data; the fixture
// records no actual increment and ignores ID, cost and context.
func (s *quotaStateRepoStub) IncrementQuotaUsedAndGetState(ctx context.Context, id int64, amount float64) (*apikey.APIKeyQuotaUsageState, error) {
	s.stateCalls++
	if s.stateErr != nil {
		return nil, s.stateErr
	}
	if s.state == nil {
		return nil, nil
	}
	out := *s.state
	return &out, nil
}

// GetCreateAttemptCount returns zero so quota-notification scenarios impose no
// creation threshold.
func (s *quotaStateCacheStub) GetCreateAttemptCount(context.Context, int64) (int, error) {
	return 0, nil
}

// IncrementCreateAttemptCount accepts unused quota-fixture counter requests
// without retaining a value.
func (s *quotaStateCacheStub) IncrementCreateAttemptCount(context.Context, int64) error {
	return nil
}

// DeleteCreateAttemptCount acknowledges unused creator-counter cleanup in this
// quota-state fixture.
func (s *quotaStateCacheStub) DeleteCreateAttemptCount(context.Context, int64) error {
	return nil
}

// IncrementDailyUsage accepts unused daily-counter requests in the
// quota-notification fixture.
func (s *quotaStateCacheStub) IncrementDailyUsage(context.Context, string) error {
	return nil
}

// SetDailyUsageExpiry acknowledges unused counter-lifetime requests without
// retaining an expiry.
func (s *quotaStateCacheStub) SetDailyUsageExpiry(context.Context, string, time.Duration) error {
	return nil
}

// GetAuthCache returns nil data with no error for each quota-fixture lookup.
// The service treats this unusable result as a reload condition, and the
// fixture retains no authentication entry.
func (s *quotaStateCacheStub) GetAuthCache(context.Context, string) (*apikey.APIKeyAuthCacheEntry, error) {
	return nil, nil
}

// SetAuthCache acknowledges publication attempts while storing no quota-fixture
// authentication entry.
func (s *quotaStateCacheStub) SetAuthCache(context.Context, string, *apikey.APIKeyAuthCacheEntry, time.Duration) error {
	return nil
}

// DeleteAuthCache captures the supplied hashed identity for quota exhaustion
// notification assertions.
func (s *quotaStateCacheStub) DeleteAuthCache(_ context.Context, key string) error {
	s.deleteAuthKeys = append(s.deleteAuthKeys, key)
	return nil
}

// PublishAuthCacheInvalidation acknowledges quota-fixture notification while
// sending no real cache message.
func (s *quotaStateCacheStub) PublishAuthCacheInvalidation(context.Context, string) error {
	return nil
}

// SubscribeAuthCacheInvalidation returns immediately with nil and invokes no
// handler. The fixture provides no held subscription, so service lifecycle
// scenarios use their dedicated subscriber barrier instead.
func (s *quotaStateCacheStub) SubscribeAuthCacheInvalidation(context.Context, func(string)) error {
	return nil
}

// Create panics when the service repository fixture receives an unsupported
// Create call.
func (s *quotaBaseAPIKeyRepoStub) Create(context.Context, *apikey.APIKey) error {
	panic("unexpected Create call")
}

// GetByID counts a follow-up read and returns nil data with no error.
// Atomic-state scenarios expect this fallback read unused; the fixture supplies
// no missing-Key sentinel on that path.
func (s *quotaBaseAPIKeyRepoStub) GetByID(context.Context, int64) (*apikey.APIKey, error) {
	s.getByIDCalls++
	return nil, nil
}

// GetKeyAndOwnerID panics when the service repository fixture receives an
// unsupported GetKeyAndOwnerID call.
func (s *quotaBaseAPIKeyRepoStub) GetKeyAndOwnerID(context.Context, int64) (string, int64, error) {
	panic("unexpected GetKeyAndOwnerID call")
}

// GetByKey panics when the service repository fixture receives an unsupported
// GetByKey call.
func (s *quotaBaseAPIKeyRepoStub) GetByKey(context.Context, string) (*apikey.APIKey, error) {
	panic("unexpected GetByKey call")
}

// GetByKeyForAuth panics when the service repository fixture receives an
// unsupported GetByKeyForAuth call.
func (s *quotaBaseAPIKeyRepoStub) GetByKeyForAuth(context.Context, string) (*apikey.APIKey, error) {
	panic("unexpected GetByKeyForAuth call")
}

// RotateCredential panics when the service repository fixture receives an
// unsupported RotateCredential call.
func (s *quotaBaseAPIKeyRepoStub) RotateCredential(context.Context, *apikey.APIKey, string) error {
	panic("unexpected RotateCredential call")
}

// Update panics when the service repository fixture receives an unsupported
// Update call.
func (s *quotaBaseAPIKeyRepoStub) Update(context.Context, *apikey.APIKey, apikey.APIKeyUpdateFields) error {
	panic("unexpected Update call")
}

// Delete panics when the service repository fixture receives an unsupported
// Delete call.
func (s *quotaBaseAPIKeyRepoStub) Delete(context.Context, int64) error {
	panic("unexpected Delete call")
}

// DeleteWithAudit panics when the service repository fixture receives an
// unsupported DeleteWithAudit call.
func (s *quotaBaseAPIKeyRepoStub) DeleteWithAudit(context.Context, int64) error {
	panic("unexpected DeleteWithAudit call")
}

// ListByUserID panics when the service repository fixture receives an
// unsupported ListByUserID call.
func (s *quotaBaseAPIKeyRepoStub) ListByUserID(context.Context, int64, pagination.PaginationParams, apikey.APIKeyListFilters) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	panic("unexpected ListByUserID call")
}

// VerifyOwnership panics when the service repository fixture receives an
// unsupported VerifyOwnership call.
func (s *quotaBaseAPIKeyRepoStub) VerifyOwnership(context.Context, int64, []int64) ([]int64, error) {
	panic("unexpected VerifyOwnership call")
}

// CountByUserID panics when the service repository fixture receives an
// unsupported CountByUserID call.
func (s *quotaBaseAPIKeyRepoStub) CountByUserID(context.Context, int64) (int64, error) {
	panic("unexpected CountByUserID call")
}

// ExistsByKey panics when the service repository fixture receives an
// unsupported ExistsByKey call.
func (s *quotaBaseAPIKeyRepoStub) ExistsByKey(context.Context, string) (bool, error) {
	panic("unexpected ExistsByKey call")
}

// ListByGroupID panics when the service repository fixture receives an
// unsupported ListByGroupID call.
func (s *quotaBaseAPIKeyRepoStub) ListByGroupID(context.Context, int64, pagination.PaginationParams) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	panic("unexpected ListByGroupID call")
}

// SearchAPIKeys panics when the service repository fixture receives an
// unsupported SearchAPIKeys call.
func (s *quotaBaseAPIKeyRepoStub) SearchAPIKeys(context.Context, int64, string, int) ([]apikey.APIKey, error) {
	panic("unexpected SearchAPIKeys call")
}

// ClearGroupIDByGroupID panics when the service repository fixture receives an
// unsupported ClearGroupIDByGroupID call.
func (s *quotaBaseAPIKeyRepoStub) ClearGroupIDByGroupID(context.Context, int64) (int64, error) {
	panic("unexpected ClearGroupIDByGroupID call")
}

// UpdateGroupIDByUserAndGroup panics when the service repository fixture
// receives an unsupported UpdateGroupIDByUserAndGroup call.
func (s *quotaBaseAPIKeyRepoStub) UpdateGroupIDByUserAndGroup(context.Context, int64, int64, int64) (int64, error) {
	panic("unexpected UpdateGroupIDByUserAndGroup call")
}

// CountByGroupID panics when the service repository fixture receives an
// unsupported CountByGroupID call.
func (s *quotaBaseAPIKeyRepoStub) CountByGroupID(context.Context, int64) (int64, error) {
	panic("unexpected CountByGroupID call")
}

// ListKeysByUserID panics when the service repository fixture receives an
// unsupported ListKeysByUserID call.
func (s *quotaBaseAPIKeyRepoStub) ListKeysByUserID(context.Context, int64) ([]string, error) {
	panic("unexpected ListKeysByUserID call")
}

// ListKeysByGroupID panics when the service repository fixture receives an
// unsupported ListKeysByGroupID call.
func (s *quotaBaseAPIKeyRepoStub) ListKeysByGroupID(context.Context, int64) ([]string, error) {
	panic("unexpected ListKeysByGroupID call")
}

// IncrementQuotaUsed panics when the service repository fixture receives an
// unsupported IncrementQuotaUsed call.
func (s *quotaBaseAPIKeyRepoStub) IncrementQuotaUsed(context.Context, int64, float64) (float64, error) {
	panic("unexpected IncrementQuotaUsed call")
}

// UpdateLastUsed panics when the service repository fixture receives an
// unsupported UpdateLastUsed call.
func (s *quotaBaseAPIKeyRepoStub) UpdateLastUsed(context.Context, int64, time.Time) error {
	panic("unexpected UpdateLastUsed call")
}

// IncrementRateLimitUsage panics when the service repository fixture receives
// an unsupported IncrementRateLimitUsage call.
func (s *quotaBaseAPIKeyRepoStub) IncrementRateLimitUsage(context.Context, int64, float64) error {
	panic("unexpected IncrementRateLimitUsage call")
}

// ResetRateLimitWindows panics when the service repository fixture receives an
// unsupported ResetRateLimitWindows call.
func (s *quotaBaseAPIKeyRepoStub) ResetRateLimitWindows(context.Context, int64) error {
	panic("unexpected ResetRateLimitWindows call")
}

// GetRateLimitData panics when the service repository fixture receives an
// unsupported GetRateLimitData call.
func (s *quotaBaseAPIKeyRepoStub) GetRateLimitData(context.Context, int64) (*apikey.APIKeyRateLimitData, error) {
	panic("unexpected GetRateLimitData call")
}

// IncrementQuotaUsed adds amount to the retained fixture record and returns its
// updated usage. It ignores context and identifier, allowing mask scenarios to
// inspect the subsequent status write separately.
func (s *updateFieldsAPIKeyRepoStub) IncrementQuotaUsed(_ context.Context, _ int64, amount float64) (float64, error) {
	s.key.QuotaUsed += amount
	return s.key.QuotaUsed, nil
}

// GetByID returns a shallow copy of the supplied mask-test record for every ID.
// Nested data stays shared, while record fields changed by service
// configuration remain outside that retained scalar record.
func (s *updateFieldsAPIKeyRepoStub) GetByID(context.Context, int64) (*apikey.APIKey, error) {
	clone := *s.key
	return &clone, nil
}

// Update records the requested persistence mask without mutating the fixture
// Key.
func (s *updateFieldsAPIKeyRepoStub) Update(_ context.Context, _ *apikey.APIKey, fields apikey.APIKeyUpdateFields) error {
	s.updateFields = append(s.updateFields, fields)
	return nil
}

// newUpdateFieldsAPIKeyService returns an unstarted service and its retained
// mask fixture around key. Callers provide a nonnil record and inspect captured
// masks; the helper performs no real persistence or cache startup.
func newUpdateFieldsAPIKeyService(key *apikey.APIKey) (*apikey.APIKeyService, *updateFieldsAPIKeyRepoStub) {
	repo := &updateFieldsAPIKeyRepoStub{key: key}
	return newAPIKeyTestService(apiKeyTestDependencies{apiKeyRepo: repo}), repo
}

// GetByID returns the retained user pointer, or user-not-found when no user is
// configured. It ignores requested identity and context, so scenarios supply
// the exact permission and account data needed before invoking service logic.
func (s *userRepoStub) GetByID(context.Context, int64) (*identity.User, error) {
	if s.user == nil {
		return nil, identity.ErrUserNotFound
	}
	return s.user, nil
}

// GetContextByUserID returns the retained context pointer for every creator
// query. Nil input state returns nil with no error; tests provide
// production-shaped member and owner records for successful team operations.
func (r *fakeTeamRepository) GetContextByUserID(context.Context, int64) (*team.TeamContext, error) {
	return r.teamContext, nil
}

// GetContextByTeamID returns the same retained context used by creator queries.
// It ignores team ID and context and can return nil without error; scenarios
// configure their intended team identity before service use.
func (r *fakeTeamRepository) GetContextByTeamID(context.Context, int64) (*team.TeamContext, error) {
	return r.teamContext, nil
}

// TrackAPIKeySlot accepts scheduler fixture tracking intent without retaining a
// request member.
func (c *stubConcurrencyCacheForTest) TrackAPIKeySlot(context.Context, int64, string) error {
	return nil
}

// ReleaseAPIKeySlot acknowledges scheduler fixture cleanup without changing
// configured observation counts.
func (c *stubConcurrencyCacheForTest) ReleaseAPIKeySlot(context.Context, int64, string) error {
	return nil
}

// GetAPIKeyConcurrencyBatch allocates observations for each requested ID from
// its configured count map. Missing entries become zero, and the returned map
// can be changed independently of the fixture source.
func (c *stubConcurrencyCacheForTest) GetAPIKeyConcurrencyBatch(_ context.Context, ids []int64) (map[int64]int, error) {
	result := make(map[int64]int, len(ids))
	for _, id := range ids {
		result[id] = c.apiKeyConcurrency[id]
	}
	return result, nil
}
