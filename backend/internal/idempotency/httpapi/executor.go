package httpapi

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/idempotency"
)

// Executor 持有应用注入的协调器，零值保留独立 HTTP 夹具的无协调器行为。
// 绑定只在提供请求前执行，运行时不替换其他处理器的依赖。
type Executor struct {
	coordinator *idempotency.IdempotencyCoordinator
}

func (e *Executor) BindIdempotency(coordinator *idempotency.IdempotencyCoordinator) {
	e.coordinator = coordinator
}

func (e *Executor) DefaultWriteIdempotencyTTL() time.Duration {
	return e.coordinator.DefaultWriteIdempotencyTTL()
}

func (e *Executor) DefaultSystemOperationIdempotencyTTL() time.Duration {
	return e.coordinator.DefaultSystemOperationIdempotencyTTL()
}
