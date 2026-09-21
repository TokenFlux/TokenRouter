package account

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 不响应取消的供应商也不能在超时返回后继续持久化隐私结果。
type privacyLifecycleStore struct{ writes atomic.Int32 }

func (s *privacyLifecycleStore) UpdatePrivacyModeIfUnchanged(context.Context, UsageObservationVersion, string) (bool, error) {
	s.writes.Add(1)
	return true, nil
}
func TestPrivacyLifecycleCancelsAndPreventsLateWrites(t *testing.T) {
	for _, ignore := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "deadline"}[ignore], func(t *testing.T) {
			store := &privacyLifecycleStore{}
			entered, release := make(chan struct{}), make(chan struct{})
			done := make(chan string, 1)
			var calls atomic.Int32
			core := NewPrivacyService(store, nil, PrivacyOptions{OpenAI: func(ctx context.Context, _, _ string) string {
				calls.Add(1)
				close(entered)
				if ignore {
					<-release
				} else {
					<-ctx.Done()
				}
				return "disabled"
			}})
			value := &Record{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "fixture"}}
			require.Zero(t, calls.Load())
			go func() { done <- core.ForceOpenAIPrivacy(context.Background(), value) }()
			<-entered
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			err := core.StopContext(ctx)
			if ignore {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.ErrorContains(t, err, "unfinished")
			} else {
				require.NoError(t, err)
			}
			close(release)
			require.Empty(t, <-done)
			require.Zero(t, store.writes.Load())
			require.Empty(t, core.ForceOpenAIPrivacy(context.Background(), value))
			require.Equal(t, int32(1), calls.Load())
			if ignore {
				require.Same(t, err, core.StopContext(context.Background()))
			} else {
				require.NoError(t, core.StopContext(context.Background()))
			}
		})
	}
}
