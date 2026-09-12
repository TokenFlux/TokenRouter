// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
)

type AuthCacheInvalidationEvent = apikey.AuthCacheInvalidationEvent

type AuthCacheInvalidationOutboxStats = apikey.AuthCacheInvalidationOutboxStats

type AuthCacheInvalidationOutboxRepository = apikey.AuthCacheInvalidationOutboxRepository

type AuthCacheInvalidationHealth = apikey.AuthCacheInvalidationHealth

type OpsAuthCacheInvalidationHealth = apikey.OpsAuthCacheInvalidationHealth

func (s *OpsService) GetAuthCacheInvalidationHealth(ctx context.Context) OpsAuthCacheInvalidationHealth {
	if s == nil {
		return OpsAuthCacheInvalidationHealth{}
	}
	health := OpsAuthCacheInvalidationHealth{}
	if s.authCacheInvalidationWorker != nil {
		health.Outbox = s.authCacheInvalidationWorker.Health(ctx)
	}
	if s.apiKeyService != nil {
		health.Subscriber = s.apiKeyService.AuthCacheInvalidationSubscriberHealth()
		health.Lookup = s.apiKeyService.AuthLookupMetrics()
		health.InvalidAbuse = s.apiKeyService.InvalidAuthAbuseHealth()
	}
	return health
}

type AuthCacheInvalidationWorker = apikey.AuthCacheInvalidationWorker

func NewAuthCacheInvalidationWorker(repo AuthCacheInvalidationOutboxRepository, cache APIKeyCache, local ...*APIKeyService) *AuthCacheInvalidationWorker {
	var cores []*apikey.APIKeyService
	for _, s := range local {
		if s != nil {
			cores = append(cores, s.APIKeyService)
		} else {
			cores = append(cores, nil)
		}
	}
	return apikey.NewAuthCacheInvalidationWorker(repo, cache, cores...)
}

// ProvideAuthCacheInvalidationWorker 委托 Key 模块的唯一实现。
func ProvideAuthCacheInvalidationWorker(repo AuthCacheInvalidationOutboxRepository, cache APIKeyCache, apiKeyService *APIKeyService) *AuthCacheInvalidationWorker {
	var core *apikey.APIKeyService
	if apiKeyService != nil {
		core = apiKeyService.APIKeyService
	}
	return apikey.ProvideAuthCacheInvalidationWorker(repo, cache, core)
}
