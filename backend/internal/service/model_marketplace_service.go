package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/BrandonVee/TokenRouter/internal/pkg/antigravity"
	"github.com/BrandonVee/TokenRouter/internal/pkg/claude"
	"github.com/BrandonVee/TokenRouter/internal/pkg/geminicli"
	"github.com/BrandonVee/TokenRouter/internal/pkg/openai"
	"github.com/BrandonVee/TokenRouter/internal/pkg/xai"
)

const (
	DefaultMarketplaceAvailabilityWindowDays    = 7
	DefaultMarketplaceAvailabilityBucketMinutes = 120
	minMarketplaceAvailabilityWindowDays        = 1
	maxMarketplaceAvailabilityWindowDays        = 90
	minMarketplaceAvailabilityBucketMinutes     = 5
	maxMarketplaceAvailabilityBucketMinutes     = 1440
	maxMarketplaceAvailabilityBuckets           = 720
)

type ModelMarketplaceGroup struct {
	ID                         int64
	Name                       string
	Description                string
	Platform                   string
	DisplayBrand               string
	SortOrder                  int
	RateMultiplier             float64
	ImageRateIndependent       bool
	ImageRateMultiplier        float64
	OfficialPriceRatio         *float64
	OfficialPriceRMBEquivalent *float64
	// DataSharingEnabled 标记公开分组是否会采集数据共享会话，供模型广场展示提示。
	DataSharingEnabled bool
	Capacity           *GroupCapacitySummary
	Availability       *GroupAvailabilitySummary
	ModelCount         int
	Models             []ModelMarketplaceModel
}

type ModelMarketplaceModel struct {
	ID          string
	DisplayName string
	Pricing     ModelDisplayPricing
}

type ModelMarketplaceService struct {
	groupRepo        GroupRepository
	settingRepo      SettingRepository
	gatewayService   *GatewayService
	billingService   *BillingService
	capacityService  *GroupCapacityService
	availabilityRepo GroupAvailabilityProbeRepository
	cfg              *config.Config
}

func NewModelMarketplaceService(
	groupRepo GroupRepository,
	settingRepo SettingRepository,
	gatewayService *GatewayService,
	billingService *BillingService,
	capacityService *GroupCapacityService,
	availabilityRepo GroupAvailabilityProbeRepository,
	cfg *config.Config,
) *ModelMarketplaceService {
	return &ModelMarketplaceService{
		groupRepo:        groupRepo,
		settingRepo:      settingRepo,
		gatewayService:   gatewayService,
		billingService:   billingService,
		capacityService:  capacityService,
		availabilityRepo: availabilityRepo,
		cfg:              cfg,
	}
}

// @project-doc docs/interfaces/model_catalog_and_marketplace.md#model_catalog_resolution
func (s *ModelMarketplaceService) ListPublic(ctx context.Context) ([]ModelMarketplaceGroup, error) {
	groups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active groups: %w", err)
	}

	discountConfig, showDiscount := s.getOfficialPriceRatioConfig(ctx)
	capacityMap := s.getPublicCapacityMap(ctx, groups)
	availabilityMap := s.getPublicAvailabilityMap(ctx, groups)
	accountsByGroup, accountsPrefetched := s.prefetchPublicGroupAccounts(ctx)
	out := make([]ModelMarketplaceGroup, 0, len(groups))
	for i := range groups {
		group := &groups[i]
		if group.IsExclusive || group.ActiveAccountCount <= 0 {
			continue
		}

		var models []ModelMarketplaceModel
		if accountsPrefetched {
			models = s.listPublicModelsForGroupWithAccounts(ctx, group, accountsByGroup[group.ID])
		} else {
			models = s.listPublicModelsForGroup(ctx, group)
		}
		if len(models) == 0 {
			continue
		}

		var officialPriceRatio *float64
		var officialPriceRMBEquivalent *float64
		if showDiscount && group.Platform != PlatformQoder {
			officialPriceRatio = discountConfig.officialPriceRatio(group.RateMultiplier)
			officialPriceRMBEquivalent = discountConfig.officialPriceRMBEquivalent(group.RateMultiplier)
		}
		out = append(out, ModelMarketplaceGroup{
			ID:                         group.ID,
			Name:                       group.Name,
			Description:                group.Description,
			Platform:                   group.Platform,
			DisplayBrand:               marketplaceGroupDisplayBrand(group),
			SortOrder:                  group.SortOrder,
			RateMultiplier:             group.RateMultiplier,
			ImageRateIndependent:       group.ImageRateIndependent,
			ImageRateMultiplier:        group.ImageRateMultiplier,
			OfficialPriceRatio:         officialPriceRatio,
			OfficialPriceRMBEquivalent: officialPriceRMBEquivalent,
			DataSharingEnabled:         group.DataSharingEnabled,
			Capacity:                   marketplaceGroupCapacity(capacityMap, group.ID),
			Availability:               marketplaceGroupAvailability(availabilityMap, group.ID),
			ModelCount:                 len(models),
			Models:                     models,
		})
	}

	return out, nil
}

