// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	slog "log/slog"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// ResolveRequestableModels 统一解析模型列表中的 R -> C -> U 链路。
// 只有至少一个平台匹配账号能够处理的客户端模型才会出现在结果中。
func (s *GatewayService) ResolveRequestableModels(ctx context.Context, groupID *int64, platform string) routing.RequestableModelsResult {
	return s.modelCatalogueCore().ResolveRequestableModels(ctx, groupID, platform)
}

// BindModelCatalogue 只在组合根开放请求前绑定，同一实例被市场与请求目录共用。
func (s *GatewayService) BindModelCatalogue(value *routing.RequestableCatalogue) { s.catalogue = value }

// modelCatalogueCore 为手工测试装配保留原数据端口；生产直接使用 app 的原生目录。
func (s *GatewayService) modelCatalogueCore() *routing.RequestableCatalogue {
	if s == nil {
		return nil
	}
	if s.catalogue != nil {
		return s.catalogue
	}
	var read func(context.Context, *int64) ([]routing.CatalogueAccount, error)
	if s.accountRepo != nil {
		read = LegacyModelListReader(s.accountRepo)
	}
	return &routing.RequestableCatalogue{Models: s.modelListCore(), Read: read, Resolver: s.requestableModelResolver(), Warn: slog.Warn}
}
