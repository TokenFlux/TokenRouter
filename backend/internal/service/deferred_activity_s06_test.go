package service

import (
	"context"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/infra/timingwheel"
	"github.com/stretchr/testify/require"
)

// 网关行为从最终写入端口核对，避免测试穿透队列内部表示。
type deferredActivityRepository struct {
	updates sync.Map
}

func (r *deferredActivityRepository) BatchUpdateLastUsed(_ context.Context, updates map[int64]time.Time) error {
	for id, ts := range updates {
		r.updates.Store(id, ts)
	}
	return nil
}
func newDeferredActivityRecorder(t *testing.T) (*account.DeferredService, *sync.Map) {
	t.Helper()
	wheel := timingwheel.New()
	repo := &deferredActivityRepository{}
	svc := account.NewDeferredService(repo, wheel, account.DeferredOptions{Interval: time.Second, Now: time.Now, Observe: log.Printf})
	t.Cleanup(func() { require.NoError(t, svc.Stop()) })
	return svc, &repo.updates
}
