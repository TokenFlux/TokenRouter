package maintenance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 同一业务 operation ID 的再次认领仍必须具有独立代次。
func TestS14B05SameOperationReclaimed(t *testing.T) {
	repo := newInMemoryIdempotencyRepo()
	s := NewSystemOperationLockService(repo, Options{ProcessingTimeout: time.Hour, SystemOperationTTL: 2 * time.Hour})
	first, err := s.Acquire(context.Background(), "same")
	require.NoError(t, err)
	repo.mu.Lock()
	for _, v := range repo.data {
		past := time.Now().Add(-time.Second)
		v.LockedUntil = &past
	}
	repo.mu.Unlock()
	second, err := s.Acquire(context.Background(), "same")
	require.NoError(t, err)
	require.NotEqual(t, first.ownership, second.ownership)
	require.ErrorIs(t, s.Release(context.Background(), first, true, ""), ErrOperationOwnershipLost)
	require.NoError(t, s.Release(context.Background(), second, true, ""))
	require.NoError(t, s.Release(context.Background(), second, false, "late"))
}
