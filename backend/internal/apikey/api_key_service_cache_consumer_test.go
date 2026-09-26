//go:build unit

package apikey_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyAuthGroupSnapshotPreservesExplicitEmptyClientProtocols(t *testing.T) {
	emptySnapshot := apikey.KeyAuthGroupSnapshotFromGroup(&routing.Group{
		ID:               1,
		Platform:         capability.PlatformOpenAI,
		AllowedProtocols: []protocol.ProtocolID{},
	})
	payload, err := json.Marshal(emptySnapshot)
	require.NoError(t, err)

	var decodedEmpty apikey.APIKeyAuthGroupSnapshot
	require.NoError(t, json.Unmarshal(payload, &decodedEmpty))
	require.NotNil(t, decodedEmpty.AllowedProtocols)
	require.Empty(t, decodedEmpty.AllowedProtocols)
	require.False(t, apikey.KeyGroupFromAuthSnapshot(&decodedEmpty).AllowsClientProtocol(protocol.ProtocolAnthropicMessages))
}

type authRepoStub struct {
	getByKeyForAuth   func(ctx context.Context, key string) (*apikey.APIKey, error)
	listKeysByUserID  func(ctx context.Context, userID int64) ([]string, error)
	listKeysByGroupID func(ctx context.Context, groupID int64) ([]string, error)
}

func (s *authRepoStub) Create(ctx context.Context, key *apikey.APIKey) error {
	panic("unexpected Create call")
}

func (s *authRepoStub) GetByID(ctx context.Context, id int64) (*apikey.APIKey, error) {
	panic("unexpected GetByID call")
}

func (s *authRepoStub) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	panic("unexpected GetKeyAndOwnerID call")
}

func (s *authRepoStub) GetByKey(ctx context.Context, key string) (*apikey.APIKey, error) {
	panic("unexpected GetByKey call")
}

func (s *authRepoStub) GetByKeyForAuth(ctx context.Context, key string) (*apikey.APIKey, error) {
	if s.getByKeyForAuth == nil {
		panic("unexpected GetByKeyForAuth call")
	}
	return s.getByKeyForAuth(ctx, key)
}

func (s *authRepoStub) Update(ctx context.Context, key *apikey.APIKey, _ apikey.APIKeyUpdateFields) error {
	panic("unexpected Update call")
}

func (s *authRepoStub) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

func (s *authRepoStub) DeleteWithAudit(ctx context.Context, id int64) error {
	panic("unexpected DeleteWithAudit call")
}

func (s *authRepoStub) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters apikey.APIKeyListFilters) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	panic("unexpected ListByUserID call")
}

func (s *authRepoStub) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	panic("unexpected VerifyOwnership call")
}

func (s *authRepoStub) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	panic("unexpected CountByUserID call")
}

func (s *authRepoStub) ExistsByKey(ctx context.Context, key string) (bool, error) {
	panic("unexpected ExistsByKey call")
}

func (s *authRepoStub) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	panic("unexpected ListByGroupID call")
}

func (s *authRepoStub) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]apikey.APIKey, error) {
	panic("unexpected SearchAPIKeys call")
}

func (s *authRepoStub) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected ClearGroupIDByGroupID call")
}

func (s *authRepoStub) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	panic("unexpected UpdateGroupIDByUserAndGroup call")
}

func (s *authRepoStub) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected CountByGroupID call")
}

func (s *authRepoStub) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	if s.listKeysByUserID == nil {
		panic("unexpected ListKeysByUserID call")
	}
	return s.listKeysByUserID(ctx, userID)
}

func (s *authRepoStub) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	if s.listKeysByGroupID == nil {
		panic("unexpected ListKeysByGroupID call")
	}
	return s.listKeysByGroupID(ctx, groupID)
}

func (s *authRepoStub) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error) {
	panic("unexpected IncrementQuotaUsed call")
}

func (s *authRepoStub) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	panic("unexpected UpdateLastUsed call")
}

func (s *authRepoStub) IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error {
	panic("unexpected IncrementRateLimitUsage call")
}

func (s *authRepoStub) ResetRateLimitWindows(ctx context.Context, id int64) error {
	panic("unexpected ResetRateLimitWindows call")
}

func (s *authRepoStub) GetRateLimitData(ctx context.Context, id int64) (*apikey.APIKeyRateLimitData, error) {
	panic("unexpected GetRateLimitData call")
}

