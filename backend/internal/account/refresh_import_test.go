package account

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 导入和后台共享真实协调器；存储替身只实现同一身份的条件写入。
type importRefreshStore struct {
	mu            sync.Mutex
	current       *Record
	reads, writes int
	failure       error
}

func (s *importRefreshStore) GetByID(context.Context, int64) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	return snapshotRefreshRecord(s.current), nil
}
func (s *importRefreshStore) UpdateOAuthCredentialsIfUnchanged(_ context.Context, v CredentialVersion, credentials map[string]any) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return false, s.failure
	}
	c := s.current
	if c.Status != v.Status || c.Platform != v.Platform || c.Type != v.Type || !reflect.DeepEqual(c.ProxyID, v.ProxyID) || !reflect.DeepEqual(c.Credentials, v.Credentials) {
		return false, nil
	}
	s.writes++
	c.Credentials = CloneValues(credentials)
	return true, nil
}
func newImportRefreshStore() *importRefreshStore {
	return &importRefreshStore{current: &Record{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Credentials: map[string]any{"refresh_token": "old"}}}
}
func TestRefreshImportedPreservesExplicitStatusAndTokenVersion(t *testing.T) {
	for _, status := range []string{StatusActive, "inactive", StatusError} {
		t.Run(status, func(t *testing.T) {
			s := newImportRefreshStore()
			s.current.Status = status
			api := NewOAuthRefreshAPI(s, nil, RefreshOptions{})
			source, _ := s.GetByID(context.Background(), 1)
			credentials := map[string]any{"refresh_token": "new", "_token_version": float64(123), "nested": map[string]any{"keep": true}}
			require.NoError(t, api.RefreshImported(context.Background(), source, "shared", func(context.Context, *Record) map[string]any { return credentials }))
			require.Equal(t, 1, s.writes)
			require.Equal(t, status, s.current.Status)
			require.Equal(t, credentials, s.current.Credentials)
			stored, ok := s.current.Credentials["nested"].(map[string]any)
			require.True(t, ok)
			stored["keep"] = false
			original, ok := credentials["nested"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, true, original["keep"])
		})
	}
}
func TestRefreshImportedStaleResultRereadsWithoutSecondExchange(t *testing.T) {
	for _, change := range []string{"credentials", "proxy", "status"} {
		t.Run(change, func(t *testing.T) {
			s := newImportRefreshStore()
			source, _ := s.GetByID(context.Background(), 1)
			api := NewOAuthRefreshAPI(s, nil, RefreshOptions{})
			calls := 0
			require.NoError(t, api.RefreshImported(context.Background(), source, "shared", func(context.Context, *Record) map[string]any {
				calls++
				s.mu.Lock()
				defer s.mu.Unlock()
				switch change {
				case "credentials":
					s.current.Credentials = map[string]any{"refresh_token": "admin"}
				case "proxy":
					id := int64(7)
					s.current.ProxyID = &id
				case "status":
					s.current.Status = "inactive"
				}
				return map[string]any{"refresh_token": "late"}
			}))
			require.Equal(t, 1, calls)
			require.Zero(t, s.writes)
			require.Equal(t, 3, s.reads)
		})
	}
}
func TestRefreshImportedSharesBackgroundLockAndStopsWaiting(t *testing.T) {
	s := newImportRefreshStore()
	api := NewOAuthRefreshAPI(s, nil, RefreshOptions{})
	source, _ := s.GetByID(context.Background(), 1)
	started, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 2)
	var calls atomic.Int32
	go func() {
		finished <- api.RefreshImported(context.Background(), source, "lifecycle:account", func(context.Context, *Record) map[string]any {
			calls.Add(1)
			close(started)
			<-release
			return map[string]any{"refresh_token": "late"}
		})
	}()
	<-started
	// 普通刷新必须等待同一把锁，不能在导入交换期间执行。
	executor := &lifecycleRefreshExecutor{started: make(chan struct{}), release: make(chan struct{})}
	go func() {
		_, err := api.RefreshIfNeeded(context.Background(), &Record{ID: 1}, executor, time.Minute)
		finished <- err
	}()
	require.Eventually(t, func() bool {
		api.activity.mu.Lock()
		defer api.activity.mu.Unlock()
		return len(api.activity.active) == 2
	}, time.Second, time.Millisecond)
	budget, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, api.StopContext(budget), context.DeadlineExceeded)
	require.Zero(t, executor.calls.Load())
	close(release)
	for range 2 {
		require.ErrorIs(t, <-finished, context.Canceled)
	}
	require.Zero(t, s.writes)
	require.Equal(t, int32(1), calls.Load())
	require.ErrorIs(t, api.RefreshImported(context.Background(), source, "shared", func(context.Context, *Record) map[string]any { t.Fatal("停止后不得交换"); return nil }), ErrRefreshStopped)
}
func TestRefreshImportedRejectsChangedSourceAndReportsPersistenceFailure(t *testing.T) {
	s := newImportRefreshStore()
	api := NewOAuthRefreshAPI(s, nil, RefreshOptions{})
	source, _ := s.GetByID(context.Background(), 1)
	source.Status = "inactive"
	require.ErrorIs(t, api.RefreshImported(context.Background(), source, "shared", func(context.Context, *Record) map[string]any { t.Fatal("旧身份不得交换"); return nil }), ErrRefreshAccountStateChanged)
	source.Status = StatusActive
	s.failure = errors.New("fixture persistence failed")
	require.ErrorIs(t, api.RefreshImported(context.Background(), source, "shared", func(context.Context, *Record) map[string]any { return map[string]any{"refresh_token": "new"} }), s.failure)
	require.Zero(t, s.writes)
}
