package account

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 五秒独立写回不阻塞查询返回，应用停止仍能等待或报告未完成。
type openAIUsageWriteOwner struct {
	OAuthUsageReader
	entered, release, exited chan struct{}
	version                  UsageObservationVersion
	updates                  map[string]any
}

func (w *openAIUsageWriteOwner) UpdateUsageExtraIfUnchanged(ctx context.Context, v UsageObservationVersion, updates map[string]any) (bool, error) {
	w.version = v
	w.updates = updates
	close(w.entered)
	defer close(w.exited)
	<-w.release
	return false, ctx.Err()
}
func TestOAuthUsageOpenAIWritebackHasBoundedOwner(t *testing.T) {
	store := &openAIUsageWriteOwner{entered: make(chan struct{}), release: make(chan struct{}), exited: make(chan struct{})}
	t.Cleanup(func() { close(store.release); <-store.exited })
	core := NewOAuthUsageService(store, nil, nil, OAuthUsageOptions{})
	observed := &Record{ID: 2, Credentials: map[string]any{"access_token": "observed"}}
	updates := map[string]any{"nested": map[string]any{"used": float64(1)}}
	core.PersistOpenAICodexProbeSnapshot(observed, updates)
	<-store.entered
	observed.Credentials["access_token"] = "changed"
	nested, ok := updates["nested"].(map[string]any)
	require.True(t, ok)
	nested["used"] = float64(2)
	require.Equal(t, "observed", store.version.Credentials["access_token"])
	copied, ok := store.updates["nested"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(1), copied["used"])
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := core.StopContext(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "unfinished")
	require.Same(t, err, core.StopContext(context.Background()))
}