type authCacheStub struct {
	getAuthCache   func(ctx context.Context, key string) (*apikey.APIKeyAuthCacheEntry, error)
	setAuthKeys    []string
	deleteAuthKeys []string
}

type authGroupRepoStub struct {
	groupsByPlatform map[string][]routing.Group
	groupsByID       map[int64]routing.Group
}

func (s *authGroupRepoStub) Create(ctx context.Context, group *routing.Group) error {
	panic("unexpected Create call")
}

func (s *authGroupRepoStub) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	panic("unexpected GetByID call")
}

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

func (s *authGroupRepoStub) Update(ctx context.Context, group *routing.Group) error {
	panic("unexpected Update call")
}

func (s *authGroupRepoStub) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

func (s *authGroupRepoStub) DeleteCascade(ctx context.Context, id int64) ([]int64, error) {
	panic("unexpected DeleteCascade call")
}

func (s *authGroupRepoStub) List(ctx context.Context, params pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *authGroupRepoStub) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]routing.Group, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *authGroupRepoStub) ListActive(ctx context.Context) ([]routing.Group, error) {
	panic("unexpected ListActive call")
}

func (s *authGroupRepoStub) ListActiveByPlatform(ctx context.Context, platform string) ([]routing.Group, error) {
	return s.ListActiveByPlatformLite(ctx, platform)
}

func (s *authGroupRepoStub) ListActiveByPlatformLite(ctx context.Context, platform string) ([]routing.Group, error) {
	groups := s.groupsByPlatform[platform]
	out := make([]routing.Group, len(groups))
	copy(out, groups)
	return out, nil
}

func (s *authGroupRepoStub) ExistsByName(ctx context.Context, name string) (bool, error) {
	panic("unexpected ExistsByName call")
}

func (s *authGroupRepoStub) GetAccountCount(ctx context.Context, groupID int64) (int64, int64, error) {
	panic("unexpected GetAccountCount call")
}

func (s *authGroupRepoStub) DeleteAccountGroupsByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected DeleteAccountGroupsByGroupID call")
}

func (s *authGroupRepoStub) GetAccountIDsByGroupIDs(ctx context.Context, groupIDs []int64) ([]int64, error) {
	panic("unexpected GetAccountIDsByGroupIDs call")
}

func (s *authGroupRepoStub) BindAccountsToGroup(ctx context.Context, groupID int64, accountIDs []int64) error {
	panic("unexpected BindAccountsToGroup call")
}

func (s *authGroupRepoStub) UpdateSortOrders(ctx context.Context, updates []routing.GroupSortOrderUpdate) error {
	panic("unexpected UpdateSortOrders call")
}

type authUserGroupRateRepoStub struct {
	overrides map[int64]*int
	calls     []int64
}

func (s *authUserGroupRateRepoStub) GetByUserID(ctx context.Context, userID int64) (map[int64]float64, error) {
	panic("unexpected GetByUserID call")
}

func (s *authUserGroupRateRepoStub) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error) {
	panic("unexpected GetByUserAndGroup call")
}

func (s *authUserGroupRateRepoStub) GetRPMOverrideByUserAndGroup(ctx context.Context, userID, groupID int64) (*int, error) {
	s.calls = append(s.calls, groupID)
	return s.overrides[groupID], nil
}

func (s *authUserGroupRateRepoStub) GetByGroupID(ctx context.Context, groupID int64) ([]billing.UserGroupRateEntry, error) {
	panic("unexpected GetByGroupID call")
}

func (s *authUserGroupRateRepoStub) SyncUserGroupRates(ctx context.Context, userID int64, rates map[int64]*float64) error {
	panic("unexpected SyncUserGroupRates call")
}

func (s *authUserGroupRateRepoStub) SyncGroupRateMultipliers(ctx context.Context, groupID int64, entries []billing.GroupRateMultiplierInput) error {
	panic("unexpected SyncGroupRateMultipliers call")
}

func (s *authUserGroupRateRepoStub) SyncGroupRPMOverrides(ctx context.Context, groupID int64, entries []billing.GroupRPMOverrideInput) error {
	panic("unexpected SyncGroupRPMOverrides call")
}