// prefetchPublicGroupAccounts 一次读取全部可调度账号，并按账号全局优先级恢复分组查询顺序。
func (s *ModelMarketplaceService) prefetchPublicGroupAccounts(ctx context.Context) (map[int64][]Account, bool) {
	if s == nil || s.gatewayService == nil || s.gatewayService.accountRepo == nil {
		return nil, false
	}
	accounts, err := s.gatewayService.accountRepo.ListSchedulable(ctx)
	if err != nil {
		slog.Warn("failed to prefetch marketplace accounts", "error", err)
		return nil, false
	}

	accountsByGroup := make(map[int64][]Account)
	for i := range accounts {
		account := accounts[i]
		seenGroups := make(map[int64]struct{}, len(account.GroupIDs)+len(account.AccountGroups))
		for _, accountGroup := range account.AccountGroups {
			if accountGroup.GroupID <= 0 {
				continue
			}
			seenGroups[accountGroup.GroupID] = struct{}{}
			accountsByGroup[accountGroup.GroupID] = append(accountsByGroup[accountGroup.GroupID], account)
		}
		for _, groupID := range account.GroupIDs {
			if groupID <= 0 {
				continue
			}
			if _, exists := seenGroups[groupID]; exists {
				continue
			}
			accountsByGroup[groupID] = append(accountsByGroup[groupID], account)
		}
	}

	for groupID := range accountsByGroup {
		groupAccounts := accountsByGroup[groupID]
		sort.SliceStable(groupAccounts, func(i, j int) bool {
			if groupAccounts[i].Priority != groupAccounts[j].Priority {
				return groupAccounts[i].Priority < groupAccounts[j].Priority
			}
			return groupAccounts[i].ID < groupAccounts[j].ID
		})
		accountsByGroup[groupID] = groupAccounts
	}
	return accountsByGroup, true
}

func (s *ModelMarketplaceService) getPublicCapacityMap(ctx context.Context, groups []Group) map[int64]GroupCapacitySummary {
	if s.capacityService == nil || len(groups) == 0 {
		return nil
	}

	groupIDs := make([]int64, 0, len(groups))
	for i := range groups {
		group := &groups[i]
		if group.IsExclusive || group.ActiveAccountCount <= 0 {
			continue
		}
		groupIDs = append(groupIDs, group.ID)
	}
	if len(groupIDs) == 0 {
		return nil
	}

	// 容量是模型广场的辅助负载信息，获取失败时不影响模型和价格展示。
	capacityMap, err := s.capacityService.GetGroupCapacityByIDs(ctx, groupIDs)
	if err != nil {
		return nil
	}
	return capacityMap
}

func marketplaceGroupCapacity(capacityMap map[int64]GroupCapacitySummary, groupID int64) *GroupCapacitySummary {
	if len(capacityMap) == 0 {
		return nil
	}
	capacity, ok := capacityMap[groupID]
	if !ok {
		return nil
	}
	return &capacity
}

