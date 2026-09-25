package dto_test

import (
	"encoding/json"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	// 记录作为回归夹具输入，实际 DTO 由所属模块生成。
)

func UserFromServiceShallow(v *identity.User) *dto.User[json.RawMessage] {
	return dto.UserFromIdentityShallow[json.RawMessage](identity.CopyUser(v))
}
func UserFromServiceAdmin(v *identity.User) *dto.AdminUser[json.RawMessage] {
	return dto.AdminUserFromIdentity[json.RawMessage](identity.CopyUser(v), nil)
}