func (s *authUserGroupRateRepoStub) ClearGroupRPMOverrides(ctx context.Context, groupID int64) error {
	panic("unexpected ClearGroupRPMOverrides call")
}

func (s *authUserGroupRateRepoStub) DeleteByGroupID(ctx context.Context, groupID int64) error {
	panic("unexpected DeleteByGroupID call")
}

func (s *authUserGroupRateRepoStub) DeleteByUserID(ctx context.Context, userID int64) error {
	panic("unexpected DeleteByUserID call")
}

func (s *authCacheStub) GetCreateAttemptCount(ctx context.Context, userID int64) (int, error) {
	return 0, nil
}

func (s *authCacheStub) IncrementCreateAttemptCount(ctx context.Context, userID int64) error {
	return nil
}

func (s *authCacheStub) DeleteCreateAttemptCount(ctx context.Context, userID int64) error {
	return nil
}

func (s *authCacheStub) IncrementDailyUsage(ctx context.Context, apiKey string) error {
	return nil
}

func (s *authCacheStub) SetDailyUsageExpiry(ctx context.Context, apiKey string, ttl time.Duration) error {
	return nil
}

// 缓存端口的未命中不依赖 Redis 类型；原生 apikey 测试另保留 redis.Nil 的兼容输入。
var errAuthCacheMiss = errors.New("auth cache miss")

func (s *authCacheStub) GetAuthCache(ctx context.Context, key string) (*apikey.APIKeyAuthCacheEntry, error) {
	if s.getAuthCache == nil {
		return nil, errAuthCacheMiss
	}
	return s.getAuthCache(ctx, key)
}

func (s *authCacheStub) SetAuthCache(ctx context.Context, key string, entry *apikey.APIKeyAuthCacheEntry, ttl time.Duration) error {
	s.setAuthKeys = append(s.setAuthKeys, key)
	return nil
}

func (s *authCacheStub) DeleteAuthCache(ctx context.Context, key string) error {
	s.deleteAuthKeys = append(s.deleteAuthKeys, key)
	return nil
}

func (s *authCacheStub) PublishAuthCacheInvalidation(ctx context.Context, cacheKey string) error {
	return nil
}

func (s *authCacheStub) SubscribeAuthCacheInvalidation(ctx context.Context, handler func(cacheKey string)) error {
	return nil
}

func TestAPIKeyService_GetByKey_UsesL2Cache(t *testing.T) {
	cache := &authCacheStub{}
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return nil, errors.New("unexpected repo call")
		},
	}
	cfg := &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L2TTLSeconds:       60,
			NegativeTTLSeconds: 30,
		},
	}
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
				ID:                  groupID,
				Name:                "g",
				Platform:            capability.PlatformAnthropic,
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

