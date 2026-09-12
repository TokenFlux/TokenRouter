// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	context "context"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	dto "github.com/TokenFlux/TokenRouter/internal/handler/dto"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type AdminAPIKeyHandler = keyhttp.AdminAPIKeyHandler[dto.Group]
type AdminUpdateAPIKeyGroupRequest = keyhttp.AdminUpdateAPIKeyGroupRequest

// NewAdminAPIKeyHandler 兼容旧聚合构造，生产直接取得唯一 Key 管理用例。
func NewAdminAPIKeyHandler(a service.AdminService) *AdminAPIKeyHandler {
	var core keyhttp.KeyAdministration = legacyKeyAdministration{a}
	if actual, ok := a.(interface{ KeyAdministration() *apikey.Admin }); ok {
		core = actual.KeyAdministration()
	}
	return keyhttp.NewAdminAPIKeyHandler(core, func(g *apikey.Group) *dto.Group { return dto.GroupFromServiceShallow(service.GroupFromAPIKeyView(g)) })
}

type legacyKeyAdministration struct{ service.AdminService }

func (a legacyKeyAdministration) UpdateManagedFields(ctx context.Context, id int64, gid *int64, reset bool) (*apikey.AdminUpdateAPIKeyGroupIDResult, error) {
	v, e := a.AdminUpdateAPIKeyFields(ctx, id, gid, reset)
	if v == nil {
		return nil, e
	}
	return &apikey.AdminUpdateAPIKeyGroupIDResult{APIKey: service.APIKeyView(v.APIKey), AutoGrantedGroupAccess: v.AutoGrantedGroupAccess, GrantedGroupID: v.GrantedGroupID, GrantedGroupName: v.GrantedGroupName}, e
}
