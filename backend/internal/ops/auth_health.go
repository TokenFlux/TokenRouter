package ops

import "context"

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
