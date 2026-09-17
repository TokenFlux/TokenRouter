package dto_test

import (
	"encoding/json"

	native "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/service" // 旧记录只作为迁移回归夹具输入，实际 DTO 由新模块生成。
)

func UserFromServiceShallow(v *service.User) *native.User[json.RawMessage] {
	return native.UserFromIdentityShallow[json.RawMessage](service.IdentityUser(v))
}
func UserFromServiceAdmin(v *service.User) *native.AdminUser[json.RawMessage] {
	return native.AdminUserFromIdentity[json.RawMessage](service.IdentityUser(v), nil)
}