func (s *ModelMarketplaceService) getPublicAvailabilityMap(ctx context.Context, groups []Group) map[int64]*GroupAvailabilitySummary {
	if s.availabilityRepo == nil || len(groups) == 0 {
		return nil
	}

	groupIDs := make([]int64, 0, len(groups))
	for i := range groups {
		group := &groups[i]
		if group.IsExclusive || group.ActiveAccountCount <= 0 {
			continue
		}
		if !group.AvailabilityProbeConfig.Enabled {
			continue
		}
		groupIDs = append(groupIDs, group.ID)
	}
	if len(groupIDs) == 0 {
		return nil
	}

	timezone := "UTC"
	if s.cfg != nil && strings.TrimSpace(s.cfg.Timezone) != "" {
		timezone = strings.TrimSpace(s.cfg.Timezone)
	}
	// 可用性是模型广场的辅助信息，获取失败时不影响模型和价格展示。
	windowDays, bucketMinutes := s.resolveMarketplaceAvailabilityWindow(ctx)
	availabilityMap, err := s.availabilityRepo.GetSummaryByGroupIDs(ctx, groupIDs, windowDays, bucketMinutes, timezone, time.Now())
	if err != nil {
		return nil
	}
	return availabilityMap
}

func (s *ModelMarketplaceService) resolveMarketplaceAvailabilityWindow(ctx context.Context) (int, int) {
	if s == nil || s.settingRepo == nil {
		return DefaultMarketplaceAvailabilityWindowDays, DefaultMarketplaceAvailabilityBucketMinutes
	}
	settings, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyMarketplaceAvailabilityWindowDays,
		SettingKeyMarketplaceAvailabilityBucketMinutes,
	})
	if err != nil {
		return DefaultMarketplaceAvailabilityWindowDays, DefaultMarketplaceAvailabilityBucketMinutes
	}
	return parseMarketplaceAvailabilityWindowSettings(settings)
}

func NormalizeMarketplaceAvailabilityWindow(windowDays int, bucketMinutes int) (int, int) {
	if windowDays <= 0 {
		windowDays = DefaultMarketplaceAvailabilityWindowDays
	}
	if bucketMinutes <= 0 {
		bucketMinutes = DefaultMarketplaceAvailabilityBucketMinutes
	}
	windowDays = clampInt(windowDays, minMarketplaceAvailabilityWindowDays, maxMarketplaceAvailabilityWindowDays)
	bucketMinutes = clampInt(bucketMinutes, minMarketplaceAvailabilityBucketMinutes, maxMarketplaceAvailabilityBucketMinutes)

	totalMinutes := windowDays * 24 * 60
	// 限制公开接口返回的桶数量，避免配置过细导致模型广场响应和渲染成本失控。
	if bucketCount := (totalMinutes + bucketMinutes - 1) / bucketMinutes; bucketCount > maxMarketplaceAvailabilityBuckets {
		bucketMinutes = (totalMinutes + maxMarketplaceAvailabilityBuckets - 1) / maxMarketplaceAvailabilityBuckets
	}
	return windowDays, bucketMinutes
}

func parseMarketplaceAvailabilityWindowSettings(settings map[string]string) (int, int) {
	windowDays := DefaultMarketplaceAvailabilityWindowDays
	bucketMinutes := DefaultMarketplaceAvailabilityBucketMinutes
	if parsed, err := strconv.Atoi(strings.TrimSpace(settings[SettingKeyMarketplaceAvailabilityWindowDays])); err == nil && parsed > 0 {
		windowDays = parsed
	}
	if parsed, err := strconv.Atoi(strings.TrimSpace(settings[SettingKeyMarketplaceAvailabilityBucketMinutes])); err == nil && parsed > 0 {
		bucketMinutes = parsed
	}
	return NormalizeMarketplaceAvailabilityWindow(windowDays, bucketMinutes)
}

func clampInt(value int, minValue int, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func marketplaceGroupAvailability(availabilityMap map[int64]*GroupAvailabilitySummary, groupID int64) *GroupAvailabilitySummary {
	if len(availabilityMap) == 0 {
		return nil
	}
	return availabilityMap[groupID]
}

func marketplaceGroupDisplayBrand(group *Group) string {
	if brand := strings.TrimSpace(group.DisplayBrand); brand != "" {
		return brand
	}
	return group.Name
}

type marketplaceDiscountConfig struct {
	reasoningPointRMBUnitPrice float64
	usdExchangeRate            float64
}

func (c marketplaceDiscountConfig) officialPriceRatio(rateMultiplier float64) *float64 {
	ratio := rateMultiplier * c.reasoningPointRMBUnitPrice / c.usdExchangeRate
	if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return nil
	}
	return &ratio
}