func TestAPIKeyService_GetByKey_FallsBackDisabledBoundGroupToPlatformDefaultFromRepo(t *testing.T) {
	disabledGroupID := int64(9)
	defaultGroupID := int64(10)
	defaultRPM := 77
	oldRPM := 3
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return &apikey.APIKey{
				ID:                                    1,
				UserID:                                2,
				GroupID:                               &disabledGroupID,
				Key:                                   key,
				Status:                                billing.StatusActive,
				FallbackToDefaultGroupWhenUnavailable: true,
				User: &identity.User{
					ID:                   2,
					Status:               billing.StatusActive,
					Role:                 identity.RoleUser,
					Balance:              10,
					Concurrency:          3,
					UserGroupRPMOverride: &oldRPM,
				},
				Group: &routing.Group{
					ID:             disabledGroupID,
					Name:           "openai-disabled",
					Platform:       capability.PlatformOpenAI,
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
					ID:             defaultGroupID,
					Name:           "openai-default",
					Platform:       capability.PlatformOpenAI,
					Status:         billing.StatusActive,
					Hydrated:       true,
					IsDefault:      true,
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
	require.Equal(t, defaultGroupID, *apiKey.GroupID)
	require.NotNil(t, apiKey.Group)
	require.Equal(t, defaultGroupID, apiKey.Group.ID)
	require.Equal(t, capability.PlatformOpenAI, apiKey.Group.Platform)
	require.Equal(t, billing.StatusActive, apiKey.Group.Status)
	require.NotNil(t, apiKey.User.UserGroupRPMOverride)
	require.Equal(t, defaultRPM, *apiKey.User.UserGroupRPMOverride)
	require.Equal(t, []int64{defaultGroupID}, rateRepo.calls)
}

func TestAPIKeyService_GetByKey_FallsBackDisabledBoundGroupToConfiguredGroup(t *testing.T) {
	disabledGroupID := int64(9)
	configuredFallbackID := int64(11)
	defaultGroupID := int64(10)
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return &apikey.APIKey{
				ID:                                    1,
				UserID:                                2,
				GroupID:                               &disabledGroupID,
				Key:                                   key,
				Status:                                billing.StatusActive,
				FallbackToDefaultGroupWhenUnavailable: true,
				User: &identity.User{
					ID:          2,
					Status:      billing.StatusActive,
					Role:        identity.RoleUser,
					Balance:     10,
					Concurrency: 3,
				},
				Group: &routing.Group{
					ID:                         disabledGroupID,
					Name:                       "openai-disabled",
					Platform:                   capability.PlatformOpenAI,
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
				ID:             configuredFallbackID,
				Name:           "openai-configured-fallback",
				Platform:       capability.PlatformOpenAI,
				Status:         billing.StatusActive,
				Hydrated:       true,
				RateMultiplier: 1.2,
			},
		},
		groupsByPlatform: map[string][]routing.Group{
			capability.PlatformOpenAI: {
				{
					ID:             defaultGroupID,
					Name:           "openai-default",
					Platform:       capability.PlatformOpenAI,
					Status:         billing.StatusActive,
					Hydrated:       true,
					IsDefault:      true,
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

func TestAPIKeyService_GetByKey_InvalidConfiguredUnavailableFallbackUsesPlatformDefault(t *testing.T) {
	disabledGroupID := int64(9)
	configuredFallbackID := int64(11)
	defaultGroupID := int64(10)
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return &apikey.APIKey{
				ID:                                    1,
				UserID:                                2,
				GroupID:                               &disabledGroupID,
				Key:                                   key,
				Status:                                billing.StatusActive,
				FallbackToDefaultGroupWhenUnavailable: true,
				User: &identity.User{
					ID:          2,
					Status:      billing.StatusActive,
					Role:        identity.RoleUser,
					Balance:     10,
					Concurrency: 3,
				},
				Group: &routing.Group{
					ID:                         disabledGroupID,
					Name:                       "openai-disabled",
					Platform:                   capability.PlatformOpenAI,
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
				ID:       configuredFallbackID,
				Name:     "gemini-wrong-platform",
				Platform: capability.PlatformGemini,
				Status:   billing.StatusActive,
				Hydrated: true,
			},
		},
		groupsByPlatform: map[string][]routing.Group{
			capability.PlatformOpenAI: {
				{
					ID:        defaultGroupID,
					Name:      "openai-default",
					Platform:  capability.PlatformOpenAI,
					Status:    billing.StatusActive,
					Hydrated:  true,
					IsDefault: true,
				},
			},
		},
	}
	svc := testkit.NewService(repo, nil, groupRepo, nil, nil, nil, &config.Config{})
	svc.Start()

	apiKey, err := svc.GetByKey(context.Background(), "k-disabled")
	require.NoError(t, err)
	require.NotNil(t, apiKey.GroupID)
	require.Equal(t, defaultGroupID, *apiKey.GroupID)
	require.NotNil(t, apiKey.Group)
	require.Equal(t, defaultGroupID, apiKey.Group.ID)
}

func TestAPIKeyService_GetByKey_FallsBackDisabledBoundGroupToPlatformDefaultFromAuthCache(t *testing.T) {
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
					ID:             defaultGroupID,
					Name:           "gemini-default",
					Platform:       capability.PlatformGemini,
					Status:         billing.StatusActive,
					Hydrated:       true,
					IsDefault:      true,
					RateMultiplier: 2,
				},
			},
		},
	}
	oldRPM := 3
	cache.getAuthCache = func(ctx context.Context, key string) (*apikey.APIKeyAuthCacheEntry, error) {
		return &apikey.APIKeyAuthCacheEntry{
			Snapshot: &apikey.APIKeyAuthSnapshot{
				Version:                               apikey.KeyApiKeyAuthSnapshotVersion,
				APIKeyID:                              1,
				UserID:                                2,
				GroupID:                               &disabledGroupID,
				Status:                                billing.StatusActive,
				FallbackToDefaultGroupWhenUnavailable: true,
				User: apikey.APIKeyAuthUserSnapshot{
					ID:                   2,
					Status:               billing.StatusActive,
					Role:                 identity.RoleUser,
					Balance:              10,
					Concurrency:          3,
					UserGroupRPMOverride: &oldRPM,
				},
				Group: &apikey.APIKeyAuthGroupSnapshot{
					ID:             disabledGroupID,
					Name:           "gemini-disabled",
					Platform:       capability.PlatformGemini,
					Status:         billing.StatusDisabled,
					RateMultiplier: 8,
				},
			},
		}, nil
	}
	cfg := &config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{L2TTLSeconds: 60}}
	svc := testkit.NewService(repo, nil, groupRepo, nil, nil, cache, cfg)
	svc.Start()

	apiKey, err := svc.GetByKey(context.Background(), "k-cached-disabled")
	require.NoError(t, err)
	require.NotNil(t, apiKey.GroupID)
	require.Equal(t, defaultGroupID, *apiKey.GroupID)
	require.NotNil(t, apiKey.Group)
	require.Equal(t, defaultGroupID, apiKey.Group.ID)
	require.Nil(t, apiKey.User.UserGroupRPMOverride)
}

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
						ID:       groupID,
						Name:     "deleted",
						Platform: capability.PlatformOpenAI,
						Status:   "deleted",
						Hydrated: true,
					},
				}, nil
			},
		}
		groupRepo := &authGroupRepoStub{
			groupsByPlatform: map[string][]routing.Group{
				capability.PlatformOpenAI: {{ID: 10, Name: "openai-default", Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Hydrated: true, IsDefault: true}},
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
				capability.PlatformAnthropic: {{ID: 10, Name: "default", Platform: capability.PlatformAnthropic, Status: billing.StatusActive, Hydrated: true, IsDefault: true}},
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

func TestAPIKeyService_SnapshotRoundTrip_PreservesMessagesDispatchModelConfig(t *testing.T) {
	svc := testkit.NewService(nil, nil, nil, nil, nil, nil, &config.Config{})
	svc.Start()
	groupID := int64(9)
	apiKey := &apikey.APIKey{
		ID:             1,
		UserID:         2,
		GroupID:        &groupID,
		Key:            "k-roundtrip",
		Name:           "Audit Key",
		Status:         billing.StatusActive,
		FastModePolicy: apikey.APIKeyFastModePolicyForceOn,
		User: &identity.User{
			ID:          2,
			Status:      billing.StatusActive,
			Role:        identity.RoleUser,
			Balance:     10,
			Concurrency: 3,
		},
		Group: &routing.Group{
			ID:                 groupID,
			Name:               "openai",
			Platform:           capability.PlatformOpenAI,
			Status:             billing.StatusActive,
			RateMultiplier:     1,
			DefaultMappedModel: "gpt-5.4",
			MessagesDispatchModelConfig: routing.OpenAIMessagesDispatchModelConfig{
				OpusMappedModel:   "gpt-5.4-nano",
				SonnetMappedModel: "gpt-5.3-codex",
				HaikuMappedModel:  "gpt-5.4-mini",
				ExactModelMappings: map[string]string{
					"claude-sonnet-4.5": "gpt-5.4-nano",
				},
			},
		},
	}

	snapshot := svc.KeySnapshotFromAPIKey(context.Background(), apiKey)
	roundTrip := svc.KeySnapshotToAPIKey(apiKey.Key, snapshot)

	require.NotNil(t, roundTrip)
	require.Equal(t, apiKey.Name, roundTrip.Name)
	require.Equal(t, apikey.APIKeyFastModePolicyForceOn, roundTrip.FastModePolicy)
	require.NotNil(t, roundTrip.Group)
	require.Equal(t, apiKey.Group.MessagesDispatchModelConfig, roundTrip.Group.MessagesDispatchModelConfig)
}

func TestAPIKeyServiceSnapshotRoundTripPreservesIndependentModelMapping(t *testing.T) {
	svc := testkit.NewService(nil, nil, nil, nil, nil, nil, &config.Config{})
	svc.Start()
	apiKey := &apikey.APIKey{
		ID:           1,
		UserID:       2,
		Key:          "k-model-mapping",
		Status:       billing.StatusActive,
		ModelMapping: map[string]string{"review": "gpt-5.6-luna"},
		User:         &identity.User{ID: 2, Status: billing.StatusActive},
	}

	snapshot := svc.KeySnapshotFromAPIKey(context.Background(), apiKey)
	require.Equal(t, apikey.KeyApiKeyAuthSnapshotVersion, snapshot.Version)
	roundTrip := svc.KeySnapshotToAPIKey(apiKey.Key, snapshot)
	require.Equal(t, apiKey.ModelMapping, roundTrip.ModelMapping)

	roundTrip.ModelMapping["review"] = "changed"
	require.Equal(t, "gpt-5.6-luna", snapshot.ModelMapping["review"])
}

func TestAPIKeyServiceSnapshotRoundTripPreservesGroupModelPricing(t *testing.T) {
	svc := testkit.NewService(nil, nil, nil, nil, nil, nil, &config.Config{})
	svc.Start()
	groupID := int64(9)
	inputPrice := 1e-6
	apiKey := &apikey.APIKey{
		ID: 1, UserID: 2, GroupID: &groupID, Key: "k-group-pricing", Status: billing.StatusActive,
		User: &identity.User{ID: 2, Status: billing.StatusActive},
		Group: &routing.Group{
			ID: groupID, Name: "openai", Platform: capability.PlatformOpenAI, Status: billing.StatusActive,
			LongContextPricingEnabled: true,
			ModelPricing: []routing.ModelPricingEntry{{
				Models: []string{"gpt-5.4"}, BillingMode: routing.BillingModeToken, InputPrice: &inputPrice,
			}},
		},
	}

	snapshot := svc.KeySnapshotFromAPIKey(context.Background(), apiKey)
	roundTrip := svc.KeySnapshotToAPIKey(apiKey.Key, snapshot)

	require.True(t, roundTrip.Group.LongContextPricingEnabled)
	require.Equal(t, apiKey.Group.ModelPricing, roundTrip.Group.ModelPricing)
	roundTrip.Group.ModelPricing[0].Models[0] = "changed"
	require.Equal(t, "gpt-5.4", snapshot.Group.ModelPricing[0].Models[0])
}

func TestAPIKeyService_SnapshotRoundTrip_PreservesReasoningEffortPolicy(t *testing.T) {
	svc := testkit.NewService(nil, nil, nil, nil, nil, nil, &config.Config{})
	svc.Start()
	groupID := int64(9)
	apiKey := &apikey.APIKey{
		ID:      1,
		UserID:  2,
		GroupID: &groupID,
		Key:     "k-reasoning-policy",
		Status:  billing.StatusActive,
		User: &identity.User{
			ID:          2,
			Status:      billing.StatusActive,
			Role:        identity.RoleUser,
			Balance:     10,
			Concurrency: 3,
		},
		Group: &routing.Group{
			ID:                          groupID,
			Name:                        "openai",
			Platform:                    capability.PlatformOpenAI,
			Status:                      billing.StatusActive,
			RateMultiplier:              1,
			MaxReasoningEffort:          "medium",
			MaxReasoningEffortOverLimit: routing.ReasoningEffortOverLimitDeny,
			ReasoningEffortMappings: []routing.ReasoningEffortMapping{
				{From: "max", To: "xhigh"},
			},
		},
	}

	snapshot := svc.KeySnapshotFromAPIKey(context.Background(), apiKey)
	roundTrip := svc.KeySnapshotToAPIKey(apiKey.Key, snapshot)

	require.NotNil(t, roundTrip)
	require.NotNil(t, roundTrip.Group)
	require.Equal(t, "medium", roundTrip.Group.MaxReasoningEffort)
	require.Equal(t, routing.ReasoningEffortOverLimitDeny, roundTrip.Group.MaxReasoningEffortOverLimit)
	require.Equal(t, apiKey.Group.ReasoningEffortMappings, roundTrip.Group.ReasoningEffortMappings)
}

func TestAPIKeyService_GetByKey_IgnoresLegacyAuthCacheSnapshotWithoutMessagesDispatchConfig(t *testing.T) {
	cache := &authCacheStub{}
	var repoCalls int32
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			atomic.AddInt32(&repoCalls, 1)
			groupID := int64(9)
			return &apikey.APIKey{
				ID:      1,
				UserID:  2,
				GroupID: &groupID,
				Status:  billing.StatusActive,
				User: &identity.User{
					ID:          2,
					Status:      billing.StatusActive,
					Role:        identity.RoleUser,
					Balance:     10,
					Concurrency: 3,
				},
				Group: &routing.Group{
					ID:                    groupID,
					Name:                  "openai",
					Platform:              capability.PlatformOpenAI,
					Status:                billing.StatusActive,
					Hydrated:              true,
					RateMultiplier:        1,
					AllowMessagesDispatch: true,
					DefaultMappedModel:    "gpt-5.4",
					MessagesDispatchModelConfig: routing.OpenAIMessagesDispatchModelConfig{
						OpusMappedModel: "gpt-5.4-nano",
					},
				},
			}, nil
		},
	}
	cfg := &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L2TTLSeconds: 60,
		},
	}
	svc := testkit.NewService(repo, nil, nil, nil, nil, cache, cfg)
	svc.Start()

	groupID := int64(9)
	cache.getAuthCache = func(ctx context.Context, key string) (*apikey.APIKeyAuthCacheEntry, error) {
		return &apikey.APIKeyAuthCacheEntry{
			Snapshot: &apikey.APIKeyAuthSnapshot{
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
					ID:                 groupID,
					Name:               "openai",
					Platform:           capability.PlatformOpenAI,
					Status:             billing.StatusActive,
					RateMultiplier:     1,
					DefaultMappedModel: "gpt-5.4",
				},
			},
		}, nil
	}

	apiKey, err := svc.GetByKey(context.Background(), "k-legacy")
	require.NoError(t, err)
	require.Equal(t, int32(1), atomic.LoadInt32(&repoCalls))
	require.NotNil(t, apiKey.Group)
	require.Equal(t, "gpt-5.4-nano", apiKey.Group.MessagesDispatchModelConfig.OpusMappedModel)
}

