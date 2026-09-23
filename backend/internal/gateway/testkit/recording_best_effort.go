package testkit

import (
	"context"

	usagecore "github.com/TokenFlux/TokenRouter/internal/usage"
)

type BestEffortUsageLogStore struct {
	usagecore.UsageLogRepository

	BestEffortErr   error
	CreateErr       error
	BestEffortCalls int
	CreateCalls     int
	LastLog         *usagecore.UsageLog
	LastCtxErr      error
}

func (s *BestEffortUsageLogStore) CreateBestEffort(ctx context.Context, log *usagecore.UsageLog) error {
	s.BestEffortCalls++
	s.LastLog = log
	s.LastCtxErr = ctx.Err()
	return s.BestEffortErr
}
func (s *BestEffortUsageLogStore) Create(ctx context.Context, log *usagecore.UsageLog) (bool, error) {
	s.CreateCalls++
	s.LastLog = log
	s.LastCtxErr = ctx.Err()
	return false, s.CreateErr
}
