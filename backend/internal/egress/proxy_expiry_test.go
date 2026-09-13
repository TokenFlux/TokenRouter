package egress

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// 重复启动或已经停止后启动都不能额外写入数据库。
type s06ExpiryRepository struct {
	ProxyRepository
	calls chan struct{}
	count atomic.Int32
}

func (r *s06ExpiryRepository) SweepExpiredProxies(context.Context, time.Time) (int64, error) {
	r.count.Add(1)
	r.calls <- struct{}{}
	return 0, nil
}
func TestS06ProxyExpiryRepeatedStart(t *testing.T) {
	repo := &s06ExpiryRepository{calls: make(chan struct{}, 4)}
	worker := NewProxyExpiryService(repo, time.Hour)
	worker.Start()
	<-repo.calls
	worker.Start()
	select {
	case <-repo.calls:
	case <-time.After(100 * time.Millisecond):
	}
	worker.Stop()
	if got := repo.count.Load(); got != 1 {
		t.Fatalf("repeated Start ran %d initial sweeps", got)
	}
}
func TestS06ProxyExpiryStopBeforeStart(t *testing.T) {
	repo := &s06ExpiryRepository{calls: make(chan struct{}, 4)}
	worker := NewProxyExpiryService(repo, time.Hour)
	worker.Stop()
	worker.Start()
	select {
	case <-repo.calls:
	case <-time.After(100 * time.Millisecond):
	}
	worker.Stop()
	if got := repo.count.Load(); got != 0 {
		t.Fatalf("Start after Stop ran %d sweeps", got)
	}
}
