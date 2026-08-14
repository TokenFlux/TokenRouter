package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/dgraph-io/ristretto"
)

const apiKeyAuthSnapshotVersion = 33 // v33：认证快照包含分组逐模型定价和长上下文开关

type apiKeyAuthCacheConfig struct {
	l1Size        int
	l1TTL         time.Duration
	l2TTL         time.Duration
	negativeTTL   time.Duration
	jitterPercent int
	singleflight  bool
}

func newAPIKeyAuthCacheConfig(cfg *config.Config) apiKeyAuthCacheConfig {
	if cfg == nil {
		return apiKeyAuthCacheConfig{}
	}
	auth := cfg.APIKeyAuth
	return apiKeyAuthCacheConfig{
		l1Size:        auth.L1Size,
		l1TTL:         time.Duration(auth.L1TTLSeconds) * time.Second,
		l2TTL:         time.Duration(auth.L2TTLSeconds) * time.Second,
		negativeTTL:   time.Duration(auth.NegativeTTLSeconds) * time.Second,
		jitterPercent: auth.JitterPercent,
		singleflight:  auth.Singleflight,
	}
}

func (c apiKeyAuthCacheConfig) l1Enabled() bool {
	return c.l1Size > 0 && c.l1TTL > 0
}

func (c apiKeyAuthCacheConfig) l2Enabled() bool {
	return c.l2TTL > 0
}

func (c apiKeyAuthCacheConfig) negativeEnabled() bool {
	return c.negativeTTL > 0
}

// jitterTTL 为缓存 TTL 添加抖动，避免多个请求在同一时刻同时过期触发集中回源。
// 这里直接使用 rand/v2 的顶层函数：并发安全，无需全局互斥锁。
func (c apiKeyAuthCacheConfig) jitterTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return ttl
	}
	if c.jitterPercent <= 0 {
		return ttl
	}
	percent := c.jitterPercent
	if percent > 100 {
		percent = 100
	}
	delta := float64(percent) / 100
	randVal := rand.Float64()
	factor := 1 - delta + randVal*(2*delta)
	if factor <= 0 {
		return ttl
	}
	return time.Duration(float64(ttl) * factor)
}

func (s *APIKeyService) initAuthCache(cfg *config.Config) {
	s.authCfg = newAPIKeyAuthCacheConfig(cfg)
	if s.authCfg.negativeEnabled() {
		negativeSize := defaultNegativeAuthCacheSize
		if s.authCfg.l1Size > 0 && s.authCfg.l1Size < negativeSize {
			negativeSize = s.authCfg.l1Size
		}
		cache, err := ristretto.NewCache(&ristretto.Config{
			NumCounters: int64(negativeSize) * 10,
			MaxCost:     int64(negativeSize),
			BufferItems: 64,
		})
		if err == nil {
			s.authNegativeCacheL1 = cache
		}
	}
	if s.authCfg.l1Enabled() {
		cache, err := ristretto.NewCache(&ristretto.Config{
			NumCounters: int64(s.authCfg.l1Size) * 10,
			MaxCost:     int64(s.authCfg.l1Size),
			BufferItems: 64,
		})
		if err == nil {
			s.authCacheL1 = cache
		}
	}
}

