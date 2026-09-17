package dto_test

import (
	"encoding/json"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	native "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// 旧记录只作为迁移回归夹具输入，实际 DTO 由新模块生成。
func APIKeyFromService(v *service.APIKey) *native.APIKey[json.RawMessage] {
	return native.APIKeyFromKey(service.APIKeyView(v), func(*apikey.Group) *json.RawMessage { return nil })
}
