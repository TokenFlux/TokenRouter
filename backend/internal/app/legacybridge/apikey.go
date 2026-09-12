// 本文件维护 legacybridge 的所属能力；兼容入口复用唯一实现。
package legacybridge

import (
	context "context"
	sql "database/sql"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	repository "github.com/TokenFlux/TokenRouter/internal/repository"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// KeyGroups 投影旧路由能力，S06 改绑后删除；不持有分组或认证缓存。
type KeyGroups struct{ Repository service.GroupRepository }

func (p KeyGroups) GetByID(ctx context.Context, id int64) (*apikey.Group, error) {
	v, e := p.Repository.GetByID(ctx, id)
	return service.APIKeyGroupView(v), e
}
func (p KeyGroups) GetByIDLite(ctx context.Context, id int64) (*apikey.Group, error) {
	v, e := p.Repository.GetByIDLite(ctx, id)
	return service.APIKeyGroupView(v), e
}
func (p KeyGroups) ListActive(ctx context.Context) ([]apikey.Group, error) {
	v, e := p.Repository.ListActive(ctx)
	if v == nil {
		return nil, e
	}
	out := make([]apikey.Group, len(v))
	for i := range v {
		out[i] = *service.APIKeyGroupView(&v[i])
	}
	return out, e
}
func (p KeyGroups) FindDefault(ctx context.Context, platform string) (*apikey.Group, error) {
	v, e := service.FindPlatformDefaultGroup(ctx, p.Repository, platform)
	return service.APIKeyGroupView(v), e
}
func KeyGroupFastPolicy(raw string, force bool) string {
	return (&service.Group{OpenAIFastPolicy: raw, ForceOpenAIFast: force}).EffectiveOpenAIFastPolicy()
}

// KeyUsageTotals 保留用量聚合查询入口与排序规则，S08 改绑。
func KeyUsageTotals(db *sql.DB, settings *service.PreAggregationSettingsService) keypostgres.UsageTotalsReader {
	return func(ctx context.Context, ids []int64) (map[int64]float64, error) {
		return repository.ReadAPIKeyUsageTotals(ctx, db, settings, ids)
	}
}
