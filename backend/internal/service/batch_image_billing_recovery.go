package service

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
)

type BatchImageBillingRecoveryService struct {
	Repo       BatchImageRepository
	Billing    UsageBillingRepository
	AuthCache  APIKeyAuthCacheInvalidator
	Queue      BatchImageQueue
	StaleAfter time.Duration
	Limit      int
}

func (s *BatchImageBillingRecoveryService) ReleaseStaleUnsubmittedOnce(ctx context.Context) (int, error) {
	return s.nativeRecovery().ReleaseStaleUnsubmittedOnce(ctx)
}

func (s *BatchImageBillingRecoveryService) nativeRecovery() *batchimage.BillingRecovery {
	if s == nil {
		return nil
	}
	out := &batchimage.BillingRecovery{Repo: s.Repo, Funding: batchFundingProjection(s.Billing), Queue: s.Queue, StaleAfter: s.StaleAfter, Limit: s.Limit, Observe: creativeLegacyObserve}
	if s.AuthCache != nil {
		out.InvalidateAuth = s.AuthCache.InvalidateAuthCacheByUserID
	}
	return out
}
