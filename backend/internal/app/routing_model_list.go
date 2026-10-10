package app

import (
	"context"
	"log/slog"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// catalogueReader 查询指定分组或全部可调度提供商，由网关适配器转换模型数据。
func catalogueReader(store *providerpostgres.ProviderStore) func(context.Context, *int64) ([]routing.CatalogueProvider, error) {
	return func(ctx context.Context, id *int64) ([]routing.CatalogueProvider, error) {
		var values []provider.Record
		var err error
		if id != nil {
			values, err = store.ListSchedulableByGroupID(ctx, *id)
		} else {
			values, err = store.ListSchedulable(ctx)
		}
		if err != nil {
			return nil, err
		}
		return gatewayprovider.CatalogueProviders(values), nil
	}
}

// provideRequestableCatalogue 为目录解析绑定提供商存储和分组策略。
func provideRequestableCatalogue(store *providerpostgres.ProviderStore, modelConfigs *routing.PricingConfigService) *routing.RequestableCatalogue {
	return &routing.RequestableCatalogue{Read: catalogueReader(store), Resolver: routing.RequestableResolver{GroupPolicies: modelConfigs, Warn: slog.Warn}, Warn: slog.Warn}
}
