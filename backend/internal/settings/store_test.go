package settings

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type writeStoreStub struct {
	Repository
	values map[string]string
	err    error
}

func (r *writeStoreStub) SetMultiple(_ context.Context, values map[string]string) error {
	if r.err != nil {
		return r.err
	}
	r.values = values
	return nil
}

func TestStorePreservesWriteAndExplicitNotificationBoundary(t *testing.T) {
	repo := &writeStoreStub{}
	store := New(repo)
	require.Same(t, store, New(store))
	var events []string
	store.SetOnUpdateCallback(func() { events = append(events, "old") })
	store.SetOnUpdateCallback(func() { events = append(events, "replacement") })
	store.Subscribe(func() { require.Equal(t, "v", repo.values["k"]); events = append(events, "subscriber") })
	repo.err = errors.New("write failed")
	require.Error(t, store.SetMultiple(context.Background(), map[string]string{"k": "v"}))
	require.Empty(t, events)
	repo.err = nil
	require.NoError(t, store.SetMultiple(context.Background(), map[string]string{"k": "v"}))
	require.Empty(t, events)
	store.NotifyUpdated()
	require.Equal(t, []string{"replacement", "subscriber"}, events)
}

func TestStoreUnsubscribeDuringNotification(t *testing.T) {
	store := New(nil)
	var cancelSecond func()
	store.Subscribe(func() { cancelSecond() })
	cancelSecond = store.Subscribe(func() { t.Error("注销后仍领取后续回调") })
	store.NotifyUpdated()
	cancelSecond()
	store.NotifyUpdated()
}

func TestStoreVersionAndSubscriptionConcurrency(t *testing.T) {
	store := New(nil)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			for range 40 {
				cancel := store.Subscribe(func() { _ = store.Version() })
				store.SetVersion("v-test")
				store.NotifyUpdated()
				cancel()
			}
		})
	}
	wg.Wait()
	require.Equal(t, "v-test", store.Version())
}