func TestAPIKeyService_GetByKey_NegativeCache(t *testing.T) {
	cache := &authCacheStub{}
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return nil, errors.New("unexpected repo call")
		},
	}
	cfg := &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L2TTLSeconds:       60,
			NegativeTTLSeconds: 30,
		},
	}
	svc := testkit.NewService(repo, nil, nil, nil, nil, cache, cfg)
	svc.Start()
	cache.getAuthCache = func(ctx context.Context, key string) (*apikey.APIKeyAuthCacheEntry, error) {
		return &apikey.APIKeyAuthCacheEntry{NotFound: true}, nil
	}

	_, err := svc.GetByKey(context.Background(), "missing")
	require.ErrorIs(t, err, apikey.ErrAPIKeyNotFound)
}

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
	cfg := &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L2TTLSeconds:       60,
			NegativeTTLSeconds: 30,
		},
	}
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

func TestAPIKeyService_InvalidateAuthCacheByUserID(t *testing.T) {
	cache := &authCacheStub{}
	repo := &authRepoStub{
		listKeysByUserID: func(ctx context.Context, userID int64) ([]string, error) {
			return []string{"k1", "k2"}, nil
		},
	}
	cfg := &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L2TTLSeconds:       60,
			NegativeTTLSeconds: 30,
		},
	}
	svc := testkit.NewService(repo, nil, nil, nil, nil, cache, cfg)
	svc.Start()

	svc.InvalidateAuthCacheByUserID(context.Background(), 7)
	require.Len(t, cache.deleteAuthKeys, 2)
}

