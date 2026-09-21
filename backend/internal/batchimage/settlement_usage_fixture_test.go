//go:build unit

package batchimage_test

import (
	"context"

	usagecore "github.com/TokenFlux/TokenRouter/internal/usage"
)

type resultUsageRepository struct {
	usagecore.UsageLogRepository

	inserted   bool
	err        error
	calls      int
	lastLog    *usagecore.UsageLog
	lastCtxErr error
}

func (s *resultUsageRepository) Create(ctx context.Context, log *usagecore.UsageLog) (bool, error) {
	s.calls++
	s.lastLog = log
	s.lastCtxErr = ctx.Err()
	return s.inserted, s.err
}
