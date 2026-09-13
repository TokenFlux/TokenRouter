package scheduler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy" // FlowAccount 是一次选择使用的无凭据投影；关联号仅供外部当前调用的形状转接。
)

type FlowAccount struct {
	Plan                                       *routing.CandidatePlan
	MixedScheduling                            bool
	ProjectionID                               uint64
	ID                                         int64
	Name, Platform, Type                       string
	Concurrency, Priority, LoadFactor, BaseRPM int
	PrivacySet                                 bool
	LastUsedAt, SessionWindowEnd               *time.Time
}

func (a *FlowAccount) IsPrivacySet() bool       { return a.PrivacySet }
func (a *FlowAccount) EffectiveLoadFactor() int { return a.LoadFactor }
func (a *FlowAccount) GetBaseRPM() int          { return a.BaseRPM }

type FlowGroup struct {
	routing.Group
	ProjectionID uint64
}
type AccountWaitPlan struct {
	AccountID      int64
	MaxConcurrency int
	Timeout        time.Duration
	MaxWaiting     int
}
type FlowSelection struct {
	Account                   *FlowAccount
	Acquired                  bool
	ReleaseFunc               func()
	WaitPlan                  *AccountWaitPlan
	AdvancedScheduler         bool
	AdvancedSchedulerFeedback *policy.FeedbackConfig
}
type FlowLoad struct {
	Account  *FlowAccount
	LoadInfo *AccountLoadInfo
}
type FlowOptions struct {
	LoadBatchEnabled, PreferSoonestReset          bool
	FallbackMaxWaiting, StickySessionMaxWaiting   int
	FallbackSelectionMode                         string
	FallbackWaitTimeout, StickySessionWaitTimeout time.Duration
}

// GenericSelectionPorts 仅提供现存平台资格、路由投影和明确的账号读写入口；选择规则归核心。
type GenericSelectionPorts struct {
	ReadGroup                   func(context.Context, int64) (*FlowGroup, error)
	ForcePlatform               func(context.Context) (string, bool)
	ResolveGroupByID            func(context.Context, int64) (*FlowGroup, error)
	ResolveGatewayGroup         func(context.Context, *int64) (*FlowGroup, *int64, error)
	HydrateSelectedAccount      func(context.Context, *FlowAccount) (*FlowAccount, error)
	RoutingAccountIDsForRequest func(context.Context, *int64, string, string) []int64
	GetSchedulableAccount       func(context.Context, int64) (*FlowAccount, error)
	IsAccountInGroup            func(*FlowAccount, *int64) bool
	LogDetailedSelectionFailure func(context.Context, *int64, string, string, string, []FlowAccount, map[int64]struct{}, bool) string

	SelectAccountForModelWithExclusions          func(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}) (*FlowAccount, error)
	AdvancedSchedulerEffectiveSettingsForRequest func(ctx context.Context, groupID *int64) policy.EffectiveSettings
	AdvancedSchedulerStats                       func() *RuntimeStats
	ChannelMappedModelForAccountLayer            func(ctx context.Context, requestedModel string) string
	CheckAndRegisterSession                      func(ctx context.Context, account *FlowAccount, session string) bool
	CheckChannelPricingRestriction               func(ctx context.Context, groupID *int64, requestedModel string) bool
	CheckClaudeCodeRestriction                   func(ctx context.Context, groupID *int64) (*FlowGroup, *int64, error)
	DebugModelRoutingEnabled                     func() bool
	GroupModelUnsupportedErrorIfApplicable       func(ctx context.Context, accounts []FlowAccount, requestedModel string, platform string, excludedIDs map[int64]struct{}, useMixed bool, groupID *int64, schedGroup *FlowGroup) error
	IsAccountAllowedForPlatform                  func(account *FlowAccount, platform string, useMixed bool) bool
	IsAccountSchedulableForModelSelection        func(ctx context.Context, account *FlowAccount, requestedModel string) bool
	IsAccountSchedulableForQuota                 func(account *FlowAccount) bool
	IsAccountSchedulableForRPM                   func(ctx context.Context, account *FlowAccount, sticky bool) bool
	IsAccountSchedulableForSelection             func(account *FlowAccount) bool
	IsAccountSchedulableForWindowCost            func(ctx context.Context, account *FlowAccount, sticky bool) bool
	IsModelSupportedByAccountWithContext         func(ctx context.Context, account *FlowAccount, requestedModel string) bool
	IsUpstreamModelRestrictedByChannel           func(ctx context.Context, groupID int64, account *FlowAccount, requestedModel string) bool
	ListSchedulableAccounts                      func(ctx context.Context, groupID *int64, platform string, hasForcePlatform bool) ([]FlowAccount, bool, error)
	NeedsUpstreamChannelRestrictionCheck         func(ctx context.Context, groupID *int64) bool
	NewSelectionResult                           func(ctx context.Context, account *FlowAccount, acquired bool, release func(), waitPlan *AccountWaitPlan) (*FlowSelection, error)
	ResolvePlatform                              func(ctx context.Context, groupID *int64, group *FlowGroup) (string, bool, error)
	SchedulingConfig                             func() FlowOptions
	ShouldClearStickySessionForAccountLayer      func(ctx context.Context, account *FlowAccount, requestedModel string) bool
	TryAcquireAccountSlot                        func(ctx context.Context, accountID int64, maxConcurrency int) (*AcquireResult, error)
	WithGroupContext                             func(ctx context.Context, group *FlowGroup) context.Context
	WithRPMPrefetch                              func(ctx context.Context, accounts []FlowAccount) context.Context
	WithWindowCostPrefetch                       func(ctx context.Context, accounts []FlowAccount) context.Context
	SetAccountError                              func(context.Context, int64, string) error
	PrefetchedSticky                             func(context.Context, *int64) int64
}

