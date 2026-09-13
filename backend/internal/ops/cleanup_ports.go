package ops

import (
	"context"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// CleanupBackend 不把 SQL 连接暴露给清理用例。
type CleanupBackend interface {
	AdvisoryLocker
	AcquireMaintenance(context.Context) (func(), bool, error)
	RunTarget(context.Context, bool, time.Time, string, string, bool, int, time.Duration) (CleanupTargetResult, error)
}
type CleanupTargetResult struct {
	Deleted, Batches int64
	Throttled        time.Duration
}

func reportCleanupCompleted(o *Options, counts string) {
	if o != nil && o.CleanupCompleted != nil {
		o.CleanupCompleted(counts)
	}
}

var ErrDatabaseMaintenanceBusy = infraerrors.Conflict("MAINTENANCE_BUSY", "another database maintenance task is running")
