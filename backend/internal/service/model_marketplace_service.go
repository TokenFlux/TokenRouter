package service

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/claude"
	"github.com/TokenFlux/TokenRouter/internal/pkg/geminicli"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai"
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

func (s *ModelMarketplaceService) ListPublic(ctx context.Context) ([]ModelMarketplaceGroup, error) {
	groups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active groups: %w", err)
	}

	discountConfig, showDiscount := s.getOfficialPriceRatioConfig(ctx)
	capacityMap := s.getPublicCapacityMap(ctx, groups)
	availabilityMap := s.getPublicAvailabilityMap(ctx, groups)
	out := make([]ModelMarketplaceGroup, 0, len(groups))
	for i := range groups {
		group := &groups[i]
		if group.IsExclusive || group.ActiveAccountCount <= 0 {
			continue
		}

		models := s.listPublicModelsForGroup(ctx, group)
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
	modelDefs := s.resolveGroupModels(ctx, group)
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
		pricingModel := modelDef.ID
		pricing := unknownDisplayPricing()
		if s.billingService != nil {
			pricing = s.getPublicModelDisplayPricing(ctx, group, pricingModel, imageConfig)
		}

		models = append(models, ModelMarketplaceModel{
			ID:          modelDef.ID,
			DisplayName: modelDef.DisplayName,
			Pricing:     pricing,
		})
	}

	return models
}

func (s *ModelMarketplaceService) getPublicModelDisplayPricing(ctx context.Context, group *Group, model string, imageConfig *ImagePriceConfig) ModelDisplayPricing {
	if s.billingService == nil {
		return unknownDisplayPricing()
	}
	if group != nil && group.Platform == PlatformQoder {
		if s.gatewayService != nil && s.gatewayService.resolver != nil {
			groupID := group.ID
			resolved := s.gatewayService.resolver.Resolve(ctx, PricingInput{
				Model:   model,
				GroupID: &groupID,
			})
			if resolved.Source == PricingSourceChannel {
				return s.billingService.getDisplayPricingWithResolved(model, group.RateMultiplier, imageConfig, resolved)
			}
		}
		if isQoderAliasBillingModel(model) {
			return s.billingService.GetDisplayPricing(qoderDefaultAliasFallbackBillingModel, group.RateMultiplier, imageConfig)
		}
		return unknownDisplayPricing()
	}
	if s.gatewayService != nil && s.gatewayService.resolver != nil {
		groupID := group.ID
		resolved := s.gatewayService.resolver.Resolve(ctx, PricingInput{
			Model:   model,
			GroupID: &groupID,
		})
		return s.billingService.getDisplayPricingWithResolved(model, group.RateMultiplier, imageConfig, resolved)
	}
	return s.billingService.GetDisplayPricing(model, group.RateMultiplier, imageConfig)
}

func (s *ModelMarketplaceService) resolveGroupModels(ctx context.Context, group *Group) []marketplaceModelDef {
	if s.gatewayService != nil {
		groupID := group.ID
		modelIDs := s.gatewayService.GetAvailableModels(ctx, &groupID, "")
		if len(modelIDs) > 0 {
			return buildMarketplaceModelDefs(modelIDs, group.Platform)
		}
	}

	return defaultMarketplaceModelDefs(group.Platform)
}

type marketplaceModelDef struct {
	ID          string
	DisplayName string
}

func buildMarketplaceModelDefs(modelIDs []string, platform string) []marketplaceModelDef {
	displayNames := marketplaceDisplayNameLookup(platform)
	seen := make(map[string]struct{}, len(modelIDs))
	models := make([]marketplaceModelDef, 0, len(modelIDs))

	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			continue
		}
		if _, ok := seen[modelID]; ok {
			continue
		}
		seen[modelID] = struct{}{}

		models = append(models, marketplaceModelDef{
			ID:          modelID,
			DisplayName: lookupMarketplaceDisplayName(modelID, displayNames),
		})
	}

	return models
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
