package service

import (
	"context"
)

func (s *CreativePublicService) ReconcileCreativeTransientOnce(ctx context.Context) (int, error) {
	return s.nativeResults().ReconcileCreativeTransientOnce(ctx)
}

func (s *CreativePublicService) RunCreativeTransientReconciler(ctx context.Context) {
	s.nativeResults().RunCreativeTransientReconciler(ctx)
}
