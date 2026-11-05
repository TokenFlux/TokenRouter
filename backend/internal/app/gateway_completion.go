package app

import (
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
)

// gateway_completion.go converts completion queue configuration into
// worker-pool options and binds completion telemetry.
// Completion recording uses configured queue capacity, task budgets, and
// overflow handling. Assembly starts from worker-pool defaults, applies fields
// according to their zero-value rules, and normalizes the result before binding
// completion telemetry to the pool.

// usageRecordPoolOptionsFromConfig returns defaults for nil cfg and normalized
// overrides otherwise. Positive capacity and step fields replace defaults;
// sample percentage, down threshold, and cooldown accept zero, while
// AutoScaleEnabled is copied directly and overflow policy is trimmed and
// lowercased.
func usageRecordPoolOptionsFromConfig(cfg *config.Config) completion.UsageRecordWorkerPoolOptions {
	opts := completion.DefaultOptions()
	if cfg == nil {
		return opts
	}
	if cfg.Usage.RecordQueue.WorkerCount > 0 {
		opts.WorkerCount = cfg.Usage.RecordQueue.WorkerCount
	}
	if cfg.Usage.RecordQueue.QueueSize > 0 {
		opts.QueueSize = cfg.Usage.RecordQueue.QueueSize
	}
	if cfg.Usage.RecordQueue.TaskTimeoutSeconds > 0 {
		// Convert configured seconds into each completion task's execution budget.
		opts.TaskTimeout = time.Duration(cfg.Usage.RecordQueue.TaskTimeoutSeconds) * time.Second
	}
	if policy := strings.TrimSpace(strings.ToLower(cfg.Usage.RecordQueue.OverflowPolicy)); policy != "" {
		opts.OverflowPolicy = policy
	}
	if cfg.Usage.RecordQueue.OverflowSamplePercent >= 0 {
		opts.OverflowSamplePercent = cfg.Usage.RecordQueue.OverflowSamplePercent
	}
	opts.AutoScaleEnabled = cfg.Usage.RecordQueue.AutoScaleEnabled
	if cfg.Usage.RecordQueue.AutoScaleMinWorkers > 0 {
		opts.AutoScaleMinWorkers = cfg.Usage.RecordQueue.AutoScaleMinWorkers
	}
	if cfg.Usage.RecordQueue.AutoScaleMaxWorkers > 0 {
		opts.AutoScaleMaxWorkers = cfg.Usage.RecordQueue.AutoScaleMaxWorkers
	}
	if cfg.Usage.RecordQueue.AutoScaleUpQueuePercent > 0 {
		opts.AutoScaleUpPercent = cfg.Usage.RecordQueue.AutoScaleUpQueuePercent
	}
	if cfg.Usage.RecordQueue.AutoScaleDownQueuePercent >= 0 {
		opts.AutoScaleDownPercent = cfg.Usage.RecordQueue.AutoScaleDownQueuePercent
	}
	if cfg.Usage.RecordQueue.AutoScaleUpStep > 0 {
		opts.AutoScaleUpStep = cfg.Usage.RecordQueue.AutoScaleUpStep
	}
	if cfg.Usage.RecordQueue.AutoScaleDownStep > 0 {
		opts.AutoScaleDownStep = cfg.Usage.RecordQueue.AutoScaleDownStep
	}
	if cfg.Usage.RecordQueue.AutoScaleCheckIntervalSeconds > 0 {
		// Scaling checks use a duration expressed in configured seconds.
		opts.AutoScaleInterval = time.Duration(cfg.Usage.RecordQueue.AutoScaleCheckIntervalSeconds) * time.Second
	}
	if cfg.Usage.RecordQueue.AutoScaleCooldownSeconds >= 0 {
		// The cooldown duration spaces scaling changes in configured seconds.
		opts.AutoScaleCooldown = time.Duration(cfg.Usage.RecordQueue.AutoScaleCooldownSeconds) * time.Second
	}
	return completion.NormalizeOptions(opts)
}

// provideUsageRecordWorkerPool constructs the bounded completion pool with
// application telemetry. The downstream constructor creates its pond pool
// immediately; lifecycle Start begins periodic scaling, and lifecycle
// StopContext waits before the task dependencies are closed.
func provideUsageRecordWorkerPool(cfg *config.Config) *completion.UsageRecordWorkerPool {
	opts := usageRecordPoolOptionsFromConfig(cfg)
	opts.Observe = telemetry.Completion
	return completion.NewUsageRecordWorkerPoolWithOptions(opts)
}
