package dto_test

import (
	"encoding/json"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// 记录作为回归夹具输入，实际 DTO 由所属模块生成。
func APIKeyFromService(v *apikey.APIKey) *dto.APIKey[json.RawMessage] {
	return dto.APIKeyFromKey(apikey.CopyAPIKey(v), func(*routing.Group) *json.RawMessage { return nil })
}
