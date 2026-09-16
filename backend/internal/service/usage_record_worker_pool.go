// 兼容入口共享 gateway 完成执行器；无第二份队列或扩缩容状态。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
)

type UsageRecordTask = completion.UsageRecordTask
type UsageRecordSubmitMode = completion.UsageRecordSubmitMode

const UsageRecordSubmitModeEnqueued = completion.UsageRecordSubmitModeEnqueued
const UsageRecordSubmitModeDropped = completion.UsageRecordSubmitModeDropped
const UsageRecordSubmitModeDroppedStopped = completion.UsageRecordSubmitModeDroppedStopped
const UsageRecordSubmitModeSync = completion.UsageRecordSubmitModeSync

type UsageRecordWorkerPoolOptions = completion.UsageRecordWorkerPoolOptions
type UsageRecordWorkerPoolStats = completion.UsageRecordWorkerPoolStats
type UsageRecordWorkerPool = completion.UsageRecordWorkerPool

func NewUsageRecordWorkerPoolWithOptions(opts UsageRecordWorkerPoolOptions) *UsageRecordWorkerPool {
	if opts.Observe == nil {
		opts.Observe = telemetry.Completion
	}
	return completion.NewUsageRecordWorkerPoolWithOptions(opts)
}
