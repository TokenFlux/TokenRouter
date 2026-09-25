package dto_test

import (
	"encoding/json"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	// 记录作为回归夹具输入，实际 DTO 由所属模块生成。
)

func GroupFromService(v *routing.Group) *dto.Group {
	return dto.GroupFromRouting(routing.CloneGroup(v))
}
func GroupFromServiceAdmin(v *routing.Group) *dto.AdminGroup[json.RawMessage] {
	return dto.AdminGroupFromRouting[json.RawMessage](routing.CloneGroup(v))
}
