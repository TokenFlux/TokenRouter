package site

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type announcementExpiryRepoStub struct {
	AnnouncementRepository
	calls  atomic.Int64
	called chan time.Time
}

func (r *announcementExpiryRepoStub) ArchiveExpired(_ context.Context, now time.Time) (int64, error) {
	r.calls.Add(1)
	if r.called != nil {
		select {
		case r.called <- now:
		default:
		}
	}
	return 1, nil
}

// 单次扫描应使用当前时间调用仓储归档操作。
func TestAnnouncementExpiryServiceRunOnceArchivesExpiredAnnouncements(t *testing.T) {
	repo := &announcementExpiryRepoStub{called: make(chan time.Time, 1)}
	svc := NewAnnouncementExpiryService(repo, time.Minute)
	before := time.Now()

	svc.runOnce()

	archivedAt := <-repo.called
	require.EqualValues(t, 1, repo.calls.Load())
	require.False(t, archivedAt.Before(before))
	require.False(t, archivedAt.After(time.Now()))
}

// 启动后应立即扫描并按间隔继续执行，停止后不得再触发扫描。
func TestAnnouncementExpiryServiceStartRunsImmediatelyAndPeriodically(t *testing.T) {
	interval := 10 * time.Millisecond
	repo := &announcementExpiryRepoStub{called: make(chan time.Time, 8)}
	svc := NewAnnouncementExpiryService(repo, interval)
	svc.Start()
	t.Cleanup(svc.Stop)

	waitForAnnouncementExpiryCall(t, repo.called)
	waitForAnnouncementExpiryCall(t, repo.called)
	svc.Stop()

	stoppedCalls := repo.calls.Load()
	time.Sleep(3 * interval)
	require.Equal(t, stoppedCalls, repo.calls.Load())
}

func waitForAnnouncementExpiryCall(t *testing.T, called <-chan time.Time) {
	t.Helper()
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("等待公告到期扫描超时")
	}
}

// 到期归档保持单一循环；停止会等待正在执行的存储调用。
type blockingExpiryRepository struct {
	AnnouncementRepository
	entered, release chan struct{}
}

func (r *blockingExpiryRepository) ArchiveExpired(ctx context.Context, _ time.Time) (int64, error) {
	close(r.entered)
	select {
	case <-r.release:
		return 0, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}
func TestExpiryStopWaitsForScan(t *testing.T) {
	repo := &blockingExpiryRepository{entered: make(chan struct{}), release: make(chan struct{})}
	svc := NewAnnouncementExpiryService(repo, time.Minute)
	svc.Start()
	svc.Start()
	<-repo.entered
	stopped := make(chan struct{})
	go func() { svc.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("归档尚未完成")
	case <-time.After(20 * time.Millisecond):
	}
	close(repo.release)
	<-stopped
	svc.Stop()
	svc.Start()
}
func TestExpiryStopBeforeStart(t *testing.T) {
	repo := &announcementExpiryRepoStub{}
	svc := NewAnnouncementExpiryService(repo, time.Minute)
	svc.Stop()
	svc.Start()
	svc.Stop()
	require.Zero(t, repo.calls.Load())
}