func TestAPIKeyService_InvalidateAuthCacheByGroupID(t *testing.T) {
	cache := &authCacheStub{}
	repo := &authRepoStub{
		listKeysByGroupID: func(ctx context.Context, groupID int64) ([]string, error) {
			return []string{"k1", "k2"}, nil
		},
	}
	cfg := &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L2TTLSeconds: 60,
		},
	}
	svc := testkit.NewService(repo, nil, nil, nil, nil, cache, cfg)
	svc.Start()

	svc.InvalidateAuthCacheByGroupID(context.Background(), 9)
	require.Len(t, cache.deleteAuthKeys, 2)
}

func TestAPIKeyService_InvalidateAuthCacheByKey(t *testing.T) {
	cache := &authCacheStub{}
	repo := &authRepoStub{
		listKeysByUserID: func(ctx context.Context, userID int64) ([]string, error) {
			return nil, nil
		},
	}
	cfg := &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L2TTLSeconds: 60,
		},
	}
	svc := testkit.NewService(repo, nil, nil, nil, nil, cache, cfg)
	svc.Start()

	svc.InvalidateAuthCacheByKey(context.Background(), "k1")
	require.Len(t, cache.deleteAuthKeys, 1)
}

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
	svc := testkit.NewService(repo, nil, nil, nil, nil, cache, &config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{L2TTLSeconds: 60}})
	svc.Start()

	for _, key := range []string{"", strings.Repeat("x", apikey.MaxAPIKeyCredentialBytes+1)} {
		_, err := svc.GetByKey(context.Background(), key)
		require.ErrorIs(t, err, apikey.ErrAPIKeyNotFound)
	}
	require.Zero(t, cacheCalls.Load())
}

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

