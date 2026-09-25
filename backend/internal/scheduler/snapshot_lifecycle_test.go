package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 记录快照重建调用，验证停止后不再启动重建。
type s07PlanSnapshotCache struct {
	SnapshotCache
	calls chan struct{}
	block chan struct{}
}

func (s *s07PlanSnapshotCache) ListBuckets(ctx context.Context) ([]SchedulerBucket, error) {
	s.calls <- struct{}{}
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, errors.New("planning probe")
}
func TestS07SnapshotStartAfterStop(t *testing.T) {
	c := &s07PlanSnapshotCache{calls: make(chan struct{}, 4)}
	s := NewSnapshotService(c, nil, nil, nil, nil)
	s.Stop()
	s.Start()
	s.Stop()
	if len(c.calls) > 0 {
		t.Fatalf("停止后仍执行初始重建: calls=%d", len(c.calls))
	}
}
func TestS07SnapshotRepeatedStart(t *testing.T) {
	c := &s07PlanSnapshotCache{calls: make(chan struct{}, 4)}
	s := NewSnapshotService(c, nil, nil, nil, nil)
	s.Start()
	<-c.calls
	s.Start()
	s.Stop()
	if len(c.calls) > 0 {
		t.Fatal("重复 Start 再次执行初始重建")
	}
}
func TestS07SnapshotStopCancelsWork(t *testing.T) {
	c := &s07PlanSnapshotCache{calls: make(chan struct{}, 4), block: make(chan struct{})}
	s := NewSnapshotService(c, nil, nil, nil, nil)
	s.Start()
	<-c.calls
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Error("Stop 没有取消支持 context 的在途重建")
	}
	close(c.block)
	<-done
}
