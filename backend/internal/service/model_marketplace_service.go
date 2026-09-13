// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	antigravity "github.com/TokenFlux/TokenRouter/internal/pkg/antigravity"
	claude "github.com/TokenFlux/TokenRouter/internal/pkg/claude"
	geminicli "github.com/TokenFlux/TokenRouter/internal/pkg/geminicli"
	openai "github.com/TokenFlux/TokenRouter/internal/pkg/openai"
	xai "github.com/TokenFlux/TokenRouter/internal/pkg/xai"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

const DefaultMarketplaceAvailabilityWindowDays = routing.DefaultMarketplaceAvailabilityWindowDays

const DefaultMarketplaceAvailabilityBucketMinutes = routing.DefaultMarketplaceAvailabilityBucketMinutes

type ModelMarketplaceGroup = routing.ModelMarketplaceGroup

type ModelMarketplaceModel = routing.ModelMarketplaceModel

type ModelMarketplaceService struct {
	marketplace      *routing.Marketplace
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
	return s.marketplaceCore().ListPublic(ctx)
}

func NormalizeMarketplaceAvailabilityWindow(windowDays int, bucketMinutes int) (int, int) {
	return routing.NormalizeMarketplaceAvailabilityWindow(windowDays, bucketMinutes)
}

type marketplaceModelDef = routing.MarketplaceModelDef

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

func sortMarketplaceModelDefs(models []marketplaceModelDef) { routing.SortMarketplaceModelDefs(models) }

func registerMarketplaceDisplayName(out map[string]string, modelID string, displayName string) {
	routing.RegisterMarketplaceDisplayName(out, modelID, displayName)
}
