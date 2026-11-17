package provider

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/provider/qodermodel"
)

// qoderCatalogStore models identity-checked persistence for catalog tests.
type qoderCatalogStore struct {
	mu  sync.Mutex // protects the mutable record
	row *Record    // current provider storage
	// committed optionally records completed fixture writes without blocking
	// persistence. Tests configure this buffered channel before starting queries.
	committed chan struct{} // optional fixture commit signal
}

// read returns a copied provider while excluding concurrent fixture writes.
func (s *qoderCatalogStore) read(context.Context, int64) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return CloneRecord(s.row), nil
}

// UpdateUsageExtraIfUnchanged rejects superseded credentials and serializes
// accepted data into detached JSON containers. This models persisted ownership
// rather than retaining a caller's mutable snapshot pointer.
func (s *qoderCatalogStore) UpdateUsageExtraIfUnchanged(ctx context.Context, version UsageObservationVersion, updates map[string]any) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if !MatchesCredentialVersion(s.row, version.CredentialVersion) {
		return false, nil
	}
	raw, err := json.Marshal(updates)
	if err != nil {
		return false, err
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false, err
	}
	if s.row.Extra == nil {
		s.row.Extra = map[string]any{}
	}
	maps.Copy(s.row.Extra, decoded)
	if s.committed != nil {
		select {
		case s.committed <- struct{}{}:
		default:
		}
	}
	return true, nil
}

// newQoderCatalogStore supplies a synthetic account with no initial models.
func newQoderCatalogStore() *qoderCatalogStore {
	return &qoderCatalogStore{row: &Record{ID: 7, Platform: PlatformQoder, Type: ProviderTypeCosy, Status: StatusActive, Credentials: map[string]any{"site": "global", "uid": "test-user", "security_oauth_token": "test-token"}}}
}

// TestQoderCatalogManualReplacement verifies that age and failed queries leave
// a valid saved catalog available. A successful empty list replaces it, while
// changing the account immediately invalidates the observation.
func TestQoderCatalogManualReplacement(t *testing.T) {
	store := newQoderCatalogStore()
	now := time.Now().UTC()
	models := []qodermodel.Model{{Key: "route", Alias: "new-model", Enabled: true, InputLimit: 1000000}}
	var fetchErr error
	service := NewQoderCatalogService(QoderCatalogOptions{Read: store.read, Writer: store, Now: func() time.Time { return now }, Fetch: func(context.Context, *Record) ([]qodermodel.Model, error) { return models, fetchErr }})
	t.Cleanup(func() { require.NoError(t, service.StopContext(context.Background())) })
	first, err := service.Refresh(t.Context(), 7)
	require.NoError(t, err)
	require.NotEmpty(t, first.Version)
	row, err := store.read(t.Context(), 7)
	require.NoError(t, err)
	require.Equal(t, []string{"new-model"}, row.QoderRequestModelIDs())
	fetchErr = errors.New("upstream failure with private body")
	now = now.Add(30 * 24 * time.Hour)
	_, err = service.Refresh(t.Context(), 7)
	require.ErrorIs(t, err, fetchErr)
	row, err = store.read(t.Context(), 7)
	require.NoError(t, err)
	retained, err := row.QoderCatalog(now)
	require.NoError(t, err)
	require.Equal(t, first.FetchedAt, retained.FetchedAt)
	require.Equal(t, first.Version, retained.Version)
	require.Equal(t, models, row.QoderCatalogStatus(now)["model_details"])
	models, fetchErr = []qodermodel.Model{}, nil
	_, err = service.Refresh(t.Context(), 7)
	require.NoError(t, err)
	row, err = store.read(t.Context(), 7)
	require.NoError(t, err)
	snapshot, err := row.QoderCatalog(now)
	require.NoError(t, err)
	require.NotNil(t, snapshot.Models)
	require.Empty(t, snapshot.Models)
	row.Credentials["uid"] = "replacement-user"
	_, err = row.QoderCatalog(now)
	require.ErrorIs(t, err, ErrQoderCatalogUnavailable)
}

// TestQoderCatalogRejectsLateAccountResult changes identity during a pending
// query. Conditional publication rejects the old account's models even when
// the upstream operation itself completes successfully.
func TestQoderCatalogRejectsLateAccountResult(t *testing.T) {
	store := newQoderCatalogStore()
	started, release := make(chan struct{}), make(chan struct{})
	service := NewQoderCatalogService(QoderCatalogOptions{Read: store.read, Writer: store, Fetch: func(context.Context, *Record) ([]qodermodel.Model, error) {
		close(started)
		<-release
		return []qodermodel.Model{{Key: "old-route", Enabled: true}}, nil
	}})
	result := make(chan error, 1)
	go func() {
		_, err := service.Refresh(t.Context(), 7)
		result <- err
	}()
	<-started
	store.mu.Lock()
	store.row.Credentials["security_oauth_token"] = "replacement-token"
	store.mu.Unlock()
	close(release)
	require.ErrorIs(t, <-result, ErrUsageObservationChanged)
	require.NoError(t, service.StopContext(t.Context()))
	row, err := store.read(t.Context(), 7)
	require.NoError(t, err)
	require.Nil(t, parseQoderCatalog(row))
}

