package testkit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/infra/timingwheel"
	"github.com/TokenFlux/TokenRouter/internal/provider"
)

// deferred_activity.go builds a deferred activity recorder and captures
// provider timestamps written during tests. The repository records the last
// persisted timestamp per provider in sync.Map, and the fixture registers
// service shutdown so buffered activity is flushed during test cleanup.

// deferredActivityRepository captures persisted activity timestamps for
// gateway assertions.
type deferredActivityRepository struct {
	updates sync.Map // latest persisted timestamp by provider
}

// BatchUpdateLastUsed stores each supplied provider timestamp for
// deferred-activity assertions. The sync.Map permits recorder writes and test
// reads from separate goroutines; repeated IDs replace their earlier observed
// timestamp.
func (r *deferredActivityRepository) BatchUpdateLastUsed(_ context.Context, updates map[int64]time.Time) error {
	for id, ts := range updates {
		r.updates.Store(id, ts)
	}
	return nil
}

// DeferredActivityRecorder constructs the production deferred service with a
// one-second flush interval and exposes its timestamp map. It registers
// StopContext in test cleanup; callers finish assertions before that test
// ends.
func DeferredActivityRecorder(t *testing.T) (*provider.DeferredService, *sync.Map) {
	t.Helper()
	wheel := timingwheel.New()
	repo := &deferredActivityRepository{}
	svc := provider.NewDeferredService(repo, wheel, provider.DeferredOptions{Interval: time.Second, Now: time.Now, Observe: func(format string, args ...any) { logging.LegacyPrintf("testkit.deferred", format, args...) }})
	// Stop the production recorder during cleanup so its registered timer and
	// queued timestamp batch are handled before the fixture's test lifetime ends.
	t.Cleanup(func() { require.NoError(t, svc.StopContext(context.Background())) })
	return svc, &repo.updates
}