func (c marketplaceDiscountConfig) officialPriceRMBEquivalent(rateMultiplier float64) *float64 {
	amount := rateMultiplier * c.reasoningPointRMBUnitPrice
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return nil
	}
	return &amount
}

func (s *ModelMarketplaceService) getOfficialPriceRatioConfig(ctx context.Context) (marketplaceDiscountConfig, bool) {
	if s.settingRepo == nil {
		return marketplaceDiscountConfig{}, false
	}

	settings, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyReasoningPointRMBUnitPrice,
		SettingKeyUSDExchangeRate,
	})
	if err != nil {
		return marketplaceDiscountConfig{}, false
	}

	// 官方价折扣依赖管理员配置，任一配置无效则不展示。
	price, priceOK := parsePositiveMarketplaceSettingFloat(settings[SettingKeyReasoningPointRMBUnitPrice])
	exchangeRate, exchangeRateOK := parsePositiveMarketplaceSettingFloat(settings[SettingKeyUSDExchangeRate])
	if !priceOK || !exchangeRateOK {
		return marketplaceDiscountConfig{}, false
	}

	return marketplaceDiscountConfig{
		reasoningPointRMBUnitPrice: price,
		usdExchangeRate:            exchangeRate,
	}, true
}

func parsePositiveMarketplaceSettingFloat(raw string) (float64, bool) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

func (s *ModelMarketplaceService) listPublicModelsForGroup(ctx context.Context, group *Group) []ModelMarketplaceModel {
	return s.buildPublicModelsForGroup(ctx, group, s.resolveGroupModels(ctx, group))
}

// listPublicModelsForGroupWithAccounts 使用本次模型广场请求预取的分组账号。
func (s *ModelMarketplaceService) listPublicModelsForGroupWithAccounts(ctx context.Context, group *Group, accounts []Account) []ModelMarketplaceModel {
	return s.buildPublicModelsForGroup(ctx, group, s.resolveGroupModelsWithAccounts(ctx, group, accounts))
}

func (s *ModelMarketplaceService) buildPublicModelsForGroup(ctx context.Context, group *Group, modelDefs []marketplaceModelDef) []ModelMarketplaceModel {
	if len(modelDefs) == 0 {
		return nil
	}

	imageConfig := &ImagePriceConfig{
		Price1K: group.ImagePrice1K,
		Price2K: group.ImagePrice2K,
		Price4K: group.ImagePrice4K,
	}

	models := make([]ModelMarketplaceModel, 0, len(modelDefs))
	for _, modelDef := range modelDefs {
		pricing := unknownDisplayPricing()
		if s.billingService != nil && !modelDef.PricingAmbiguous {
			pricing = s.getRequestableModelDisplayPricing(ctx, group, modelDef, imageConfig)
		}

		models = append(models, ModelMarketplaceModel{
			ID:          modelDef.ID,
			DisplayName: modelDef.DisplayName,
			Pricing:     pricing,
		})
	}

	return models
}

// getRequestableModelDisplayPricing 使用共享解析器确定的定价模型，避免展示层再次推导映射链。
func (s *ModelMarketplaceService) getRequestableModelDisplayPricing(ctx context.Context, group *Group, model marketplaceModelDef, imageConfig *ImagePriceConfig) ModelDisplayPricing {
	pricingModel := strings.TrimSpace(model.PricingModel)
	if pricingModel == "" {
		pricingModel = model.ID
	}
	if group == nil || group.Platform != PlatformQoder {
		return s.getPublicModelDisplayPricing(ctx, group, pricingModel, imageConfig)
	}

	imageRateMultiplier := marketplaceImageRateMultiplier(group)
	if s.gatewayService != nil && s.gatewayService.resolver != nil {
		groupID := group.ID
		resolved := s.gatewayService.resolver.Resolve(ctx, PricingInput{Model: pricingModel, GroupID: &groupID, Group: group})
		if resolved.HasEffectiveOverridePricing() {
			return s.billingService.getDisplayPricingWithResolvedMultipliers(pricingModel, group.RateMultiplier, imageRateMultiplier, imageConfig, resolved)
		}
	}
	// Qoder 内置别名和路由键必须由渠道手工定价，不能回退到默认模型价格。
	if qoderAliasRequiresManualPricingAny(pricingModel) {
		return unknownDisplayPricing()
	}
	if qoderCanUseDefaultDisplayPricing(s.billingService, pricingModel) {
		return s.billingService.getDisplayPricing(pricingModel, group.RateMultiplier, imageRateMultiplier, imageConfig)
	}
	return unknownDisplayPricing()
}