// GenericSelector 无第二份缓存；依赖 app 已构造的唯一计数、粘性和反馈实例。
type GenericSelector struct {
	ports              GenericSelectionPorts
	cache              StickyCache
	concurrencyService *ConcurrencyService
	diagnostics        Diagnostics
	now                func() time.Time
}

func NewGenericSelector(ports GenericSelectionPorts, cache StickyCache, concurrency *ConcurrencyService, diagnostics Diagnostics, now func() time.Time) *GenericSelector {
	return &GenericSelector{ports: ports, cache: cache, concurrencyService: concurrency, diagnostics: diagnostics, now: now}
}

var ErrNoAvailableAccounts = errors.New("no available accounts")

const stickySessionTTL = time.Hour

func derefGroupID(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}
func (s *GenericSelector) Select(ctx context.Context, input SelectionInput) (*FlowSelection, error) {
	groupID, sessionHash, requestedModel, excludedIDs := input.GroupID, input.SessionHash, input.RequestedModel, input.ExcludedIDs

	excludedIDsList := make([]int64, 0, len(excludedIDs))
	for id := range excludedIDs {
		excludedIDsList = append(excludedIDsList, id)
	}
	s.diagnostics.event("debug", "account_scheduling_starting",
		"group_id", derefGroupID(groupID),
		"model", requestedModel,
		"session", shortFlowSessionHash(sessionHash),
		"excluded_ids", excludedIDsList)

	cfg := s.ports.SchedulingConfig()

	group, groupID, err := s.ports.CheckClaudeCodeRestriction(ctx, groupID)
	if err != nil {
		return nil, err
	}
	ctx = s.ports.WithGroupContext(ctx, group)
	usesAdvancedScheduler := group != nil && group.UsesAdvancedScheduler()
	// 高级调度开启粘性加权后，旧硬粘性不能抢在评分前返回；否则 session
	// 粘性不会作为统一候选评分的一部分。关闭加权时保留原有硬粘性语义。
	advancedStickyWeighted := usesAdvancedScheduler && s.ports.AdvancedSchedulerEffectiveSettingsForRequest(ctx, groupID).StickyWeightedEnabled

	if s.ports.CheckChannelPricingRestriction(ctx, groupID, requestedModel) {
		s.diagnostics.event("warn", "channel pricing restriction blocked request",
			"group_id", derefGroupID(groupID),
			"model", requestedModel)
		return nil, fmt.Errorf("%w supporting model: %s (channel pricing restriction)", ErrNoAvailableAccounts, requestedModel)
	}

	var stickyAccountID int64
	var stickySource string
	if prefetch := s.ports.PrefetchedSticky(ctx, groupID); prefetch > 0 {
		stickyAccountID = prefetch
		stickySource = "prefetch"
	} else if sessionHash != "" && s.cache != nil {
		if accountID, err := s.cache.GetSessionAccountID(ctx, derefGroupID(groupID), sessionHash); err == nil {
			stickyAccountID = accountID
			stickySource = "cache"
		}
	}
	stickyEscaped := false
	if usesAdvancedScheduler && !advancedStickyWeighted && stickyAccountID > 0 {
		escapeCfg := s.ports.AdvancedSchedulerEffectiveSettingsForRequest(ctx, groupID).StickyEscape
		if reason, errorRate, ttft, shouldEscape := ShouldEscapeSticky(s.ports.AdvancedSchedulerStats(), stickyAccountID, escapeCfg); shouldEscape {
			stickyEscaped = true
			ctx = WithPreservedSticky(ctx)
			s.diagnostics.event("info", "sticky_escape_triggered",
				"account_id", stickyAccountID,
				"reason", reason,
				"error_rate", errorRate,
				"ttft", ttft,
			)
		}
	}

	s.diagnostics.event("info", "sticky.scheduler_entry",
		"group_id", derefGroupID(groupID),
		"session_hash", shortFlowSessionHash(sessionHash),
		"sticky_account_id", stickyAccountID,
		"sticky_source", stickySource,
		"model", requestedModel,
		"load_batch", cfg.LoadBatchEnabled,
		"has_concurrency_svc", s.concurrencyService != nil,
		"excluded_count", len(excludedIDs),
	)

	if s.ports.DebugModelRoutingEnabled() && requestedModel != "" {
		groupPlatform := ""
		if group != nil {
			groupPlatform = group.Platform
		}
		s.diagnostics.printf("service.gateway", "[ModelRoutingDebug] select entry: group_id=%v group_platform=%s model=%s session=%s sticky_account=%d load_batch=%v concurrency=%v",
			derefGroupID(groupID), groupPlatform, requestedModel, shortFlowSessionHash(sessionHash), stickyAccountID, cfg.LoadBatchEnabled, s.concurrencyService != nil)
	}

	// 基础调度器保留原有无负载批路径。高级分组即使没有负载批服务，
	// 也必须进入统一评分核心，并将缺失负载作为中性信号处理。
	if !usesAdvancedScheduler && (s.concurrencyService == nil || !cfg.LoadBatchEnabled) {

		localExcluded := make(map[int64]struct{})
		for k, v := range excludedIDs {
			localExcluded[k] = v
		}

		for {
			account, err := s.selectRoutes(ctx, groupID, sessionHash, requestedModel, localExcluded)
			if err != nil {
				return nil, err
			}

			result, err := s.ports.TryAcquireAccountSlot(ctx, account.ID, account.Concurrency)
			if err == nil && result.Acquired {

				if !s.ports.CheckAndRegisterSession(ctx, account, sessionHash) {
					result.ReleaseFunc()
					localExcluded[account.ID] = struct{}{}
					continue
				}
				return s.ports.NewSelectionResult(ctx, account, true, result.ReleaseFunc, nil)
			}

			if !s.ports.CheckAndRegisterSession(ctx, account, sessionHash) {
				localExcluded[account.ID] = struct{}{}
				continue
			}

			if stickyAccountID > 0 && stickyAccountID == account.ID && s.concurrencyService != nil {
				waitingCount, _ := s.concurrencyService.GetAccountWaitingCount(ctx, account.ID)
				if waitingCount < cfg.StickySessionMaxWaiting {
					return s.ports.NewSelectionResult(ctx, account, false, nil, &AccountWaitPlan{
						AccountID:      account.ID,
						MaxConcurrency: account.Concurrency,
						Timeout:        cfg.StickySessionWaitTimeout,
						MaxWaiting:     cfg.StickySessionMaxWaiting,
					})
				}
			}
			return s.ports.NewSelectionResult(ctx, account, false, nil, &AccountWaitPlan{
				AccountID:      account.ID,
				MaxConcurrency: account.Concurrency,
				Timeout:        cfg.FallbackWaitTimeout,
				MaxWaiting:     cfg.FallbackMaxWaiting,
			})
		}
	}

	platform, hasForcePlatform, err := s.ports.ResolvePlatform(ctx, groupID, group)
	if err != nil {
		return nil, err
	}
	preferOAuth := platform == capability.PlatformGemini
	if s.ports.DebugModelRoutingEnabled() && platform == capability.PlatformAnthropic && requestedModel != "" {
		s.diagnostics.printf("service.gateway", "[ModelRoutingDebug] load-aware enabled: group_id=%v model=%s session=%s platform=%s", derefGroupID(groupID), requestedModel, shortFlowSessionHash(sessionHash), platform)
	}

	accounts, useMixed, err := s.ports.ListSchedulableAccounts(ctx, groupID, platform, hasForcePlatform)
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, ErrNoAvailableAccounts
	}
	ctx = s.ports.WithWindowCostPrefetch(ctx, accounts)
	ctx = s.ports.WithRPMPrefetch(ctx, accounts)

	accountByID := make(map[int64]*FlowAccount, len(accounts))
	for i := range accounts {
		accountByID[accounts[i].ID] = &accounts[i]
	}
	isExcluded := func(accountID int64) bool {
		if excludedIDs == nil {
			return false
		}
		_, excluded := excludedIDs[accountID]
		return excluded
	}
	// upstream 依据必须逐账号计算最终模型，所有负载感知选择入口共用同一过滤规则。
	needsUpstreamCheck := s.ports.NeedsUpstreamChannelRestrictionCheck(ctx, groupID)
	isUpstreamAllowed := func(account *FlowAccount) bool {
		return !needsUpstreamCheck || !s.ports.IsUpstreamModelRestrictedByChannel(ctx, *groupID, account, requestedModel)
	}

	var routingAccountIDs []int64
	if group != nil && requestedModel != "" && group.Platform == capability.PlatformAnthropic {
		routingModel := s.ports.ChannelMappedModelForAccountLayer(ctx, requestedModel)
		routingAccountIDs = group.GetRoutingAccountIDs(routingModel)
		if s.ports.DebugModelRoutingEnabled() {
			s.diagnostics.printf("service.gateway", "[ModelRoutingDebug] context group routing: group_id=%d model=%s enabled=%v rules=%d matched_ids=%v session=%s sticky_account=%d",
				group.ID, requestedModel, group.ModelRoutingEnabled, len(group.ModelRouting), routingAccountIDs, shortFlowSessionHash(sessionHash), stickyAccountID)
			if len(routingAccountIDs) == 0 && group.ModelRoutingEnabled && len(group.ModelRouting) > 0 {
				keys := make([]string, 0, len(group.ModelRouting))
				for k := range group.ModelRouting {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				const maxKeys = 20
				if len(keys) > maxKeys {
					keys = keys[:maxKeys]
				}
				s.diagnostics.printf("service.gateway", "[ModelRoutingDebug] context group routing miss: group_id=%d model=%s patterns(sample)=%v", group.ID, requestedModel, keys)
			}
		}
	}

	if len(routingAccountIDs) > 0 && (s.concurrencyService != nil || usesAdvancedScheduler) {

		var routingCandidates []*FlowAccount
		var filteredExcluded, filteredMissing, filteredUnsched, filteredPlatform, filteredModelScope, filteredModelMapping, filteredWindowCost int
		var modelScopeSkippedIDs []int64
		for _, routingAccountID := range routingAccountIDs {
			if isExcluded(routingAccountID) {
				filteredExcluded++
				continue
			}
			account, ok := accountByID[routingAccountID]
			if !ok || !s.ports.IsAccountSchedulableForSelection(account) {
				if !ok {
					filteredMissing++
				} else {
					filteredUnsched++
				}
				continue
			}
			if group != nil && group.RequirePrivacySet && !account.IsPrivacySet() {
				_ = s.ports.SetAccountError(ctx, account.ID,
					fmt.Sprintf("Privacy not set, required by group [%s]", group.Name))
				continue
			}
			if !s.ports.IsAccountAllowedForPlatform(account, platform, useMixed) {
				filteredPlatform++
				continue
			}
			if requestedModel != "" && !s.ports.IsModelSupportedByAccountWithContext(ctx, account, requestedModel) {
				filteredModelMapping++
				continue
			}
			if !isUpstreamAllowed(account) {
				continue
			}
			if !s.ports.IsAccountSchedulableForModelSelection(ctx, account, requestedModel) {
				filteredModelScope++
				modelScopeSkippedIDs = append(modelScopeSkippedIDs, account.ID)
				continue
			}

			if !s.ports.IsAccountSchedulableForQuota(account) {
				continue
			}

			weightedSticky := advancedStickyWeighted && account.ID == stickyAccountID
			if !s.ports.IsAccountSchedulableForWindowCost(ctx, account, weightedSticky) {
				filteredWindowCost++
				continue
			}

			if !s.ports.IsAccountSchedulableForRPM(ctx, account, weightedSticky) {
				continue
			}
			routingCandidates = append(routingCandidates, account)
		}

		if s.ports.DebugModelRoutingEnabled() {
			s.diagnostics.printf("service.gateway", "[ModelRoutingDebug] routed candidates: group_id=%v model=%s routed=%d candidates=%d filtered(excluded=%d missing=%d unsched=%d platform=%d model_scope=%d model_mapping=%d window_cost=%d)",
				derefGroupID(groupID), requestedModel, len(routingAccountIDs), len(routingCandidates),
				filteredExcluded, filteredMissing, filteredUnsched, filteredPlatform, filteredModelScope, filteredModelMapping, filteredWindowCost)
			if len(modelScopeSkippedIDs) > 0 {
				s.diagnostics.printf("service.gateway", "[ModelRoutingDebug] model_rate_limited accounts skipped: group_id=%v model=%s account_ids=%v",
					derefGroupID(groupID), requestedModel, modelScopeSkippedIDs)
			}
		}

		if len(routingCandidates) > 0 {
			if !stickyEscaped && (!usesAdvancedScheduler || !advancedStickyWeighted) && sessionHash != "" && stickyAccountID > 0 {
				s.diagnostics.event("debug", "sticky.layer1_5_checking",
					"sticky_account_id", stickyAccountID,
					"in_routing_list", slices.Contains(routingAccountIDs, stickyAccountID),
					"is_excluded", isExcluded(stickyAccountID),
					"in_account_map", func() bool { _, ok := accountByID[stickyAccountID]; return ok }(),
					"session", shortFlowSessionHash(sessionHash),
				)
				if slices.Contains(routingAccountIDs, stickyAccountID) && !isExcluded(stickyAccountID) {

					if stickyAccount, ok := accountByID[stickyAccountID]; ok {
						var stickyCacheMissReason string

						gatePass := s.ports.IsAccountSchedulableForSelection(stickyAccount) &&
							s.ports.IsAccountAllowedForPlatform(stickyAccount, platform, useMixed) &&
							(requestedModel == "" || s.ports.IsModelSupportedByAccountWithContext(ctx, stickyAccount, requestedModel)) &&
							isUpstreamAllowed(stickyAccount) &&
							s.ports.IsAccountSchedulableForModelSelection(ctx, stickyAccount, requestedModel) &&
							s.ports.IsAccountSchedulableForQuota(stickyAccount) &&
							s.ports.IsAccountSchedulableForWindowCost(ctx, stickyAccount, true)

						rpmPass := gatePass && s.ports.IsAccountSchedulableForRPM(ctx, stickyAccount, true)

						if rpmPass {
							result, err := s.ports.TryAcquireAccountSlot(ctx, stickyAccountID, stickyAccount.Concurrency)
							if err == nil && result.Acquired {

								if !s.ports.CheckAndRegisterSession(ctx, stickyAccount, sessionHash) {
									result.ReleaseFunc()
									stickyCacheMissReason = "session_limit"

								} else {
									s.diagnostics.event("debug", "sticky.layer1_5_hit",
										"account_id", stickyAccountID,
										"session", shortFlowSessionHash(sessionHash),
										"result", "slot_acquired",
									)
									if s.ports.DebugModelRoutingEnabled() {
										s.diagnostics.printf("service.gateway", "[ModelRoutingDebug] routed sticky hit: group_id=%v model=%s session=%s account=%d", derefGroupID(groupID), requestedModel, shortFlowSessionHash(sessionHash), stickyAccountID)
									}
									return s.ports.NewSelectionResult(ctx, stickyAccount, true, result.ReleaseFunc, nil)
								}
							}

							if stickyCacheMissReason == "" {
								waitingCount, _ := s.concurrencyService.GetAccountWaitingCount(ctx, stickyAccountID)
								if waitingCount < cfg.StickySessionMaxWaiting {

									if !s.ports.CheckAndRegisterSession(ctx, stickyAccount, sessionHash) {
										stickyCacheMissReason = "session_limit"

									} else {
										// 必须走 newSelectionResult 以 hydrate 账号凭证：
										// 调度快照中的账号是精简版（OAuth token 等被剥离），
										// 直接返回会导致后续转发缺少凭证而鉴权失败。
										return s.ports.NewSelectionResult(ctx, stickyAccount, false, nil, &AccountWaitPlan{
											AccountID:      stickyAccountID,
											MaxConcurrency: stickyAccount.Concurrency,
											Timeout:        cfg.StickySessionWaitTimeout,
											MaxWaiting:     cfg.StickySessionMaxWaiting,
										})
									}
								} else {
									stickyCacheMissReason = "wait_queue_full"
								}
							}

						} else if !gatePass {
							stickyCacheMissReason = "gate_check"
						} else {
							stickyCacheMissReason = "rpm_red"
						}

						if stickyCacheMissReason != "" {
							baseRPM := stickyAccount.GetBaseRPM()
							var currentRPM int
							if count, ok := PrefetchedRPM(ctx, stickyAccount.ID); ok {
								currentRPM = count
							}
							s.diagnostics.printf("service.gateway", "[StickyCacheMiss] reason=%s account_id=%d session=%s current_rpm=%d base_rpm=%d",
								stickyCacheMissReason, stickyAccountID, shortFlowSessionHash(sessionHash), currentRPM, baseRPM)
						}
					} else {
						_ = s.cache.DeleteSessionAccountID(ctx, derefGroupID(groupID), sessionHash)
						s.diagnostics.printf("service.gateway", "[StickyCacheMiss] reason=account_cleared account_id=%d session=%s current_rpm=0 base_rpm=0",
							stickyAccountID, shortFlowSessionHash(sessionHash))
					}
				}
			}

			if usesAdvancedScheduler && (s.concurrencyService == nil || !cfg.LoadBatchEnabled) {
				if selection, ok, selectErr := s.TryAdvancedWithoutLoad(ctx, groupID, sessionHash, routingCandidates); selectErr != nil {
					return nil, selectErr
				} else if ok {
					return selection, nil
				}
				return nil, ErrNoAvailableAccounts
			}

			routingLoads := make([]AccountWithConcurrency, 0, len(routingCandidates))
			for _, acc := range routingCandidates {
				routingLoads = append(routingLoads, AccountWithConcurrency{
					ID:             acc.ID,
					MaxConcurrency: acc.EffectiveLoadFactor(),
				})
			}
			routingLoadMap, _ := s.concurrencyService.GetAccountsLoadBatch(ctx, routingLoads)

			var routingAvailable []FlowLoad
			for _, acc := range routingCandidates {
				loadInfo := routingLoadMap[acc.ID]
				if loadInfo == nil && !usesAdvancedScheduler {
					loadInfo = &AccountLoadInfo{AccountID: acc.ID}
				}
				if usesAdvancedScheduler || loadInfo == nil || loadInfo.LoadRate < 100 {
					routingAvailable = append(routingAvailable, FlowLoad{Account: acc, LoadInfo: loadInfo})
				}
			}

			if len(routingAvailable) > 0 {
				// 模型路由只负责提供硬约束候选；高级分组仍统一交给通用评分核心，
				// 候选耗尽后不能再降级到基础排序。
				if usesAdvancedScheduler {
					if selection, ok, selectErr := s.TryAdvanced(ctx, groupID, sessionHash, routingAvailable); selectErr != nil {
						return nil, selectErr
					} else if ok {
						return selection, nil
					}
					return nil, ErrNoAvailableAccounts
				}

				sort.SliceStable(routingAvailable, func(i, j int) bool {
					a, b := routingAvailable[i], routingAvailable[j]
					if a.Account.Priority != b.Account.Priority {
						return a.Account.Priority < b.Account.Priority
					}
					if a.LoadInfo.LoadRate != b.LoadInfo.LoadRate {
						return a.LoadInfo.LoadRate < b.LoadInfo.LoadRate
					}
					switch {
					case a.Account.LastUsedAt == nil && b.Account.LastUsedAt != nil:
						return true
					case a.Account.LastUsedAt != nil && b.Account.LastUsedAt == nil:
						return false
					case a.Account.LastUsedAt == nil && b.Account.LastUsedAt == nil:
						return false
					default:
						return a.Account.LastUsedAt.Before(*b.Account.LastUsedAt)
					}
				})
				flowShuffleWithinSortGroups(routingAvailable)

				for _, item := range routingAvailable {
					result, err := s.ports.TryAcquireAccountSlot(ctx, item.Account.ID, item.Account.Concurrency)
					if err == nil && result.Acquired {

						if !s.ports.CheckAndRegisterSession(ctx, item.Account, sessionHash) {
							result.ReleaseFunc()
							continue
						}
						if sessionHash != "" && s.cache != nil {
							_ = s.cache.SetSessionAccountID(ctx, derefGroupID(groupID), sessionHash, item.Account.ID, stickySessionTTL)
						}
						if s.ports.DebugModelRoutingEnabled() {
							s.diagnostics.printf("service.gateway", "[ModelRoutingDebug] routed select: group_id=%v model=%s session=%s account=%d", derefGroupID(groupID), requestedModel, shortFlowSessionHash(sessionHash), item.Account.ID)
						}
						return s.ports.NewSelectionResult(ctx, item.Account, true, result.ReleaseFunc, nil)
					}
				}

				for _, item := range routingAvailable {
					if !s.ports.CheckAndRegisterSession(ctx, item.Account, sessionHash) {
						continue
					}
					if s.ports.DebugModelRoutingEnabled() {
						s.diagnostics.printf("service.gateway", "[ModelRoutingDebug] routed wait: group_id=%v model=%s session=%s account=%d", derefGroupID(groupID), requestedModel, shortFlowSessionHash(sessionHash), item.Account.ID)
					}
					return s.ports.NewSelectionResult(ctx, item.Account, false, nil, &AccountWaitPlan{
						AccountID:      item.Account.ID,
						MaxConcurrency: item.Account.Concurrency,
						Timeout:        cfg.StickySessionWaitTimeout,
						MaxWaiting:     cfg.StickySessionMaxWaiting,
					})
				}

			}

			s.diagnostics.printf("service.gateway", "[ModelRouting] All routed accounts unavailable for model=%s, falling back to normal selection", requestedModel)
		}
	}

	if !stickyEscaped && len(routingAccountIDs) == 0 && (!usesAdvancedScheduler || !advancedStickyWeighted) && sessionHash != "" && stickyAccountID > 0 && !isExcluded(stickyAccountID) {
		accountID := stickyAccountID
		if accountID > 0 && !isExcluded(accountID) {
			account, ok := accountByID[accountID]
			if ok {

				clearSticky := s.ports.ShouldClearStickySessionForAccountLayer(ctx, account, requestedModel)
				if clearSticky {
					s.diagnostics.event("debug", "sticky.layer1_5_no_routing_clear",
						"account_id", accountID,
						"reason", "should_clear_sticky_session",
						"session", shortFlowSessionHash(sessionHash),
					)
					_ = s.cache.DeleteSessionAccountID(ctx, derefGroupID(groupID), sessionHash)
				}

				platformOK := s.ports.IsAccountAllowedForPlatform(account, platform, useMixed)
				modelSupported := requestedModel == "" || s.ports.IsModelSupportedByAccountWithContext(ctx, account, requestedModel)
				upstreamAllowed := isUpstreamAllowed(account)
				modelSchedulable := s.ports.IsAccountSchedulableForModelSelection(ctx, account, requestedModel)
				quotaOK := s.ports.IsAccountSchedulableForQuota(account)
				windowCostOK := s.ports.IsAccountSchedulableForWindowCost(ctx, account, true)
				rpmOK := s.ports.IsAccountSchedulableForRPM(ctx, account, true)
				schedulable := s.ports.IsAccountSchedulableForSelection(account)

				s.diagnostics.event("debug", "sticky.layer1_5_no_routing_checks",
					"account_id", accountID,
					"session", shortFlowSessionHash(sessionHash),
					"clear_sticky", clearSticky,
					"schedulable", schedulable,
					"platform_ok", platformOK,
					"model_supported", modelSupported,
					"upstream_allowed", upstreamAllowed,
					"model_schedulable", modelSchedulable,
					"quota_ok", quotaOK,
					"window_cost_ok", windowCostOK,
					"rpm_ok", rpmOK,
				)

				if !clearSticky && platformOK && modelSupported && upstreamAllowed && modelSchedulable && quotaOK && windowCostOK && rpmOK && schedulable {
					result, err := s.ports.TryAcquireAccountSlot(ctx, accountID, account.Concurrency)
					if err == nil && result.Acquired {

						if !s.ports.CheckAndRegisterSession(ctx, account, sessionHash) {
							result.ReleaseFunc()
							s.diagnostics.event("debug", "sticky.layer1_5_no_routing_miss",
								"account_id", accountID,
								"reason", "session_limit",
								"session", shortFlowSessionHash(sessionHash),
							)
						} else {
							s.diagnostics.event("debug", "sticky.layer1_5_no_routing_hit",
								"account_id", accountID,
								"session", shortFlowSessionHash(sessionHash),
								"result", "slot_acquired",
							)
							if s.cache != nil {
								_ = s.cache.RefreshSessionTTL(ctx, derefGroupID(groupID), sessionHash, stickySessionTTL)
							}
							return s.ports.NewSelectionResult(ctx, account, true, result.ReleaseFunc, nil)
						}
					} else {
						s.diagnostics.event("debug", "sticky.layer1_5_no_routing_slot_busy",
							"account_id", accountID,
							"session", shortFlowSessionHash(sessionHash),
						)
					}

					waitingCount, _ := s.concurrencyService.GetAccountWaitingCount(ctx, accountID)
					if waitingCount < cfg.StickySessionMaxWaiting {

						if !s.ports.CheckAndRegisterSession(ctx, account, sessionHash) {

						} else {
							s.diagnostics.event("debug", "sticky.layer1_5_no_routing_hit",
								"account_id", accountID,
								"session", shortFlowSessionHash(sessionHash),
								"result", "wait_plan",
							)
							return s.ports.NewSelectionResult(ctx, account, false, nil, &AccountWaitPlan{
								AccountID:      accountID,
								MaxConcurrency: account.Concurrency,
								Timeout:        cfg.StickySessionWaitTimeout,
								MaxWaiting:     cfg.StickySessionMaxWaiting,
							})
						}
					}
				} else if !clearSticky {
					s.diagnostics.event("debug", "sticky.layer1_5_no_routing_miss",
						"account_id", accountID,
						"reason", "gate_check_failed",
						"session", shortFlowSessionHash(sessionHash),
					)
				}
			} else {
				s.diagnostics.event("debug", "sticky.layer1_5_no_routing_miss",
					"account_id", accountID,
					"reason", "account_not_in_map",
					"session", shortFlowSessionHash(sessionHash),
				)
			}
		}
	} else if len(routingAccountIDs) == 0 && sessionHash != "" {
		s.diagnostics.event("debug", "sticky.layer1_5_no_routing_skip",
			"sticky_account_id", stickyAccountID,
			"is_excluded", func() bool { return stickyAccountID > 0 && isExcluded(stickyAccountID) }(),
			"session", shortFlowSessionHash(sessionHash),
			"reason", func() string {
				if stickyAccountID == 0 {
					return "no_sticky_binding"
				}
				return "sticky_account_excluded"
			}(),
		)
	}

	s.diagnostics.event("debug", "sticky.layer2_fallback",
		"session", shortFlowSessionHash(sessionHash),
		"sticky_account_id", stickyAccountID,
		"reason", "sticky_not_used_falling_back_to_load_balance",
		"total_accounts", len(accounts),
	)
	candidates := make([]*FlowAccount, 0, len(accounts))
	for i := range accounts {
		acc := &accounts[i]
		if isExcluded(acc.ID) {
			continue
		}

		if !s.ports.IsAccountSchedulableForSelection(acc) {
			continue
		}
		if group != nil && group.RequirePrivacySet && !acc.IsPrivacySet() {
			_ = s.ports.SetAccountError(ctx, acc.ID,
				fmt.Sprintf("Privacy not set, required by group [%s]", group.Name))
			continue
		}
		if !s.ports.IsAccountAllowedForPlatform(acc, platform, useMixed) {
			continue
		}
		if requestedModel != "" && !s.ports.IsModelSupportedByAccountWithContext(ctx, acc, requestedModel) {
			continue
		}
		if !isUpstreamAllowed(acc) {
			continue
		}
		if !s.ports.IsAccountSchedulableForModelSelection(ctx, acc, requestedModel) {
			continue
		}

		if !s.ports.IsAccountSchedulableForQuota(acc) {
			continue
		}

		weightedSticky := advancedStickyWeighted && acc.ID == stickyAccountID
		if !s.ports.IsAccountSchedulableForWindowCost(ctx, acc, weightedSticky) {
			continue
		}

		if !s.ports.IsAccountSchedulableForRPM(ctx, acc, weightedSticky) {
			continue
		}
		candidates = append(candidates, acc)
	}

	if len(candidates) == 0 {
		if err := s.ports.GroupModelUnsupportedErrorIfApplicable(ctx, accounts, requestedModel, platform, excludedIDs, useMixed, groupID, group); err != nil {
			return nil, err
		}
		return nil, ErrNoAvailableAccounts
	}

	if usesAdvancedScheduler && (s.concurrencyService == nil || !cfg.LoadBatchEnabled) {
		if selection, ok, selectErr := s.TryAdvancedWithoutLoad(ctx, groupID, sessionHash, candidates); selectErr != nil {
			return nil, selectErr
		} else if ok {
			return selection, nil
		}
		return nil, ErrNoAvailableAccounts
	}

	accountLoads := make([]AccountWithConcurrency, 0, len(candidates))
	for _, acc := range candidates {
		accountLoads = append(accountLoads, AccountWithConcurrency{
			ID:             acc.ID,
			MaxConcurrency: acc.EffectiveLoadFactor(),
		})
	}

	loadMap, err := s.concurrencyService.GetAccountsLoadBatch(ctx, accountLoads)
	if err != nil {
		if group != nil && group.UsesAdvancedScheduler() {
			if result, ok, advancedErr := s.TryAdvancedWithoutLoad(ctx, groupID, sessionHash, candidates); advancedErr != nil {
				return nil, advancedErr
			} else if ok {
				return result, nil
			}
			return nil, ErrNoAvailableAccounts
		}
		if result, ok, legacyErr := s.TryLegacyOrder(ctx, candidates, groupID, sessionHash, preferOAuth); legacyErr != nil {
			return nil, legacyErr
		} else if ok {
			return result, nil
		}
	} else {
		var available []FlowLoad
		for _, acc := range candidates {
			loadInfo := loadMap[acc.ID]
			if loadInfo == nil && !usesAdvancedScheduler {
				loadInfo = &AccountLoadInfo{AccountID: acc.ID}
			}
			if usesAdvancedScheduler || loadInfo == nil || loadInfo.LoadRate < 100 {
				available = append(available, FlowLoad{
					Account:  acc,
					LoadInfo: loadInfo,
				})
			}
		}

		if group != nil && group.UsesAdvancedScheduler() {
			if selection, ok, selectErr := s.TryAdvanced(ctx, groupID, sessionHash, available); selectErr != nil {
				return nil, selectErr
			} else if ok {
				return selection, nil
			}
			// 高级分组已完成自己的可用候选与等待计划选择；不可回退到基础排序，
			// 否则会破坏分组明确选择高级调度器的语义。
			return nil, ErrNoAvailableAccounts
		}

		for len(available) > 0 {

			candidates := flowFilterByMinPriority(available)

			if cfg.PreferSoonestReset {
				candidates = flowFilterBySoonestReset(candidates, s.now)
			}

			candidates = flowFilterByMinLoadRate(candidates)

			selected := flowSelectByLRU(candidates, preferOAuth)
			if selected == nil {
				break
			}

			result, err := s.ports.TryAcquireAccountSlot(ctx, selected.Account.ID, selected.Account.Concurrency)
			if err == nil && result.Acquired {

				if !s.ports.CheckAndRegisterSession(ctx, selected.Account, sessionHash) {
					result.ReleaseFunc()
				} else {
					if sessionHash != "" && s.cache != nil {
						_ = s.cache.SetSessionAccountID(ctx, derefGroupID(groupID), sessionHash, selected.Account.ID, stickySessionTTL)
					}
					return s.ports.NewSelectionResult(ctx, selected.Account, true, result.ReleaseFunc, nil)
				}
			}

			selectedID := selected.Account.ID
			newAvailable := make([]FlowLoad, 0, len(available)-1)
			for _, acc := range available {
				if acc.Account.ID != selectedID {
					newAvailable = append(newAvailable, acc)
				}
			}
			available = newAvailable
		}
	}

	s.SortCandidatesForFallback(candidates, preferOAuth, cfg.FallbackSelectionMode)
	for _, acc := range candidates {

		if !s.ports.CheckAndRegisterSession(ctx, acc, sessionHash) {
			continue
		}
		return s.ports.NewSelectionResult(ctx, acc, false, nil, &AccountWaitPlan{
			AccountID:      acc.ID,
			MaxConcurrency: acc.Concurrency,
			Timeout:        cfg.FallbackWaitTimeout,
			MaxWaiting:     cfg.FallbackMaxWaiting,
		})
	}
	return nil, ErrNoAvailableAccounts
}

