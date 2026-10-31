package plugin

import (
	"context"
	"errors"
	"maps"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// kvMaintenanceFixture models sampled usage and a finite set of expired rows.
type kvMaintenanceFixture struct {
	KVStore // unused request storage operations
	// mu protects the worker observations and configured outcomes so test calls can inspect progress while maintenance owns its goroutine.
	mu           sync.Mutex
	usage        map[string]KVUsage // next complete usage observation
	err          error              // configured measurement read failure
	measureCalls int                // completed measurement invocation count
	expireCalls  int                // completed expiration batch count
	remaining    int64              // expired rows awaiting fixture cleanup
}

// KVMeasure returns a detached sample or the configured measurement failure.
func (f *kvMaintenanceFixture) KVMeasure(context.Context) (map[string]KVUsage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.measureCalls++
	return maps.Clone(f.usage), f.err
}

// KVExpire consumes at most one requested batch from the retained expired count.
func (f *kvMaintenanceFixture) KVExpire(_ context.Context, limit int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expireCalls++
	count := min(f.remaining, int64(limit))
	f.remaining -= count
	return count, nil
}

// TestKVMaintenanceIntervalsAndTerminalShutdown advances the synthetic clock through initial sampling, five-minute refresh, and ten-minute cleanup.
// One worker runs despite repeated starts, cleanup drains bounded batches, and shutdown prevents all later scheduled work or restart.
func TestKVMaintenanceIntervalsAndTerminalShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &kvMaintenanceFixture{usage: map[string]KVUsage{"test.kv": {Keys: 10}}, remaining: 2001}
		service := NewKV(store, nil)
		require.NoError(t, service.Start(t.Context()))
		t.Cleanup(func() { require.NoError(t, service.Stop(context.Background())) })
		require.NoError(t, service.Start(t.Context()))
		synctest.Wait()
		measures, batches, remaining := store.observations()
		require.Equal(t, 1, measures)
		require.Zero(t, batches)
		require.Equal(t, int64(2001), remaining)
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		measures, batches, remaining = store.observations()
		require.Equal(t, 2, measures)
		require.Zero(t, batches)
		require.Equal(t, int64(2001), remaining)
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		measures, batches, remaining = store.observations()
		require.Equal(t, 3, measures)
		require.Equal(t, 3, batches)
		require.Zero(t, remaining)
		require.NoError(t, service.Stop(t.Context()))
		require.NoError(t, service.Start(t.Context()))
		time.Sleep(20 * time.Minute)
		synctest.Wait()
		measures, batches, _ = store.observations()
		require.Equal(t, 3, measures)
		require.Equal(t, 3, batches)
	})
}

// TestKVFailedMeasurementPreservesLastCompleteQuota publishes a full sample and then makes the replacement measurement fail.
// Admission retains the prior blocked namespace, while a later successful empty sample clears it after cleanup has reclaimed the data.
func TestKVFailedMeasurementPreservesLastCompleteQuota(t *testing.T) {
	store := &kvMaintenanceFixture{usage: map[string]KVUsage{"test.kv": {Bytes: KVMaxBytes}}}
	service := NewKV(store, nil)
	require.NoError(t, service.RefreshUsage(t.Context()))
	require.True(t, service.blocked("test.kv"))
	store.usage = nil
	store.err = errors.New("measurement failed")
	require.Error(t, service.RefreshUsage(t.Context()))
	require.True(t, service.blocked("test.kv"))
	store.err = nil
	require.NoError(t, service.RefreshUsage(t.Context()))
	require.False(t, service.blocked("test.kv"))
}

// observations copies worker counters so failed assertions never retain the fixture mutex.
func (f *kvMaintenanceFixture) observations() (int, int, int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.measureCalls, f.expireCalls, f.remaining
}
