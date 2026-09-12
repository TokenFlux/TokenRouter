// 本文件维护 apikey 的所属能力；兼容入口复用唯一实现。
package apikey

import (
	context "context"
	errors "errors"
	fmt "fmt"
	accessview "github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	ristretto "github.com/dgraph-io/ristretto"
	slog "log/slog"
	maps "maps"
	rand "math/rand/v2"
	"slices"
	time "time"
)

const KeyApiKeyAuthSnapshotVersion = 40

type KeyApiKeyAuthCacheConfig struct {
	l1Size        int
	l1TTL         time.Duration
	l2TTL         time.Duration
	negativeTTL   time.Duration
	jitterPercent int
	singleflight  bool
}

func KeyNewAPIKeyAuthCacheConfig(cfg *Options) KeyApiKeyAuthCacheConfig {
	if cfg == nil {
		return KeyApiKeyAuthCacheConfig{}
	}
	auth := cfg.APIKeyAuth
	return KeyApiKeyAuthCacheConfig{
		l1Size:        auth.L1Size,
		l1TTL:         time.Duration(auth.L1TTLSeconds) * time.Second,
		l2TTL:         time.Duration(auth.L2TTLSeconds) * time.Second,
		negativeTTL:   time.Duration(auth.NegativeTTLSeconds) * time.Second,
		jitterPercent: auth.JitterPercent,
		singleflight:  auth.Singleflight,
	}
}

func (c KeyApiKeyAuthCacheConfig) KeyL1Enabled() bool {
	return c.l1Size > 0 && c.l1TTL > 0
}

func (c KeyApiKeyAuthCacheConfig) KeyL2Enabled() bool {
	return c.l2TTL > 0
}

func (c KeyApiKeyAuthCacheConfig) KeyNegativeEnabled() bool {
	return c.negativeTTL > 0
}