func (s *GenericSelector) TryAdvancedWithoutLoad(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	accounts []*FlowAccount,
) (*FlowSelection, bool, error) {
	available := make([]FlowLoad, 0, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		available = append(available, FlowLoad{
			Account: account,
			// 负载批查询失败时不伪造零负载，评分核心会使用中性因子。
			LoadInfo: nil,
		})
	}
	return s.TryAdvanced(ctx, groupID, sessionHash, available)
}

func (s *GenericSelector) TryAdvanced(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	available []FlowLoad,
) (*FlowSelection, bool, error) {
	if s == nil || len(available) == 0 {
		return nil, false, nil
	}
	accounts := make([]*FlowAccount, 0, len(available))
	loadMap := make(map[int64]*AccountLoadInfo, len(available))
	for _, item := range available {
		if item.Account == nil {
			continue
		}
		accounts = append(accounts, item.Account)
		loadMap[item.Account.ID] = item.LoadInfo
	}
	if len(accounts) == 0 {
		return nil, false, nil
	}

	stickyAccountID := int64(0)
	if sessionHash != "" && s.cache != nil {
		stickyAccountID, _ = s.cache.GetSessionAccountID(ctx, derefGroupID(groupID), sessionHash)
	}
	effectiveSettings := s.ports.AdvancedSchedulerEffectiveSettingsForRequest(ctx, groupID)
	candidates, _ := flowScoreCandidates(
		accounts,
		loadMap,
		s.ports.AdvancedSchedulerStats(),
		effectiveSettings.Weights,
		ScoreInput{
			GroupID:         groupID,
			SessionHash:     sessionHash,
			StickyAccountID: stickyAccountID,
			StickyWeighted:  effectiveSettings.StickyWeightedEnabled,
			TopK:            effectiveSettings.TopK,
		},
		s.now(),
	)
	selectionOrder := flowBuildSelectionOrder(candidates, ScoreInput{
		GroupID:         groupID,
		SessionHash:     sessionHash,
		StickyAccountID: stickyAccountID,
		StickyWeighted:  effectiveSettings.StickyWeightedEnabled,
		TopK:            effectiveSettings.TopK,
	})
	for _, candidate := range selectionOrder {
		if candidate.Account == nil {
			continue
		}
		result, err := s.ports.TryAcquireAccountSlot(ctx, candidate.Account.ID, candidate.Account.Concurrency)
		if err != nil || result == nil || !result.Acquired {
			continue
		}
		if !s.ports.CheckAndRegisterSession(ctx, candidate.Account, sessionHash) {
			result.ReleaseFunc()
			continue
		}
		if sessionHash != "" && s.cache != nil && !PreserveStickyFromContext(ctx) {
			_ = s.cache.SetSessionAccountID(ctx, derefGroupID(groupID), sessionHash, candidate.Account.ID, stickySessionTTL)
		}
		selection, selectErr := s.ports.NewSelectionResult(ctx, candidate.Account, true, result.ReleaseFunc, nil)
		if selectErr != nil {
			result.ReleaseFunc()
			return nil, true, selectErr
		}
		selection.AdvancedScheduler = true
		feedback := effectiveSettings.Feedback
		selection.AdvancedSchedulerFeedback = &feedback
		return selection, true, nil
	}
	for _, candidate := range selectionOrder {
		if candidate.Account == nil || !s.ports.CheckAndRegisterSession(ctx, candidate.Account, sessionHash) {
			continue
		}
		selection, selectErr := s.ports.NewSelectionResult(ctx, candidate.Account, false, nil, &AccountWaitPlan{
			AccountID:      candidate.Account.ID,
			MaxConcurrency: candidate.Account.Concurrency,
			Timeout:        s.ports.SchedulingConfig().FallbackWaitTimeout,
			MaxWaiting:     s.ports.SchedulingConfig().FallbackMaxWaiting,
		})
		if selectErr != nil {
			return nil, true, selectErr
		}
		selection.AdvancedScheduler = true
		feedback := effectiveSettings.Feedback
		selection.AdvancedSchedulerFeedback = &feedback
		return selection, true, nil
	}
	return nil, false, nil
}

