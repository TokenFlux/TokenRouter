// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	"context"
	"errors"
	"fmt"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	wireprotocol "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

const GroupSortOrderStep = 10

// Group management implementations
func (s *GroupAdmin) ListGroups(ctx context.Context, page, pageSize int, platform, status, search string, isExclusive *bool, sortBy, sortOrder string) ([]Group, int64, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize, SortBy: sortBy, SortOrder: sortOrder}
	groups, result, err := s.groupRepo.ListWithFilters(ctx, params, platform, status, search, isExclusive)
	if err != nil {
		return nil, 0, err
	}
	return groups, result.Total, nil
}

func (s *GroupAdmin) GetAllGroups(ctx context.Context) ([]Group, error) {
	return s.groupRepo.ListActive(ctx)
}

func (s *GroupAdmin) GetAllGroupsByPlatform(ctx context.Context, platform string) ([]Group, error) {
	return s.groupRepo.ListActiveByPlatform(ctx, platform)
}

func (s *GroupAdmin) GetAllGroupsIncludingInactive(ctx context.Context) ([]Group, error) {
	// ListWithFilters 的空 status 表示不按状态过滤，因此会返回启用和禁用分组。
	// PageSize 10000 有意放宽；实际分组数量通常只是几十个。
	groups, _, err := s.groupRepo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 10000}, "", "", "", nil)
	return groups, err
}

func (s *GroupAdmin) GetGroup(ctx context.Context, id int64) (*Group, error) {
	return s.groupRepo.GetByID(ctx, id)
}

func (s *GroupAdmin) GetGroupModelsListCandidates(ctx context.Context, id int64, platform string) ([]string, error) {
	platform = strings.TrimSpace(platform)
	var existingGroup *Group
	if id > 0 {
		group, err := s.groupRepo.GetByIDLite(ctx, id)
		if err != nil {
			return nil, err
		}
		existingGroup = group
		if platform == "" {
			platform = group.Platform
		}
	}
	if platform == "" {
		platform = PlatformAnthropic
	}

	if id <= 0 || s.accountRepo == nil {
		return s.options.DefaultModels(platform), nil
	}

	accounts, err := s.accountRepo.ListSchedulableByGroupID(ctx, id)
	if err != nil {
		return nil, err
	}

	candidates := ConfiguredModelsListCandidateIDs(accounts, platform)
	if existingGroup != nil && existingGroup.Platform == platform && existingGroup.CustomModelsListEnabled() {
		return FilterModelsListCandidates(candidates, existingGroup.ModelsListConfig.Models), nil
	}
	if len(candidates) > 0 {
		return candidates, nil
	}
	return s.options.DefaultModels(platform), nil
}

func DefaultAllowImageGenerationForPlatform(platform string) bool {
	// Grok 图片和视频生成路由共用历史图片生成开关；旧客户端不会显式传 true。
	return platform == PlatformGrok
}

// GroupSupportsOpenAIFast 判断分组是否允许配置 OpenAI Fast 强制策略。
func GroupSupportsOpenAIFast(platform string) bool {
	return platform == PlatformOpenAI
}

// SanitizeGroupOpenAIFast 清除不支持平台上的组级 Fast 开关，避免无效配置持久化。
func SanitizeGroupOpenAIFast(group *Group) {
	if group == nil {
		return
	}
	group.OpenAIFastPolicy = group.EffectiveOpenAIFastPolicy()
	group.ForceOpenAIFast = group.OpenAIFastPolicy == GroupOpenAIFastPolicyForcePriority
	if !GroupSupportsOpenAIFast(group.Platform) {
		group.OpenAIFastPolicy = GroupOpenAIFastPolicyFollowRequest
		group.ForceOpenAIFast = false
		group.FreeOpenAIFast = false
	}
}

