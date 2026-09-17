package dto_test

import (
	"encoding/json"

	native "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/service" // 旧记录只作为迁移回归夹具输入，实际 DTO 由新模块生成。
)

func GroupFromService(v *service.Group) *native.Group {
	return native.GroupFromRouting(service.RoutingGroupView(v))
}
func GroupFromServiceAdmin(v *service.Group) *native.AdminGroup[json.RawMessage] {
	return native.AdminGroupFromRouting[json.RawMessage](service.RoutingGroupView(v))
}
