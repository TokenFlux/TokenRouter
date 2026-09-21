package account_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/stretchr/testify/require"
)

type fakeLeaderLockCache struct {
	mu         sync.Mutex
	owners     map[string]string
	acquireErr error
}

func (f *fakeLeaderLockCache) TryAcquireLeaderLock(_ context.Context, key, owner string, _ time.Duration) (bool, error) {
	if f.acquireErr != nil {
		return false, f.acquireErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.owners == nil {
		f.owners = map[string]string{}
	}
	if _, held := f.owners[key]; held {
		return false, nil
	}
	f.owners[key] = owner
	return true, nil
}

func (f *fakeLeaderLockCache) ReleaseLeaderLock(_ context.Context, key, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.owners[key] == owner {
		delete(f.owners, key)
	}
	return nil
}

func (f *fakeLeaderLockCache) heldBy(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.owners[key]
}

func TestTryAcquireSingletonLeaderLock_NoBackendRunsUngated(t *testing.T) {
	release, ok := account.AcquireSingletonLease(context.Background(), nil, nil, "k", "inst", time.Minute)
	require.True(t, ok)
	require.NotNil(t, release)
	require.NotPanics(t, release)
}

func TestTryAcquireSingletonLeaderLock_ContendedThenReleased(t *testing.T) {
	cache := &fakeLeaderLockCache{}
	ctx := context.Background()
	const key = "leader:test:contended"

	releaseA, ok := account.AcquireSingletonLease(ctx, cache, nil, key, "A", time.Minute)
	require.True(t, ok)
	require.Equal(t, "A", cache.heldBy(key))

	_, okB := account.AcquireSingletonLease(ctx, cache, nil, key, "B", time.Minute)
	require.False(t, okB)

	releaseA()
	require.Empty(t, cache.heldBy(key))

	releaseB, okB := account.AcquireSingletonLease(ctx, cache, nil, key, "B", time.Minute)
	require.True(t, okB)
	releaseB()
}

func TestTryAcquireSingletonLeaderLock_CacheErrorFallsThrough(t *testing.T) {
	cache := &fakeLeaderLockCache{acquireErr: context.DeadlineExceeded}

	release, ok := account.AcquireSingletonLease(context.Background(), cache, nil, "k", "inst", time.Minute)

	require.True(t, ok)
	require.NotNil(t, release)
	require.NotPanics(t, release)
}