func (s *GroupAdmin) CreateGroup(ctx context.Context, input *CreateGroupInput) (*Group, error) {
	if err := ValidateGroupRoutingPolicy(input.RoutingPolicy); err != nil {
		return nil, err
	}
	fastPolicy, policyErr := ResolveGroupOpenAIFastPolicyInput(input.OpenAIFastPolicy, input.ForceOpenAIFast)
	if policyErr != nil {
		return nil, policyErr
	}
	if input.RateMultiplier <= 0 {
		return nil, errors.New("rate_multiplier must be > 0")
	}

	platform := input.Platform
	if platform == "" {
		platform = PlatformAnthropic
	}
	schedulerType, err := NormalizeGroupSchedulerType(input.SchedulerType)
	if err != nil {
		return nil, infraerrors.Newf(infraerrors.Category(400), "INVALID_SCHEDULER_TYPE", "%v", err)
	}
	if err := s.ValidateAdvancedOverrides(ctx, input.AdvancedSchedulerOverrides); err != nil {
		return nil, infraerrors.Newf(infraerrors.Category(400), "INVALID_ADVANCED_SCHEDULER_OVERRIDES", "%v", err)
	}
	allowedClientProtocols := input.AllowedProtocols
	if allowedClientProtocols == nil {
		allowedClientProtocols = capability.DefaultGroupClientProtocols(platform)
		if platform == PlatformOpenAI {
			allowedClientProtocols = capability.SetGroupClientProtocol(allowedClientProtocols, wireprotocol.ProtocolAnthropicMessages, input.AllowMessagesDispatch)
		}
	}

	modelPricing, err := s.options.Pricing.NormalizeGroupPricing(platform, input.ModelPricing)
	if err != nil {
		return nil, err
	}
	longContextPricingEnabled := true
	if input.LongContextPricingEnabled != nil {
		longContextPricingEnabled = *input.LongContextPricingEnabled
	}
	maxReasoningEffort, err := NormalizeMaxReasoningEffortForPlatform(platform, input.MaxReasoningEffort)
	if err != nil {
		return nil, infraerrors.Newf(infraerrors.Category(400), "INVALID_MAX_REASONING_EFFORT", "%v", err)
	}
	maxReasoningEffortOverLimit, err := NormalizeMaxReasoningEffortOverLimitForPlatform(platform, input.MaxReasoningEffortOverLimit)
	if err != nil {
		return nil, infraerrors.Newf(infraerrors.Category(400), "INVALID_MAX_REASONING_EFFORT_OVER_LIMIT", "%v", err)
	}
	reasoningEffortMappings, err := NormalizeReasoningEffortMappings(platform, input.ReasoningEffortMappings)
	if err != nil {
		return nil, infraerrors.Newf(infraerrors.Category(400), "INVALID_REASONING_EFFORT_MAPPING", "%v", err)
	}

	// 工具与语音价格：负数表示清除，0 保留（表示免费）
	webSearchPricePerCall := NormalizePrice(input.WebSearchPricePerCall)
	searchPricePer1k := NormalizePrice(input.SearchPricePer1k)
	audioRealtimePricePerMin := NormalizePrice(input.AudioRealtimePricePerMin)
	audioTTSPricePerMillionChars := NormalizePrice(input.AudioTTSPricePerMillionChars)
	audioSTTPricePerHour := NormalizePrice(input.AudioSTTPricePerHour)
	batchImageDiscountMultiplier := 0.5
	if input.BatchImageDiscountMultiplier != nil {
		if *input.BatchImageDiscountMultiplier < 0 {
			return nil, errors.New("batch_image_discount_multiplier must be >= 0")
		}
		batchImageDiscountMultiplier = *input.BatchImageDiscountMultiplier
	}
	batchImageHoldMultiplier := 0.6
	if input.BatchImageHoldMultiplier != nil {
		if *input.BatchImageHoldMultiplier < 0 {
			return nil, errors.New("batch_image_hold_multiplier must be >= 0")
		}
		batchImageHoldMultiplier = *input.BatchImageHoldMultiplier
	}
	// 不变式：hold 比例 >= discount 比例。否则批量任务成功率足够高时
	// 实际成本会超过冻结额，结算永远失败、用户冻结余额无法解冻。
	if batchImageHoldMultiplier < batchImageDiscountMultiplier {
		return nil, errors.New("batch_image_hold_multiplier must be >= batch_image_discount_multiplier")
	}

	peakRateMultiplier := 1.0
	if input.PeakRateMultiplier != nil {
		peakRateMultiplier = *input.PeakRateMultiplier
	}
	// 高峰配置先归一化再校验，确保创建和更新写路径行为一致。
	peakRateEnabled, peakStart, peakEnd, peakRateMultiplier := NormalizePeakRateConfig(input.PeakRateEnabled, input.PeakStart, input.PeakEnd, peakRateMultiplier)
	if err := ValidatePeakRateConfig(peakRateEnabled, peakStart, peakEnd, peakRateMultiplier); err != nil {
		return nil, err
	}

	// 校验降级分组
	if input.FallbackGroupID != nil {
		if err := s.ValidateFallbackGroup(ctx, 0, *input.FallbackGroupID); err != nil {
			return nil, err
		}
	}
	unavailableFallbackGroupID := input.UnavailableFallbackGroupID
	if unavailableFallbackGroupID != nil && *unavailableFallbackGroupID <= 0 {
		unavailableFallbackGroupID = nil
	}
	if unavailableFallbackGroupID != nil {
		if err := s.ValidateUnavailableFallbackGroup(ctx, 0, platform, *unavailableFallbackGroupID); err != nil {
			return nil, err
		}
	}
	fallbackOnInvalidRequest := input.FallbackGroupIDOnInvalidRequest
	if fallbackOnInvalidRequest != nil && *fallbackOnInvalidRequest <= 0 {
		fallbackOnInvalidRequest = nil
	}
	// 校验无效请求兜底分组
	if fallbackOnInvalidRequest != nil {
		if err := s.ValidateFallbackGroupOnInvalidRequest(ctx, 0, platform, *fallbackOnInvalidRequest); err != nil {
			return nil, err
		}
	}

	// MCPXMLInject：默认为 true，仅当显式传入 false 时关闭
	mcpXMLInject := true
	if input.MCPXMLInject != nil {
		mcpXMLInject = *input.MCPXMLInject
	}

	allowImageGeneration := input.AllowImageGeneration || DefaultAllowImageGenerationForPlatform(platform)
	allowBatchImageGeneration := input.AllowBatchImageGeneration && allowImageGeneration && platform == PlatformGemini

	// 如果指定了复制账号的源分组，先获取账号 ID 列表
	var accountIDsToCopy []int64
	if len(input.CopyAccountsFromGroupIDs) > 0 {
		// 去重源分组 IDs
		seen := make(map[int64]struct{})
		uniqueSourceGroupIDs := make([]int64, 0, len(input.CopyAccountsFromGroupIDs))
		for _, srcGroupID := range input.CopyAccountsFromGroupIDs {
			if _, exists := seen[srcGroupID]; !exists {
				seen[srcGroupID] = struct{}{}
				uniqueSourceGroupIDs = append(uniqueSourceGroupIDs, srcGroupID)
			}
		}

		// 校验源分组的平台是否与新分组一致
		for _, srcGroupID := range uniqueSourceGroupIDs {
			srcGroup, err := s.groupRepo.GetByIDLite(ctx, srcGroupID)
			if err != nil {
				return nil, fmt.Errorf("source group %d not found: %w", srcGroupID, err)
			}
			if srcGroup.Platform != platform {
				return nil, fmt.Errorf("source group %d platform mismatch: expected %s, got %s", srcGroupID, platform, srcGroup.Platform)
			}
		}

		// 获取所有源分组的账号（去重）
		var err error
		accountIDsToCopy, err = s.groupRepo.GetAccountIDsByGroupIDs(ctx, uniqueSourceGroupIDs)
		if err != nil {
			return nil, fmt.Errorf("failed to get accounts from source groups: %w", err)
		}
	}
	availabilityProbeConfig, err := NormalizeGroupAvailabilityProbeConfigForAdminWrite(input.AvailabilityProbeConfig)
	if err != nil {
		return nil, err
	}

	sortOrder := 0
	if input.SortOrder != nil {
		sortOrder = *input.SortOrder
	}
	group := &Group{
		Name:                            input.Name,
		Description:                     input.Description,
		Platform:                        platform,
		SchedulerType:                   schedulerType,
		AdvancedSchedulerOverrides:      policy.CloneGroupAdvancedSchedulerOverrides(input.AdvancedSchedulerOverrides),
		DisplayBrand:                    strings.TrimSpace(input.DisplayBrand),
		SortOrder:                       sortOrder,
		RateMultiplier:                  input.RateMultiplier,
		IsExclusive:                     input.IsExclusive,
		IsDefault:                       input.IsDefault,
		SessionIsolationEnabled:         input.SessionIsolationEnabled,
		Status:                          StatusActive,
		LongContextPricingEnabled:       longContextPricingEnabled,
		ModelPricing:                    modelPricing,
		RoutingPolicy:                   input.RoutingPolicy.Clone(),
		AllowImageGeneration:            allowImageGeneration,
		AllowBatchImageGeneration:       allowBatchImageGeneration,
		BatchImageDiscountMultiplier:    batchImageDiscountMultiplier,
		BatchImageHoldMultiplier:        batchImageHoldMultiplier,
		PeakRateEnabled:                 peakRateEnabled,
		PeakStart:                       peakStart,
		PeakEnd:                         peakEnd,
		PeakRateMultiplier:              peakRateMultiplier,
		WebSearchPricePerCall:           webSearchPricePerCall,
		SearchPricePer1k:                searchPricePer1k,
		AudioRealtimePricePerMin:        audioRealtimePricePerMin,
		AudioTTSPricePerMillionChars:    audioTTSPricePerMillionChars,
		AudioSTTPricePerHour:            audioSTTPricePerHour,
		ClaudeCodeOnly:                  input.ClaudeCodeOnly,
		FallbackGroupID:                 input.FallbackGroupID,
		FallbackGroupIDOnInvalidRequest: fallbackOnInvalidRequest,
		UnavailableFallbackGroupID:      unavailableFallbackGroupID,
		ModelRouting:                    input.ModelRouting,
		MCPXMLInject:                    mcpXMLInject,
		SupportedModelScopes:            input.SupportedModelScopes,
		AllowedProtocols:                allowedClientProtocols,
		ProtocolFallbacks:               input.ProtocolFallbacks,
		ResponsesImagePolicy:            input.ResponsesImagePolicy,
		AllowLive:                       input.AllowLive,
		ForceOpenAIFast:                 input.ForceOpenAIFast,
		OpenAIFastPolicy:                fastPolicy,
		FreeOpenAIFast:                  input.FreeOpenAIFast,
		RequireOAuthOnly:                input.RequireOAuthOnly,
		RequirePrivacySet:               input.RequirePrivacySet,
		DefaultMappedModel:              input.DefaultMappedModel,
		MessagesDispatchModelConfig:     NormalizeMessagesDispatchConfig(input.MessagesDispatchModelConfig, s.options.NormalizeMappedModel),
		ModelsListConfig:                NormalizeGroupModelsListConfig(input.ModelsListConfig),
		AvailabilityProbeConfig:         availabilityProbeConfig,
		RPMLimit:                        input.RPMLimit,
		MaxReasoningEffort:              maxReasoningEffort,
		MaxReasoningEffortOverLimit:     maxReasoningEffortOverLimit,
		ReasoningEffortMappings:         reasoningEffortMappings,
	}
	SanitizeGroupMessagesDispatchFields(group)
	SanitizeGroupOpenAIFast(group)
	if group.Platform != PlatformOpenAI {
		group.AllowLive = false
	}
	SanitizeGroupReasoningEffortPolicy(group)
	NormalizeGroupDefaultState(group)
	if group.ProtocolFallbacks == nil {
		group.ProtocolFallbacks = capability.DefaultProtocolFallbacks(platform)
	}
	var legacy *LegacyGroupProtocolPatch
	if input.AllowedProtocols == nil || input.LegacyProtocolInput {
		legacy = &LegacyGroupProtocolPatch{Image: &group.AllowImageGeneration, Batch: &group.AllowBatchImageGeneration, Live: &group.AllowLive}
	}
	if err := NormalizeGroupProtocolPolicy(group, legacy); err != nil {
		return nil, err
	}

	// require_oauth_only: 过滤掉 apikey 类型账号
	if group.RequireOAuthOnly && (group.Platform == PlatformOpenAI || group.Platform == PlatformAntigravity || group.Platform == PlatformAnthropic || group.Platform == PlatformGemini || group.Platform == PlatformGrok) && len(accountIDsToCopy) > 0 {
		accounts, err := s.accountRepo.GetByIDs(ctx, accountIDsToCopy)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch accounts for oauth filter: %w", err)
		}
		oauthIDs := make(map[int64]struct{}, len(accounts))
		for _, acc := range accounts {
			if acc.Type != capability.AccountTypeAPIKey {
				oauthIDs[acc.ID] = struct{}{}
			}
		}
		var filtered []int64
		for _, aid := range accountIDsToCopy {
			if _, ok := oauthIDs[aid]; ok {
				filtered = append(filtered, aid)
			}
		}
		accountIDsToCopy = filtered
	}

	if err := s.options.Mutate(ctx, func(opCtx context.Context) error {
		if input.SortOrder == nil {
			resolvedSortOrder, err := s.NextGroupSortOrder(opCtx)
			if err != nil {
				return err
			}
			group.SortOrder = resolvedSortOrder
		}
		if err := s.ClearOtherPlatformDefaultGroups(opCtx, group.Platform, 0, group.IsDefault); err != nil {
			return err
		}
		if err := s.groupRepo.Create(opCtx, group); err != nil {
			return TranslateGroupDefaultConflict(err)
		}
		// 账号复制与默认组切换放在同一事务中，避免出现部分提交。
		if len(accountIDsToCopy) > 0 {
			if err := s.groupRepo.BindAccountsToGroup(opCtx, group.ID, accountIDsToCopy); err != nil {
				return fmt.Errorf("failed to bind accounts to new group: %w", err)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if len(accountIDsToCopy) > 0 {
		group.AccountCount = int64(len(accountIDsToCopy))
	}

	return group, nil
}

// NextGroupSortOrder 在创建事务内分配末尾排序值，避免并发新建产生重复位置。
func (s *GroupAdmin) NextGroupSortOrder(ctx context.Context) (int, error) {
	if s.groupSortOrderRepo != nil {
		if err := s.groupSortOrderRepo.LockGroupSortOrder(ctx); err != nil {
			return 0, fmt.Errorf("lock group sort order: %w", err)
		}
	}

	groups, _, err := s.groupRepo.ListWithFilters(
		ctx,
		pagination.PaginationParams{
			Page:      1,
			PageSize:  1,
			SortBy:    "sort_order",
			SortOrder: "desc",
		},
		"",
		"",
		"",
		nil,
	)
	if err != nil {
		return 0, fmt.Errorf("load last group sort order: %w", err)
	}
	if len(groups) == 0 {
		return 0, nil
	}

	next := groups[0].SortOrder + GroupSortOrderStep
	if next < groups[0].SortOrder {
		return 0, errors.New("group sort order overflow")
	}
	return next, nil
}

// NormalizePrice 将负数转换为 nil（表示使用默认价格），0 保留（表示免费）
func NormalizePrice(price *float64) *float64 {
	if price == nil || *price < 0 {
		return nil
	}
	return price
}

// ValidateFallbackGroup 校验降级分组的有效性
// currentGroupID: 当前分组 ID（新建时为 0）
// fallbackGroupID: 降级分组 ID
func (s *GroupAdmin) ValidateFallbackGroup(ctx context.Context, currentGroupID, fallbackGroupID int64) error {
	// 不能将自己设置为降级分组
	if currentGroupID > 0 && currentGroupID == fallbackGroupID {
		return fmt.Errorf("cannot set self as fallback group")
	}

	visited := map[int64]struct{}{}
	nextID := fallbackGroupID
	for {
		if _, seen := visited[nextID]; seen {
			return fmt.Errorf("fallback group cycle detected")
		}
		visited[nextID] = struct{}{}
		if currentGroupID > 0 && nextID == currentGroupID {
			return fmt.Errorf("fallback group cycle detected")
		}

		// 检查降级分组是否存在
		fallbackGroup, err := s.groupRepo.GetByIDLite(ctx, nextID)
		if err != nil {
			return fmt.Errorf("fallback group not found: %w", err)
		}

		// 降级分组不能启用 claude_code_only，否则会造成死循环
		if nextID == fallbackGroupID && fallbackGroup.ClaudeCodeOnly {
			return fmt.Errorf("fallback group cannot have claude_code_only enabled")
		}

		if fallbackGroup.FallbackGroupID == nil {
			return nil
		}
		nextID = *fallbackGroup.FallbackGroupID
	}
}

// ValidateFallbackGroupOnInvalidRequest 校验无效请求兜底分组的有效性。
// currentGroupID: 当前分组 ID（新建时为 0）
// platform: 当前分组的平台
// fallbackGroupID: 兜底分组 ID
func (s *GroupAdmin) ValidateFallbackGroupOnInvalidRequest(ctx context.Context, currentGroupID int64, platform string, fallbackGroupID int64) error {
	if platform != PlatformAnthropic && platform != PlatformAntigravity {
		return fmt.Errorf("invalid request fallback only supported for anthropic or antigravity groups")
	}
	if currentGroupID > 0 && currentGroupID == fallbackGroupID {
		return fmt.Errorf("cannot set self as invalid request fallback group")
	}

	fallbackGroup, err := s.groupRepo.GetByIDLite(ctx, fallbackGroupID)
	if err != nil {
		return fmt.Errorf("fallback group not found: %w", err)
	}
	if fallbackGroup.Platform != PlatformAnthropic {
		return fmt.Errorf("fallback group must be anthropic platform")
	}
	if fallbackGroup.FallbackGroupIDOnInvalidRequest != nil {
		return fmt.Errorf("fallback group cannot have invalid request fallback configured")
	}
	return nil
}

func (s *GroupAdmin) UpdateGroup(ctx context.Context, id int64, input *UpdateGroupInput) (*Group, error) {
	group, err := s.groupRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	previousPlatform := group.Platform
	previousAllowedProtocols := group.EffectiveAllowedProtocols()

	if input.Name != "" {
		group.Name = input.Name
	}
	if input.Description != nil {
		group.Description = *input.Description
	}
	if input.Platform != "" {
		group.Platform = input.Platform
	}
	if input.SchedulerType != nil {
		schedulerType, normalizeErr := NormalizeGroupSchedulerType(*input.SchedulerType)
		if normalizeErr != nil {
			return nil, infraerrors.Newf(infraerrors.Category(400), "INVALID_SCHEDULER_TYPE", "%v", normalizeErr)
		}
		group.SchedulerType = schedulerType
	}
	if input.AdvancedSchedulerOverrides != nil {
		if validationErr := s.ValidateAdvancedOverrides(ctx, *input.AdvancedSchedulerOverrides); validationErr != nil {
			return nil, infraerrors.Newf(infraerrors.Category(400), "INVALID_ADVANCED_SCHEDULER_OVERRIDES", "%v", validationErr)
		}
		group.AdvancedSchedulerOverrides = policy.CloneGroupAdvancedSchedulerOverrides(*input.AdvancedSchedulerOverrides)
	}
	if input.AllowedProtocols != nil {
		group.AllowedProtocols = append([]wireprotocol.ProtocolID{}, *input.AllowedProtocols...)
	} else {
		// 字段缺省时保留原集合；切换平台只移除新平台不支持的协议。
		group.AllowedProtocols = previousAllowedProtocols
		if input.Platform != "" && group.Platform != previousPlatform {
			group.AllowedProtocols = FilterGroupClientProtocolsForPlatform(group.Platform, group.AllowedProtocols)
		}
	}
	if input.DisplayBrand != nil {
		group.DisplayBrand = strings.TrimSpace(*input.DisplayBrand)
	}
	if input.SortOrder != nil {
		group.SortOrder = *input.SortOrder
	}
	if input.RateMultiplier != nil {
		if *input.RateMultiplier <= 0 {
			return nil, errors.New("rate_multiplier must be > 0")
		}
		group.RateMultiplier = *input.RateMultiplier
	}
	if input.IsExclusive != nil {
		group.IsExclusive = *input.IsExclusive
	}
	if input.IsDefault != nil {
		group.IsDefault = *input.IsDefault
	}
	if input.SessionIsolationEnabled != nil {
		group.SessionIsolationEnabled = *input.SessionIsolationEnabled
	}
	if input.Status != "" {
		group.Status = input.Status
	}
	if input.LongContextPricingEnabled != nil {
		group.LongContextPricingEnabled = *input.LongContextPricingEnabled
	}
	if input.RoutingPolicy != nil {
		if err := ValidateGroupRoutingPolicy(*input.RoutingPolicy); err != nil {
			return nil, err
		}
		group.RoutingPolicy = input.RoutingPolicy.Clone()
	}
	if input.ModelPricing != nil {
		modelPricing, normalizeErr := s.options.Pricing.NormalizeGroupPricing(group.Platform, *input.ModelPricing)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		group.ModelPricing = modelPricing
	}

	// 图片能力和批量图片策略独立于模型价卡。
	if input.AllowImageGeneration != nil {
		group.AllowImageGeneration = *input.AllowImageGeneration
	}
	if input.AllowBatchImageGeneration != nil {
		group.AllowBatchImageGeneration = *input.AllowBatchImageGeneration
	}
	if !group.AllowImageGeneration || group.Platform != PlatformGemini {
		group.AllowBatchImageGeneration = false
	}
	if input.BatchImageDiscountMultiplier != nil {
		if *input.BatchImageDiscountMultiplier < 0 {
			return nil, errors.New("batch_image_discount_multiplier must be >= 0")
		}
		group.BatchImageDiscountMultiplier = *input.BatchImageDiscountMultiplier
	}
	if input.BatchImageHoldMultiplier != nil {
		if *input.BatchImageHoldMultiplier < 0 {
			return nil, errors.New("batch_image_hold_multiplier must be >= 0")
		}
		group.BatchImageHoldMultiplier = *input.BatchImageHoldMultiplier
	}
	// 仅在本次更新显式触碰任一比例时校验合并后的不变式（hold >= discount），
	// 避免存量脏数据阻塞其他字段的正常更新（提交侧另有钳制兜底）。
	if (input.BatchImageDiscountMultiplier != nil || input.BatchImageHoldMultiplier != nil) &&
		group.BatchImageHoldMultiplier < group.BatchImageDiscountMultiplier {
		return nil, errors.New("batch_image_hold_multiplier must be >= batch_image_discount_multiplier")
	}
	if input.PeakRateEnabled != nil {
		group.PeakRateEnabled = *input.PeakRateEnabled
	}
	if input.PeakStart != nil {
		group.PeakStart = *input.PeakStart
	}
	if input.PeakEnd != nil {
		group.PeakEnd = *input.PeakEnd
	}
	if input.PeakRateMultiplier != nil {
		group.PeakRateMultiplier = *input.PeakRateMultiplier
	}
	group.PeakRateEnabled, group.PeakStart, group.PeakEnd, group.PeakRateMultiplier = NormalizePeakRateConfig(group.PeakRateEnabled, group.PeakStart, group.PeakEnd, group.PeakRateMultiplier)
	// 收敛校验：Update 可能只传部分 peak 字段，需对合并后的最终配置统一校验，
	// 防止单独修改 start/end 导致最终 start>=end 等非法配置入库。与 CreateGroup 同一收口。
	if err := ValidatePeakRateConfig(group.PeakRateEnabled, group.PeakStart, group.PeakEnd, group.PeakRateMultiplier); err != nil {
		return nil, err
	}
	if input.WebSearchPricePerCall != nil {
		group.WebSearchPricePerCall = NormalizePrice(input.WebSearchPricePerCall)
	}
	if input.SearchPricePer1k != nil {
		group.SearchPricePer1k = NormalizePrice(input.SearchPricePer1k)
	}
	if input.AudioRealtimePricePerMin != nil {
		group.AudioRealtimePricePerMin = NormalizePrice(input.AudioRealtimePricePerMin)
	}
	if input.AudioTTSPricePerMillionChars != nil {
		group.AudioTTSPricePerMillionChars = NormalizePrice(input.AudioTTSPricePerMillionChars)
	}
	if input.AudioSTTPricePerHour != nil {
		group.AudioSTTPricePerHour = NormalizePrice(input.AudioSTTPricePerHour)
	}

	// Claude Code 客户端限制
	if input.ClaudeCodeOnly != nil {
		group.ClaudeCodeOnly = *input.ClaudeCodeOnly
	}
	if input.FallbackGroupID != nil {
		// 校验降级分组
		if *input.FallbackGroupID > 0 {
			if err := s.ValidateFallbackGroup(ctx, id, *input.FallbackGroupID); err != nil {
				return nil, err
			}
			group.FallbackGroupID = input.FallbackGroupID
		} else {
			// 传入 0 或负数表示清除降级分组
			group.FallbackGroupID = nil
		}
	}
	fallbackOnInvalidRequest := group.FallbackGroupIDOnInvalidRequest
	if input.FallbackGroupIDOnInvalidRequest != nil {
		if *input.FallbackGroupIDOnInvalidRequest > 0 {
			fallbackOnInvalidRequest = input.FallbackGroupIDOnInvalidRequest
		} else {
			fallbackOnInvalidRequest = nil
		}
	}
	if fallbackOnInvalidRequest != nil {
		if err := s.ValidateFallbackGroupOnInvalidRequest(ctx, id, group.Platform, *fallbackOnInvalidRequest); err != nil {
			return nil, err
		}
	}
	group.FallbackGroupIDOnInvalidRequest = fallbackOnInvalidRequest
	unavailableFallbackGroupID := group.UnavailableFallbackGroupID
	if input.UnavailableFallbackGroupID != nil {
		if *input.UnavailableFallbackGroupID > 0 {
			unavailableFallbackGroupID = input.UnavailableFallbackGroupID
		} else {
			unavailableFallbackGroupID = nil
		}
	}
	if unavailableFallbackGroupID != nil {
		if err := s.ValidateUnavailableFallbackGroup(ctx, id, group.Platform, *unavailableFallbackGroupID); err != nil {
			return nil, err
		}
	}
	group.UnavailableFallbackGroupID = unavailableFallbackGroupID

	// 模型路由配置
	if input.ModelRouting != nil {
		group.ModelRouting = input.ModelRouting
	}
	if input.ModelRoutingEnabled != nil {
		group.ModelRoutingEnabled = *input.ModelRoutingEnabled
	}
	if input.MCPXMLInject != nil {
		group.MCPXMLInject = *input.MCPXMLInject
	}

	// 支持的模型系列（仅 antigravity 平台使用）
	if input.SupportedModelScopes != nil {
		group.SupportedModelScopes = *input.SupportedModelScopes
	}

	// 旧开关已在协议集合归一化阶段处理，此处只保留其它 OpenAI 专用配置。
	if input.AllowLive != nil {
		group.AllowLive = *input.AllowLive
	}
	if input.OpenAIFastPolicy != nil || input.ForceOpenAIFast != nil {
		legacyForce := input.ForceOpenAIFast != nil && *input.ForceOpenAIFast
		policy, err := ResolveGroupOpenAIFastPolicyInput(input.OpenAIFastPolicy, legacyForce)
		if err != nil {
			return nil, err
		}
		group.OpenAIFastPolicy = policy
	}
	if input.FreeOpenAIFast != nil {
		group.FreeOpenAIFast = *input.FreeOpenAIFast
	}
	if input.RequireOAuthOnly != nil {
		group.RequireOAuthOnly = *input.RequireOAuthOnly
	}
	if input.RequirePrivacySet != nil {
		group.RequirePrivacySet = *input.RequirePrivacySet
	}
	if input.DefaultMappedModel != nil {
		group.DefaultMappedModel = *input.DefaultMappedModel
	}
	if input.MessagesDispatchModelConfig != nil {
		group.MessagesDispatchModelConfig = NormalizeMessagesDispatchConfig(*input.MessagesDispatchModelConfig, s.options.NormalizeMappedModel)
	}
	if input.ModelsListConfig != nil {
		group.ModelsListConfig = NormalizeGroupModelsListConfig(*input.ModelsListConfig)
	}
	if input.AvailabilityProbeConfig != nil {
		config, err := NormalizeGroupAvailabilityProbeConfigForAdminWrite(*input.AvailabilityProbeConfig)
		if err != nil {
			return nil, err
		}
		group.AvailabilityProbeConfig = config
	}
	if input.RPMLimit != nil {
		group.RPMLimit = *input.RPMLimit
	}
	if input.MaxReasoningEffort != nil {
		maxReasoningEffort, err := NormalizeMaxReasoningEffortForPlatform(group.Platform, *input.MaxReasoningEffort)
		if err != nil {
			return nil, infraerrors.Newf(infraerrors.Category(400), "INVALID_MAX_REASONING_EFFORT", "%v", err)
		}
		group.MaxReasoningEffort = maxReasoningEffort
	}
	if input.MaxReasoningEffortOverLimit != nil {
		maxReasoningEffortOverLimit, err := NormalizeMaxReasoningEffortOverLimitForPlatform(group.Platform, *input.MaxReasoningEffortOverLimit)
		if err != nil {
			return nil, infraerrors.Newf(infraerrors.Category(400), "INVALID_MAX_REASONING_EFFORT_OVER_LIMIT", "%v", err)
		}
		group.MaxReasoningEffortOverLimit = maxReasoningEffortOverLimit
	}
	if input.ReasoningEffortMappings != nil {
		reasoningEffortMappings, err := NormalizeReasoningEffortMappings(group.Platform, *input.ReasoningEffortMappings)
		if err != nil {
			return nil, infraerrors.Newf(infraerrors.Category(400), "INVALID_REASONING_EFFORT_MAPPING", "%v", err)
		}
		group.ReasoningEffortMappings = reasoningEffortMappings
	}
	SanitizeGroupMessagesDispatchFields(group)
	SanitizeGroupOpenAIFast(group)
	if group.Platform != PlatformOpenAI {
		group.AllowLive = false
	}
	SanitizeGroupReasoningEffortPolicy(group)
	NormalizeGroupDefaultState(group)
	if input.LegacyProtocolInput {
		for _, protocol := range previousAllowedProtocols {
			if protocol != wireprotocol.ProtocolAnthropicMessages && protocol != wireprotocol.ProtocolOpenAIResponses && protocol != wireprotocol.ProtocolOpenAIChatCompletions && protocol != wireprotocol.ProtocolGeminiGenerateContent {
				group.AllowedProtocols = append(group.AllowedProtocols, protocol)
			}
		}
	}
	if input.ProtocolFallbacks != nil {
		group.ProtocolFallbacks = input.ProtocolFallbacks
	} else if input.Platform != "" && input.Platform != previousPlatform {
		group.ProtocolFallbacks = capability.DefaultProtocolFallbacks(group.Platform)
	}
	if input.ResponsesImagePolicy != "" {
		group.ResponsesImagePolicy = input.ResponsesImagePolicy
	}
	var legacy *LegacyGroupProtocolPatch
	if input.AllowedProtocols == nil || input.LegacyProtocolInput {
		legacy = &LegacyGroupProtocolPatch{Image: input.AllowImageGeneration, Live: input.AllowLive}
		if input.AllowBatchImageGeneration != nil {
			legacy.Batch = &group.AllowBatchImageGeneration
		}
		if input.AllowedProtocols == nil && group.Platform == PlatformOpenAI {
			legacy.Messages = input.AllowMessagesDispatch
		}
	}
	if err := NormalizeGroupProtocolPolicy(group, legacy); err != nil {
		return nil, err
	}

	// 如果指定了复制账号的源分组，同步绑定（替换当前分组的账号）
	var accountIDsToCopy []int64
	if len(input.CopyAccountsFromGroupIDs) > 0 {
		// 去重源分组 IDs
		seen := make(map[int64]struct{})
		uniqueSourceGroupIDs := make([]int64, 0, len(input.CopyAccountsFromGroupIDs))
		for _, srcGroupID := range input.CopyAccountsFromGroupIDs {
			// 校验：源分组不能是自身
			if srcGroupID == id {
				return nil, fmt.Errorf("cannot copy accounts from self")
			}
			// 去重
			if _, exists := seen[srcGroupID]; !exists {
				seen[srcGroupID] = struct{}{}
				uniqueSourceGroupIDs = append(uniqueSourceGroupIDs, srcGroupID)
			}
		}

		// 校验源分组的平台是否与当前分组一致
		for _, srcGroupID := range uniqueSourceGroupIDs {
			srcGroup, err := s.groupRepo.GetByIDLite(ctx, srcGroupID)
			if err != nil {
				return nil, fmt.Errorf("source group %d not found: %w", srcGroupID, err)
			}
			if srcGroup.Platform != group.Platform {
				return nil, fmt.Errorf("source group %d platform mismatch: expected %s, got %s", srcGroupID, group.Platform, srcGroup.Platform)
			}
		}

		// 获取所有源分组的账号（去重）
		accountIDsToCopy, err = s.groupRepo.GetAccountIDsByGroupIDs(ctx, uniqueSourceGroupIDs)
		if err != nil {
			return nil, fmt.Errorf("failed to get accounts from source groups: %w", err)
		}

		// require_oauth_only: 过滤掉 apikey 类型账号
		if group.RequireOAuthOnly && (group.Platform == PlatformOpenAI || group.Platform == PlatformAntigravity || group.Platform == PlatformAnthropic || group.Platform == PlatformGemini || group.Platform == PlatformGrok) && len(accountIDsToCopy) > 0 {
			accounts, err := s.accountRepo.GetByIDs(ctx, accountIDsToCopy)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch accounts for oauth filter: %w", err)
			}
			oauthIDs := make(map[int64]struct{}, len(accounts))
			for _, acc := range accounts {
				if acc.Type != capability.AccountTypeAPIKey {
					oauthIDs[acc.ID] = struct{}{}
				}
			}
			var filtered []int64
			for _, aid := range accountIDsToCopy {
				if _, ok := oauthIDs[aid]; ok {
					filtered = append(filtered, aid)
				}
			}
			accountIDsToCopy = filtered
		}
	}

	if err := s.options.Mutate(ctx, func(opCtx context.Context) error {
		if err := s.ClearOtherPlatformDefaultGroups(opCtx, group.Platform, group.ID, group.IsDefault); err != nil {
			return err
		}
		if err := s.groupRepo.Update(opCtx, group); err != nil {
			return TranslateGroupDefaultConflict(err)
		}
		// 分组属性更新和账号替换必须同事务提交，避免删绑成功一半。
		if len(input.CopyAccountsFromGroupIDs) > 0 {
			if _, err := s.groupRepo.DeleteAccountGroupsByGroupID(opCtx, id); err != nil {
				return fmt.Errorf("failed to clear existing account bindings: %w", err)
			}
			if len(accountIDsToCopy) > 0 {
				if err := s.groupRepo.BindAccountsToGroup(opCtx, id, accountIDsToCopy); err != nil {
					return fmt.Errorf("failed to bind accounts to group: %w", err)
				}
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByGroupID(ctx, id)
	}
	// 共享价格配置缓存按分组平台索引价卡；分组策略通过认证快照独立读取。
	// 仅在平台实际变化且事务提交成功后失效，避免继续按旧平台匹配。
	if group.Platform != previousPlatform && s.pricingConfigCacheInvalidator != nil {
		s.pricingConfigCacheInvalidator.InvalidateCache()
	}

	return group, nil
}

func (s *GroupAdmin) DeleteGroup(ctx context.Context, id int64) error {
	var groupKeys []string
	if s.authCacheInvalidator != nil {
		keys, err := s.apiKeyRepo.ListKeysByGroupID(ctx, id)
		if err == nil {
			groupKeys = keys
		}
	}

	_, err := s.groupRepo.DeleteCascade(ctx, id)
	if err != nil {
		return err
	}
	// 注意：user_group_rate_multipliers 表通过外键 ON DELETE CASCADE 自动清理
	if s.authCacheInvalidator != nil {
		for _, key := range groupKeys {
			s.authCacheInvalidator.InvalidateAuthCacheByKey(ctx, key)
		}
	}

	return nil
}

func (s *GroupAdmin) UpdateGroupSortOrders(ctx context.Context, updates []GroupSortOrderUpdate) error {
	return s.groupRepo.UpdateSortOrders(ctx, updates)
}
