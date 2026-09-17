package service

import (
	"context"
)

func (s *CreativePublicService) ReconcileCreativeOutboxOnce(ctx context.Context) (int, error) {
	return s.nativeResults().ReconcileCreativeOutboxOnce(ctx)
}

func (s *CreativePublicService) RunCreativeOutboxReconciler(ctx context.Context) {
	s.nativeResults().RunCreativeOutboxReconciler(ctx)
}
