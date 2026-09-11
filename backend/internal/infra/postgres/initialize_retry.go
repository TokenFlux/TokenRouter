package postgres

import (
	"context"
	"errors"
	"github.com/lib/pq"
	"log/slog"
	"strings"
	"time"
)

const (
	maxDatabaseInitializationRetries = 8
	databaseInitializationRetryBase  = time.Second
	databaseInitializationRetryMax   = 30 * time.Second
)

// initializeDatabaseWithRetry 仅对 PostgreSQL 启动阶段的暂时错误重试；配置、迁移
// 和数据等永久错误立即返回，确保运维人员能看到真实故障。
func InitializeWithRetry(ctx context.Context, initialize func(context.Context) error) error {
	return initializeDatabaseWithRetryWithWait(ctx, initialize, waitForDatabaseInitializationRetry)
}

func initializeDatabaseWithRetryWithWait(
	ctx context.Context,
	initialize func(context.Context) error,
	wait func(context.Context, time.Duration) error,
) error {
	for attempt := 1; ; attempt++ {
		if err := initialize(ctx); err == nil {
			return nil
		} else {
			if !isTransientDatabaseInitializationError(err) || attempt > maxDatabaseInitializationRetries {
				return err
			}

			delay := databaseInitializationRetryBase * time.Duration(1<<(attempt-1))
			if delay > databaseInitializationRetryMax {
				delay = databaseInitializationRetryMax
			}
			slog.Warn("database initialization temporarily unavailable; retrying",
				"retry", attempt,
				"max_retries", maxDatabaseInitializationRetries,
				"retry_in", delay,
				"error", err,
			)
			if err := wait(ctx, delay); err != nil {
				return err
			}
		}
	}
}

func waitForDatabaseInitializationRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isTransientDatabaseInitializationError(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	code := string(pqErr.Code)
	return code == "57P03" || strings.HasPrefix(code, "08")
}
