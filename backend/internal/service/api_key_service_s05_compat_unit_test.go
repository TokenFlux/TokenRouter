//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package service

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
)

const apiKeyLimitUpperBound = apikey.KeyApiKeyLimitUpperBound

// validateCreateAPIKeyRequest 委托 Key 模块的唯一实现。
func validateCreateAPIKeyRequest(req CreateAPIKeyRequest) error {
	return apikey.KeyValidateCreateAPIKeyRequest(req)
}

// validateUpdateAPIKeyRequest 委托 Key 模块的唯一实现。
func validateUpdateAPIKeyRequest(req UpdateAPIKeyRequest) error {
	return apikey.KeyValidateUpdateAPIKeyRequest(req)
}

// checkTeamMemberLimitSnapshot 委托 Key 模块的唯一实现。
func checkTeamMemberLimitSnapshot(member *TeamMembership) error {
	return apikey.KeyCheckTeamMemberLimitSnapshot(member)
}
