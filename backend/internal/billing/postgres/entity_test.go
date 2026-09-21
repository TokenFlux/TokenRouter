//go:build unit

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

// 未登记的任务作用域不能开启任务资金事务，任务表不再由 billing 决定。
func TestTaskProjectionRequiresRegisteredScope(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store := NewSettlementStore(db, timezone.NewCalendar(time.Local), nil)
	_, err = store.Reserve(context.Background(), &billing.TaskFundsCommand{Task: billing.TaskReference{Scope: "unknown", ID: "opaque", ReserveRequestID: "hold:opaque"}, RequestID: "hold:opaque"})
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 资金核心读取显式预占 ID，不根据 ID 前缀猜测所属任务模块。
func TestBatchImageHoldClaimRequestID(t *testing.T) {
	require.Empty(t, taskHoldClaimRequestID(nil))
	for _, id := range []string{"batch_image_hold:imgbatch_x", "creative_hold:crun_x"} {
		require.Equal(t, id, taskHoldClaimRequestID(&billing.TaskFundsCommand{Task: billing.TaskReference{ID: "opaque", ReserveRequestID: id}}))
	}
}
