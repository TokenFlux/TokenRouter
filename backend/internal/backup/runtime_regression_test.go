package backup

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// runtimeSettings 用受控屏障验证生命周期，不依赖错误实现才能到达的交错。
type runtimeSettings struct {
	mu        sync.Mutex
	values    map[string]string
	entered   chan struct{}
	once      sync.Once
	fail      bool
	cancelled atomic.Bool
}

func (r *runtimeSettings) GetValue(ctx context.Context, key string) (string, error) {
	if r.entered != nil {
		r.once.Do(func() { close(r.entered) })
		<-ctx.Done()
		r.cancelled.Store(true)
		return "", ctx.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.values[key], nil
}
func (r *runtimeSettings) Set(ctx context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return errors.New("planned record write failure")
	}
	if r.values == nil {
		r.values = map[string]string{}
	}
	r.values[key] = value
	return nil
}

type runtimeArchive struct{ calls atomic.Int32 }

func (a *runtimeArchive) Write(context.Context, *BackupRecord, BackupObjectStore, *BackupS3Config, BackupDumpOptions, func(context.Context, *BackupRecord) error, func(time.Duration) (context.Context, context.CancelFunc)) (int64, error) {
	return 0, nil
}
func (a *runtimeArchive) Restore(context.Context, *BackupRecord, BackupObjectStore) error {
	a.calls.Add(1)
	return nil
}
func runtimeBackup(repo *runtimeSettings, archive *runtimeArchive) *BackupService {
	return New(repo, Options{DatabaseName: "test", Now: time.Now}, nil, nil, nil, archive, nil)
}
func TestS14B01RepeatedStartAndStop(t *testing.T) {
	s := runtimeBackup(&runtimeSettings{}, &runtimeArchive{})
	require.NoError(t, s.StartContext(context.Background()))
	first := s.cronSched
	require.NoError(t, s.StartContext(context.Background()))
	require.Same(t, first, s.cronSched)
	require.NoError(t, s.StopContext(context.Background()))
	require.NoError(t, s.StartContext(context.Background()))
	require.Same(t, first, s.cronSched)
	_, err := s.StartBackup(context.Background(), "manual", 1)
	require.Error(t, err)
}
func TestS14B02StopCancelsWarmup(t *testing.T) {
	repo := &runtimeSettings{entered: make(chan struct{})}
	s := runtimeBackup(repo, &runtimeArchive{})
	started := make(chan struct{})
	go func() { _ = s.StartContext(context.Background()); close(started) }()
	<-repo.entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, s.StopContext(ctx))
	<-started
	require.True(t, repo.cancelled.Load())
}
func TestS14B06RestoreMustRegister(t *testing.T) {
	repo := &runtimeSettings{}
	archive := &runtimeArchive{}
	s := runtimeBackup(repo, archive)
	data, err := json.Marshal([]BackupRecord{{ID: "fixture", Status: "completed", StorageType: "local", StorageKey: "fixture.gz"}})
	require.NoError(t, err)
	require.NoError(t, repo.Set(context.Background(), settingKeyBackupRecords, string(data)))
	repo.fail = true
	_, err = s.StartRestore(context.Background(), "fixture")
	require.Error(t, err)
	s.Stop()
	require.Zero(t, archive.calls.Load())
}

// 阻塞操作的超时结果必须被后续 Stop 复用，不能伪装成已排空。
func TestS14B02StopBudget(t *testing.T) {
	s := runtimeBackup(&runtimeSettings{}, &runtimeArchive{})
	_, done, err := s.begin(context.Background())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	first := s.StopContext(ctx)
	require.ErrorIs(t, first, context.DeadlineExceeded)
	require.Equal(t, first, s.StopContext(context.Background()))
	done()
}

// 无效 cron 配置使定时备份降级，不得阻断整套应用启动。
func TestS14StoredCronFailureDegrades(t *testing.T) {
	repo := &runtimeSettings{values: map[string]string{settingKeyBackupSchedule: `{"enabled":true,"cron_expr":"not-a-cron"}`}}
	s := runtimeBackup(repo, &runtimeArchive{})
	require.NoError(t, s.StartContext(context.Background()))
	require.NoError(t, s.StopContext(context.Background()))
}

// 已在停机前开始的清理，也必须受随后传入的应用剩余预算约束。
func TestS14B02CleanupUsesShutdownBudget(t *testing.T) {
	s := runtimeBackup(&runtimeSettings{}, &runtimeArchive{})
	cleanup, cancelCleanup := s.cleanupContext(time.Hour)
	defer cancelCleanup()
	budget, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	s.BeginStopContext(budget)
	select {
	case <-cleanup.Done():
		require.Error(t, cleanup.Err())
	case <-time.After(time.Second):
		t.Fatal("清理没有接入停止预算")
	}
	s.Stop()
}