// StartAuthCacheInvalidationSubscriber starts the Pub/Sub subscriber for L1 cache invalidation.
// This should be called after the service is fully initialized.
func (s *APIKeyService) StartAuthCacheInvalidationSubscriber(ctx context.Context) {
	if s.cache == nil || (s.authCacheL1 == nil && s.authNegativeCacheL1 == nil) {
		return
	}
	s.authInvalidationStart.Do(func() {
		subscriberCtx, cancel := context.WithCancel(ctx)
		subscriberCtx = withAuthCacheSubscriptionReady(subscriberCtx, func() {
			s.authInvalidationConnected.Store(true)
		})
		s.authInvalidationCancel = cancel
		s.authInvalidationWG.Add(1)
		go func() {
			defer s.authInvalidationWG.Done()
			backoff := time.Second
			for {
				err := s.cache.SubscribeAuthCacheInvalidation(subscriberCtx, func(cacheKey string) {
					s.invalidateLocalAuthCache(cacheKey)
				})
				wasConnected := s.authInvalidationConnected.Swap(false)
				if subscriberCtx.Err() != nil {
					return
				}
				if wasConnected {
					backoff = time.Second
				}
				s.authInvalidationFailures.Add(1)
				if err == nil {
					err = errors.New("auth cache invalidation subscription closed")
				}
				slog.Warn("failed to start auth cache invalidation subscriber; retrying", "error", err, "retry_in", backoff)
				timer := time.NewTimer(backoff)
				select {
				case <-subscriberCtx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				if backoff < 30*time.Second {
					backoff *= 2
					if backoff > 30*time.Second {
						backoff = 30 * time.Second
					}
				}
			}
		}()
	})
}

func (s *APIKeyService) invalidateLocalAuthCache(cacheKey string) {
	if s == nil {
		return
	}
	if s.authCacheL1 != nil {
		s.authCacheL1.Del(cacheKey)
	}
	if s.authNegativeCacheL1 != nil {
		s.authNegativeCacheL1.Del(cacheKey)
	}
}

type AuthCacheInvalidationSubscriberHealth struct {
	Connected bool   `json:"connected"`
	Failures  uint64 `json:"failures"`
}

func (s *APIKeyService) AuthCacheInvalidationSubscriberHealth() AuthCacheInvalidationSubscriberHealth {
	if s == nil {
		return AuthCacheInvalidationSubscriberHealth{}
	}
	return AuthCacheInvalidationSubscriberHealth{
		Connected: s.authInvalidationConnected.Load(),
		Failures:  s.authInvalidationFailures.Load(),
	}
}

func (s *APIKeyService) StopAuthCacheInvalidationSubscriber() {
	if s == nil {
		return
	}
	s.authInvalidationStop.Do(func() {
		if s.authInvalidationCancel != nil {
			s.authInvalidationCancel()
		}
		s.authInvalidationWG.Wait()
	})
}

func (s *APIKeyService) authCacheKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func (s *APIKeyService) getAuthCacheEntry(ctx context.Context, cacheKey string) (*APIKeyAuthCacheEntry, bool) {
	if s.authCacheL1 != nil {
		if val, ok := s.authCacheL1.Get(cacheKey); ok {
			if entry, ok := val.(*APIKeyAuthCacheEntry); ok {
				return entry, true
			}
		}
	}
	if s.authNegativeCacheL1 != nil {
		if val, ok := s.authNegativeCacheL1.Get(cacheKey); ok {
			if entry, ok := val.(*APIKeyAuthCacheEntry); ok && entry.NotFound {
				return entry, true
			}
		}
	}
	if s.cache == nil || !s.authCfg.l2Enabled() {
		return nil, false
	}
	entry, err := s.cache.GetAuthCache(ctx, cacheKey)
	if err != nil {
		return nil, false
	}
	s.setAuthCacheL1(cacheKey, entry)
	return entry, true
}

func (s *APIKeyService) setAuthCacheL1(cacheKey string, entry *APIKeyAuthCacheEntry) {
	if entry == nil {
		return
	}
	if entry.NotFound {
		if s.authNegativeCacheL1 != nil && s.authCfg.negativeTTL > 0 {
			_ = s.authNegativeCacheL1.SetWithTTL(cacheKey, entry, 1, s.authCfg.jitterTTL(s.authCfg.negativeTTL))
		}
		return
	}
	if s.authCacheL1 == nil {
		return
	}
	ttl := s.authCfg.l1TTL
	ttl = s.authCfg.jitterTTL(ttl)
	_ = s.authCacheL1.SetWithTTL(cacheKey, entry, 1, ttl)
}

func (s *APIKeyService) setAuthCacheEntry(ctx context.Context, cacheKey string, entry *APIKeyAuthCacheEntry, ttl time.Duration) {
	if entry == nil {
		return
	}
	s.setAuthCacheL1(cacheKey, entry)
	if s.cache == nil || !s.authCfg.l2Enabled() {
		return
	}
	_ = s.cache.SetAuthCache(ctx, cacheKey, entry, s.authCfg.jitterTTL(ttl))
}

func (s *APIKeyService) deleteAuthCache(ctx context.Context, cacheKey string) {
	if s.authCacheL1 != nil {
		s.authCacheL1.Del(cacheKey)
	}
	if s.authNegativeCacheL1 != nil {
		s.authNegativeCacheL1.Del(cacheKey)
	}
	if s.cache == nil {
		return
	}
	_ = s.cache.DeleteAuthCache(ctx, cacheKey)
	// Publish invalidation message to other instances
	_ = s.cache.PublishAuthCacheInvalidation(ctx, cacheKey)
}

func (s *APIKeyService) loadAuthCacheEntry(ctx context.Context, key, cacheKey string) (*APIKeyAuthCacheEntry, error) {
	apiKey, err := s.lookupAPIKeyForAuth(ctx, key)
	if err != nil {
		if errors.Is(err, ErrAPIKeyNotFound) {
			entry := &APIKeyAuthCacheEntry{NotFound: true}
			if s.authCfg.negativeEnabled() {
				// 无效 Key 由攻击者控制且基数很高，只在有界的进程本地缓存中保存负缓存项，
				// 避免随机 Key 扫描放大为每个实例的 Redis 写入。
				s.setAuthCacheL1(cacheKey, entry)
			}
			return entry, nil
		}
		return nil, fmt.Errorf("get api key: %w", err)
	}
	apiKey.Key = key
	snapshot := s.snapshotFromAPIKey(ctx, apiKey)
	if snapshot == nil {
		return nil, fmt.Errorf("get api key: %w", ErrAPIKeyNotFound)
	}
	entry := &APIKeyAuthCacheEntry{Snapshot: snapshot}
	if apiKey.TeamID != nil {
		// 团队成员限额是高频变化数据，首版不缓存团队认证快照以保证阻断及时生效。
		return entry, nil
	}
	s.setAuthCacheEntry(ctx, cacheKey, entry, s.authCfg.l2TTL)
	return entry, nil
}

func (s *APIKeyService) lookupAPIKeyForAuth(ctx context.Context, key string) (*APIKey, error) {
	if s == nil || s.apiKeyRepo == nil {
		return nil, ErrAPIKeyNotFound
	}
	if s.authLookupSlots == nil {
		apiKey, err := s.apiKeyRepo.GetByKeyForAuth(ctx, key)
		return s.hydrateTeamAPIKey(ctx, apiKey, err)
	}
	s.authLookupTotal.Add(1)
	select {
	case s.authLookupSlots <- struct{}{}:
		s.authLookupInFlight.Add(1)
		defer func() {
			s.authLookupInFlight.Add(-1)
			<-s.authLookupSlots
		}()
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		s.authLookupRejected.Add(1)
		return nil, ErrAPIKeyAuthOverloaded
	}
	apiKey, err := s.apiKeyRepo.GetByKeyForAuth(ctx, key)
	return s.hydrateTeamAPIKey(ctx, apiKey, err)
}

func (s *APIKeyService) hydrateTeamAPIKey(ctx context.Context, apiKey *APIKey, err error) (*APIKey, error) {
	if err != nil || apiKey == nil || apiKey.TeamID == nil {
		return apiKey, err
	}
	// 禁用 Key 必须先由中间件返回稳定的 401，不能因离队后无法加载 Membership 变成 500。
	if !apiKey.IsActive() && apiKey.Status != StatusAPIKeyExpired && apiKey.Status != StatusAPIKeyQuotaExhausted {
		return apiKey, nil
	}
	if s.cfg != nil && !s.cfg.Team.Enabled {
		return nil, ErrTeamFeatureDisabled
	}
	if s.teamRepo == nil {
		return nil, ErrTeamFeatureDisabled
	}
	teamCtx, err := s.teamRepo.GetContextByUserID(ctx, apiKey.UserID)
	if err != nil {
		if errors.Is(err, ErrTeamNotFound) {
			return nil, ErrTeamMembershipRequired
		}
		return nil, err
	}
	if teamCtx == nil || teamCtx.Team == nil || teamCtx.Membership == nil || teamCtx.Team.ID != *apiKey.TeamID {
		return nil, ErrTeamMembershipRequired
	}
	if teamCtx.Membership.JoinedAt.After(apiKey.CreatedAt) {
		return nil, ErrTeamMembershipRequired
	}
	actor, err := s.userRepo.GetByID(ctx, apiKey.UserID)
	if err != nil {
		return nil, err
	}
	owner, err := s.userRepo.GetByID(ctx, teamCtx.Owner.UserID)
	if err != nil {
		return nil, err
	}
	apiKey.ActorUser = actor
	apiKey.User = owner
	apiKey.Team = teamCtx.Team
	apiKey.TeamMembership = teamCtx.Membership
	return apiKey, nil
}

func (s *APIKeyService) applyAuthCacheEntry(key string, entry *APIKeyAuthCacheEntry) (*APIKey, bool, error) {
	if entry == nil {
		return nil, false, nil
	}
	if entry.NotFound {
		return nil, true, ErrAPIKeyNotFound
	}
	if entry.Snapshot == nil {
		return nil, false, nil
	}
	if entry.Snapshot.Version != apiKeyAuthSnapshotVersion {
		return nil, false, nil
	}
	return s.snapshotToAPIKey(key, entry.Snapshot), true, nil
}

func (s *APIKeyService) snapshotFromAPIKey(ctx context.Context, apiKey *APIKey) *APIKeyAuthSnapshot {
	if apiKey == nil || apiKey.User == nil {
		return nil
	}
	snapshot := &APIKeyAuthSnapshot{
		Version:                               apiKeyAuthSnapshotVersion,
		APIKeyID:                              apiKey.ID,
		UserID:                                apiKey.UserID,
		TeamID:                                apiKey.TeamID,
		TeamOwnerDisabled:                     apiKey.TeamOwnerDisabled,
		CreatedAt:                             apiKey.CreatedAt,
		GroupID:                               apiKey.GroupID,
		IsComposite:                           apiKey.IsComposite,
		Name:                                  apiKey.Name,
		Status:                                apiKey.Status,
		FastModePolicy:                        apiKey.FastModePolicy,
		BillingMode:                           apiKey.BillingMode,
		PreferredSubscriptionID:               apiKey.PreferredSubscriptionID,
		ModelMapping:                          CloneModelMapping(apiKey.ModelMapping),
		IPWhitelist:                           apiKey.IPWhitelist,
		IPBlacklist:                           apiKey.IPBlacklist,
		Quota:                                 apiKey.Quota,
		QuotaUsed:                             apiKey.QuotaUsed,
		ExpiresAt:                             apiKey.ExpiresAt,
		RateLimit5h:                           apiKey.RateLimit5h,
		RateLimit1d:                           apiKey.RateLimit1d,
		RateLimit7d:                           apiKey.RateLimit7d,
		FallbackToDefaultGroupWhenUnavailable: apiKey.FallbackToDefaultGroupWhenUnavailable,
		User: APIKeyAuthUserSnapshot{
			ID:                         apiKey.User.ID,
			Status:                     apiKey.User.Status,
			Role:                       apiKey.User.Role,
			Balance:                    apiKey.User.Balance,
			Concurrency:                apiKey.User.Concurrency,
			AllowedGroups:              apiKey.User.AllowedGroups,
			Email:                      apiKey.User.Email,
			Username:                   apiKey.User.Username,
			BalanceNotifyEnabled:       apiKey.User.BalanceNotifyEnabled,
			BalanceNotifyThresholdType: apiKey.User.BalanceNotifyThresholdType,
			BalanceNotifyThreshold:     apiKey.User.BalanceNotifyThreshold,
			BalanceNotifyExtraEmails:   apiKey.User.BalanceNotifyExtraEmails,
			TotalRecharged:             apiKey.User.TotalRecharged,
			RPMLimit:                   apiKey.User.RPMLimit,
			DisabledPublicGroups:       append([]int64(nil), apiKey.User.DisabledPublicGroups...),
		},
	}
	if apiKey.ActorUser != nil {
		snapshot.ActorUser = &APIKeyAuthActorSnapshot{ID: apiKey.ActorUser.ID, Status: apiKey.ActorUser.Status, Email: apiKey.ActorUser.Email, Username: apiKey.ActorUser.Username}
	}
	if apiKey.Team != nil {
		snapshot.Team = &APIKeyAuthTeamSnapshot{ID: apiKey.Team.ID, Name: apiKey.Team.Name, Status: apiKey.Team.Status}
		snapshot.TeamMembership = apiKey.TeamMembership
	}

	// 填充 (user, group) RPM override —— 仅对可用分组预取，避免停用分组的 override 进入认证快照。
	if apiKey.GroupID != nil && *apiKey.GroupID > 0 && apiKey.Group != nil && apiKey.Group.IsActive() && s.userGroupRateRepo != nil {
		override, err := s.userGroupRateRepo.GetRPMOverrideByUserAndGroup(ctx, apiKey.User.ID, *apiKey.GroupID)
		if err == nil && override != nil {
			snapshot.User.UserGroupRPMOverride = override
		}
		// 查询失败或无 override 时留 nil，checkRPM 会回退到 DB 查询
	}
	if apiKey.Group != nil {
		snapshot.Group = &APIKeyAuthGroupSnapshot{
			ID:                              apiKey.Group.ID,
			Name:                            apiKey.Group.Name,
			Platform:                        apiKey.Group.Platform,
			SchedulerType:                   apiKey.Group.SchedulerType,
			AdvancedSchedulerOverrides:      CloneGroupAdvancedSchedulerOverrides(apiKey.Group.AdvancedSchedulerOverrides),
			IsExclusive:                     apiKey.Group.IsExclusive,
			Status:                          apiKey.Group.Status,
			RateMultiplier:                  apiKey.Group.RateMultiplier,
			DataSharingEnabled:              apiKey.Group.DataSharingEnabled,
			SessionIsolationEnabled:         apiKey.Group.SessionIsolationEnabled,
			AllowImageGeneration:            apiKey.Group.AllowImageGeneration,
			AllowBatchImageGeneration:       apiKey.Group.AllowBatchImageGeneration,
			ImageRateIndependent:            apiKey.Group.ImageRateIndependent,
			ImageRateMultiplier:             apiKey.Group.ImageRateMultiplier,
			ImagePrice1K:                    apiKey.Group.ImagePrice1K,
			ImagePrice2K:                    apiKey.Group.ImagePrice2K,
			ImagePrice4K:                    apiKey.Group.ImagePrice4K,
			VideoRateIndependent:            apiKey.Group.VideoRateIndependent,
			VideoRateMultiplier:             apiKey.Group.VideoRateMultiplier,
			VideoPrice480P:                  apiKey.Group.VideoPrice480P,
			VideoPrice720P:                  apiKey.Group.VideoPrice720P,
			VideoPrice1080P:                 apiKey.Group.VideoPrice1080P,
			VideoModelPrices:                NormalizeVideoModelPrices(apiKey.Group.VideoModelPrices),
			WebSearchPricePerCall:           apiKey.Group.WebSearchPricePerCall,
			SearchPricePer1k:                apiKey.Group.SearchPricePer1k,
			AudioRealtimePricePerMin:        apiKey.Group.AudioRealtimePricePerMin,
			AudioTTSPricePerMillionChars:    apiKey.Group.AudioTTSPricePerMillionChars,
			AudioSTTPricePerHour:            apiKey.Group.AudioSTTPricePerHour,
			LongContextPricingEnabled:       apiKey.Group.LongContextPricingEnabled,
			ModelPricing:                    cloneChannelModelPricingEntries(apiKey.Group.ModelPricing),
			ClaudeCodeOnly:                  apiKey.Group.ClaudeCodeOnly,
			FallbackGroupID:                 apiKey.Group.FallbackGroupID,
			FallbackGroupIDOnInvalidRequest: apiKey.Group.FallbackGroupIDOnInvalidRequest,
			UnavailableFallbackGroupID:      apiKey.Group.UnavailableFallbackGroupID,
			ModelRouting:                    apiKey.Group.ModelRouting,
			ModelRoutingEnabled:             apiKey.Group.ModelRoutingEnabled,
			MCPXMLInject:                    apiKey.Group.MCPXMLInject,
			SupportedModelScopes:            apiKey.Group.SupportedModelScopes,
			AllowedClientProtocols:          cloneGroupClientProtocols(apiKey.Group.AllowedClientProtocols),
			AllowLive:                       apiKey.Group.AllowLive,
			DefaultMappedModel:              apiKey.Group.DefaultMappedModel,
			MessagesDispatchModelConfig:     apiKey.Group.MessagesDispatchModelConfig,
			ModelsListConfig:                apiKey.Group.ModelsListConfig,
			RPMLimit:                        apiKey.Group.RPMLimit,
			MaxReasoningEffort:              apiKey.Group.MaxReasoningEffort,
			ReasoningEffortMappings:         apiKey.Group.ReasoningEffortMappings,
			PeakRateEnabled:                 apiKey.Group.PeakRateEnabled,
			PeakStart:                       apiKey.Group.PeakStart,
			PeakEnd:                         apiKey.Group.PeakEnd,
			PeakRateMultiplier:              apiKey.Group.PeakRateMultiplier,
		}
	}
	if apiKey.IsComposite {
		snapshot.CompositeGroups = make([]APIKeyAuthCompositeGroupSnapshot, 0, len(apiKey.CompositeGroups))
		for _, binding := range apiKey.CompositeGroups {
			bindingSnapshot := APIKeyAuthCompositeGroupSnapshot{
				ID:                       binding.ID,
				GroupID:                  binding.GroupID,
				Prefix:                   binding.Prefix,
				NormalizedPrefix:         binding.NormalizedPrefix,
				SortOrder:                binding.SortOrder,
				DataSharingNoticeVersion: binding.DataSharingNoticeVersion,
				DataSharingConfirmedAt:   binding.DataSharingConfirmedAt,
				Group:                    authGroupSnapshotFromGroup(binding.Group),
			}
			if binding.Group != nil && binding.Group.IsActive() && s.userGroupRateRepo != nil {
				override, err := s.userGroupRateRepo.GetRPMOverrideByUserAndGroup(ctx, apiKey.User.ID, binding.GroupID)
				if err == nil {
					bindingSnapshot.UserGroupRPMOverride = override
				}
			}
			snapshot.CompositeGroups = append(snapshot.CompositeGroups, bindingSnapshot)
		}
	}
	return snapshot
}

func (s *APIKeyService) snapshotToAPIKey(key string, snapshot *APIKeyAuthSnapshot) *APIKey {
	if snapshot == nil {
		return nil
	}
	apiKey := &APIKey{
		ID:                                    snapshot.APIKeyID,
		UserID:                                snapshot.UserID,
		TeamID:                                snapshot.TeamID,
		TeamOwnerDisabled:                     snapshot.TeamOwnerDisabled,
		CreatedAt:                             snapshot.CreatedAt,
		GroupID:                               snapshot.GroupID,
		IsComposite:                           snapshot.IsComposite,
		Key:                                   key,
		Name:                                  snapshot.Name,
		Status:                                snapshot.Status,
		FastModePolicy:                        snapshot.FastModePolicy,
		BillingMode:                           snapshot.BillingMode,
		PreferredSubscriptionID:               snapshot.PreferredSubscriptionID,
		ModelMapping:                          CloneModelMapping(snapshot.ModelMapping),
		IPWhitelist:                           snapshot.IPWhitelist,
		IPBlacklist:                           snapshot.IPBlacklist,
		Quota:                                 snapshot.Quota,
		QuotaUsed:                             snapshot.QuotaUsed,
		ExpiresAt:                             snapshot.ExpiresAt,
		RateLimit5h:                           snapshot.RateLimit5h,
		RateLimit1d:                           snapshot.RateLimit1d,
		RateLimit7d:                           snapshot.RateLimit7d,
		FallbackToDefaultGroupWhenUnavailable: snapshot.FallbackToDefaultGroupWhenUnavailable,
		User: &User{
			ID:                         snapshot.User.ID,
			Status:                     snapshot.User.Status,
			Role:                       snapshot.User.Role,
			Balance:                    snapshot.User.Balance,
			Concurrency:                snapshot.User.Concurrency,
			AllowedGroups:              snapshot.User.AllowedGroups,
			Email:                      snapshot.User.Email,
			Username:                   snapshot.User.Username,
			BalanceNotifyEnabled:       snapshot.User.BalanceNotifyEnabled,
			BalanceNotifyThresholdType: snapshot.User.BalanceNotifyThresholdType,
			BalanceNotifyThreshold:     snapshot.User.BalanceNotifyThreshold,
			BalanceNotifyExtraEmails:   snapshot.User.BalanceNotifyExtraEmails,
			TotalRecharged:             snapshot.User.TotalRecharged,
			RPMLimit:                   snapshot.User.RPMLimit,
			UserGroupRPMOverride:       snapshot.User.UserGroupRPMOverride,
			DisabledPublicGroups:       append([]int64(nil), snapshot.User.DisabledPublicGroups...),
			GroupRestrictionsLoaded:    true,
		},
	}
	if snapshot.ActorUser != nil {
		apiKey.ActorUser = &User{ID: snapshot.ActorUser.ID, Status: snapshot.ActorUser.Status, Email: snapshot.ActorUser.Email, Username: snapshot.ActorUser.Username}
	} else {
		apiKey.ActorUser = apiKey.User
	}
	if snapshot.Team != nil {
		apiKey.Team = &Team{ID: snapshot.Team.ID, Name: snapshot.Team.Name, Status: snapshot.Team.Status}
		apiKey.TeamMembership = snapshot.TeamMembership
	}
	if snapshot.Group != nil {
		apiKey.Group = &Group{
			ID:                              snapshot.Group.ID,
			Name:                            snapshot.Group.Name,
			Platform:                        snapshot.Group.Platform,
			SchedulerType:                   snapshot.Group.SchedulerType,
			AdvancedSchedulerOverrides:      CloneGroupAdvancedSchedulerOverrides(snapshot.Group.AdvancedSchedulerOverrides),
			IsExclusive:                     snapshot.Group.IsExclusive,
			Status:                          snapshot.Group.Status,
			Hydrated:                        true,
			RateMultiplier:                  snapshot.Group.RateMultiplier,
			DataSharingEnabled:              snapshot.Group.DataSharingEnabled,
			SessionIsolationEnabled:         snapshot.Group.SessionIsolationEnabled,
			AllowImageGeneration:            snapshot.Group.AllowImageGeneration,
			AllowBatchImageGeneration:       snapshot.Group.AllowBatchImageGeneration,
			ImageRateIndependent:            snapshot.Group.ImageRateIndependent,
			ImageRateMultiplier:             snapshot.Group.ImageRateMultiplier,
			ImagePrice1K:                    snapshot.Group.ImagePrice1K,
			ImagePrice2K:                    snapshot.Group.ImagePrice2K,
			ImagePrice4K:                    snapshot.Group.ImagePrice4K,
			VideoRateIndependent:            snapshot.Group.VideoRateIndependent,
			VideoRateMultiplier:             snapshot.Group.VideoRateMultiplier,
			VideoPrice480P:                  snapshot.Group.VideoPrice480P,
			VideoPrice720P:                  snapshot.Group.VideoPrice720P,
			VideoPrice1080P:                 snapshot.Group.VideoPrice1080P,
			VideoModelPrices:                NormalizeVideoModelPrices(snapshot.Group.VideoModelPrices),
			WebSearchPricePerCall:           snapshot.Group.WebSearchPricePerCall,
			SearchPricePer1k:                snapshot.Group.SearchPricePer1k,
			AudioRealtimePricePerMin:        snapshot.Group.AudioRealtimePricePerMin,
			AudioTTSPricePerMillionChars:    snapshot.Group.AudioTTSPricePerMillionChars,
			AudioSTTPricePerHour:            snapshot.Group.AudioSTTPricePerHour,
			LongContextPricingEnabled:       snapshot.Group.LongContextPricingEnabled,
			ModelPricing:                    cloneChannelModelPricingEntries(snapshot.Group.ModelPricing),
			ClaudeCodeOnly:                  snapshot.Group.ClaudeCodeOnly,
			FallbackGroupID:                 snapshot.Group.FallbackGroupID,
			FallbackGroupIDOnInvalidRequest: snapshot.Group.FallbackGroupIDOnInvalidRequest,
			UnavailableFallbackGroupID:      snapshot.Group.UnavailableFallbackGroupID,
			ModelRouting:                    snapshot.Group.ModelRouting,
			ModelRoutingEnabled:             snapshot.Group.ModelRoutingEnabled,
			MCPXMLInject:                    snapshot.Group.MCPXMLInject,
			SupportedModelScopes:            snapshot.Group.SupportedModelScopes,
			AllowedClientProtocols:          cloneGroupClientProtocols(snapshot.Group.AllowedClientProtocols),
			AllowLive:                       snapshot.Group.AllowLive,
			DefaultMappedModel:              snapshot.Group.DefaultMappedModel,
			MessagesDispatchModelConfig:     snapshot.Group.MessagesDispatchModelConfig,
			ModelsListConfig:                snapshot.Group.ModelsListConfig,
			RPMLimit:                        snapshot.Group.RPMLimit,
			MaxReasoningEffort:              snapshot.Group.MaxReasoningEffort,
			ReasoningEffortMappings:         snapshot.Group.ReasoningEffortMappings,
			PeakRateEnabled:                 snapshot.Group.PeakRateEnabled,
			PeakStart:                       snapshot.Group.PeakStart,
			PeakEnd:                         snapshot.Group.PeakEnd,
			PeakRateMultiplier:              snapshot.Group.PeakRateMultiplier,
		}
	}
	if snapshot.IsComposite {
		apiKey.CompositeGroups = make([]APIKeyCompositeGroup, 0, len(snapshot.CompositeGroups))
		for _, binding := range snapshot.CompositeGroups {
			apiKey.CompositeGroups = append(apiKey.CompositeGroups, APIKeyCompositeGroup{
				ID:                       binding.ID,
				APIKeyID:                 snapshot.APIKeyID,
				GroupID:                  binding.GroupID,
				Prefix:                   binding.Prefix,
				NormalizedPrefix:         binding.NormalizedPrefix,
				SortOrder:                binding.SortOrder,
				DataSharingNoticeVersion: binding.DataSharingNoticeVersion,
				DataSharingConfirmedAt:   binding.DataSharingConfirmedAt,
				UserGroupRPMOverride:     binding.UserGroupRPMOverride,
				Group:                    groupFromAuthSnapshot(binding.Group),
			})
		}
	}
	s.compileAPIKeyIPRules(apiKey)
	return apiKey
}

// authGroupSnapshotFromGroup 将分组复制到认证缓存，避免复合映射共享可变对象。
func authGroupSnapshotFromGroup(group *Group) *APIKeyAuthGroupSnapshot {
	if group == nil {
		return nil
	}
	return &APIKeyAuthGroupSnapshot{
		ID: group.ID, Name: group.Name, Platform: group.Platform, SchedulerType: group.SchedulerType,
		AdvancedSchedulerOverrides: CloneGroupAdvancedSchedulerOverrides(group.AdvancedSchedulerOverrides), IsExclusive: group.IsExclusive,
		Status: group.Status, RateMultiplier: group.RateMultiplier, DataSharingEnabled: group.DataSharingEnabled,
		SessionIsolationEnabled: group.SessionIsolationEnabled, AllowImageGeneration: group.AllowImageGeneration,
		AllowBatchImageGeneration: group.AllowBatchImageGeneration, ImageRateIndependent: group.ImageRateIndependent,
		ImageRateMultiplier: group.ImageRateMultiplier, ImagePrice1K: group.ImagePrice1K, ImagePrice2K: group.ImagePrice2K,
		ImagePrice4K: group.ImagePrice4K, VideoRateIndependent: group.VideoRateIndependent,
		VideoRateMultiplier: group.VideoRateMultiplier, VideoPrice480P: group.VideoPrice480P,
		VideoPrice720P: group.VideoPrice720P, VideoPrice1080P: group.VideoPrice1080P,
		VideoModelPrices: NormalizeVideoModelPrices(group.VideoModelPrices), WebSearchPricePerCall: group.WebSearchPricePerCall,
		SearchPricePer1k: group.SearchPricePer1k, AudioRealtimePricePerMin: group.AudioRealtimePricePerMin,
		AudioTTSPricePerMillionChars: group.AudioTTSPricePerMillionChars, AudioSTTPricePerHour: group.AudioSTTPricePerHour,
		LongContextPricingEnabled: group.LongContextPricingEnabled, ModelPricing: cloneChannelModelPricingEntries(group.ModelPricing),
		ClaudeCodeOnly:  group.ClaudeCodeOnly,
		FallbackGroupID: group.FallbackGroupID, FallbackGroupIDOnInvalidRequest: group.FallbackGroupIDOnInvalidRequest,
		UnavailableFallbackGroupID: group.UnavailableFallbackGroupID, ModelRouting: group.ModelRouting,
		ModelRoutingEnabled: group.ModelRoutingEnabled, MCPXMLInject: group.MCPXMLInject,
		SupportedModelScopes: group.SupportedModelScopes, AllowedClientProtocols: cloneGroupClientProtocols(group.AllowedClientProtocols),
		AllowLive: group.AllowLive, DefaultMappedModel: group.DefaultMappedModel,
		MessagesDispatchModelConfig: group.MessagesDispatchModelConfig, ModelsListConfig: group.ModelsListConfig,
		RPMLimit: group.RPMLimit, MaxReasoningEffort: group.MaxReasoningEffort,
		ReasoningEffortMappings: group.ReasoningEffortMappings, PeakRateEnabled: group.PeakRateEnabled,
		PeakStart: group.PeakStart, PeakEnd: group.PeakEnd, PeakRateMultiplier: group.PeakRateMultiplier,
	}
}

// groupFromAuthSnapshot 为单次请求还原独立分组对象。
func groupFromAuthSnapshot(snapshot *APIKeyAuthGroupSnapshot) *Group {
	if snapshot == nil {
		return nil
	}
	return &Group{
		ID: snapshot.ID, Name: snapshot.Name, Platform: snapshot.Platform, SchedulerType: snapshot.SchedulerType,
		AdvancedSchedulerOverrides: CloneGroupAdvancedSchedulerOverrides(snapshot.AdvancedSchedulerOverrides), IsExclusive: snapshot.IsExclusive,
		Status: snapshot.Status, Hydrated: true, RateMultiplier: snapshot.RateMultiplier,
		DataSharingEnabled: snapshot.DataSharingEnabled, SessionIsolationEnabled: snapshot.SessionIsolationEnabled,
		AllowImageGeneration: snapshot.AllowImageGeneration, AllowBatchImageGeneration: snapshot.AllowBatchImageGeneration,
		ImageRateIndependent: snapshot.ImageRateIndependent, ImageRateMultiplier: snapshot.ImageRateMultiplier,
		ImagePrice1K: snapshot.ImagePrice1K, ImagePrice2K: snapshot.ImagePrice2K, ImagePrice4K: snapshot.ImagePrice4K,
		VideoRateIndependent: snapshot.VideoRateIndependent, VideoRateMultiplier: snapshot.VideoRateMultiplier,
		VideoPrice480P: snapshot.VideoPrice480P, VideoPrice720P: snapshot.VideoPrice720P,
		VideoPrice1080P: snapshot.VideoPrice1080P, VideoModelPrices: NormalizeVideoModelPrices(snapshot.VideoModelPrices),
		WebSearchPricePerCall: snapshot.WebSearchPricePerCall, SearchPricePer1k: snapshot.SearchPricePer1k,
		AudioRealtimePricePerMin:     snapshot.AudioRealtimePricePerMin,
		AudioTTSPricePerMillionChars: snapshot.AudioTTSPricePerMillionChars,
		AudioSTTPricePerHour:         snapshot.AudioSTTPricePerHour,
		LongContextPricingEnabled:    snapshot.LongContextPricingEnabled,
		ModelPricing:                 cloneChannelModelPricingEntries(snapshot.ModelPricing),
		ClaudeCodeOnly:               snapshot.ClaudeCodeOnly, FallbackGroupID: snapshot.FallbackGroupID,
		FallbackGroupIDOnInvalidRequest: snapshot.FallbackGroupIDOnInvalidRequest,
		UnavailableFallbackGroupID:      snapshot.UnavailableFallbackGroupID, ModelRouting: snapshot.ModelRouting,
		ModelRoutingEnabled: snapshot.ModelRoutingEnabled, MCPXMLInject: snapshot.MCPXMLInject,
		SupportedModelScopes: snapshot.SupportedModelScopes, AllowedClientProtocols: cloneGroupClientProtocols(snapshot.AllowedClientProtocols),
		AllowLive: snapshot.AllowLive, DefaultMappedModel: snapshot.DefaultMappedModel,
		MessagesDispatchModelConfig: snapshot.MessagesDispatchModelConfig, ModelsListConfig: snapshot.ModelsListConfig,
		RPMLimit: snapshot.RPMLimit, MaxReasoningEffort: snapshot.MaxReasoningEffort,
		ReasoningEffortMappings: snapshot.ReasoningEffortMappings, PeakRateEnabled: snapshot.PeakRateEnabled,
		PeakStart: snapshot.PeakStart, PeakEnd: snapshot.PeakEnd, PeakRateMultiplier: snapshot.PeakRateMultiplier,
	}
}

// cloneChannelModelPricingEntries 复制认证快照中的价卡切片，避免请求对象修改缓存内容。
func cloneChannelModelPricingEntries(entries []ChannelModelPricing) []ChannelModelPricing {
	if entries == nil {
		return nil
	}
	cloned := make([]ChannelModelPricing, len(entries))
	for i := range entries {
		cloned[i] = entries[i].Clone()
	}
	return cloned
}
