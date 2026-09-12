// 本文件维护 legacybridge 的所属能力；兼容入口复用唯一实现。
package legacybridge

import (
	context "context"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// IdentityAdminGroups 只提供用户管理所需分组字段，S06 改绑。
type IdentityAdminGroups struct{ Repository service.GroupRepository }

func (p IdentityAdminGroups) GetByID(ctx context.Context, id int64) (*identity.AdminGroup, error) {
	v, e := p.Repository.GetByID(ctx, id)
	return identityAdminGroup(v), e
}
func (p IdentityAdminGroups) GetByIDLite(ctx context.Context, id int64) (*identity.AdminGroup, error) {
	v, e := p.Repository.GetByIDLite(ctx, id)
	return identityAdminGroup(v), e
}
func identityAdminGroup(v *service.Group) *identity.AdminGroup {
	if v == nil {
		return nil
	}
	return &identity.AdminGroup{ID: v.ID, Name: v.Name, Status: v.Status, IsExclusive: v.IsExclusive, RPMLimit: v.RPMLimit}
}
