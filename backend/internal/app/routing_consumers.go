// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	context "context"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// keyGroups 投影旧路由能力，直接读取新 routing；不持有分组或认证缓存。
type keyGroups struct{ Repository routing.GroupRepository }

func (p keyGroups) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	v, e := p.Repository.GetByID(ctx, id)
	return apikey.GroupFromRouting(v), e
}
func (p keyGroups) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	v, e := p.Repository.GetByIDLite(ctx, id)
	return apikey.GroupFromRouting(v), e
}
func (p keyGroups) ListActive(ctx context.Context) ([]routing.Group, error) {
	v, e := p.Repository.ListActive(ctx)
	if v == nil {
		return nil, e
	}
	out := make([]routing.Group, len(v))
	for i := range v {
		out[i] = *apikey.GroupFromRouting(&v[i])
	}
	return out, e
}
func (p keyGroups) FindDefault(ctx context.Context, platform string) (*routing.Group, error) {
	v, e := routing.FindPlatformDefaultGroup(ctx, p.Repository, platform)
	return apikey.GroupFromRouting(v), e
}
func keyGroupFastPolicy(raw string, force bool) string {
	return (&routing.Group{OpenAIFastPolicy: raw, ForceOpenAIFast: force}).EffectiveOpenAIFastPolicy()
}

// identityAdminGroups 只提供用户管理所需分组字段，直接读取新 routing。
type identityAdminGroups struct{ Repository routing.GroupRepository }

func (p identityAdminGroups) GetByID(ctx context.Context, id int64) (*identity.AdminGroup, error) {
	v, e := p.Repository.GetByID(ctx, id)
	return identityAdminGroup(v), e
}
func (p identityAdminGroups) GetByIDLite(ctx context.Context, id int64) (*identity.AdminGroup, error) {
	v, e := p.Repository.GetByIDLite(ctx, id)
	return identityAdminGroup(v), e
}
func identityAdminGroup(v *routing.Group) *identity.AdminGroup {
	if v == nil {
		return nil
	}
	return &identity.AdminGroup{ID: v.ID, Name: v.Name, Status: v.Status, IsExclusive: v.IsExclusive, RPMLimit: v.RPMLimit}
}

// billingGroups 只读取套餐展示所需名称，直接读取新 routing。
type billingGroups struct{ Repository routing.GroupRepository }

func (b billingGroups) GetByIDLite(ctx context.Context, id int64) (*billing.SubscriptionPlanGroup, error) {
	if b.Repository == nil {
		return nil, nil
	}
	g, err := b.Repository.GetByIDLite(ctx, id)
	if err != nil || g == nil {
		return nil, err
	}
	return &billing.SubscriptionPlanGroup{ID: g.ID, Name: g.Name}, nil
}
