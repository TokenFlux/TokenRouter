package service

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/claude"
	"github.com/TokenFlux/TokenRouter/internal/pkg/geminicli"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai"
)

type ModelMarketplaceGroup struct {
	ID                 int64
	Name               string
	Description        string
	Platform           string
	DisplayBrand       string
	SortOrder          int
	RateMultiplier     float64
	OfficialPriceRatio *float64
	ModelCount         int
	Models             []ModelMarketplaceModel
}

type ModelMarketplaceModel struct {
	ID          string
	DisplayName string
	Pricing     ModelDisplayPricing
}

type ModelMarketplaceService struct {
	groupRepo      GroupRepository
	settingRepo    SettingRepository
	gatewayService *GatewayService
	billingService *BillingService
}

func NewModelMarketplaceService(
	groupRepo GroupRepository,
	settingRepo SettingRepository,
	gatewayService *GatewayService,
	billingService *BillingService,
) *ModelMarketplaceService {
	return &ModelMarketplaceService{
		groupRepo:      groupRepo,
		settingRepo:    settingRepo,
		gatewayService: gatewayService,
		billingService: billingService,
	}
}

func (s *ModelMarketplaceService) ListPublic(ctx context.Context) ([]ModelMarketplaceGroup, error) {
	groups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active groups: %w", err)
	}

	discountConfig, showDiscount := s.getOfficialPriceRatioConfig(ctx)
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
		if showDiscount {
			officialPriceRatio = discountConfig.officialPriceRatio(group.RateMultiplier)
		}
		out = append(out, ModelMarketplaceGroup{
			ID:                 group.ID,
			Name:               group.Name,
			Description:        group.Description,
			Platform:           group.Platform,
			DisplayBrand:       marketplaceGroupDisplayBrand(group),
			SortOrder:          group.SortOrder,
			RateMultiplier:     group.RateMultiplier,
			OfficialPriceRatio: officialPriceRatio,
			ModelCount:         len(models),
			Models:             models,
		})
	}

	return out, nil
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
		pricing := unknownDisplayPricing()
		if s.billingService != nil {
			pricing = s.getPublicModelDisplayPricing(ctx, group, modelDef.ID, imageConfig)
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
	default:
		return nil
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
