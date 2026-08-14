package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/pkg/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/claude"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/TokenFlux/TokenRouter/internal/pkg/geminicli"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"
)

const groupSortOrderStep = 10

// Group management implementations
func (s *adminServiceImpl) ListGroups(ctx context.Context, page, pageSize int, platform, status, search string, isExclusive *bool, sortBy, sortOrder string) ([]Group, int64, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize, SortBy: sortBy, SortOrder: sortOrder}
	groups, result, err := s.groupRepo.ListWithFilters(ctx, params, platform, status, search, isExclusive)
	if err != nil {
		return nil, 0, err
	}
	return groups, result.Total, nil
}

func (s *adminServiceImpl) GetAllGroups(ctx context.Context) ([]Group, error) {
	return s.groupRepo.ListActive(ctx)
}

func (s *adminServiceImpl) GetAllGroupsByPlatform(ctx context.Context, platform string) ([]Group, error) {
	return s.groupRepo.ListActiveByPlatform(ctx, platform)
}

func (s *adminServiceImpl) GetAllGroupsIncludingInactive(ctx context.Context) ([]Group, error) {
	// ListWithFilters 的空 status 表示不按状态过滤，因此会返回启用和禁用分组。
	// PageSize 10000 有意放宽；实际分组数量通常只是几十个。
	groups, _, err := s.groupRepo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 10000}, "", "", "", nil)
	return groups, err
}

func (s *adminServiceImpl) GetGroup(ctx context.Context, id int64) (*Group, error) {
	return s.groupRepo.GetByID(ctx, id)
}

func (s *adminServiceImpl) GetGroupModelsListCandidates(ctx context.Context, id int64, platform string) ([]string, error) {
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
		return defaultModelsListCandidateIDs(platform), nil
	}

	accounts, err := s.accountRepo.ListSchedulableByGroupID(ctx, id)
	if err != nil {
		return nil, err
	}

	candidates := configuredModelsListCandidateIDs(accounts, platform)
	if existingGroup != nil && existingGroup.Platform == platform && existingGroup.CustomModelsListEnabled() {
		return filterModelsListCandidates(candidates, existingGroup.ModelsListConfig.Models), nil
	}
	if len(candidates) > 0 {
		return candidates, nil
	}
	return defaultModelsListCandidateIDs(platform), nil
}

func defaultModelsListCandidateIDs(platform string) []string {
	switch platform {
	case PlatformOpenAI:
		return openai.DefaultModelIDs()
	case PlatformGemini:
		ids := make([]string, 0, len(geminicli.DefaultModels))
		for _, model := range geminicli.DefaultModels {
			ids = append(ids, model.ID)
		}
		return ids
	case PlatformAntigravity:
		models := antigravity.DefaultModels()
		ids := make([]string, 0, len(models))
		for _, model := range models {
			ids = append(ids, model.ID)
		}
		return ids
	case PlatformQoder:
		return qoder.DefaultRequestModelIDs()
	case PlatformGrok:
		return xai.DefaultModelIDs()
	default:
		ids := make([]string, 0, len(claude.DefaultModels))
		for _, model := range claude.DefaultModels {
			ids = append(ids, model.ID)
		}
		return ids
	}
}

func defaultAllowImageGenerationForPlatform(platform string) bool {
	// Grok 图片和视频生成路由共用历史图片生成开关；旧客户端不会显式传 true。
	return platform == PlatformGrok
}

