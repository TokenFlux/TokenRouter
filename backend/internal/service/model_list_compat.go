// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// LegacyModelListReader 只提供原账号查询和模型配置投影，不创建缓存。
func LegacyModelListReader(repo AccountRepository) func(context.Context, *int64) ([]routing.CatalogueAccount, error) {
	return func(ctx context.Context, id *int64) ([]routing.CatalogueAccount, error) {
		var values []Account
		var err error
		if id != nil {
			values, err = repo.ListSchedulableByGroupID(ctx, *id)
		} else {
			values, err = repo.ListSchedulable(ctx)
		}
		if err != nil {
			return nil, err
		}
		return legacyCatalogueAccounts(nil, values), nil
	}
}
func (s *GatewayService) modelListCore() *routing.ModelList {
	if s.modelsList != nil {
		return s.modelsList
	}
	return &routing.ModelList{Cache: s.modelsListCache, TTL: s.modelsListCacheTTL, Read: LegacyModelListReader(s.accountRepo)}
}
