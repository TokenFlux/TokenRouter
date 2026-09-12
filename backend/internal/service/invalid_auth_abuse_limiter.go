// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	time "time"
)

type InvalidAuthAbuseHealth = apikey.InvalidAuthAbuseHealth

// CheckInvalidAuthAbuse 委托 Key 模块的唯一实现。
func (s *APIKeyService) CheckInvalidAuthAbuse(clientKey string) (time.Duration, bool) {
	return s.APIKeyService.CheckInvalidAuthAbuse(clientKey)
}

// RecordInvalidAuthFailure 委托 Key 模块的唯一实现。
func (s *APIKeyService) RecordInvalidAuthFailure(clientKey string) {
	s.APIKeyService.RecordInvalidAuthFailure(clientKey)
}

// InvalidAuthAbuseHealth 委托 Key 模块的唯一实现。
func (s *APIKeyService) InvalidAuthAbuseHealth() InvalidAuthAbuseHealth {
	return s.APIKeyService.InvalidAuthAbuseHealth()
}
