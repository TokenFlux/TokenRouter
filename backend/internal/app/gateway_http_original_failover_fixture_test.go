//go:build unit

package app

import (
	"context"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
)

// mockTempUnscheduler 记录 TempUnscheduleRetryableError 的调用信息。
type mockTempUnscheduler struct {
	calls []tempUnscheduleCall
}

type tempUnscheduleCall struct {
	accountID   int64
	failoverErr *forwardcore.UpstreamFailoverError
}

func (m *mockTempUnscheduler) TempUnscheduleRetryableError(_ context.Context, accountID int64, failoverErr *forwardcore.UpstreamFailoverError) {
	m.calls = append(m.calls, tempUnscheduleCall{accountID: accountID, failoverErr: failoverErr})
}