func (s *adminServiceImpl) CreateGroup(ctx context.Context, input *CreateGroupInput) (*Group, error) {
	if input.RateMultiplier <= 0 {
		return nil, errors.New("rate_multiplier must be > 0")
	}

	platform := input.Platform
	if platform == "" {
		platform = PlatformAnthropic
	}
	schedulerType, err := NormalizeGroupSchedulerType(input.SchedulerType)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadRequest, "INVALID_SCHEDULER_TYPE", "%v", err)
	}
	if err := s.validateGroupAdvancedSchedulerOverridesForWrite(ctx, input.AdvancedSchedulerOverrides); err != nil {
		return nil, infraerrors.Newf(http.StatusBadRequest, "INVALID_ADVANCED_SCHEDULER_OVERRIDES", "%v", err)
	}
	allowedClientProtocols := input.AllowedClientProtocols
	if allowedClientProtocols == nil {
		allowedClientProtocols = defaultGroupClientProtocols(platform)
		if platform == PlatformOpenAI {
			allowedClientProtocols = setGroupClientProtocol(allowedClientProtocols, GroupClientProtocolAnthropicMessages, input.AllowMessagesDispatch)
		}
	} else {
		normalizedProtocols, validationErr := normalizeExplicitGroupClientProtocols(platform, allowedClientProtocols)
		if validationErr != nil {
			return nil, infraerrors.Newf(http.StatusBadRequest, "INVALID_ALLOWED_CLIENT_PROTOCOLS", "%v", validationErr)
		}
		allowedClientProtocols = normalizedProtocols
	}
	modelPricing, err := normalizeGroupModelPricing(platform, input.ModelPricing)
	if err != nil {
		return nil, err
	}
	longContextPricingEnabled := true
	if input.LongContextPricingEnabled != nil {
		longContextPricingEnabled = *input.LongContextPricingEnabled
	}
	maxReasoningEffort, err := normalizeMaxReasoningEffortForPlatform(platform, input.MaxReasoningEffort)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadRequest, "INVALID_MAX_REASONING_EFFORT", "%v", err)
	}
	reasoningEffortMappings, err := NormalizeReasoningEffortMappings(platform, input.ReasoningEffortMappings)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadRequest, "INVALID_REASONING_EFFORT_MAPPING", "%v", err)
	}

	// 图片价格：负数表示清除（使用默认价格），0 保留（表示免费）
	imagePrice1K := normalizePrice(input.ImagePrice1K)
	imagePrice2K := normalizePrice(input.ImagePrice2K)
	imagePrice4K := normalizePrice(input.ImagePrice4K)
	videoPrice480P := normalizePrice(input.VideoPrice480P)
	videoPrice720P := normalizePrice(input.VideoPrice720P)
	videoPrice1080P := normalizePrice(input.VideoPrice1080P)
	webSearchPricePerCall := normalizePrice(input.WebSearchPricePerCall)
	searchPricePer1k := normalizePrice(input.SearchPricePer1k)
	audioRealtimePricePerMin := normalizePrice(input.AudioRealtimePricePerMin)
	audioTTSPricePerMillionChars := normalizePrice(input.AudioTTSPricePerMillionChars)
	audioSTTPricePerHour := normalizePrice(input.AudioSTTPricePerHour)
	imageRateMultiplier := 1.0
	if input.ImageRateMultiplier != nil {
		if *input.ImageRateMultiplier < 0 {
			return nil, errors.New("image_rate_multiplier must be >= 0")
		}
		imageRateMultiplier = *input.ImageRateMultiplier
	}
	batchImageDiscountMultiplier := defaultBatchImageDiscountMultiplier
	if input.BatchImageDiscountMultiplier != nil {
		if *input.BatchImageDiscountMultiplier < 0 {
			return nil, errors.New("batch_image_discount_multiplier must be >= 0")
		}
		batchImageDiscountMultiplier = *input.BatchImageDiscountMultiplier
	}
	batchImageHoldMultiplier := defaultBatchImageHoldMultiplier
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
	videoRateMultiplier := 1.0
	if input.VideoRateMultiplier != nil {
		if *input.VideoRateMultiplier < 0 {
			return nil, errors.New("video_rate_multiplier must be >= 0")
		}
		videoRateMultiplier = *input.VideoRateMultiplier
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
		if err := s.validateFallbackGroup(ctx, 0, *input.FallbackGroupID); err != nil {
			return nil, err
		}
	}
	unavailableFallbackGroupID := input.UnavailableFallbackGroupID
	if unavailableFallbackGroupID != nil && *unavailableFallbackGroupID <= 0 {
		unavailableFallbackGroupID = nil
	}
	if unavailableFallbackGroupID != nil {
		if err := s.validateUnavailableFallbackGroup(ctx, 0, platform, *unavailableFallbackGroupID); err != nil {
			return nil, err
		}
	}
	fallbackOnInvalidRequest := input.FallbackGroupIDOnInvalidRequest
	if fallbackOnInvalidRequest != nil && *fallbackOnInvalidRequest <= 0 {
		fallbackOnInvalidRequest = nil
	}
	// 校验无效请求兜底分组
	if fallbackOnInvalidRequest != nil {
		if err := s.validateFallbackGroupOnInvalidRequest(ctx, 0, platform, *fallbackOnInvalidRequest); err != nil {
			return nil, err
		}
	}

	// MCPXMLInject：默认为 true，仅当显式传入 false 时关闭
	mcpXMLInject := true
	if input.MCPXMLInject != nil {
		mcpXMLInject = *input.MCPXMLInject
	}

	allowImageGeneration := input.AllowImageGeneration || defaultAllowImageGenerationForPlatform(platform)
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
	availabilityProbeConfig, err := normalizeGroupAvailabilityProbeConfigForAdminWrite(input.AvailabilityProbeConfig)
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
		AdvancedSchedulerOverrides:      CloneGroupAdvancedSchedulerOverrides(input.AdvancedSchedulerOverrides),
		DisplayBrand:                    strings.TrimSpace(input.DisplayBrand),
		SortOrder:                       sortOrder,
		RateMultiplier:                  input.RateMultiplier,
		IsExclusive:                     input.IsExclusive,
		IsDefault:                       input.IsDefault,
		DataSharingEnabled:              input.DataSharingEnabled,
		SessionIsolationEnabled:         input.SessionIsolationEnabled,
		Status:                          StatusActive,
		LongContextPricingEnabled:       longContextPricingEnabled,
		ModelPricing:                    modelPricing,
		AllowImageGeneration:            allowImageGeneration,
		AllowBatchImageGeneration:       allowBatchImageGeneration,
		ImageRateIndependent:            input.ImageRateIndependent,
		ImageRateMultiplier:             imageRateMultiplier,
		BatchImageDiscountMultiplier:    batchImageDiscountMultiplier,
		BatchImageHoldMultiplier:        batchImageHoldMultiplier,
		VideoRateIndependent:            input.VideoRateIndependent,
		VideoRateMultiplier:             videoRateMultiplier,
		PeakRateEnabled:                 peakRateEnabled,
		PeakStart:                       peakStart,
		PeakEnd:                         peakEnd,
		PeakRateMultiplier:              peakRateMultiplier,
		ImagePrice1K:                    imagePrice1K,
		ImagePrice2K:                    imagePrice2K,
		ImagePrice4K:                    imagePrice4K,
		VideoPrice480P:                  videoPrice480P,
		VideoPrice720P:                  videoPrice720P,
		VideoPrice1080P:                 videoPrice1080P,
		VideoModelPrices:                NormalizeVideoModelPrices(input.VideoModelPrices),
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
		AllowedClientProtocols:          allowedClientProtocols,
		AllowLive:                       input.AllowLive,
		RequireOAuthOnly:                input.RequireOAuthOnly,
		RequirePrivacySet:               input.RequirePrivacySet,
		DefaultMappedModel:              input.DefaultMappedModel,
		MessagesDispatchModelConfig:     normalizeOpenAIMessagesDispatchModelConfig(input.MessagesDispatchModelConfig),
		ModelsListConfig:                normalizeGroupModelsListConfig(input.ModelsListConfig),
		AvailabilityProbeConfig:         availabilityProbeConfig,
		RPMLimit:                        input.RPMLimit,
		MaxReasoningEffort:              maxReasoningEffort,
		ReasoningEffortMappings:         reasoningEffortMappings,
	}
	sanitizeGroupMessagesDispatchFields(group)
	if group.Platform != PlatformOpenAI {
		group.AllowLive = false
	}
	sanitizeGroupReasoningEffortPolicy(group)
	normalizeGroupDefaultState(group)

	// require_oauth_only: 过滤掉 apikey 类型账号
	if group.RequireOAuthOnly && (group.Platform == PlatformOpenAI || group.Platform == PlatformAntigravity || group.Platform == PlatformAnthropic || group.Platform == PlatformGemini || group.Platform == PlatformGrok) && len(accountIDsToCopy) > 0 {
		accounts, err := s.accountRepo.GetByIDs(ctx, accountIDsToCopy)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch accounts for oauth filter: %w", err)
		}
		oauthIDs := make(map[int64]struct{}, len(accounts))
		for _, acc := range accounts {
			if acc.Type != AccountTypeAPIKey {
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

	if err := s.runGroupMutationTx(ctx, func(opCtx context.Context) error {
		if input.SortOrder == nil {
			resolvedSortOrder, err := s.nextGroupSortOrder(opCtx)
			if err != nil {
				return err
			}
			group.SortOrder = resolvedSortOrder
		}
		if err := s.clearOtherPlatformDefaultGroups(opCtx, group.Platform, 0, group.IsDefault); err != nil {
			return err
		}
		if err := s.groupRepo.Create(opCtx, group); err != nil {
			return translateGroupDefaultConflict(err)
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

// nextGroupSortOrder 在创建事务内分配末尾排序值，避免并发新建产生重复位置。
func (s *adminServiceImpl) nextGroupSortOrder(ctx context.Context) (int, error) {
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

	next := groups[0].SortOrder + groupSortOrderStep
	if next < groups[0].SortOrder {
		return 0, errors.New("group sort order overflow")
	}
	return next, nil
}

// normalizePrice 将负数转换为 nil（表示使用默认价格），0 保留（表示免费）
func normalizePrice(price *float64) *float64 {
	if price == nil || *price < 0 {
		return nil
	}
	return price
}

// validateFallbackGroup 校验降级分组的有效性
// currentGroupID: 当前分组 ID（新建时为 0）
// fallbackGroupID: 降级分组 ID
func (s *adminServiceImpl) validateFallbackGroup(ctx context.Context, currentGroupID, fallbackGroupID int64) error {
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

// validateFallbackGroupOnInvalidRequest 校验无效请求兜底分组的有效性。
// currentGroupID: 当前分组 ID（新建时为 0）
// platform: 当前分组的平台
// fallbackGroupID: 兜底分组 ID
func (s *adminServiceImpl) validateFallbackGroupOnInvalidRequest(ctx context.Context, currentGroupID int64, platform string, fallbackGroupID int64) error {
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

func (s *adminServiceImpl) UpdateGroup(ctx context.Context, id int64, input *UpdateGroupInput) (*Group, error) {
	group, err := s.groupRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	previousPlatform := group.Platform
	previousAllowedClientProtocols := group.EffectiveAllowedClientProtocols()

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
			return nil, infraerrors.Newf(http.StatusBadRequest, "INVALID_SCHEDULER_TYPE", "%v", normalizeErr)
		}
		group.SchedulerType = schedulerType
	}
	if input.AdvancedSchedulerOverrides != nil {
		if validationErr := s.validateGroupAdvancedSchedulerOverridesForWrite(ctx, *input.AdvancedSchedulerOverrides); validationErr != nil {
			return nil, infraerrors.Newf(http.StatusBadRequest, "INVALID_ADVANCED_SCHEDULER_OVERRIDES", "%v", validationErr)
		}
		group.AdvancedSchedulerOverrides = CloneGroupAdvancedSchedulerOverrides(*input.AdvancedSchedulerOverrides)
	}
	if input.AllowedClientProtocols != nil {
		group.AllowedClientProtocols, err = normalizeExplicitGroupClientProtocols(group.Platform, *input.AllowedClientProtocols)
		if err != nil {
			return nil, infraerrors.Newf(http.StatusBadRequest, "INVALID_ALLOWED_CLIENT_PROTOCOLS", "%v", err)
		}
	} else {
		// 字段缺省时保留原集合；切换平台只移除新平台不支持的协议。
		group.AllowedClientProtocols = previousAllowedClientProtocols
		if input.Platform != "" && group.Platform != previousPlatform {
			group.AllowedClientProtocols = filterGroupClientProtocolsForPlatform(group.Platform, group.AllowedClientProtocols)
		}
		if group.Platform == PlatformOpenAI && input.AllowMessagesDispatch != nil {
			group.AllowedClientProtocols = setGroupClientProtocol(group.AllowedClientProtocols, GroupClientProtocolAnthropicMessages, *input.AllowMessagesDispatch)
		}
		group.AllowedClientProtocols, err = normalizeExplicitGroupClientProtocols(group.Platform, group.AllowedClientProtocols)
		if err != nil {
			return nil, infraerrors.Newf(http.StatusBadRequest, "INVALID_ALLOWED_CLIENT_PROTOCOLS", "%v", err)
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
	if input.DataSharingEnabled != nil {
		group.DataSharingEnabled = *input.DataSharingEnabled
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
	if input.ModelPricing != nil {
		modelPricing, normalizeErr := normalizeGroupModelPricing(group.Platform, *input.ModelPricing)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		group.ModelPricing = modelPricing
	}

	// 图片生成计费配置：负数表示清除（使用默认价格）
	if input.AllowImageGeneration != nil {
		group.AllowImageGeneration = *input.AllowImageGeneration
	}
	if input.AllowBatchImageGeneration != nil {
		group.AllowBatchImageGeneration = *input.AllowBatchImageGeneration
	}
	if !group.AllowImageGeneration || group.Platform != PlatformGemini {
		group.AllowBatchImageGeneration = false
	}
	if input.ImageRateIndependent != nil {
		group.ImageRateIndependent = *input.ImageRateIndependent
	}
	if input.ImageRateMultiplier != nil {
		if *input.ImageRateMultiplier < 0 {
			return nil, errors.New("image_rate_multiplier must be >= 0")
		}
		group.ImageRateMultiplier = *input.ImageRateMultiplier
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
	if input.VideoRateIndependent != nil {
		group.VideoRateIndependent = *input.VideoRateIndependent
	}
	if input.VideoRateMultiplier != nil {
		if *input.VideoRateMultiplier < 0 {
			return nil, errors.New("video_rate_multiplier must be >= 0")
		}
		group.VideoRateMultiplier = *input.VideoRateMultiplier
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
	if input.ImagePrice1K != nil {
		group.ImagePrice1K = normalizePrice(input.ImagePrice1K)
	}
	if input.ImagePrice2K != nil {
		group.ImagePrice2K = normalizePrice(input.ImagePrice2K)
	}
	if input.ImagePrice4K != nil {
		group.ImagePrice4K = normalizePrice(input.ImagePrice4K)
	}
	if input.VideoPrice480P != nil {
		group.VideoPrice480P = normalizePrice(input.VideoPrice480P)
	}
	if input.VideoPrice720P != nil {
		group.VideoPrice720P = normalizePrice(input.VideoPrice720P)
	}
	if input.VideoPrice1080P != nil {
		group.VideoPrice1080P = normalizePrice(input.VideoPrice1080P)
	}
	// nil 表示不修改，空 map 表示清除按模型价格。
	if input.VideoModelPrices != nil {
		group.VideoModelPrices = NormalizeVideoModelPrices(input.VideoModelPrices)
	}
	if input.WebSearchPricePerCall != nil {
		group.WebSearchPricePerCall = normalizePrice(input.WebSearchPricePerCall)
	}
	if input.SearchPricePer1k != nil {
		group.SearchPricePer1k = normalizePrice(input.SearchPricePer1k)
	}
	if input.AudioRealtimePricePerMin != nil {
		group.AudioRealtimePricePerMin = normalizePrice(input.AudioRealtimePricePerMin)
	}
	if input.AudioTTSPricePerMillionChars != nil {
		group.AudioTTSPricePerMillionChars = normalizePrice(input.AudioTTSPricePerMillionChars)
	}
	if input.AudioSTTPricePerHour != nil {
		group.AudioSTTPricePerHour = normalizePrice(input.AudioSTTPricePerHour)
	}

	// Claude Code 客户端限制
	if input.ClaudeCodeOnly != nil {
		group.ClaudeCodeOnly = *input.ClaudeCodeOnly
	}
	if input.FallbackGroupID != nil {
		// 校验降级分组
		if *input.FallbackGroupID > 0 {
			if err := s.validateFallbackGroup(ctx, id, *input.FallbackGroupID); err != nil {
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
		if err := s.validateFallbackGroupOnInvalidRequest(ctx, id, group.Platform, *fallbackOnInvalidRequest); err != nil {
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
		if err := s.validateUnavailableFallbackGroup(ctx, id, group.Platform, *unavailableFallbackGroupID); err != nil {
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
		group.MessagesDispatchModelConfig = normalizeOpenAIMessagesDispatchModelConfig(*input.MessagesDispatchModelConfig)
	}
	if input.ModelsListConfig != nil {
		group.ModelsListConfig = normalizeGroupModelsListConfig(*input.ModelsListConfig)
	}
	if input.AvailabilityProbeConfig != nil {
		config, err := normalizeGroupAvailabilityProbeConfigForAdminWrite(*input.AvailabilityProbeConfig)
		if err != nil {
			return nil, err
		}
		group.AvailabilityProbeConfig = config
	}
	if input.RPMLimit != nil {
		group.RPMLimit = *input.RPMLimit
	}
	if input.MaxReasoningEffort != nil {
		maxReasoningEffort, err := normalizeMaxReasoningEffortForPlatform(group.Platform, *input.MaxReasoningEffort)
		if err != nil {
			return nil, infraerrors.Newf(http.StatusBadRequest, "INVALID_MAX_REASONING_EFFORT", "%v", err)
		}
		group.MaxReasoningEffort = maxReasoningEffort
	}
	if input.ReasoningEffortMappings != nil {
		reasoningEffortMappings, err := NormalizeReasoningEffortMappings(group.Platform, *input.ReasoningEffortMappings)
		if err != nil {
			return nil, infraerrors.Newf(http.StatusBadRequest, "INVALID_REASONING_EFFORT_MAPPING", "%v", err)
		}
		group.ReasoningEffortMappings = reasoningEffortMappings
	}
	sanitizeGroupMessagesDispatchFields(group)
	if group.Platform != PlatformOpenAI {
		group.AllowLive = false
	}
	sanitizeGroupReasoningEffortPolicy(group)
	normalizeGroupDefaultState(group)

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
				if acc.Type != AccountTypeAPIKey {
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

	if err := s.runGroupMutationTx(ctx, func(opCtx context.Context) error {
		if err := s.clearOtherPlatformDefaultGroups(opCtx, group.Platform, group.ID, group.IsDefault); err != nil {
			return err
		}
		if err := s.groupRepo.Update(opCtx, group); err != nil {
			return translateGroupDefaultConflict(err)
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
	// 渠道缓存持有分组平台，渠道定价、模型映射和模型白名单均依赖该值。
	// 仅在平台实际变化且事务提交成功后失效，避免继续按旧平台匹配。
	if group.Platform != previousPlatform && s.channelCacheInvalidator != nil {
		s.channelCacheInvalidator.InvalidateCache()
	}

	return group, nil
}

func normalizeGroupModelPricing(platform string, pricing []ChannelModelPricing) ([]ChannelModelPricing, error) {
	out := make([]ChannelModelPricing, len(pricing))
	for i := range pricing {
		out[i] = pricing[i].Clone()
		out[i].ID = 0
		out[i].ChannelID = 0
		if strings.TrimSpace(out[i].Platform) == "" {
			out[i].Platform = platform
		}
		for j := range out[i].Models {
			out[i].Models[j] = strings.TrimSpace(out[i].Models[j])
		}
		if len(out[i].Models) == 0 {
			return nil, infraerrors.New(http.StatusBadRequest, "GROUP_MODEL_PRICING_MODELS_REQUIRED", "group model pricing entry requires at least one model")
		}
	}
	if err := validatePricingEntries(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *adminServiceImpl) DeleteGroup(ctx context.Context, id int64) error {
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

func (s *adminServiceImpl) GetGroupAPIKeys(ctx context.Context, groupID int64, page, pageSize int) ([]APIKey, int64, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize}
	keys, result, err := s.apiKeyRepo.ListByGroupID(ctx, groupID, params)
	if err != nil {
		return nil, 0, err
	}
	return keys, result.Total, nil
}

func (s *adminServiceImpl) GetGroupRateMultipliers(ctx context.Context, groupID int64) ([]UserGroupRateEntry, error) {
	if s.userGroupRateRepo == nil {
		return nil, nil
	}
	return s.userGroupRateRepo.GetByGroupID(ctx, groupID)
}

func (s *adminServiceImpl) ClearGroupRateMultipliers(ctx context.Context, groupID int64) error {
	if s.userGroupRateRepo == nil {
		return nil
	}
	return s.userGroupRateRepo.DeleteByGroupID(ctx, groupID)
}

func (s *adminServiceImpl) BatchSetGroupRateMultipliers(ctx context.Context, groupID int64, entries []GroupRateMultiplierInput) error {
	if s.userGroupRateRepo == nil {
		return nil
	}
	for _, e := range entries {
		if e.RateMultiplier <= 0 {
			return fmt.Errorf("rate_multiplier must be > 0 (user_id=%d)", e.UserID)
		}
	}
	return s.userGroupRateRepo.SyncGroupRateMultipliers(ctx, groupID, entries)
}

func (s *adminServiceImpl) ClearGroupRPMOverrides(ctx context.Context, groupID int64) error {
	if s.userGroupRateRepo == nil {
		return nil
	}
	if err := s.userGroupRateRepo.ClearGroupRPMOverrides(ctx, groupID); err != nil {
		return err
	}
	// RPM override 已嵌入 auth cache snapshot (v7)，变更后必须失效相关缓存。
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByGroupID(ctx, groupID)
	}
	return nil
}

func (s *adminServiceImpl) BatchSetGroupRPMOverrides(ctx context.Context, groupID int64, entries []GroupRPMOverrideInput) error {
	if s.userGroupRateRepo == nil {
		return nil
	}
	for _, e := range entries {
		if e.RPMOverride != nil && *e.RPMOverride < 0 {
			return infraerrors.BadRequest("INVALID_RPM_OVERRIDE", fmt.Sprintf("rpm_override must be >= 0 (user_id=%d)", e.UserID))
		}
	}
	if err := s.userGroupRateRepo.SyncGroupRPMOverrides(ctx, groupID, entries); err != nil {
		return err
	}
	// RPM override 已嵌入 auth cache snapshot (v7)，变更后必须失效相关缓存。
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByGroupID(ctx, groupID)
	}
	return nil
}

func (s *adminServiceImpl) UpdateGroupSortOrders(ctx context.Context, updates []GroupSortOrderUpdate) error {
	return s.groupRepo.UpdateSortOrders(ctx, updates)
}

// AdminUpdateAPIKeyGroupID 管理员修改 API Key 分组绑定
// groupID: nil=不修改, 指向0=解绑, 指向正整数=绑定到目标分组
func (s *adminServiceImpl) AdminUpdateAPIKeyGroupID(ctx context.Context, keyID int64, groupID *int64) (*AdminUpdateAPIKeyGroupIDResult, error) {
	apiKey, err := s.apiKeyRepo.GetByID(ctx, keyID)
	if err != nil {
		return nil, err
	}
	if apiKey.IsComposite {
		return nil, ErrCompositeKeyGroupConflict
	}

	if groupID == nil {
		// nil 表示不修改，直接返回
		return &AdminUpdateAPIKeyGroupIDResult{APIKey: apiKey}, nil
	}

	if *groupID < 0 {
		return nil, infraerrors.BadRequest("INVALID_GROUP_ID", "group_id must be non-negative")
	}

	result := &AdminUpdateAPIKeyGroupIDResult{}

	if *groupID == 0 {
		// 0 表示解绑分组（不修改 user_allowed_groups，避免影响用户其他 Key）
		apiKey.GroupID = nil
		apiKey.Group = nil
	} else {
		// 验证目标分组存在且状态为 active
		group, err := s.groupRepo.GetByID(ctx, *groupID)
		if err != nil {
			return nil, err
		}
		if group.Status != StatusActive {
			return nil, infraerrors.BadRequest("GROUP_NOT_ACTIVE", "target group is not active")
		}
		if !group.IsExclusive {
			if s.userRepo == nil {
				return nil, infraerrors.InternalServer("USER_REPOSITORY_UNAVAILABLE", "user repository unavailable")
			}
			user, err := s.userRepo.GetByID(ctx, apiKey.UserID)
			if err != nil {
				return nil, fmt.Errorf("get user: %w", err)
			}
			if !user.CanBindGroup(group.ID, group.IsExclusive) {
				return nil, ErrGroupNotAllowed
			}
		}
		gid := *groupID
		apiKey.GroupID = &gid
		apiKey.Group = group

		// 专属标准分组：使用事务保证「添加分组权限」与「更新 API Key」的原子性
		if group.IsExclusive {
			opCtx := ctx
			var tx *dbent.Tx
			if s.entClient == nil {
				logger.LegacyPrintf("service.admin", "Warning: entClient is nil, skipping transaction protection for exclusive group binding")
			} else {
				var txErr error
				tx, txErr = s.entClient.Tx(ctx)
				if txErr != nil {
					return nil, fmt.Errorf("begin transaction: %w", txErr)
				}
				defer func() { _ = tx.Rollback() }()
				opCtx = dbent.NewTxContext(ctx, tx)
			}

			if addErr := s.userRepo.AddGroupToAllowedGroups(opCtx, apiKey.UserID, gid); addErr != nil {
				return nil, fmt.Errorf("add group to user allowed groups: %w", addErr)
			}
			if err := s.apiKeyRepo.Update(opCtx, apiKey, APIKeyUpdateFields{GroupID: true}); err != nil {
				return nil, fmt.Errorf("update api key: %w", err)
			}
			if tx != nil {
				if err := tx.Commit(); err != nil {
					return nil, fmt.Errorf("commit transaction: %w", err)
				}
			}

			result.AutoGrantedGroupAccess = true
			result.GrantedGroupID = &gid
			result.GrantedGroupName = group.Name

			// 失效认证缓存（在事务提交后执行）
			if s.authCacheInvalidator != nil {
				s.authCacheInvalidator.InvalidateAuthCacheByKey(ctx, apiKey.Key)
			}

			result.APIKey = apiKey
			return result, nil
		}
	}

	// 非专属分组 / 解绑：无需事务，单步更新即可
	if err := s.apiKeyRepo.Update(ctx, apiKey, APIKeyUpdateFields{GroupID: true}); err != nil {
		return nil, fmt.Errorf("update api key: %w", err)
	}

	// 失效认证缓存
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByKey(ctx, apiKey.Key)
	}

	result.APIKey = apiKey
	return result, nil
}

// AdminResetAPIKeyRateLimitUsage resets an API key's rolling rate-limit usage windows.
func (s *adminServiceImpl) AdminResetAPIKeyRateLimitUsage(ctx context.Context, keyID int64) (*APIKey, error) {
	apiKey, err := s.apiKeyRepo.GetByID(ctx, keyID)
	if err != nil {
		return nil, err
	}

	apiKey.Usage5h = 0
	apiKey.Usage1d = 0
	apiKey.Usage7d = 0
	apiKey.Window5hStart = nil
	apiKey.Window1dStart = nil
	apiKey.Window7dStart = nil
	if err := s.apiKeyRepo.Update(ctx, apiKey, APIKeyUpdateFields{RateLimitUsage: true}); err != nil {
		return nil, fmt.Errorf("reset api key rate limit usage: %w", err)
	}

	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByKey(ctx, apiKey.Key)
	}
	if s.billingCacheService != nil && s.billingCacheService.cache != nil {
		_ = s.billingCacheService.cache.InvalidateAPIKeyRateLimit(ctx, apiKey.ID)
	}

	return apiKey, nil
}

// ReplaceUserGroup 替换用户的专属分组
func (s *adminServiceImpl) ReplaceUserGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (*ReplaceUserGroupResult, error) {
	if oldGroupID == newGroupID {
		return nil, infraerrors.BadRequest("SAME_GROUP", "old and new group must be different")
	}

	// 验证新分组存在且为活跃的专属分组
	newGroup, err := s.groupRepo.GetByID(ctx, newGroupID)
	if err != nil {
		return nil, err
	}
	if newGroup.Status != StatusActive {
		return nil, infraerrors.BadRequest("GROUP_NOT_ACTIVE", "target group is not active")
	}
	if !newGroup.IsExclusive {
		return nil, infraerrors.BadRequest("GROUP_NOT_EXCLUSIVE", "target group is not exclusive")
	}

	// 事务保证原子性
	if s.entClient == nil {
		return nil, fmt.Errorf("entClient is nil, cannot perform group replacement")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	opCtx := dbent.NewTxContext(ctx, tx)

	// 1. 授予新分组权限
	if err := s.userRepo.AddGroupToAllowedGroups(opCtx, userID, newGroupID); err != nil {
		return nil, fmt.Errorf("add new group to allowed groups: %w", err)
	}

	// 2. 迁移绑定旧分组的 Key 到新分组
	migrated, err := s.apiKeyRepo.UpdateGroupIDByUserAndGroup(opCtx, userID, oldGroupID, newGroupID)
	if err != nil {
		return nil, fmt.Errorf("migrate api keys: %w", err)
	}

	// 3. 移除旧分组权限
	if err := s.userRepo.RemoveGroupFromUserAllowedGroups(opCtx, userID, oldGroupID); err != nil {
		return nil, fmt.Errorf("remove old group from allowed groups: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	// 失效该用户所有 Key 的认证缓存
	if s.authCacheInvalidator != nil {
		keys, keyErr := s.apiKeyRepo.ListKeysByUserID(ctx, userID)
		if keyErr == nil {
			for _, k := range keys {
				s.authCacheInvalidator.InvalidateAuthCacheByKey(ctx, k)
			}
		}
	}

	return &ReplaceUserGroupResult{MigratedKeys: migrated}, nil
}
