// 旧调用方提供完整操作闭包，重试机制由 infra/postgres 唯一实现。
package repository

import (
	"context"

	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
)

// postgresDeadlockMaxAttempts 保留调用方的降级观测口径，数值由技术实现唯一提供。
const postgresDeadlockMaxAttempts = postgresinfra.DeadlockMaxAttempts

func retryPostgresDeadlock[T any](ctx context.Context, operation string, batchSize int, fn func() (T, error)) (T, error) {
	return postgresinfra.RetryDeadlock(ctx, operation, batchSize, fn)
}
