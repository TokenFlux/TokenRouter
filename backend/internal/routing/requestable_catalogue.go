package routing

import "context"

// RequestableCatalogue 组合唯一短缓存与原账号查询；自身不持有第二份缓存或计费实例。
type RequestableCatalogue struct {
	Models   *ModelList
	Read     func(context.Context, *int64) ([]CatalogueAccount, error)
	Resolver RequestableResolver
	Warn     func(string, ...any)
}

func (c *RequestableCatalogue) Prefetch(ctx context.Context) ([]CatalogueAccount, bool, error) {
	if c == nil || c.Read == nil {
		return nil, false, nil
	}
	values, err := c.Read(ctx, nil)
	return values, err == nil, err
}

// ResolveRequestableModels 保留先取基础目录再查询候选、以及查询失败后的原降级。
func (c *RequestableCatalogue) ResolveRequestableModels(ctx context.Context, groupID *int64, platform string) RequestableModelsResult {
	if c == nil || c.Read == nil {
		return RequestableModelsResult{}
	}
	models := c.Models
	if models == nil {
		models = &ModelList{Read: c.Read}
	}
	base := models.Available(ctx, groupID, platform)
	accounts, err := c.Read(ctx, groupID)
	if err != nil {
		if c.Warn != nil {
			var id int64
			if groupID != nil {
				id = *groupID
			}
			c.Warn("failed to load accounts for requestable model resolution", "group_id", id, "platform", platform, "error", err)
		}
		result := RequestableModelsFallback(base, platform, c.Resolver.Defaults)
		result.HadExplicitAccountModels = len(base) > 0
		return result
	}
	return c.Resolver.ResolveWithAccounts(ctx, groupID, platform, base, accounts)
}
