package app

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/stretchr/testify/require"
)

func TestUsageRecordWorkerPool_OptionsFromConfig_AutoScaleDisabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.UsageRecord.WorkerCount = 64
	cfg.Gateway.UsageRecord.QueueSize = 128
	cfg.Gateway.UsageRecord.TaskTimeoutSeconds = 7
	cfg.Gateway.UsageRecord.OverflowPolicy = config.UsageRecordOverflowPolicyDrop
	cfg.Gateway.UsageRecord.OverflowSamplePercent = 0
	cfg.Gateway.UsageRecord.AutoScaleEnabled = false
	cfg.Gateway.UsageRecord.AutoScaleMinWorkers = 1
	cfg.Gateway.UsageRecord.AutoScaleMaxWorkers = 512

	opts := usageRecordPoolOptionsFromConfig(cfg)
	require.False(t, opts.AutoScaleEnabled)
	require.Equal(t, 64, opts.WorkerCount)
	require.Equal(t, 64, opts.AutoScaleMinWorkers)
	require.Equal(t, 64, opts.AutoScaleMaxWorkers)
	require.Equal(t, 7*time.Second, opts.TaskTimeout)
}
func TestNewUsageRecordWorkerPool_FromConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.UsageRecord.WorkerCount = 3
	cfg.Gateway.UsageRecord.QueueSize = 16
	cfg.Gateway.UsageRecord.TaskTimeoutSeconds = 2
	cfg.Gateway.UsageRecord.OverflowPolicy = config.UsageRecordOverflowPolicyDrop
	cfg.Gateway.UsageRecord.AutoScaleEnabled = false

	pool := provideUsageRecordWorkerPool(cfg)
	pool.Start()
	t.Cleanup(pool.Stop)

	stats := pool.Stats()
	require.Equal(t, 3, stats.MaxConcurrency)
}
func TestUsageRecordWorkerPool_OptionsFromConfig_NilConfig(t *testing.T) {
	opts := usageRecordPoolOptionsFromConfig(nil)
	require.Equal(t, completion.DefaultOptions().WorkerCount, opts.WorkerCount)
	require.Equal(t, completion.DefaultOptions().QueueSize, opts.QueueSize)
	require.Equal(t, completion.DefaultOptions().TaskTimeout, opts.TaskTimeout)
	require.Equal(t, completion.DefaultOptions().OverflowPolicy, opts.OverflowPolicy)
	require.Equal(t, completion.DefaultOptions().OverflowSamplePercent, opts.OverflowSamplePercent)
	require.True(t, opts.AutoScaleEnabled)
	require.Equal(t, completion.DefaultOptions().AutoScaleMinWorkers, opts.AutoScaleMinWorkers)
	require.Equal(t, completion.DefaultOptions().AutoScaleMaxWorkers, opts.AutoScaleMaxWorkers)
}
