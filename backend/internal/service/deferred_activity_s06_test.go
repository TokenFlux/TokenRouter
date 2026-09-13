package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

// 网关行为从最终写入端口核对，避免测试穿透队列内部表示。
type deferredActivityRepository struct {
	AccountRepository
	updates sync.Map
}

func (r *deferredActivityRepository) BatchUpdateLastUsed(_ context.Context, updates map[int64]time.Time) error {
	for id, ts := range updates {
		r.updates.Store(id, ts)
	}
	return nil
}
func newDeferredActivityRecorder(t *testing.T) (*DeferredService, *sync.Map) {
	t.Helper()
	wheel, err := NewTimingWheelService()
	require.NoError(t, err)
	repo := &deferredActivityRepository{}
	svc := NewDeferredService(repo, wheel, time.Second)
	t.Cleanup(func() { require.NoError(t, svc.Stop()) })
	return svc, &repo.updates
}