// KeyJitterTTL 为缓存 TTL 添加抖动，避免多个请求在同一时刻同时过期触发集中回源。
// 这里直接使用 rand/v2 的顶层函数：并发安全，无需全局互斥锁。
func (c KeyApiKeyAuthCacheConfig) KeyJitterTTL(ttl time.Duration) time.Duration {
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

func (s *APIKeyService) initAuthCaches() {
	if s.authCfg.KeyNegativeEnabled() {
		negativeSize := KeyDefaultNegativeAuthCacheSize
		if s.authCfg.l1Size > 0 && s.authCfg.l1Size < negativeSize {
			negativeSize = s.authCfg.l1Size
		}
		cache, err := ristretto.NewCache(&ristretto.Config{
			NumCounters: int64(negativeSize) * 10,
			MaxCost:     int64(negativeSize),
			BufferItems: 64,
		})
		if err == nil {
			s.authNegativeCacheL1.Store(cache)
		}
	}
	if s.authCfg.KeyL1Enabled() {
		cache, err := ristretto.NewCache(&ristretto.Config{
			NumCounters: int64(s.authCfg.l1Size) * 10,
			MaxCost:     int64(s.authCfg.l1Size),
			BufferItems: 64,
		})
		if err == nil {
			s.authCacheL1.Store(cache)
		}
	}
}

// StartAuthCacheInvalidationSubscriber starts the Pub/Sub subscriber for L1 cache invalidation.
// This should be called after the service is fully initialized.
func (s *APIKeyService) StartAuthCacheInvalidationSubscriber(ctx context.Context) {
	if s == nil {
		return
	}
	s.subscriberMu.Lock()
	defer s.subscriberMu.Unlock()
	if s.subscriberStopped {
		return
	}

	if s.cache == nil || (s.authCacheL1.Load() == nil && s.authNegativeCacheL1.Load() == nil) {
		return
	}
	s.authInvalidationStart.Do(func() {
		subscriberCtx, cancel := context.WithCancel(ctx)
		subscriberCtx = KeyWithAuthCacheSubscriptionReady(subscriberCtx, func() {
			s.authInvalidationConnected.Store(true)
		})
		s.authInvalidationCancel = cancel
		s.authInvalidationWG.Add(1)
		go func() {
			defer s.authInvalidationWG.Done()
			backoff := time.Second
			for {
				err := s.cache.SubscribeAuthCacheInvalidation(subscriberCtx, func(cacheKey string) {
					s.KeyInvalidateLocalAuthCache(cacheKey)
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

func (s *APIKeyService) KeyInvalidateLocalAuthCache(cacheKey string) {
	if s == nil {
		return
	}
	if s.authCacheL1.Load() != nil {
		s.authCacheL1.Load().Del(cacheKey)
	}
	if s.authNegativeCacheL1.Load() != nil {
		s.authNegativeCacheL1.Load().Del(cacheKey)
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
		s.subscriberMu.Lock()
		s.subscriberStopped = true
		cancel := s.authInvalidationCancel
		s.subscriberMu.Unlock()
		if cancel != nil {
			cancel()
		}
		s.authInvalidationWG.Wait()
	})
}

func (s *APIKeyService) KeyAuthCacheKey(key string) string { return AuthCacheKey(key) }

func (s *APIKeyService) KeyGetAuthCacheEntry(ctx context.Context, cacheKey string) (*APIKeyAuthCacheEntry, bool) {
	if s.authCacheL1.Load() != nil {
		if val, ok := s.authCacheL1.Load().Get(cacheKey); ok {
			if entry, ok := val.(*APIKeyAuthCacheEntry); ok {
				return entry, true
			}
		}
	}
	if s.authNegativeCacheL1.Load() != nil {
		if val, ok := s.authNegativeCacheL1.Load().Get(cacheKey); ok {
			if entry, ok := val.(*APIKeyAuthCacheEntry); ok && entry.NotFound {
				return entry, true
			}
		}
	}
	if s.cache == nil || !s.authCfg.KeyL2Enabled() {
		return nil, false
	}
	entry, err := s.cache.GetAuthCache(ctx, cacheKey)
	if err != nil {
		return nil, false
	}
	s.KeySetAuthCacheL1(cacheKey, entry)
	return entry, true
}

func (s *APIKeyService) KeySetAuthCacheL1(cacheKey string, entry *APIKeyAuthCacheEntry) {
	if entry == nil {
		return
	}
	if entry.NotFound {
		if s.authNegativeCacheL1.Load() != nil && s.authCfg.negativeTTL > 0 {
			_ = s.authNegativeCacheL1.Load().SetWithTTL(cacheKey, entry, 1, s.authCfg.KeyJitterTTL(s.authCfg.negativeTTL))
		}
		return
	}
	if s.authCacheL1.Load() == nil {
		return
	}
	ttl := s.authCfg.l1TTL
	ttl = s.authCfg.KeyJitterTTL(ttl)
	_ = s.authCacheL1.Load().SetWithTTL(cacheKey, entry, 1, ttl)
}

func (s *APIKeyService) KeySetAuthCacheEntry(ctx context.Context, cacheKey string, entry *APIKeyAuthCacheEntry, ttl time.Duration) {
	if entry == nil {
		return
	}
	s.KeySetAuthCacheL1(cacheKey, entry)
	if s.cache == nil || !s.authCfg.KeyL2Enabled() {
		return
	}
	_ = s.cache.SetAuthCache(ctx, cacheKey, entry, s.authCfg.KeyJitterTTL(ttl))
}

func (s *APIKeyService) KeyDeleteAuthCache(ctx context.Context, cacheKey string) {
	if s.authCacheL1.Load() != nil {
		s.authCacheL1.Load().Del(cacheKey)
	}
	if s.authNegativeCacheL1.Load() != nil {
		s.authNegativeCacheL1.Load().Del(cacheKey)
	}
	if s.cache == nil {
		return
	}
	_ = s.cache.DeleteAuthCache(ctx, cacheKey)
	// Publish invalidation message to other instances
	_ = s.cache.PublishAuthCacheInvalidation(ctx, cacheKey)
}

func (s *APIKeyService) KeyLoadAuthCacheEntry(ctx context.Context, key, cacheKey string) (*APIKeyAuthCacheEntry, error) {
	apiKey, err := s.KeyLookupAPIKeyForAuth(ctx, key)
	if err != nil {
		if errors.Is(err, ErrAPIKeyNotFound) {
			entry := &APIKeyAuthCacheEntry{NotFound: true}
			if s.authCfg.KeyNegativeEnabled() {
				// 无效 Key 由攻击者控制且基数很高，只在有界的进程本地缓存中保存负缓存项，
				// 避免随机 Key 扫描放大为每个实例的 Redis 写入。
				s.KeySetAuthCacheL1(cacheKey, entry)
			}
			return entry, nil
		}
		return nil, fmt.Errorf("get api key: %w", err)
	}
	apiKey.Key = key
	snapshot := s.KeySnapshotFromAPIKey(ctx, apiKey)
	if snapshot == nil {
		return nil, fmt.Errorf("get api key: %w", ErrAPIKeyNotFound)
	}
	entry := &APIKeyAuthCacheEntry{Snapshot: snapshot}
	if apiKey.TeamID != nil {
		// 团队成员限额是高频变化数据，首版不缓存团队认证快照以保证阻断及时生效。
		return entry, nil
	}
	s.KeySetAuthCacheEntry(ctx, cacheKey, entry, s.authCfg.l2TTL)
	return entry, nil
}

func (s *APIKeyService) KeyLookupAPIKeyForAuth(ctx context.Context, key string) (*APIKey, error) {
	if s == nil || s.apiKeyRepo == nil {
		return nil, ErrAPIKeyNotFound
	}
	if s.authLookupSlots == nil {
		apiKey, err := s.apiKeyRepo.GetByKeyForAuth(ctx, key)
		return s.KeyHydrateTeamAPIKey(ctx, apiKey, err)
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
	return s.KeyHydrateTeamAPIKey(ctx, apiKey, err)
}

func (s *APIKeyService) KeyHydrateTeamAPIKey(ctx context.Context, apiKey *APIKey, err error) (*APIKey, error) {
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

func (s *APIKeyService) KeyApplyAuthCacheEntry(key string, entry *APIKeyAuthCacheEntry) (*APIKey, bool, error) {
	if entry == nil {
		return nil, false, nil
	}
	if entry.NotFound {
		return nil, true, ErrAPIKeyNotFound
	}
	if entry.Snapshot == nil {
		return nil, false, nil
	}
	if entry.Snapshot.Version != KeyApiKeyAuthSnapshotVersion {
		return nil, false, nil
	}
	return s.KeySnapshotToAPIKey(key, entry.Snapshot), true, nil
}

func (s *APIKeyService) KeySnapshotFromAPIKey(ctx context.Context, apiKey *APIKey) *APIKeyAuthSnapshot {
	if apiKey == nil || apiKey.User == nil {
		return nil
	}
	snapshot := &APIKeyAuthSnapshot{
		Version:                               KeyApiKeyAuthSnapshotVersion,
		APIKeyID:                              apiKey.ID,
		UserID:                                apiKey.UserID,
		TeamID:                                clonePointer(apiKey.TeamID),
		TeamOwnerDisabled:                     apiKey.TeamOwnerDisabled,
		CreatedAt:                             apiKey.CreatedAt,
		GroupID:                               clonePointer(apiKey.GroupID),
		IsComposite:                           apiKey.IsComposite,
		Name:                                  apiKey.Name,
		Status:                                apiKey.Status,
		FastModePolicy:                        apiKey.FastModePolicy,
		BillingMode:                           apiKey.BillingMode,
		PreferredSubscriptionID:               clonePointer(apiKey.PreferredSubscriptionID),
		ModelMapping:                          CloneModelMapping(apiKey.ModelMapping),
		IPWhitelist:                           slices.Clone(apiKey.IPWhitelist),
		IPBlacklist:                           slices.Clone(apiKey.IPBlacklist),
		Quota:                                 apiKey.Quota,
		QuotaUsed:                             apiKey.QuotaUsed,
		ExpiresAt:                             clonePointer(apiKey.ExpiresAt),
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
			AllowedGroups:              slices.Clone(apiKey.User.AllowedGroups),
			Email:                      apiKey.User.Email,
			Username:                   apiKey.User.Username,
			BalanceNotifyEnabled:       apiKey.User.BalanceNotifyEnabled,
			BalanceNotifyThresholdType: apiKey.User.BalanceNotifyThresholdType,
			BalanceNotifyThreshold:     clonePointer(apiKey.User.BalanceNotifyThreshold),
			BalanceNotifyExtraEmails:   slices.Clone(apiKey.User.BalanceNotifyExtraEmails),
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
		snapshot.TeamMembership = cloneMembership(apiKey.TeamMembership)
	}

	// 填充 (user, group) RPM override —— 仅对可用分组预取，避免停用分组的 override 进入认证快照。
	if apiKey.GroupID != nil && *apiKey.GroupID > 0 && apiKey.Group != nil && apiKey.Group.IsActive() && s.userGroupRateRepo != nil {
		override, err := s.userGroupRateRepo.GetRPMOverrideByUserAndGroup(ctx, apiKey.User.ID, *apiKey.GroupID)
		if err == nil && override != nil {
			snapshot.User.UserGroupRPMOverride = clonePointer(override)
		}
		// 查询失败或无 override 时留 nil，checkRPM 会回退到 DB 查询
	}
	if apiKey.Group != nil {
		snapshot.Group = &APIKeyAuthGroupSnapshot{
			ID:                              apiKey.Group.ID,
			Name:                            apiKey.Group.Name,
			Platform:                        apiKey.Group.Platform,
			SchedulerType:                   apiKey.Group.SchedulerType,
			AdvancedSchedulerOverrides:      accessview.CloneGroupAdvancedSchedulerOverrides(apiKey.Group.AdvancedSchedulerOverrides),
			IsExclusive:                     apiKey.Group.IsExclusive,
			Status:                          apiKey.Group.Status,
			RateMultiplier:                  apiKey.Group.RateMultiplier,
			SessionIsolationEnabled:         apiKey.Group.SessionIsolationEnabled,
			AllowImageGeneration:            apiKey.Group.AllowImageGeneration,
			AllowBatchImageGeneration:       apiKey.Group.AllowBatchImageGeneration,
			WebSearchPricePerCall:           clonePointer(apiKey.Group.WebSearchPricePerCall),
			SearchPricePer1k:                clonePointer(apiKey.Group.SearchPricePer1k),
			AudioRealtimePricePerMin:        clonePointer(apiKey.Group.AudioRealtimePricePerMin),
			AudioTTSPricePerMillionChars:    clonePointer(apiKey.Group.AudioTTSPricePerMillionChars),
			AudioSTTPricePerHour:            clonePointer(apiKey.Group.AudioSTTPricePerHour),
			LongContextPricingEnabled:       apiKey.Group.LongContextPricingEnabled,
			ModelPricing:                    KeyCloneChannelModelPricingEntries(apiKey.Group.ModelPricing),
			ClaudeCodeOnly:                  apiKey.Group.ClaudeCodeOnly,
			FallbackGroupID:                 clonePointer(apiKey.Group.FallbackGroupID),
			FallbackGroupIDOnInvalidRequest: clonePointer(apiKey.Group.FallbackGroupIDOnInvalidRequest),
			UnavailableFallbackGroupID:      clonePointer(apiKey.Group.UnavailableFallbackGroupID),
			ModelRouting:                    cloneModelRouting(apiKey.Group.ModelRouting),
			ModelRoutingEnabled:             apiKey.Group.ModelRoutingEnabled,
			MCPXMLInject:                    apiKey.Group.MCPXMLInject,
			SupportedModelScopes:            slices.Clone(apiKey.Group.SupportedModelScopes),
			AllowedProtocols:                cloneGroupClientProtocols(apiKey.Group.AllowedProtocols),
			ProtocolFallbacks:               maps.Clone(apiKey.Group.ProtocolFallbacks),
			ResponsesImagePolicy:            apiKey.Group.ResponsesImagePolicy,
			AllowLive:                       apiKey.Group.AllowLive,
			ForceOpenAIFast:                 apiKey.Group.ForceOpenAIFast,
			OpenAIFastPolicy:                s.groupPolicy(apiKey.Group),
			FreeOpenAIFast:                  apiKey.Group.FreeOpenAIFast,
			DefaultMappedModel:              apiKey.Group.DefaultMappedModel,
			MessagesDispatchModelConfig:     cloneMessagesDispatch(apiKey.Group.MessagesDispatchModelConfig),
			ModelsListConfig:                cloneModelsList(apiKey.Group.ModelsListConfig),
			RPMLimit:                        apiKey.Group.RPMLimit,
			MaxReasoningEffort:              apiKey.Group.MaxReasoningEffort,
			MaxReasoningEffortOverLimit:     apiKey.Group.MaxReasoningEffortOverLimit,
			ReasoningEffortMappings:         slices.Clone(apiKey.Group.ReasoningEffortMappings),
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
				ID:               binding.ID,
				GroupID:          binding.GroupID,
				Prefix:           binding.Prefix,
				NormalizedPrefix: binding.NormalizedPrefix,
				SortOrder:        binding.SortOrder,
				Group:            KeyAuthGroupSnapshotFromGroup(binding.Group),
			}
			if binding.Group != nil && binding.Group.IsActive() && s.userGroupRateRepo != nil {
				override, err := s.userGroupRateRepo.GetRPMOverrideByUserAndGroup(ctx, apiKey.User.ID, binding.GroupID)
				if err == nil {
					bindingSnapshot.UserGroupRPMOverride = clonePointer(override)
				}
			}
			snapshot.CompositeGroups = append(snapshot.CompositeGroups, bindingSnapshot)
		}
	}
	return snapshot
}

func (s *APIKeyService) KeySnapshotToAPIKey(key string, snapshot *APIKeyAuthSnapshot) *APIKey {
	if snapshot == nil {
		return nil
	}
	apiKey := &APIKey{
		ID:                                    snapshot.APIKeyID,
		UserID:                                snapshot.UserID,
		TeamID:                                clonePointer(snapshot.TeamID),
		TeamOwnerDisabled:                     snapshot.TeamOwnerDisabled,
		CreatedAt:                             snapshot.CreatedAt,
		GroupID:                               clonePointer(snapshot.GroupID),
		IsComposite:                           snapshot.IsComposite,
		Key:                                   key,
		Name:                                  snapshot.Name,
		Status:                                snapshot.Status,
		FastModePolicy:                        snapshot.FastModePolicy,
		BillingMode:                           snapshot.BillingMode,
		PreferredSubscriptionID:               clonePointer(snapshot.PreferredSubscriptionID),
		ModelMapping:                          CloneModelMapping(snapshot.ModelMapping),
		IPWhitelist:                           slices.Clone(snapshot.IPWhitelist),
		IPBlacklist:                           slices.Clone(snapshot.IPBlacklist),
		Quota:                                 snapshot.Quota,
		QuotaUsed:                             snapshot.QuotaUsed,
		ExpiresAt:                             clonePointer(snapshot.ExpiresAt),
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
			AllowedGroups:              slices.Clone(snapshot.User.AllowedGroups),
			Email:                      snapshot.User.Email,
			Username:                   snapshot.User.Username,
			BalanceNotifyEnabled:       snapshot.User.BalanceNotifyEnabled,
			BalanceNotifyThresholdType: snapshot.User.BalanceNotifyThresholdType,
			BalanceNotifyThreshold:     clonePointer(snapshot.User.BalanceNotifyThreshold),
			BalanceNotifyExtraEmails:   slices.Clone(snapshot.User.BalanceNotifyExtraEmails),
			TotalRecharged:             snapshot.User.TotalRecharged,
			RPMLimit:                   snapshot.User.RPMLimit,
			UserGroupRPMOverride:       clonePointer(snapshot.User.UserGroupRPMOverride),
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
		apiKey.TeamMembership = cloneMembership(snapshot.TeamMembership)
	}
	if snapshot.Group != nil {
		apiKey.Group = &Group{
			ID:                              snapshot.Group.ID,
			Name:                            snapshot.Group.Name,
			Platform:                        snapshot.Group.Platform,
			SchedulerType:                   snapshot.Group.SchedulerType,
			AdvancedSchedulerOverrides:      accessview.CloneGroupAdvancedSchedulerOverrides(snapshot.Group.AdvancedSchedulerOverrides),
			IsExclusive:                     snapshot.Group.IsExclusive,
			Status:                          snapshot.Group.Status,
			Hydrated:                        true,
			RateMultiplier:                  snapshot.Group.RateMultiplier,
			SessionIsolationEnabled:         snapshot.Group.SessionIsolationEnabled,
			AllowImageGeneration:            snapshot.Group.AllowImageGeneration,
			AllowBatchImageGeneration:       snapshot.Group.AllowBatchImageGeneration,
			WebSearchPricePerCall:           clonePointer(snapshot.Group.WebSearchPricePerCall),
			SearchPricePer1k:                clonePointer(snapshot.Group.SearchPricePer1k),
			AudioRealtimePricePerMin:        clonePointer(snapshot.Group.AudioRealtimePricePerMin),
			AudioTTSPricePerMillionChars:    clonePointer(snapshot.Group.AudioTTSPricePerMillionChars),
			AudioSTTPricePerHour:            clonePointer(snapshot.Group.AudioSTTPricePerHour),
			LongContextPricingEnabled:       snapshot.Group.LongContextPricingEnabled,
			ModelPricing:                    KeyCloneChannelModelPricingEntries(snapshot.Group.ModelPricing),
			ClaudeCodeOnly:                  snapshot.Group.ClaudeCodeOnly,
			FallbackGroupID:                 clonePointer(snapshot.Group.FallbackGroupID),
			FallbackGroupIDOnInvalidRequest: clonePointer(snapshot.Group.FallbackGroupIDOnInvalidRequest),
			UnavailableFallbackGroupID:      clonePointer(snapshot.Group.UnavailableFallbackGroupID),
			ModelRouting:                    cloneModelRouting(snapshot.Group.ModelRouting),
			ModelRoutingEnabled:             snapshot.Group.ModelRoutingEnabled,
			MCPXMLInject:                    snapshot.Group.MCPXMLInject,
			SupportedModelScopes:            slices.Clone(snapshot.Group.SupportedModelScopes),
			AllowedProtocols:                cloneGroupClientProtocols(snapshot.Group.AllowedProtocols),
			ProtocolFallbacks:               maps.Clone(snapshot.Group.ProtocolFallbacks),
			ResponsesImagePolicy:            snapshot.Group.ResponsesImagePolicy,
			AllowLive:                       snapshot.Group.AllowLive,
			ForceOpenAIFast:                 snapshot.Group.ForceOpenAIFast,
			OpenAIFastPolicy:                snapshot.Group.OpenAIFastPolicy,
			FreeOpenAIFast:                  snapshot.Group.FreeOpenAIFast,
			DefaultMappedModel:              snapshot.Group.DefaultMappedModel,
			MessagesDispatchModelConfig:     cloneMessagesDispatch(snapshot.Group.MessagesDispatchModelConfig),
			ModelsListConfig:                cloneModelsList(snapshot.Group.ModelsListConfig),
			RPMLimit:                        snapshot.Group.RPMLimit,
			MaxReasoningEffort:              snapshot.Group.MaxReasoningEffort,
			MaxReasoningEffortOverLimit:     snapshot.Group.MaxReasoningEffortOverLimit,
			ReasoningEffortMappings:         slices.Clone(snapshot.Group.ReasoningEffortMappings),
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
				ID:                   binding.ID,
				APIKeyID:             snapshot.APIKeyID,
				GroupID:              binding.GroupID,
				Prefix:               binding.Prefix,
				NormalizedPrefix:     binding.NormalizedPrefix,
				SortOrder:            binding.SortOrder,
				UserGroupRPMOverride: clonePointer(binding.UserGroupRPMOverride),
				Group:                KeyGroupFromAuthSnapshot(binding.Group),
			})
		}
	}
	s.KeyCompileAPIKeyIPRules(apiKey)
	return apiKey
}

// KeyAuthGroupSnapshotFromGroup 将分组复制到认证缓存，避免复合映射共享可变对象。
func KeyAuthGroupSnapshotFromGroup(group *Group) *APIKeyAuthGroupSnapshot {
	if group == nil {
		return nil
	}
	return &APIKeyAuthGroupSnapshot{
		ID: group.ID, Name: group.Name, Platform: group.Platform, SchedulerType: group.SchedulerType,
		AdvancedSchedulerOverrides: accessview.CloneGroupAdvancedSchedulerOverrides(group.AdvancedSchedulerOverrides), IsExclusive: group.IsExclusive,
		Status: group.Status, RateMultiplier: group.RateMultiplier,
		SessionIsolationEnabled: group.SessionIsolationEnabled, AllowImageGeneration: group.AllowImageGeneration,
		AllowBatchImageGeneration: group.AllowBatchImageGeneration,
		WebSearchPricePerCall:     clonePointer(group.WebSearchPricePerCall),
		SearchPricePer1k:          clonePointer(group.SearchPricePer1k), AudioRealtimePricePerMin: clonePointer(group.AudioRealtimePricePerMin),
		AudioTTSPricePerMillionChars: clonePointer(group.AudioTTSPricePerMillionChars), AudioSTTPricePerHour: clonePointer(group.AudioSTTPricePerHour),
		LongContextPricingEnabled: group.LongContextPricingEnabled, ModelPricing: KeyCloneChannelModelPricingEntries(group.ModelPricing),
		ClaudeCodeOnly:  group.ClaudeCodeOnly,
		FallbackGroupID: clonePointer(group.FallbackGroupID), FallbackGroupIDOnInvalidRequest: clonePointer(group.FallbackGroupIDOnInvalidRequest),
		UnavailableFallbackGroupID: clonePointer(group.UnavailableFallbackGroupID), ModelRouting: cloneModelRouting(group.ModelRouting),
		ModelRoutingEnabled: group.ModelRoutingEnabled, MCPXMLInject: group.MCPXMLInject,
		ProtocolFallbacks: maps.Clone(group.ProtocolFallbacks), ResponsesImagePolicy: group.ResponsesImagePolicy,
		SupportedModelScopes: slices.Clone(group.SupportedModelScopes), AllowedProtocols: cloneGroupClientProtocols(group.AllowedProtocols),
		AllowLive: group.AllowLive, ForceOpenAIFast: group.ForceOpenAIFast, OpenAIFastPolicy: group.OpenAIFastPolicy, FreeOpenAIFast: group.FreeOpenAIFast, DefaultMappedModel: group.DefaultMappedModel,
		MessagesDispatchModelConfig: cloneMessagesDispatch(group.MessagesDispatchModelConfig), ModelsListConfig: cloneModelsList(group.ModelsListConfig),
		RPMLimit: group.RPMLimit, MaxReasoningEffort: group.MaxReasoningEffort,
		MaxReasoningEffortOverLimit: group.MaxReasoningEffortOverLimit,
		ReasoningEffortMappings:     slices.Clone(group.ReasoningEffortMappings), PeakRateEnabled: group.PeakRateEnabled,
		PeakStart: group.PeakStart, PeakEnd: group.PeakEnd, PeakRateMultiplier: group.PeakRateMultiplier,
	}
}

// KeyGroupFromAuthSnapshot 为单次请求还原独立分组对象。
func KeyGroupFromAuthSnapshot(snapshot *APIKeyAuthGroupSnapshot) *Group {
	if snapshot == nil {
		return nil
	}
	return &Group{
		ID: snapshot.ID, Name: snapshot.Name, Platform: snapshot.Platform, SchedulerType: snapshot.SchedulerType,
		AdvancedSchedulerOverrides: accessview.CloneGroupAdvancedSchedulerOverrides(snapshot.AdvancedSchedulerOverrides), IsExclusive: snapshot.IsExclusive,
		Status: snapshot.Status, Hydrated: true, RateMultiplier: snapshot.RateMultiplier,
		SessionIsolationEnabled: snapshot.SessionIsolationEnabled,
		AllowImageGeneration:    snapshot.AllowImageGeneration, AllowBatchImageGeneration: snapshot.AllowBatchImageGeneration,
		WebSearchPricePerCall: clonePointer(snapshot.WebSearchPricePerCall), SearchPricePer1k: clonePointer(snapshot.SearchPricePer1k),
		AudioRealtimePricePerMin:     clonePointer(snapshot.AudioRealtimePricePerMin),
		AudioTTSPricePerMillionChars: clonePointer(snapshot.AudioTTSPricePerMillionChars),
		AudioSTTPricePerHour:         clonePointer(snapshot.AudioSTTPricePerHour),
		LongContextPricingEnabled:    snapshot.LongContextPricingEnabled,
		ModelPricing:                 KeyCloneChannelModelPricingEntries(snapshot.ModelPricing),
		ClaudeCodeOnly:               snapshot.ClaudeCodeOnly, FallbackGroupID: clonePointer(snapshot.FallbackGroupID),
		FallbackGroupIDOnInvalidRequest: clonePointer(snapshot.FallbackGroupIDOnInvalidRequest),
		UnavailableFallbackGroupID:      clonePointer(snapshot.UnavailableFallbackGroupID), ModelRouting: cloneModelRouting(snapshot.ModelRouting),
		ModelRoutingEnabled: snapshot.ModelRoutingEnabled, MCPXMLInject: snapshot.MCPXMLInject,
		ProtocolFallbacks: maps.Clone(snapshot.ProtocolFallbacks), ResponsesImagePolicy: snapshot.ResponsesImagePolicy,
		SupportedModelScopes: slices.Clone(snapshot.SupportedModelScopes), AllowedProtocols: cloneGroupClientProtocols(snapshot.AllowedProtocols),
		AllowLive: snapshot.AllowLive, ForceOpenAIFast: snapshot.ForceOpenAIFast, OpenAIFastPolicy: snapshot.OpenAIFastPolicy, FreeOpenAIFast: snapshot.FreeOpenAIFast, DefaultMappedModel: snapshot.DefaultMappedModel,
		MessagesDispatchModelConfig: cloneMessagesDispatch(snapshot.MessagesDispatchModelConfig), ModelsListConfig: cloneModelsList(snapshot.ModelsListConfig),
		RPMLimit: snapshot.RPMLimit, MaxReasoningEffort: snapshot.MaxReasoningEffort,
		MaxReasoningEffortOverLimit: snapshot.MaxReasoningEffortOverLimit,
		ReasoningEffortMappings:     slices.Clone(snapshot.ReasoningEffortMappings), PeakRateEnabled: snapshot.PeakRateEnabled,
		PeakStart: snapshot.PeakStart, PeakEnd: snapshot.PeakEnd, PeakRateMultiplier: snapshot.PeakRateMultiplier,
	}
}

// KeyCloneChannelModelPricingEntries 复制认证快照中的价卡切片，避免请求对象修改缓存内容。
func KeyCloneChannelModelPricingEntries(entries []ChannelModelPricing) []ChannelModelPricing {
	if entries == nil {
		return nil
	}
	cloned := make([]ChannelModelPricing, len(entries))
	for i := range entries {
		cloned[i] = entries[i].Clone()
	}
	return cloned
}