func (s *GenericSelector) TryLegacyOrder(ctx context.Context, candidates []*FlowAccount, groupID *int64, sessionHash string, preferOAuth bool) (*FlowSelection, bool, error) {
	ordered := append([]*FlowAccount(nil), candidates...)
	flowSortAccountsByPriorityAndLastUsed(ordered, preferOAuth)

	for _, acc := range ordered {
		result, err := s.ports.TryAcquireAccountSlot(ctx, acc.ID, acc.Concurrency)
		if err == nil && result.Acquired {
			// 会话数量限制检查
			if !s.ports.CheckAndRegisterSession(ctx, acc, sessionHash) {
				result.ReleaseFunc() // 释放槽位，继续尝试下一个账号
				continue
			}
			if sessionHash != "" && s.cache != nil {
				_ = s.cache.SetSessionAccountID(ctx, derefGroupID(groupID), sessionHash, acc.ID, stickySessionTTL)
			}
			selection, err := s.ports.NewSelectionResult(ctx, acc, true, result.ReleaseFunc, nil)
			if err != nil {
				return nil, false, err
			}
			return selection, true, nil
		}
	}

	return nil, false, nil
}

func (s *GenericSelector) SortCandidatesForFallback(accounts []*FlowAccount, preferOAuth bool, mode string) {
	if mode == "random" {
		// 先按优先级排序，然后在同优先级内随机打乱
		flowSortAccountsByPriorityOnly(accounts, preferOAuth)
		flowShuffleWithinPriority(accounts, s.now)
	} else {
		// 默认按最后使用时间排序
		flowSortAccountsByPriorityAndLastUsed(accounts, preferOAuth)
	}
}
func shortFlowSessionHash(sessionHash string) string {
	if sessionHash == "" {
		return ""
	}
	if len(sessionHash) <= 8 {
		return sessionHash
	}
	return sessionHash[:8]
}

// IsMixedSchedulingEnabled 读取本次候选的纯账号资格投影。
func (a *FlowAccount) IsMixedSchedulingEnabled() bool { return a.MixedScheduling }

// SelectOnly 供辅助入口选择账号，不创建请求槽或会话注册；原路由和资格查询顺序不变。
func (s *GenericSelector) SelectOnly(ctx context.Context, input SelectionInput) (*FlowAccount, error) {
	return s.selectRoutes(WithSelectOnly(ctx), input.GroupID, input.SessionHash, input.RequestedModel, input.ExcludedIDs)
}
