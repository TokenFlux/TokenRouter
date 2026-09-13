package account

import (
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// 旧批次写回失败时，同账号的新活动时间必须保留，不能由旧批次回填覆盖。
func TestS06DeferredFailureKeepsNewerPendingActivity(t *testing.T) {
	repo := &deferredDrainRepository{entered: make(chan struct{}), release: make(chan struct{}), err: errors.New("forced write failure")}
	wheel, err := NewTimingWheelService()
	require.NoError(t, err)
	svc := NewDeferredService(repo, wheel, DeferredOptions{Interval: time.Hour})
	svc.lastUsedUpdates.Store(int64(1), time.Unix(1, 0))
	flushed := make(chan error, 1)
	go func() { flushed <- svc.flushLastUsedErr() }()
	<-repo.entered
	svc.ScheduleLastUsedUpdate(1)
	expected, ok := svc.lastUsedUpdates.Load(int64(1))
	require.True(t, ok)
	close(repo.release)
	require.Error(t, <-flushed)
	actual, ok := svc.lastUsedUpdates.Load(int64(1))
	require.True(t, ok)
	require.Equal(t, expected, actual)
}