func (s *ModelMarketplaceService) getPublicModelDisplayPricing(ctx context.Context, group *Group, model string, imageConfig *ImagePriceConfig, baseModelHints ...string) ModelDisplayPricing {
	if s.billingService == nil {
		return unknownDisplayPricing()
	}
	imageRateMultiplier := marketplaceImageRateMultiplier(group)
	if group != nil && group.Platform == PlatformQoder {
		billingModel := strings.TrimSpace(model)
		baseHint := firstNonEmptyMarketplaceHint(baseModelHints...)
		if s.gatewayService != nil && s.gatewayService.resolver != nil {
			billingModel, _, _ = s.qoderMarketplacePricingModels(ctx, group, model, baseHint)
			resolved, pricingModel := s.gatewayService.resolveQoderChannelPricingForUsage(ctx, billingModel, &APIKey{Group: group})
			if resolved != nil && resolved.HasEffectiveChannelPricing() {
				return s.billingService.getDisplayPricingWithResolvedMultipliers(pricingModel, group.RateMultiplier, imageRateMultiplier, imageConfig, resolved)
			}
		}
		if qoderAliasRequiresManualPricingAny(billingModel) || !qoderCanUseDefaultDisplayPricing(s.billingService, billingModel) {
			return unknownDisplayPricing()
		}
		pricing := s.billingService.getDisplayPricing(billingModel, group.RateMultiplier, imageRateMultiplier, imageConfig)
		if pricing.PriceStatus != "unpriced" {
			return pricing
		}
		return unknownDisplayPricing()
	}
	if s.gatewayService != nil && s.gatewayService.resolver != nil {
		groupID := group.ID
		resolved := s.gatewayService.resolver.Resolve(ctx, PricingInput{
			Model:   model,
			GroupID: &groupID,
			Group:   group,
		})
		return s.billingService.getDisplayPricingWithResolvedMultipliers(model, group.RateMultiplier, imageRateMultiplier, imageConfig, resolved)
	}
	return s.billingService.getDisplayPricing(model, group.RateMultiplier, imageRateMultiplier, imageConfig)
}

// marketplaceImageRateMultiplier 返回模型广场图片价格应使用的倍率。
func marketplaceImageRateMultiplier(group *Group) float64 {
	if group == nil {
		return 1
	}
	if !group.ImageRateIndependent {
		return group.RateMultiplier
	}
	if group.ImageRateMultiplier < 0 {
		return 0
	}
	return group.ImageRateMultiplier
}

func qoderCanUseDefaultDisplayPricing(billingService *BillingService, model string) bool {
	if !looksLikeImageModel(model) {
		return true
	}
	if qoderKnownDefaultImagePricingModel(model) {
		return true
	}
	return billingService != nil && hasExplicitImageGenerationPricing(billingService.getRawModelPricing(model))
}