func TestAPIKeyService_AuthLookupBulkheadRejectsExcessMisses(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	repo := &authRepoStub{getByKeyForAuth: func(context.Context, string) (*apikey.APIKey, error) {
		close(entered)
		<-release
		return nil, apikey.ErrAPIKeyNotFound
	}}
	svc := testkit.NewService(repo, nil, nil, nil, nil, nil, &config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{LookupConcurrency: 1}})
	svc.Start()

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

	close(release)
	require.ErrorIs(t, <-done, apikey.ErrAPIKeyNotFound)
}

func TestAPIKeyService_GetByKey_SingleflightCollapses(t *testing.T) {
	var calls int32
	cache := &authCacheStub{}
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			atomic.AddInt32(&calls, 1)
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
	cfg := &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			Singleflight: true,
		},
	}
	svc := testkit.NewService(repo, nil, nil, nil, nil, cache, cfg)
	svc.Start()

	start := make(chan struct{})
	wg := sync.WaitGroup{}
	errs := make([]error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
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

// TestAPIKeyServiceZeroValueLookup 保持旧零值入口在 WS 快照复查时返回未找到。
func TestAPIKeyServiceZeroValueLookup(t *testing.T) {
	var svc apikey.APIKeyService
	key, err := svc.GetByKey(context.Background(), "sk-zero-value")
	require.Nil(t, key)
	require.ErrorIs(t, err, apikey.ErrAPIKeyNotFound)
	require.Equal(t, "get api key: "+apikey.ErrAPIKeyNotFound.Error(), err.Error())
}

func TestAPIKeySnapshotPreservesIndependentRoutingPolicy(t *testing.T) {
	svc := testkit.NewService(nil, nil, nil, nil, nil, nil, &config.Config{})
	groupID := int64(9)
	key := &apikey.APIKey{
		ID: 1, UserID: 2, GroupID: &groupID, Key: "policy-snapshot", Status: billing.StatusActive,
		User: &identity.User{ID: 2, Status: billing.StatusActive}, Group: &routing.Group{
			ID: groupID, Platform: capability.PlatformOpenAI,
			RoutingPolicy: routing.GroupRoutingPolicy{
				Enabled: true, RestrictModels: true, RestrictionModelSource: routing.BillingModelSourceUpstream,
				ModelMapping: map[string]map[string]string{"openai": {"alias": "real"}}, AllowedModels: map[string][]string{"openai": {"real"}},
				FeaturesConfig: map[string]any{"codex_image_generation_bridge": map[string]any{"openai": false}},
			},
		},
	}
	snapshot := svc.KeySnapshotFromAPIKey(context.Background(), key)
	require.Equal(t, 41, snapshot.Version)
	restored := svc.KeySnapshotToAPIKey(key.Key, snapshot)
	require.Equal(t, key.Group.RoutingPolicy, restored.Group.RoutingPolicy)
	restored.Group.RoutingPolicy.ModelMapping["openai"]["alias"] = "changed"
	restored.Group.RoutingPolicy.AllowedModels["openai"][0] = "changed"
	require.Equal(t, "real", snapshot.Group.RoutingPolicy.ModelMapping["openai"]["alias"])
	require.Equal(t, "real", snapshot.Group.RoutingPolicy.AllowedModels["openai"][0])
	snapshot.Version = 40
	cached, ok, err := svc.KeyApplyAuthCacheEntry(key.Key, &apikey.APIKeyAuthCacheEntry{Snapshot: snapshot})
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, cached)
}