// TestQoderCatalogQuerySurvivesWaiterCancellation keeps an upstream query alive
// when its waiter leaves. The successful observation is still persisted, and
// shutdown rejects subsequent refreshes after joining the shared work.
func TestQoderCatalogQuerySurvivesWaiterCancellation(t *testing.T) {
	store := newQoderCatalogStore()
	store.committed = make(chan struct{}, 1)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	service := NewQoderCatalogService(QoderCatalogOptions{Read: store.read, Writer: store, Fetch: func(ctx context.Context, _ *Record) ([]qodermodel.Model, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return []qodermodel.Model{{Key: "route", Enabled: true}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}})
	t.Cleanup(func() { require.NoError(t, service.StopContext(context.Background())) })
	ctx, cancel := context.WithCancel(t.Context())
	first := make(chan error, 1)
	go func() {
		_, err := service.Refresh(ctx, 7)
		first <- err
	}()
	<-started
	cancel()
	require.ErrorIs(t, <-first, context.Canceled)
	close(release)
	select {
	case <-store.committed:
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter prevented shared catalog publication")
	}
	require.Equal(t, int32(1), calls.Load())
	require.NoError(t, service.StopContext(t.Context()))
	_, err := service.Refresh(t.Context(), 7)
	require.ErrorIs(t, err, ErrProbeStopped)
}

// TestQoderCatalogLimitsConcurrentAccountsAndCancelsQueuedWork starts more
// distinct account queries than the shared pool admits. Shutdown must cancel
// the active transports, release every waiting caller, and preserve the stored
// catalog because none of the network observations completed successfully.
func TestQoderCatalogLimitsConcurrentAccountsAndCancelsQueuedWork(t *testing.T) {
	const accounts = 8
	store := newQoderCatalogStore()
	entered := make(chan int64, accounts)
	transportExited := make(chan struct{}, accounts)
	service := NewQoderCatalogService(QoderCatalogOptions{
		Read: func(ctx context.Context, id int64) (*Record, error) {
			record, err := store.read(ctx, id)
			record.ID = id
			return record, err
		},
		Writer: store,
		Fetch: func(ctx context.Context, record *Record) ([]qodermodel.Model, error) {
			entered <- record.ID
			<-ctx.Done()
			transportExited <- struct{}{}
			return nil, ctx.Err()
		},
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	defer func() { _ = service.StopContext(ctx) }()
	results := make(chan error, accounts)
	for index := range accounts {
		go func() {
			_, err := service.Refresh(ctx, int64(index+1))
			results <- err
		}()
	}
	for range 4 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("four distinct accounts did not reach the upstream transport")
		}
	}
	select {
	case <-entered:
		t.Fatal("a fifth account entered an occupied upstream pool")
	case <-time.After(50 * time.Millisecond):
	}
	require.NoError(t, service.StopContext(ctx))
	for range accounts {
		select {
		case err := <-results:
			require.Error(t, err)
		case <-ctx.Done():
			t.Fatal("catalog shutdown left a refresh waiting")
		}
	}
	require.Len(t, transportExited, 4)
	require.Empty(t, store.row.Extra)
}

// TestQoderCatalogIndexPublication keeps a hydrated account stable across record
// copies and denies stale whitelist entries after replacement. Explicit mappings
// can still introduce a custom route, and replacing credentials invalidates it.
func TestQoderCatalogIndexPublication(t *testing.T) {
	row := newQoderCatalogStore().row
	row.Credentials["model_whitelist"] = []string{"old-key", "old-alias"}
	snap := QoderCatalogSnapshot{Identity: QoderCatalogIdentity(row), Version: "first", FetchedAt: time.Now(), Models: []qodermodel.Model{{Key: "old-key", Alias: "old-alias", Enabled: true}}}
	row.Extra = map[string]any{QoderCatalogExtraKey: snap}
	row.PrepareQoderCatalog()
	first, err := row.QoderModelIndex()
	require.NoError(t, err)
	clone := CloneRecord(row)
	copyIndex, err := clone.QoderModelIndex()
	require.NoError(t, err)
	require.Same(t, first, copyIndex)
	snap.Version = "second"
	snap.Models = []qodermodel.Model{}
	row.Extra[QoderCatalogExtraKey] = snap
	row.PrepareQoderCatalog()
	second, err := row.QoderModelIndex()
	require.NoError(t, err)
	require.NotEqual(t, first.Version(), second.Version())
	require.False(t, row.QoderModelRules().QoderCompatible("old-key"))
	require.False(t, row.QoderModelRules().QoderCompatible("old-alias"))
	require.True(t, clone.QoderModelRules().QoderCompatible("old-alias"))
	row.Credentials["model_mapping"] = map[string]any{"custom": "new-key"}
	require.True(t, row.QoderModelRules().QoderCompatible("new-key"))
	row.Credentials["uid"] = "another-user"
	_, err = row.QoderModelIndex()
	require.ErrorIs(t, err, ErrQoderCatalogUnavailable)
}

// TestOlderCatalogUsesDisplayIDAndNativeDefault reads a snapshot produced before
// branded models received display IDs. Management, selection and the runtime
// index must agree on both the public alias and the mandatory reasoning switch.
func TestOlderCatalogUsesDisplayIDAndNativeDefault(t *testing.T) {
	row := newQoderCatalogStore().row
	row.Extra = map[string]any{QoderCatalogExtraKey: QoderCatalogSnapshot{Identity: QoderCatalogIdentity(row), Version: "older", FetchedAt: time.Now(), Models: []qodermodel.Model{{Key: "cmodel", Name: "Cantus", Enabled: true, Reasoning: new(true), CanDisable: new(false), DefaultEffort: "high", Efforts: []string{"high"}}}}}
	row.PrepareQoderCatalog()
	require.Equal(t, []string{"cantus"}, row.QoderRequestModelIDs())
	index, err := row.QoderModelIndex()
	require.NoError(t, err)
	for _, id := range []string{"cantus", "cmodel"} {
		model, ok := index.Lookup(id)
		require.True(t, ok)
		require.Equal(t, "cmodel", model.Key)
		require.True(t, model.DefaultThinking)
	}
	observation, err := row.QoderCatalog(time.Now())
	require.NoError(t, err)
	require.True(t, observation.Models[0].DefaultThinking)
}
