// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

func (s *ModelMarketplaceService) getPublicModelDisplayPricing(ctx context.Context, group *Group, model string) ModelDisplayPricing {
	return s.marketplaceCore().PublicModelPricing(ctx, groupRules(group), model)
}
func (s *ModelMarketplaceService) getRequestableModelDisplayPricing(ctx context.Context, group *Group, model marketplaceModelDef) ModelDisplayPricing {
	return s.marketplaceCore().RequestableModelPricing(ctx, groupRules(group), model)
}
func (s *ModelMarketplaceService) listPublicModelsForGroup(ctx context.Context, group *Group) []ModelMarketplaceModel {
	return s.marketplaceCore().ModelsForGroup(ctx, groupRules(group))
}
func (s *ModelMarketplaceService) buildPublicModelsForGroup(ctx context.Context, group *Group, models []marketplaceModelDef) []ModelMarketplaceModel {
	return s.marketplaceCore().BuildPublicModels(ctx, groupRules(group), models)
}
func (s *ModelMarketplaceService) marketplaceModelModalities(model marketplaceModelDef) ([]string, []string) {
	return s.marketplaceCore().ModelModalities(model)
}

func (s *ModelMarketplaceService) prefetchPublicGroupAccounts(ctx context.Context) (map[int64][]routing.CatalogueAccount, bool) {
	return s.marketplaceCore().PrefetchAccounts(ctx)
}
