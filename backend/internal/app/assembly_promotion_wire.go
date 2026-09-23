//go:build wireinject

package app

import (
	"github.com/google/wire"
)

// promotion 模块的组合根登记；这里只分组原 provider，不创建资源或复制业务实现。
var promotionAssemblyProviders = wire.NewSet(
	providePromotionPromoHTTP,
	providePromotionAffiliateHTTP,
	providePromotionPromoStore,
	providePromotionPromo,
	providePromotionAffiliateStore,
	providePromotionAffiliate,
	providePromotionSettings,
	providePromotionUserHTTP,
)