func firstNonEmptyMarketplaceHint(hints ...string) string {
	for _, hint := range hints {
		if trimmed := strings.TrimSpace(hint); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (s *ModelMarketplaceService) qoderMarketplacePricingModels(ctx context.Context, group *Group, model string, baseHintOverride string) (billingModel, billingSource, baseHint string) {
	billingModel = strings.TrimSpace(model)
	billingSource = BillingModelSourceRequested
	if trimmed := strings.TrimSpace(baseHintOverride); trimmed != "" {
		baseHint = trimmed
	} else if info, ok := lookupQoderModelAlias(billingModel); ok {
		baseHint = strings.TrimSpace(info.Key)
	}
	if s == nil || s.gatewayService == nil || s.gatewayService.channelService == nil || group == nil {
		return billingModel, billingSource, baseHint
	}

	mapping := s.gatewayService.channelService.ResolveChannelMapping(ctx, group.ID, billingModel)
	if mapping.BillingModelSource != "" {
		billingSource = mapping.BillingModelSource
	}
	if !mapping.Mapped {
		return billingModel, billingSource, baseHint
	}
	baseHint = strings.TrimSpace(mapping.MappedModel)
	switch billingSource {
	case BillingModelSourceRequested:
		return billingModel, billingSource, baseHint
	case BillingModelSourceUpstream, BillingModelSourceChannelMapped:
		return baseHint, billingSource, baseHint
	default:
		return baseHint, BillingModelSourceChannelMapped, baseHint
	}
}

func (s *ModelMarketplaceService) resolveGroupModels(ctx context.Context, group *Group) []marketplaceModelDef {
	if s.gatewayService != nil && group != nil {
		groupID := group.ID
		resolution := s.gatewayService.ResolveRequestableModels(ctx, &groupID, group.Platform)
		if len(resolution.Models) > 0 {
			return buildMarketplaceModelDefsFromRequestable(resolution.Models, group.Platform)
		}
		// 已完成账号和渠道解析后，空结果必须保持为空，不能再次回退平台默认模型。
		return nil
	}

	if group == nil {
		return nil
	}
	return defaultMarketplaceModelDefs(group.Platform)
}

// resolveGroupModelsWithAccounts 直接使用预取账号生成候选和执行 R -> C -> U 校验。
func (s *ModelMarketplaceService) resolveGroupModelsWithAccounts(ctx context.Context, group *Group, accounts []Account) []marketplaceModelDef {
	if s == nil || s.gatewayService == nil || group == nil {
		return nil
	}
	groupID := group.ID
	baseModels := configuredRequestModelsFromAccounts(accounts, group.Platform)
	resolution := s.gatewayService.resolveRequestableModelsWithAccounts(ctx, &groupID, group.Platform, baseModels, accounts)
	if len(resolution.Models) == 0 {
		return nil
	}
	return buildMarketplaceModelDefsFromRequestable(resolution.Models, group.Platform)
}

type marketplaceModelDef struct {
	ID               string
	DisplayName      string
	BaseModelHint    string
	PricingModel     string
	PricingAmbiguous bool
}

func buildMarketplaceModelDefsFromRequestable(models []RequestableModel, platform string) []marketplaceModelDef {
	displayNames := marketplaceDisplayNameLookup(platform)
	defs := make([]marketplaceModelDef, 0, len(models))
	for _, model := range models {
		defs = append(defs, marketplaceModelDef{
			ID:               model.ID,
			DisplayName:      lookupMarketplaceDisplayName(model.ID, displayNames),
			PricingModel:     model.PricingModel,
			PricingAmbiguous: model.PricingAmbiguous,
		})
	}
	return defs
}

func defaultMarketplaceModelDefs(platform string) []marketplaceModelDef {
	switch platform {
	case PlatformOpenAI:
		models := make([]marketplaceModelDef, 0, len(openai.DefaultModels))
		for _, model := range openai.DefaultModels {
			models = append(models, marketplaceModelDef{
				ID:          model.ID,
				DisplayName: model.DisplayName,
			})
		}
		return models
	case PlatformAnthropic:
		models := make([]marketplaceModelDef, 0, len(claude.DefaultModels))
		for _, model := range claude.DefaultModels {
			models = append(models, marketplaceModelDef{
				ID:          model.ID,
				DisplayName: model.DisplayName,
			})
		}
		return models
	case PlatformGemini:
		models := make([]marketplaceModelDef, 0, len(geminicli.DefaultModels))
		for _, model := range geminicli.DefaultModels {
			models = append(models, marketplaceModelDef{
				ID:          model.ID,
				DisplayName: model.DisplayName,
			})
		}
		return models
	case PlatformGrok:
		defaultModels := xai.DefaultModels()
		models := make([]marketplaceModelDef, 0, len(defaultModels))
		for _, model := range defaultModels {
			models = append(models, marketplaceModelDef{
				ID:          model.ID,
				DisplayName: model.DisplayName,
			})
		}
		return models
	case PlatformAntigravity:
		defaultModels := antigravity.DefaultModels()
		models := make([]marketplaceModelDef, 0, len(defaultModels))
		for _, model := range defaultModels {
			models = append(models, marketplaceModelDef{
				ID:          model.ID,
				DisplayName: model.DisplayName,
			})
		}
		return models
	case PlatformQoder:
		models := make([]marketplaceModelDef, 0, len(defaultQoderModelAliases))
		models = append(models, qoderDefaultPublicModels()...)
		return models
	default:
		return nil
	}
}

func marketplaceDisplayNameLookup(platform string) map[string]string {
	switch platform {
	case PlatformOpenAI:
		out := make(map[string]string, len(openai.DefaultModels))
		for _, model := range openai.DefaultModels {
			registerMarketplaceDisplayName(out, model.ID, model.DisplayName)
		}
		return out
	case PlatformAnthropic:
		out := make(map[string]string, len(claude.DefaultModels))
		for _, model := range claude.DefaultModels {
			registerMarketplaceDisplayName(out, model.ID, model.DisplayName)
		}
		return out
	case PlatformGemini:
		out := make(map[string]string, len(geminicli.DefaultModels))
		for _, model := range geminicli.DefaultModels {
			registerMarketplaceDisplayName(out, model.ID, model.DisplayName)
		}
		return out
	case PlatformGrok:
		defaultModels := xai.DefaultModels()
		out := make(map[string]string, len(defaultModels))
		for _, model := range defaultModels {
			registerMarketplaceDisplayName(out, model.ID, model.DisplayName)
		}
		return out
	case PlatformAntigravity:
		defaultModels := antigravity.DefaultModels()
		out := make(map[string]string, len(defaultModels))
		for _, model := range defaultModels {
			registerMarketplaceDisplayName(out, model.ID, model.DisplayName)
		}
		return out
	case PlatformQoder:
		out := make(map[string]string, len(defaultQoderModelAliases))
		for _, model := range qoderDefaultPublicModels() {
			registerMarketplaceDisplayName(out, model.ID, model.DisplayName)
		}
		return out
	default:
		return nil
	}
}

func qoderDefaultPublicModels() []marketplaceModelDef {
	models := make([]marketplaceModelDef, 0, len(defaultQoderModelAliases))
	for alias, info := range defaultQoderModelAliases {
		displayName := info.DisplayName
		if displayName == "" {
			displayName = alias
		}
		models = append(models, marketplaceModelDef{
			ID:          alias,
			DisplayName: displayName,
		})
	}
	sortMarketplaceModelDefs(models)
	return models
}

func sortMarketplaceModelDefs(models []marketplaceModelDef) {
	for i := 1; i < len(models); i++ {
		for j := i; j > 0 && models[j-1].ID > models[j].ID; j-- {
			models[j-1], models[j] = models[j], models[j-1]
		}
	}
}

func lookupMarketplaceDisplayName(modelID string, displayNames map[string]string) string {
	for _, candidate := range marketplaceLookupCandidates(modelID) {
		if displayName, ok := displayNames[candidate]; ok && strings.TrimSpace(displayName) != "" {
			return displayName
		}
	}
	return modelID
}

func registerMarketplaceDisplayName(out map[string]string, modelID string, displayName string) {
	for _, key := range marketplaceLookupCandidates(modelID) {
		if _, exists := out[key]; exists {
			continue
		}
		out[key] = displayName
	}
}

func marketplaceLookupCandidates(modelID string) []string {
	candidates := []string{
		strings.TrimSpace(modelID),
		strings.TrimPrefix(strings.TrimSpace(modelID), "models/"),
	}

	trimmed := strings.TrimSpace(modelID)
	if idx := strings.LastIndex(trimmed, "/models/"); idx != -1 {
		candidates = append(candidates, trimmed[idx+len("/models/"):])
	}
	if idx := strings.LastIndex(trimmed, "/"); idx != -1 {
		candidates = append(candidates, trimmed[idx+1:])
	}

	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}
