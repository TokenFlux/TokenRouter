package repository

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/logger"
)

const (
	postgresDeadlockMaxRetries  = 2
	postgresDeadlockMaxAttempts = postgresDeadlockMaxRetries + 1
	postgresDeadlockRetryJitter = 30 * time.Millisecond
)

var postgresDeadlockRetryBaseDelays = [...]time.Duration{
	20 * time.Millisecond,
	50 * time.Millisecond,
}

// retryPostgresDeadlock 对完整操作做有界重试；调用方必须确保每次 fn 都创建独立事务或语句。
func retryPostgresDeadlock[T any](ctx context.Context, operation string, batchSize int, fn func() (T, error)) (T, error) {
	var zero T
	startedAt := time.Now()

	for attempt := 1; ; attempt++ {
		result, err := fn()
		if err == nil {
			if attempt > 1 {
				logger.LegacyPrintf(
					"repository.postgres_retry",
					"operation=%s sqlstate=40P01 attempt=%d batch_size=%d retry_succeeded=true fallback_failed=false elapsed_ms=%d",
					operation,
					attempt,
					batchSize,
					time.Since(startedAt).Milliseconds(),
				)
			}
			return result, nil
		}

		if !isPostgresDeadlock(err) {
			return zero, err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return zero, ctxErr
		}

		retryAvailable := attempt <= postgresDeadlockMaxRetries
		logger.LegacyPrintf(
			"repository.postgres_retry",
			"operation=%s sqlstate=40P01 attempt=%d batch_size=%d retry_succeeded=false fallback_failed=false elapsed_ms=%d retry_scheduled=%t",
			operation,
			attempt,
			batchSize,
			time.Since(startedAt).Milliseconds(),
			retryAvailable,
		)
		if !retryAvailable {
			return zero, err
		}
		if waitErr := waitPostgresDeadlockRetry(ctx, attempt-1); waitErr != nil {
			return zero, waitErr
		}
	}
}

// waitPostgresDeadlockRetry 使用可取消计时器等待，避免调用方超时后仍继续重试。
func waitPostgresDeadlockRetry(ctx context.Context, retryIndex int) error {
	if retryIndex < 0 || retryIndex >= len(postgresDeadlockRetryBaseDelays) {
		return nil
	}
	delay := postgresDeadlockRetryBaseDelays[retryIndex]
	if postgresDeadlockRetryJitter > 0 {
		delay += time.Duration(rand.Int64N(int64(postgresDeadlockRetryJitter) + 1))
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
