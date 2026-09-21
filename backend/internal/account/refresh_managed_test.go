package account

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 手动与后台入口真实使用同一个协调器，停止取消已接纳工作与锁等待。
func TestManagedRefreshSharesLockAndStopWithBackground(t *testing.T) {
	repo := &lifecycleRefreshRepository{}
	api := NewOAuthRefreshAPI(repo, nil, RefreshOptions{})
	entered := make(chan struct{})
	done := make(chan error, 2)
	observed := &Record{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive}
	executor := &lifecycleRefreshExecutor{started: make(chan struct{}), release: make(chan struct{})}
	go func() {
		_, _, err := api.WithManagedRefresh(context.Background(), observed, executor.CacheKey(observed), func(ctx context.Context, _ *Record) (*Record, string, error) {
			close(entered)
			<-ctx.Done()
			return nil, "", ctx.Err()
		})
		done <- err
	}()
	<-entered
	go func() {
		_, err := api.RefreshIfNeeded(context.Background(), observed, executor, time.Minute)
		done <- err
	}()
	require.Eventually(t, func() bool {
		api.activity.mu.Lock()
		defer api.activity.mu.Unlock()
		return len(api.activity.active) == 2
	}, time.Second, time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, api.StopContext(ctx))
	for range 2 {
		require.ErrorIs(t, <-done, context.Canceled)
	}
	require.Zero(t, executor.calls.Load())
	require.Zero(t, repo.writes.Load())
}

type managedRefreshReader struct {
	reads   atomic.Int32
	current *Record
}

func (r *managedRefreshReader) GetByID(context.Context, int64) (*Record, error) {
	r.reads.Add(1)
	return CloneRecord(r.current), nil
}
func TestManagedRefreshUsesLockTimeCredentialSnapshot(t *testing.T) {
	observed := &Record{ID: 5, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusDisabled, Credentials: map[string]any{"refresh_token": "selected"}}
	current := CloneRecord(observed)
	current.Credentials["refresh_token"] = "current"
	reader := &managedRefreshReader{current: current}
	api := NewOAuthRefreshAPI(reader, nil, RefreshOptions{})
	out, warning, err := api.WithManagedRefresh(context.Background(), observed, "openai:5", func(_ context.Context, v *Record) (*Record, string, error) {
		require.Equal(t, "current", v.Credentials["refresh_token"])
		return v, "", nil
	})
	require.NoError(t, err)
	require.Empty(t, warning)
	require.Equal(t, StatusDisabled, out.Status, "显式管理刷新保持禁用账号资格")
	require.Equal(t, "selected", observed.Credentials["refresh_token"])
	require.Equal(t, int32(1), reader.reads.Load())
}
func TestManagedRefreshRejectsShadowBeforeReading(t *testing.T) {
	parent := int64(1)
	reader := &managedRefreshReader{}
	api := NewOAuthRefreshAPI(reader, nil, RefreshOptions{})
	_, _, err := api.WithManagedRefresh(context.Background(), &Record{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent, QuotaDimension: QuotaDimensionSpark}, "unused", nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "SPARK_SHADOW_NO_REFRESH")
	require.Zero(t, reader.reads.Load())
}
func TestManagedRefreshConditionIsNotHTTPInput(t *testing.T) {
	payload, err := json.Marshal(UpdateAccountInput{ExpectedCredentials: &CredentialVersion{Credentials: map[string]any{"refresh_token": "internal-secret"}}})
	require.NoError(t, err)
	require.NotContains(t, string(payload), "internal-secret")
	require.NotContains(t, string(payload), "ExpectedCredentials")
}
