//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package handler

import (
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
)

func validateAPIKeyCreateRequest(req CreateAPIKeyRequest) error {
	return keyhttp.ValidateAPIKeyCreateRequest(req)
}

func validateAPIKeyUpdateRequest(req UpdateAPIKeyRequest) error {
	return keyhttp.ValidateAPIKeyUpdateRequest(req)
}
