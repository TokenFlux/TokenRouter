package settings

import (
	"context"
	"errors"
	"maps"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeStoreStub records accepted batches and injects persistence failures for Store tests.
type writeStoreStub struct {
	Repository // unused inherited repository operations
	// values retains the map received by a successful SetMultiple call. Tests
	// read it after that call and keep its entries stable during subscriber assertions.
	values map[string]string
	err    error // configured batch persistence failure
}

// configStoreStub implements ConfigurationRepository to verify direct delegation
// from Store.GetConfigurationValues without invoking Repository.GetAll.
type configStoreStub struct {
	Repository                   // unused inherited repository operations
	config     map[string]string // pre-filtered configuration values returned by stub
	called     bool              // whether GetConfigurationValues was invoked
}

// SetMultiple returns the configured persistence error or retains the supplied
// map for assertions. The fixture ignores context cancellation and copies no
// entries, so the successful caller's map remains shared with values.
func (r *writeStoreStub) SetMultiple(_ context.Context, values map[string]string) error {
	if r.err != nil {
		return r.err
	}
	r.values = values
	return nil
}

// GetAll returns a copy of values or the configured error so fallback filtering
// tests can inspect Store.GetConfigurationValues without mutating fixture entries.
func (r *writeStoreStub) GetAll(_ context.Context) (map[string]string, error) {
	if r.err != nil {
		return nil, r.err
	}
	out := make(map[string]string, len(r.values))
	maps.Copy(out, r.values)
	return out, nil
}

// GetConfigurationValues records invocation and returns the stub's configuration map.
func (r *configStoreStub) GetConfigurationValues(_ context.Context) (map[string]string, error) {
	r.called = true
	return r.config, nil
}

// TestStorePreservesWriteAndExplicitNotificationBoundary fails and then accepts a
// settings batch while tracking subscription calls. Neither write invokes callbacks;
// NotifyUpdated later observes the saved value and returns the expected event order.
func TestStorePreservesWriteAndExplicitNotificationBoundary(t *testing.T) {
	repo := &writeStoreStub{}
	store := New(repo)
	require.Same(t, store, New(store))
	var events []string
	store.Subscribe(func() { require.Equal(t, "v", repo.values["k"]); events = append(events, "subscriber") })
	repo.err = errors.New("write failed")
	require.Error(t, store.SetMultiple(context.Background(), map[string]string{"k": "v"}))
	require.Empty(t, events)
	repo.err = nil
	require.NoError(t, store.SetMultiple(context.Background(), map[string]string{"k": "v"}))
	require.Empty(t, events)
	store.NotifyUpdated()
	require.Equal(t, []string{"subscriber"}, events)
}

// TestStoreUnsubscribeDuringNotification has the first callback remove the second
// registration during notification. The second callback stays uncalled across
// repeated notification, and repeated removal of its registration succeeds.
func TestStoreUnsubscribeDuringNotification(t *testing.T) {
	store := New(nil)
	var cancelSecond func()
	store.Subscribe(func() { cancelSecond() })
	cancelSecond = store.Subscribe(func() { t.Error("注销后仍领取后续回调") })
	store.NotifyUpdated()
	cancelSecond()
	store.NotifyUpdated()
}

// TestStoreVersionAndSubscriptionConcurrency repeatedly registers, publishes, and
// removes callbacks while changing the version from concurrent workers. After all
// workers finish, the stored version retains their common published text.
func TestStoreVersionAndSubscriptionConcurrency(t *testing.T) {
	store := New(nil)
	var wg sync.WaitGroup
	// Overlap registration, version access, and notification across multiple workers
	// so callback reads exercise the same lock and active-flag transitions during publication.
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

// TestStoreGetConfigurationValuesChecksDelegationAndFallback exercises both a
// repository implementing ConfigurationRepository and an ordinary Repository stub.
// Direct readers delegate without calling GetAll, while fallback readers strip
// notification delivery, locale, and preference prefixes and retain site settings.
func TestStoreGetConfigurationValuesChecksDelegationAndFallback(t *testing.T) {
	direct := &configStoreStub{config: map[string]string{"site_name": "TokenRouter"}}
	gotDirect, err := New(direct).GetConfigurationValues(context.Background())
	require.NoError(t, err)
	require.True(t, direct.called)
	require.Equal(t, map[string]string{"site_name": "TokenRouter"}, gotDirect)

	fallback := &writeStoreStub{values: map[string]string{
		"site_name":                               "TokenRouter",
		"notification_email_template:welcome":     "{}",
		"notification_email_unsubscribe_secret":   "secret",
		"notification_email_delivery:v2:abc":      "2026-11-19T00:00:00Z",
		"notification_email_locale:email:def":     "zh-CN",
		"notification_email_preference:user:1:op": "unsubscribed",
	}}
	gotFallback, err := New(fallback).GetConfigurationValues(context.Background())
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"site_name":                             "TokenRouter",
		"notification_email_template:welcome":   "{}",
		"notification_email_unsubscribe_secret": "secret",
	}, gotFallback)
}
