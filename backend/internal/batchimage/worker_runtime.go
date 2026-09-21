package batchimage

import "context"

// NewWorkerRuntime 统一持有消费、延迟搬运、失活恢复与资金恢复循环，不在构造时启动。
func NewWorkerRuntime(worker *BatchImageWorker, recovery *BillingRecovery, enabled bool) *Runtime {
	var loops []func(context.Context)
	if worker != nil {
		loops = []func(context.Context){worker.Run, worker.RunDelayedMover, worker.RunStaleActiveRecovery}
		if recovery != nil {
			loops = append(loops, func(ctx context.Context) {
				interval := worker.Options().RecoveryInterval
				for {
					if ctx.Err() != nil {
						return
					}
					_, _ = recovery.ReleaseStaleUnsubmittedOnce(ctx)
					SleepOrDone(ctx, interval)
				}
			})
		}
	}
	return NewRuntime("batch image worker", enabled && worker != nil, loops...)
}
