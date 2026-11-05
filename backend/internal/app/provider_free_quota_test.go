package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// Four separately constructed quota gates begin with cold caches over one
// usage reader. The fixture waits for each accepted refresh before checking
// its blocking decision, then closes task admission and verifies that a new
// provider miss causes no additional statistics load.

// freeQuotaReaderFixture supplies fixed provider-window statistics and counts asynchronous refresh loads.
type freeQuotaReaderFixture struct {
	// UsageLogRepository is unset; this fixture implements only the statistics
	// method exercised by the quota binding and leaves other methods unused.
	usage.UsageLogRepository // unimplemented repository methods
	// calls counts method entries from refresh goroutines. The test waits for
	// tracked work before comparing this atomic count with the number of gates.
	calls atomic.Int64 // statistics load entries
}

// GetProviderWindowStatsBatch counts the load and returns provider seven's
// fixed statistics. Context, IDs, and start time are ignored, letting the test
// compare per-gate query counts with one stable usage result.
func (f *freeQuotaReaderFixture) GetProviderWindowStatsBatch(_ context.Context, _ []int64, _ time.Time) (map[int64]*usage.ProviderStats, error) {
	// Refresh callbacks run in task-owned goroutines. Publish their entry count
	// atomically so test observations can safely read it after waiting for work.
	f.calls.Add(1)
	// This usage exceeds 95 percent of the test's 500,000-token limit.
	return map[int64]*usage.ProviderStats{7: {Tokens: 480_000}}, nil
}

// TestFreeQuotaBindingPreservesCacheScopesAndTaskOwner exercises generic,
// compatible, and two advanced gates over a shared reader. Each gate must allow
// its initial miss and block after refresh, while completed task shutdown must
// prevent a later miss from starting another load.
func TestFreeQuotaBindingPreservesCacheScopesAndTaskOwner(t *testing.T) {
	cfg := &config.Config{}
	cfg.Scheduling.GrokFreeQuota.Enabled = true
	cfg.Scheduling.GrokFreeQuota.TokenLimit = 500_000
	cfg.Scheduling.GrokFreeQuota.ThresholdPercent = 95
	cfg.Scheduling.GrokFreeQuota.WindowHours = 24
	cfg.Scheduling.GrokFreeQuota.StatsCacheSeconds = 60
	tasks := lifecycle.NewTasks()
	reader := &freeQuotaReaderFixture{}
	bound := provideSelectionFreeQuota(cfg, reader, tasks)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Request task shutdown at fixture cleanup and check its result. Accepted
	// quota refresh work is waited on within this separate cleanup budget.
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		require.NoError(t, tasks.Stop(cleanup))
	})
	candidates := []provider.FreeQuotaCandidate{{ID: 7, Eligible: true}}
	gates := []*provider.FreeQuotaGate{bound.Generic, bound.Compatible, bound.Advanced(), bound.Advanced()}
	for _, gate := range gates {
		require.Empty(t, gate.Blocked(candidates), "首次缺失保持放行")
		// The query count increases before cache publication. Wait for tracked
		// refresh completion so the next check sees the loaded usage for this gate.
		require.NoError(t, tasks.Wait(ctx))
		require.True(t, gate.Blocked(candidates)[7])
	}
	require.Equal(t, int64(4), reader.calls.Load())
	// The tracker is idle before Stop seals admission. A new provider ID forces
	// a cache miss, whose refresh is refused instead of querying the shared reader.
	require.NoError(t, tasks.Stop(ctx))
	require.Empty(t, bound.Generic.Blocked([]provider.FreeQuotaCandidate{{ID: 8, Eligible: true}}))
	require.Equal(t, int64(4), reader.calls.Load())
}
