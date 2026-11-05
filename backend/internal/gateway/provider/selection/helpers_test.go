package selection

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// The interface assertion keeps the Gemini group fixture aligned with the full
// and lightweight reader methods required by selection.
var _ Groups = (*mockGroupRepoForGemini)(nil)

// candidateAttributeRepo supplies attribute profiles for candidate and probe tests.
type candidateAttributeRepo struct {
	failure                          error                         // configured profile read failure
	routing.ModelAttributeRepository                               // unused attribute repository operations
	config                           *routing.ModelAttributeConfig // configured route capability rules
}

// grokFreeQuotaUsageRepoStub records quota usage reads and copies provider
// statistics under its mutex.
type grokFreeQuotaUsageRepoStub struct {
	usage.UsageLogRepository // unused usage operations panic

	// mu serializes reads of stats and err with updates to calls, lastIDs, and
	// start. Batch results copy each statistics value before releasing that query
	// lock.
	mu    sync.Mutex                     // batch query serialization
	stats map[int64]*usage.ProviderStats // configured provider usage statistics
	// err supplies the usage query failure. A nonnil value returns after the
	// query trace updates and before any configured statistics are copied.
	err     error     // configured usage-read failure
	calls   int       // recorded batch query count
	lastIDs []int64   // copied last requested identifiers
	start   time.Time // requested usage-window start
}

// mockProviderRepoForPlatform supplies permissive platform candidates and
// mutable full-read fixture targets.
type mockProviderRepoForPlatform struct {
	providers     []gatewayprovider.ExecutionProvider          // permissive platform candidate slice
	providersByID map[int64]*gatewayprovider.ExecutionProvider // mutable full-read target pointers
	// listPlatformFunc overrides default platform filtering when configured. Its
	// returned pool and error let callers inject the scenario's candidate-read
	// outcome.
	listPlatformFunc func(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) // platform query override callback
	getByIDCalls     int                                                                                     // full provider lookup count
}

// mockGatewayCacheForPlatform records hash affinity and deletions for generic
// platform selection tests.
type mockGatewayCacheForPlatform struct {
	// sessionBindings supplies hash affinity and deletedSessions counts removal
	// attempts for those hashes. Reads and writes share both maps; group and TTL
	// inputs are ignored, so this fixture observes affinity and cleanup calls.
	sessionBindings map[string]int64 // fixture hash provider affinity
	deletedSessions map[string]int   // fixture hash removal counts
}

// mockGroupRepoForGateway supplies configured groups and counts full versus
// lightweight reads.
type mockGroupRepoForGateway struct {
	groups           map[int64]*routing.Group // configured group lookup pointers
	getByIDCalls     int                      // full group lookup count
	getByIDLiteCalls int                      // lightweight group lookup count
}

// mockConcurrencyCache supplies configurable slot outcomes and load
// observations for generic scheduler tests.
type mockConcurrencyCache struct {
	acquireProviderCalls int            // provider acquisition attempt count
	loadBatchCalls       int            // provider load batch count
	acquireResults       map[int64]bool // configured provider admission outcomes
	// loadBatchErr supplies the load query failure. A nonnil value returns
	// after incrementing loadBatchCalls and before configured or idle observations
	// are added to a result map.
	loadBatchErr error // configured load-read failure
	// loadMap and skipDefaultLoad control missing observations together:
	// configured entries win, and a nonnil map with omission enabled keeps absent
	// IDs missing for scheduler default handling.
	loadMap         map[int64]*schedulercore.ProviderLoadInfo // shared provider load observations
	waitCounts      map[int64]int                             // configured provider queue observations
	skipDefaultLoad bool                                      // missing-load omission switch
}

// mockGroupRepoForGemini supplies configured groups, a default group, and
// separate read counters.
type mockGroupRepoForGemini struct {
	groups           map[int64]*routing.Group // configured Gemini group pointers
	getByIDCalls     int                      // full Gemini lookup count
	getByIDLiteCalls int                      // lightweight Gemini lookup count
}

// selectionFixtureGroups returns hydrated active groups for selectors using
// default fixture admission.
type selectionFixtureGroups struct{}

// mixedGroupProviders filters explicit fixture memberships and records the
// group IDs queried by selection.
type mixedGroupProviders struct {
	Providers                                        // unused candidate reads panic
	values       []gatewayprovider.ExecutionProvider // explicit group member fixtures
	groupQueries []int64                             // requested group lookup trace
}

// openAISnapshotCacheStub separates cached candidate slices from full provider
// reads in scheduler tests.
type openAISnapshotCacheStub struct {
	schedulercore.SnapshotCache // unused snapshot operations panic
	// snapshotProviders and providersByID describe distinct reads: candidates can
	// remain stale while later full lookups supply different records with the same
	// persisted provider ID.
	snapshotProviders []*gatewayprovider.ExecutionProvider         // cached candidate target pointers
	providersByID     map[int64]*gatewayprovider.ExecutionProvider // full cached target pointers
}

// schedulerTestOpenAIProviderRepo supplies compatible fixture records and
// prepares omitted selection defaults.
type schedulerTestOpenAIProviderRepo struct {
	gatewayprovider.ExecutionProviderStore // unused provider operations panic

	// providers holds the mutable compatible candidate slice.
	providers []gatewayprovider.
			ExecutionProvider
}

// schedulerGroupAwareOpenAIProviderRepo constrains compatible fixture queries
// using existing stored group memberships.
type schedulerGroupAwareOpenAIProviderRepo struct {
	schedulerTestOpenAIProviderRepo // base compatible fixture reads
}

// schedulerTestConcurrencyCache supplies compatible slot and load observations
// with optional acquisition/release traces.
type schedulerTestConcurrencyCache struct {
	schedulercore.ConcurrencyCache // unused capacity operations panic
	// loadBatchErr supplies the compatible load query failure. A nonnil value
	// returns before reading loadMap or constructing idle default observations.
	loadBatchErr error // configured compatible load failure
	// loadMap and skipDefaultLoad distinguish an absent observation from a fixture
	// idle default. Explicit pointers remain shared; a nonnil map with omission
	// enabled returns the configured subset.
	loadMap         map[int64]*schedulercore.ProviderLoadInfo // shared compatible load observations
	acquireResults  map[int64]bool                            // configured compatible slot outcomes
	waitCounts      map[int64]int                             // configured compatible queue observations
	skipDefaultLoad bool                                      // missing-load omission switch
	// acquiredIDs and releasedIDs point to traces shared with the scenario.
	// Value-receiver calls append through those pointers, keeping acquisition and
	// cleanup order visible outside the cache copy.
	acquiredIDs *[]int64 // shared acquired-provider trace pointer
	releasedIDs *[]int64 // shared released-provider trace pointer
}

// schedulerTestGatewayCache records compatible hash bindings and deletion
// attempts for picker tests.
type schedulerTestGatewayCache struct {
	// sessionBindings stores compatible hash affinity, while deletedSessions
	// traces best-effort removals. Both maps remain attached to this fixture as
	// the picker updates and rereads the scenario's sticky decisions.
	sessionBindings map[string]int64 // compatible hash provider bindings
	deletedSessions map[string]int   // compatible hash removal counts
}

// advancedSchedulerSettingRepoStub supplies configured string settings while
// rejecting unexpected store operations.
type advancedSchedulerSettingRepoStub struct {
	values map[string]string // configured string setting inputs
}

// thresholdSelectionProviderRepoStub supplies platform candidates and embeds
// health-mutation recording for threshold tests.
type thresholdSelectionProviderRepoStub struct {
	gatewaytestkit.HealthStoreRecorder // observed health mutation recorder

	providers []gatewayprovider.ExecutionProvider // threshold platform candidate slice
}

// selectionProviderFixture supplies prepared compatible records and leaves
// unused reader operations unimplemented.
type selectionProviderFixture struct {
	Providers                                     // unused selection reads panic
	providers []gatewayprovider.ExecutionProvider // prepared compatible fixture slice
}

// responseCacheFixture combines hash-affinity fixture operations with unused
// response-cache methods for store tests.
type responseCacheFixture struct {
	stickyCacheFixture   // hash affinity fixture operations
	session.GatewayCache // unused response operations panic
}

// selectionConcurrencyFixture supplies compatible slot outcomes and load
// observations for basic selection scenarios.
type selectionConcurrencyFixture struct {
	schedulercore.ConcurrencyCache                // unused capacity operations panic
	acquireResults                 map[int64]bool // configured basic slot outcomes
	waitCounts                     map[int64]int  // configured basic queue observations
	// loadBatchErr supplies the basic load query failure. A nonnil value
	// returns before reading loadMap or constructing idle default observations.
	loadBatchErr error // configured basic load failure
	// loadMap and skipDefaultLoad control whether missing candidates receive idle
	// values. Explicit observations retain their pointer identity, while omission
	// requires both the switch and a nonnil map.
	loadMap         map[int64]*schedulercore.ProviderLoadInfo // shared basic load observations
	skipDefaultLoad bool                                      // missing-load omission switch
}

// hydrationProviderSource converts repository execution targets to snapshot
// records while retaining read errors.
type hydrationProviderSource struct {
	schedulercore.SnapshotProviderSource           // unused source operations panic
	source                               Providers // repository hydration target reader
}

// snapshotHydrationCache supplies separate lightweight candidates and full
// records for provider hydration tests.
type snapshotHydrationCache struct {
	schedulercore.SnapshotCache // unused hydration cache operations
	// snapshot and providers separate lightweight candidate choice from later
	// full-record hydration. Tests omit or change full records independently so
	// selection completion can observe preparation failures.
	snapshot  []*gatewayprovider.ExecutionProvider         // lightweight candidate target pointers
	providers map[int64]*gatewayprovider.ExecutionProvider // full hydration target pointers
}

// stickyCacheFixture stores hash bindings and deletion counts while accepting
// expiration refresh calls.
type stickyCacheFixture struct {
	// sessionBindings stores current and legacy hash affinity, and deletedSessions
	// records removal calls for those keys. Expiration is unmodeled, so later
	// reads continue to observe the fixture's stored provider until deletion.
	sessionBindings map[string]int64 // stored hash provider affinity
	deletedSessions map[string]int   // recorded hash removal counts
}

// availabilityRecords filters fixture records by supplied platforms, active
// status, scheduling flag, and optional group. Group queries inspect
// ProviderGroups, while includeGrouped allows grouped records in a request
// lacking an explicit group.
func (m *mockProviderRepoForPlatform) availabilityRecords(_ context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]gatewayprovider.ExecutionProvider, error) {
	platformSet := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		platformSet[platform] = struct{}{}
	}
	result := make([]gatewayprovider.ExecutionProvider, 0, len(m.providers))
	for _, acc := range m.providers {
		if _, ok := platformSet[acc.Record.Platform]; !ok || acc.Record.Status != billing.StatusActive || !acc.Record.Schedulable {
			continue
		}
		if groupID != nil {
			inGroup := false
			for _, providerGroup := range acc.Record.ProviderGroups {
				if providerGroup.GroupID == *groupID {
					inGroup = true
					break
				}
			}
			if !inGroup {
				continue
			}
		} else if !includeGrouped && (len(acc.Record.ProviderGroups) > 0 || len(acc.Record.GroupIDs) > 0) {
			continue
		}
		result = append(result, acc)
	}
	return result, nil
}

// isProviderRequestCompatible returns the boolean part of the fixture's
// compatible request eligibility result.
func (s *compatiblePicker) isProviderRequestCompatible(ctx context.Context, provider *gatewayprovider.ExecutionProvider, req schedulercore.PlatformSelectionInput) bool {
	compatible, _ := s.isProviderRequestCompatibleReason(ctx, provider, req)
	return compatible
}

// diagnosticParameterDefaults returns scheduler defaults with supplied process
// scoring and EWMA settings applied. Nil configuration retains built-ins;
// positive Top-K overrides the count, while configured weights and normalized
// sticky escape inputs replace their fixture defaults.
func diagnosticParameterDefaults(cfg *config.Config) schedulercore.ParameterDefaults {
	defaults := schedulercore.DefaultParameters()
	if cfg == nil {
		return defaults
	}
	value := cfg.Scheduling.Advanced
	if value.LBTopK > 0 {
		defaults.TopK = value.LBTopK
	}
	weights := value.ScoreWeights
	defaults.Weights = policy.ScoreWeights{Priority: weights.Priority, Load: weights.Load, Queue: weights.Queue, ErrorRate: weights.ErrorRate, TTFT: weights.TTFT, Reset: weights.Reset, QuotaHeadroom: weights.QuotaHeadroom, Previous: weights.PreviousResponse, SessionSticky: weights.SessionSticky}
	defaults.Runtime.EwmaErrorRateAlpha = value.EWMAErrorRateAlpha
	defaults.Runtime.EwmaTTFTAlpha = value.EWMATTFTAlpha
	defaults.Runtime.StickyEscape = policy.NormalizeStickyEscape(policy.StickyEscapeConfig{Enabled: value.StickyEscapeEnabled, TtftMs: float64(value.StickyEscapeTTFTMs), ErrorRate: value.StickyEscapeErrorRate})
	return defaults
}

// GetProviderWindowStatsBatch records requested IDs and the window start under
// the fixture mutex. Configured errors return before copying results, while
// present statistics receive separate value pointers so readers do not mutate
// fixture entries.
func (r *grokFreeQuotaUsageRepoStub) GetProviderWindowStatsBatch(_ context.Context, providerIDs []int64, start time.Time) (map[int64]*usage.ProviderStats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.lastIDs = append([]int64(nil), providerIDs...)
	r.start = start
	if r.err != nil {
		return nil, r.err
	}
	result := make(map[int64]*usage.ProviderStats, len(providerIDs))
	for _, providerID := range providerIDs {
		if stats := r.stats[providerID]; stats != nil {
			copyStats := *stats
			result[providerID] = &copyStats
		}
	}
	return result, nil
}

// newGrokFreeQuotaTestGate binds dynamic fixture settings, usage reads, and
// tracked background submission to FreeQuotaGate. Callers supply cfg and a
// background callback; the gate reads current Grok soft-budget options on each
// observation.
func newGrokFreeQuotaTestGate(cfg *config.Config, reader usage.UsageLogRepository, background func(string, func()) bool) *providercore.FreeQuotaGate {
	return providercore.NewFreeQuotaGate(func() providercore.FreeQuotaOptions {
		v := cfg.Scheduling.GrokFreeQuota
		return providercore.FreeQuotaOptions{Enabled: v.Enabled, TokenLimit: v.TokenLimit, Percent: v.ThresholdPercent, WindowHours: v.WindowHours, CacheSeconds: v.StatsCacheSeconds}
	}, func(ctx context.Context, ids []int64, start time.Time) (map[int64]int64, error) {
		return usage.ReadProviderTokenWindow(ctx, reader, ids, start)
	}, background, time.Now, nil, nil)
}

// healthyGrokOAuthGatewayTestProvider constructs an active Grok OAuth target
// with fictitious credentials. Token expiry is beyond the refresh skew, and
// callers supply the provider ID and access token needed by their eligibility
// scenario.
func healthyGrokOAuthGatewayTestProvider(id int64, token string) *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: id,
			Name:        "grok",
			Platform:    capability.PlatformGrok,
			Type:        capability.ProviderTypeOAuth,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token":  token,
				"refresh_token": "refresh-token",
				"expires_at":    time.Now().Add(2 * providercore.GrokTokenRefreshSkew).UTC().Format(time.RFC3339),
				"base_url":      xai.DefaultCLIBaseURL,
			},
		},
	}
}

// freeQuotaFactoryForTest returns a factory producing independent quota caches
// from the same fixture readers. It tracks accepted refresh tasks and waits
// during test cleanup, so background usage reads finish before fixture
// teardown.
func freeQuotaFactoryForTest(t *testing.T, cfg *config.Config, source usage.UsageLogRepository) func() *providercore.FreeQuotaGate {
	var tasks sync.WaitGroup
	t.Cleanup(tasks.Wait)
	// Refresh queries outlive the initial gate check. Track every accepted task
	// and wait during test cleanup so its fixture reads finish before teardown.
	background := func(_ string, work func()) bool {
		tasks.Go(func() { ; work() })
		return true
	}
	return func() *providercore.FreeQuotaGate { return newGrokFreeQuotaTestGate(cfg, source, background) }
}

// testConfig returns a zero-valued process configuration for scenario-specific
// fixture setup.
func testConfig() *config.Config {
	return &config.Config{}
}

// GetByID counts the lookup, prepares the stored fixture target, and returns
// its pointer. Missing IDs return a provider-not-found error; fixture defaults
// and referenced data remain visible to later reads.
func (m *mockProviderRepoForPlatform) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	m.getByIDCalls++
	if acc, ok := m.providersByID[id]; ok {
		prepareSelectionFixtureProvider(ctx, acc, nil)
		return acc, nil
	}
	return nil, errors.New("provider not found")
}

// ListSchedulableByPlatform invokes the configured query callback or filters
// schedulable platform matches. The callback supplies its own pool or error,
// while default results retain referenced data from the fixture records.
func (m *mockProviderRepoForPlatform) ListSchedulableByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	if m.listPlatformFunc != nil {
		return m.listPlatformFunc(ctx, platform)
	}
	var result []gatewayprovider.ExecutionProvider
	for _, acc := range m.providers {
		if acc.Record.Platform == platform && acc.View().IsSchedulable() {
			result = append(result, acc)
		}
	}
	return result, nil
}

// ListSchedulableByGroupIDAndPlatform fills omitted memberships before the
// fixture's platform query. Existing memberships remain present, and this
// permissive query leaves actual group isolation to selector checks or a
// group-aware fixture.
func (m *mockProviderRepoForPlatform) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	for i := range m.providers {
		prepareSelectionFixtureProvider(ctx, &m.providers[i], &groupID)
	}
	return m.ListSchedulableByPlatform(ctx, platform)
}

// ListSchedulableByGroupID returns no targets for the unused platform-fixture
// group query.
func (m *mockProviderRepoForPlatform) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]gatewayprovider.ExecutionProvider, error) {
	return nil, nil
}

// ListSchedulableByPlatforms returns schedulable fixture values in the supplied
// platform set.
func (m *mockProviderRepoForPlatform) ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]gatewayprovider.ExecutionProvider, error) {
	var result []gatewayprovider.ExecutionProvider
	platformSet := make(map[string]bool)
	for _, p := range platforms {
		platformSet[p] = true
	}
	for _, acc := range m.providers {
		if platformSet[acc.Record.Platform] && acc.View().IsSchedulable() {
			result = append(result, acc)
		}
	}
	return result, nil
}

// ListSchedulableByGroupIDAndPlatforms fills omitted memberships before
// multi-platform fixture lookup. The underlying permissive query retains
// existing group metadata, allowing selector tests to provide their own
// membership filtering scenarios.
func (m *mockProviderRepoForPlatform) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]gatewayprovider.ExecutionProvider, error) {
	for i := range m.providers {
		prepareSelectionFixtureProvider(ctx, &m.providers[i], &groupID)
	}
	return m.ListSchedulableByPlatforms(ctx, platforms)
}

// ListSchedulableUngroupedByPlatform delegates the permissive platform fixture
// query with memberships retained. Tests requiring ungrouped isolation use
// selector validation or a group-aware reader, while this method supplies the
// same configured platform records.
func (m *mockProviderRepoForPlatform) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return m.ListSchedulableByPlatform(ctx, platform)
}

// ListSchedulableUngroupedByPlatforms delegates permissive multi-platform
// lookup with fixture memberships retained. Its returned record values continue
// to share referenced data from the source pool used by these platform
// scenarios.
func (m *mockProviderRepoForPlatform) ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]gatewayprovider.ExecutionProvider, error) {
	return m.ListSchedulableByPlatforms(ctx, platforms)
}

// GetSessionProviderID reads a hash-keyed platform fixture binding. Group and
// TTL behavior are unmodeled, and missing hashes return a cache-miss error for
// the selector's sticky lookup handling.
func (m *mockGatewayCacheForPlatform) GetSessionProviderID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	if id, ok := m.sessionBindings[sessionHash]; ok {
		return id, nil
	}
	return 0, errors.New("not found")
}

// SetSessionProviderID stores a provider by hash, allocating fixture storage
// when needed. Group and TTL inputs are ignored, so subsequent assertions
// inspect affinity rather than a real expiring Redis binding.
func (m *mockGatewayCacheForPlatform) SetSessionProviderID(ctx context.Context, groupID int64, sessionHash string, providerID int64, ttl time.Duration) error {
	if m.sessionBindings == nil {
		m.sessionBindings = make(map[string]int64)
	}
	m.sessionBindings[sessionHash] = providerID
	return nil
}

// RefreshSessionTTL accepts refresh for the platform fixture and returns
// success. Bindings have no expiration state here, leaving stored affinity
// available for subsequent provider-choice and cleanup assertions.
func (m *mockGatewayCacheForPlatform) RefreshSessionTTL(ctx context.Context, groupID int64, sessionHash string, ttl time.Duration) error {
	return nil
}

// DeleteSessionProviderID records a hash deletion attempt and removes its
// fixture binding. A nil binding map returns immediately; once present,
// repeated removals remain visible in deletedSessions even after the binding is
// absent.
func (m *mockGatewayCacheForPlatform) DeleteSessionProviderID(ctx context.Context, groupID int64, sessionHash string) error {
	if m.sessionBindings == nil {
		return nil
	}
	if m.deletedSessions == nil {
		m.deletedSessions = make(map[string]int)
	}
	m.deletedSessions[sessionHash]++
	delete(m.sessionBindings, sessionHash)
	return nil
}

// GetByID increments the full-group read count and returns the configured group
// pointer. Missing IDs return ErrGroupNotFound, allowing admission and
// optional-setting paths to distinguish absent fixture groups.
func (m *mockGroupRepoForGateway) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	m.getByIDCalls++
	if g, ok := m.groups[id]; ok {
		return g, nil
	}
	return nil, routing.ErrGroupNotFound
}

// GetByIDLite increments the lightweight read count and returns the configured
// group pointer. Missing IDs return ErrGroupNotFound, keeping its admission
// lookup trace separate from full scheduling-setting reads.
func (m *mockGroupRepoForGateway) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	m.getByIDLiteCalls++
	if g, ok := m.groups[id]; ok {
		return g, nil
	}
	return nil, routing.ErrGroupNotFound
}

// AcquireProviderSlot counts acquisition calls and returns a configured
// provider outcome when present. Missing outcomes admit the request, while the
// fixture leaves real occupancy and request-ID ownership unmodeled.
func (m *mockConcurrencyCache) AcquireProviderSlot(ctx context.Context, providerID int64, maxConcurrency int, requestID string) (bool, error) {
	m.acquireProviderCalls++
	if m.acquireResults != nil {
		if result, ok := m.acquireResults[providerID]; ok {
			return result, nil
		}
	}
	return true, nil
}

// ReleaseProviderSlot accepts generic fixture cleanup and returns success.
// Acquisition outcomes come from the configured map, so releasing does not
// change occupancy or cause a later attempt's configured outcome to differ.
func (m *mockConcurrencyCache) ReleaseProviderSlot(ctx context.Context, providerID int64, requestID string) error {
	return nil
}

// GetProviderConcurrency returns zero occupancy for the generic concurrency
// fixture. Slot outcomes and load observations are configured separately, so
// this individual read supplies the idle default requested by callers.
func (m *mockConcurrencyCache) GetProviderConcurrency(ctx context.Context, providerID int64) (int, error) {
	return 0, nil
}

// GetProviderConcurrencyBatch builds a new map reporting zero occupancy for
// each requested provider. Its values are fixture defaults, while independently
// configured acquisition and load observations drive the scenario's capacity
// decisions.
func (m *mockConcurrencyCache) GetProviderConcurrencyBatch(ctx context.Context, providerIDs []int64) (map[int64]int, error) {
	result := make(map[int64]int, len(providerIDs))
	for _, providerID := range providerIDs {
		result[providerID] = 0
	}
	return result, nil
}

// IncrementProviderWaitCount accepts fixture queue admission and returns true.
// Waiting observations are supplied separately through waitCounts, so this call
// leaves those configured values available unchanged for later selection
// assertions.
func (m *mockConcurrencyCache) IncrementProviderWaitCount(ctx context.Context, providerID int64, maxWait int) (bool, error) {
	return true, nil
}

// DecrementProviderWaitCount accepts provider queue cleanup and returns success
// in the fixture. Its waiting counts are configured observations, so cleanup
// does not mutate the map used by later selection checks.
func (m *mockConcurrencyCache) DecrementProviderWaitCount(ctx context.Context, providerID int64) error {
	return nil
}

// GetProviderWaitingCount returns a configured provider wait count or zero when
// missing. The value is a scenario observation and stays independent of fixture
// queue increment or decrement calls.
func (m *mockConcurrencyCache) GetProviderWaitingCount(ctx context.Context, providerID int64) (int, error) {
	if m.waitCounts != nil {
		if count, ok := m.waitCounts[providerID]; ok {
			return count, nil
		}
	}
	return 0, nil
}

// AcquireUserSlot returns admission success for the generic fixture's
// user-capacity path. User occupancy is unmodeled here, letting
// provider-selection tests concentrate on their configured provider slot and
// load observations.
func (m *mockConcurrencyCache) AcquireUserSlot(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error) {
	return true, nil
}

// ReleaseUserSlot accepts user cleanup and returns success in the fixture. User
// acquisition always admits, and this method retains no occupancy or request
// ownership state for subsequent selection calls.
func (m *mockConcurrencyCache) ReleaseUserSlot(ctx context.Context, userID int64, requestID string) error {
	return nil
}

// GetUserConcurrency returns zero user occupancy for this fixture. User slots
// always admit here, while provider acquisition and load observations are
// configured separately for the scenario's capacity checks.
func (m *mockConcurrencyCache) GetUserConcurrency(ctx context.Context, userID int64) (int, error) {
	return 0, nil
}

// IncrementWaitCount returns successful user-wait admission for this fixture.
// It stores no user queue state, allowing provider-selection scenarios to
// concentrate on their configured provider load and waiting observations.
func (m *mockConcurrencyCache) IncrementWaitCount(ctx context.Context, userID int64, maxWait int) (bool, error) {
	return true, nil
}

// DecrementWaitCount accepts user-wait cleanup and returns success in this
// fixture. User queue state is unmodeled, so the call leaves provider waiting
// observations available for subsequent selection assertions.
func (m *mockConcurrencyCache) DecrementWaitCount(ctx context.Context, userID int64) error {
	return nil
}

// GetProvidersLoadBatch counts batch reads and returns configured load errors
// before observations. Explicit load entries take precedence; skipDefaultLoad
// with a configured map omits missing IDs, while other missing entries receive
// new idle load values.
func (m *mockConcurrencyCache) GetProvidersLoadBatch(ctx context.Context, providers []schedulercore.ProviderWithConcurrency) (map[int64]*schedulercore.ProviderLoadInfo, error) {
	m.loadBatchCalls++
	if m.loadBatchErr != nil {
		return nil, m.loadBatchErr
	}
	result := make(map[int64]*schedulercore.ProviderLoadInfo, len(providers))
	if m.skipDefaultLoad && m.loadMap != nil {
		for _, acc := range providers {
			if load, ok := m.loadMap[acc.ID]; ok {
				result[acc.ID] = load
			}
		}
		return result, nil
	}
	for _, acc := range providers {
		if m.loadMap != nil {
			if load, ok := m.loadMap[acc.ID]; ok {
				result[acc.ID] = load
				continue
			}
		}
		result[acc.ID] = &schedulercore.ProviderLoadInfo{
			ProviderID:         acc.ID,
			CurrentConcurrency: 0,
			WaitingCount:       0,
			LoadRate:           0,
		}
	}
	return result, nil
}

// CleanupExpiredProviderSlots accepts maintenance for the requested provider
// and returns success. The fixture stores configured slot outcomes rather than
// expiring request ownership, so cleanup does not change later scenario
// admission.
func (m *mockConcurrencyCache) CleanupExpiredProviderSlots(ctx context.Context, providerID int64) error {
	return nil
}

// CleanupExpiredProviderSlotKeys accepts fixture-wide slot-key maintenance and
// returns success. Expiring request keys are unmodeled, leaving configured
// provider outcomes and load observations available unchanged for the selection
// scenario.
func (m *mockConcurrencyCache) CleanupExpiredProviderSlotKeys(ctx context.Context) error {
	return nil
}

// CleanupStaleProcessSlots accepts process-slot maintenance and returns success
// in this fixture. Process ownership is unmodeled, so the requested prefix does
// not alter the provider outcomes used by later selection calls.
func (m *mockConcurrencyCache) CleanupStaleProcessSlots(ctx context.Context, activeRequestPrefix string) error {
	return nil
}

// GetUsersLoadBatch returns newly allocated idle load values for requested user
// IDs. The fixture supplies zero occupancy, waiting, and load rate, keeping
// user load independent of configured provider-capacity scenarios.
func (m *mockConcurrencyCache) GetUsersLoadBatch(ctx context.Context, users []schedulercore.UserWithConcurrency) (map[int64]*schedulercore.UserLoadInfo, error) {
	result := make(map[int64]*schedulercore.UserLoadInfo, len(users))
	for _, user := range users {
		result[user.ID] = &schedulercore.UserLoadInfo{
			UserID:             user.ID,
			CurrentConcurrency: 0,
			WaitingCount:       0,
			LoadRate:           0,
		}
	}
	return result, nil
}

// GetByID counts the full Gemini group lookup and returns configured pointers
// first. Unconfigured ID one receives a fresh active hydrated default group,
// while other missing IDs return a group-not-found error.
func (m *mockGroupRepoForGemini) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	m.getByIDCalls++
	if g, ok := m.groups[id]; ok {
		return g, nil
	}
	if id == 1 {
		return &routing.Group{ID: id, Hydrated: true, Status: routing.StatusActive}, nil
	}
	return nil, errors.New("group not found")
}

// GetByIDLite counts lightweight Gemini group lookup separately from full
// reads. Configured pointers win, ID one receives the default active hydrated
// group, and other missing IDs return a lookup error.
func (m *mockGroupRepoForGemini) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	m.getByIDLiteCalls++
	if g, ok := m.groups[id]; ok {
		return g, nil
	}
	if id == 1 {
		return &routing.Group{ID: id, Hydrated: true, Status: routing.StatusActive}, nil
	}
	return nil, errors.New("group not found")
}

// newGenericSelectionForTest fills missing fixture group and parameter readers
// before constructing Generic. Supplied dependencies remain bound, while
// selectionOptionsForTest translates optional process settings into the
// selector's startup value.
func newGenericSelectionForTest(deps GenericDependencies, cfg *config.Config) *Generic {
	if deps.Groups == nil {
		deps.Groups = selectionFixtureGroups{}
	}
	if deps.Parameters == nil {
		deps.Parameters = schedulercore.NewParameters(schedulercore.NewSettingsRuntime(schedulercore.Diagnostics{}), nil, diagnosticParameterDefaults(cfg))
	}
	return NewGeneric(deps, selectionOptionsForTest(cfg))
}

// selectionOptionsForTest starts with DefaultOptions and applies the supplied
// scheduling and sticky migration configuration. Nil configuration retains
// defaults; positive session and response TTL seconds convert to durations
// while nonpositive values keep default lifetimes.
func selectionOptionsForTest(cfg *config.Config) Options {
	options := DefaultOptions()
	if cfg == nil {
		return options
	}

	value := cfg.Scheduling
	options.Scheduling = schedulercore.FlowOptions{LoadBatchEnabled: value.LoadBatch.Enabled, PreferSoonestReset: value.PreferSoonestReset, FallbackMaxWaiting: value.AlternateProviderWait.MaxWaiting, StickySessionMaxWaiting: value.StickyProviderWait.MaxWaiting, FallbackSelectionMode: value.AlternateSelectionMode, FallbackWaitTimeout: time.Duration(value.AlternateProviderWait.TimeoutSeconds) * time.Second, StickySessionWaitTimeout: time.Duration(value.StickyProviderWait.TimeoutSeconds) * time.Second}
	ws := cfg.Scheduling.SessionAffinity
	options.ReadLegacySticky = ws.ReadLegacyHash
	options.WriteLegacySticky = ws.DualWriteLegacyHash
	if ws.TTLSeconds > 0 {
		// Startup configuration stores seconds; sticky callers consume a duration.
		options.StickyTTL = time.Duration(ws.TTLSeconds) * time.Second
	}
	if ws.ResponseIDTTLSeconds > 0 {
		// Response affinity configuration stores seconds; bindings consume a duration.
		options.ResponseTTL = time.Duration(ws.ResponseIDTTLSeconds) * time.Second
	}
	return options
}

// newGeminiSelectionForTest supplies default fixture groups and parameters when
// omitted before constructing Gemini. Existing quota, snapshot, feedback, and
// cache bindings remain supplied by the scenario, with startup options
// translated from cfg.
func newGeminiSelectionForTest(deps GeminiDependencies, cfg *config.Config) *Gemini {
	if deps.Groups == nil {
		deps.Groups = selectionFixtureGroups{}
	}
	if deps.Parameters == nil {
		deps.Parameters = schedulercore.NewParameters(schedulercore.NewSettingsRuntime(schedulercore.Diagnostics{}), nil, diagnosticParameterDefaults(cfg))
	}
	return NewGemini(deps, selectionOptionsForTest(cfg))
}

// newCompatibleSelectionForTest supplies omitted fixture group, response-state,
// and parameter dependencies before constructing Compatible. Response state
// uses a GatewayCache when the bound sticky cache implements it; scenario-owned
// dependencies remain attached.
func newCompatibleSelectionForTest(deps CompatibleDependencies, cfg *config.Config) *Compatible {
	if deps.Groups == nil {
		deps.Groups = selectionFixtureGroups{}
	}
	if deps.Responses == nil {
		cache, _ := deps.Cache.(session.GatewayCache)
		deps.Responses = session.NewOpenAIWSStateStore(cache, gatewayprovider.LogOpenAIWSModeInfo)
	}
	if deps.Parameters == nil {
		deps.Parameters = schedulercore.NewParameters(schedulercore.NewSettingsRuntime(schedulercore.Diagnostics{}), nil, diagnosticParameterDefaults(cfg))
	}
	return NewCompatible(deps, selectionOptionsForTest(cfg))
}

// ForGroup returns the configured profile or read failure for this selection.
func (r candidateAttributeRepo) ForGroup(context.Context, int64) (*routing.ModelAttributeConfig, error) {
	return r.config, r.failure
}

// selectionFixtureGroupID returns a fixture group from request state, candidate
// policy, or captured selection input in that order. If none is available it
// allocates ID one, keeping default fixture targets in the same namespace.
func selectionFixtureGroupID(ctx context.Context) *int64 {
	if group, ok := requeststate.GroupFromContext(ctx); ok && group != nil {
		return &group.ID
	}
	if policy, ok := ctx.Value(candidatePolicyKey{}).(candidatePolicy); ok && policy.groupID != nil {
		return policy.groupID
	}
	if input, ok := ctx.Value(selectionRequestKey{}).(selectionRequest); ok && input.groupID != nil {
		return input.groupID
	}
	id := int64(1)
	return &id
}

// prepareSelectionFixtureProvider fills omitted authentication, credential,
// whitelist, and membership data on a fixture target. Nil input is ignored, and
// existing model-whitelist or membership fields retain their supplied values.
//
// An empty resolved mapping supplies wildcard scope; otherwise its target names
// populate the omitted whitelist. A missing group pointer comes from
// selectionFixtureGroupID, and membership is added only when both stored
// representations are empty. This makes permissive fixtures usable by current
// selection rules while preserving deliberately restrictive scenario inputs.
func prepareSelectionFixtureProvider(ctx context.Context, value *gatewayprovider.ExecutionProvider, groupID *int64) {
	if value == nil {
		return
	}
	if value.Record.Type == "" {
		value.Record.Type = capability.ProviderTypeAPIKey
	}
	if value.Record.Credentials == nil {
		value.Record.Credentials = map[string]any{}
	}
	if _, set := value.Record.Credentials["model_whitelist"]; !set {
		mapping := providercore.ResolveModelMapping(&value.Record, provideradapter.ModelDefaults())
		if len(mapping) == 0 {
			value.Record.Credentials["model_whitelist"] = []string{"*"}
		} else {
			models := make([]string, 0, len(mapping))
			for _, model := range mapping {
				models = append(models, model)
			}
			value.Record.Credentials["model_whitelist"] = models
		}
	}
	if groupID == nil {
		groupID = selectionFixtureGroupID(ctx)
	}
	if len(value.Record.GroupIDs) == 0 && len(value.Record.ProviderGroups) == 0 {
		value.Record.GroupIDs = []int64{*groupID}
	}
}

// GetByID returns a fresh active hydrated fixture group for the requested
// identifier.
func (selectionFixtureGroups) GetByID(_ context.Context, id int64) (*routing.Group, error) {
	return &routing.Group{ID: id, Hydrated: true, Status: routing.StatusActive}, nil
}

// GetByIDLite delegates construction of the default active hydrated fixture
// group.
func (s selectionFixtureGroups) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	return s.GetByID(ctx, id)
}

// GetByID returns a value copy of the matching mixed-group target with
// referenced record data retained. Missing identifiers return an error, leaving
// explicit fixture memberships unchanged for admission checks.
func (s *mixedGroupProviders) GetByID(_ context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	for i := range s.values {
		if s.values[i].Record.ID == id {
			value := s.values[i]
			return &value, nil
		}
	}
	return nil, fmt.Errorf("provider %d missing", id)
}

// ListSchedulableByGroupIDAndPlatforms records the queried group and returns
// explicit members in the supplied platform set. It filters GroupIDs rather
// than provider runtime state, so scenarios provide the intended eligibility
// fields.
func (s *mixedGroupProviders) ListSchedulableByGroupIDAndPlatforms(_ context.Context, group int64, platforms []string) ([]gatewayprovider.ExecutionProvider, error) {
	s.groupQueries = append(s.groupQueries, group)
	var out []gatewayprovider.ExecutionProvider
	for _, value := range s.values {
		if slices.Contains(platforms, value.Record.Platform) && slices.Contains(value.Record.GroupIDs, group) {
			out = append(out, value)
		}
	}
	return out, nil
}

// ListSchedulableByGroupIDAndPlatform delegates explicit fixture membership
// lookup for one requested platform.
func (s *mixedGroupProviders) ListSchedulableByGroupIDAndPlatform(ctx context.Context, group int64, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return s.ListSchedulableByGroupIDAndPlatforms(ctx, group, []string{platform})
}

// mixedGroupProvider constructs an active API Key execution target in the
// supplied group with one allowed model. It uses fictitious credentials,
// concurrency two, and gateway record copying for cross-platform selection
// fixtures.
func mixedGroupProvider(id int64, platform, model string, groupID int64) gatewayprovider.ExecutionProvider {
	value := providercore.Record{ID: id, Platform: platform, Type: capability.ProviderTypeAPIKey, Status: providercore.StatusActive, Schedulable: true, Concurrency: 2, GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "test-key", "model_whitelist": []string{model}}}
	return *gatewayprovider.NewExecutionProvider(&value)
}

// withAdvancedSchedulerTestGroup installs an active hydrated advanced group
// snapshot under the supplied ID. It uses requeststate's snapshot copying,
// allowing picker fixtures to enter advanced mode with current admission data.
func withAdvancedSchedulerTestGroup(ctx context.Context, groupID int64) context.Context {
	return requeststate.WithGroup(ctx, &routing.Group{
		ID: groupID,

		SchedulerType: routing.GroupSchedulerTypeAdvanced,
		Status:        billing.StatusActive,
		Hydrated:      true,
	})
}

// GetByID prepares defaults on the matching compatible fixture element and
// returns its pointer. Missing IDs return a provider-not-found error; the value
// receiver still shares the source slice's element storage.
func (r schedulerTestOpenAIProviderRepo) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	for i := range r.providers {
		if r.providers[i].Record.ID == id {
			prepareSelectionFixtureProvider(ctx, &r.providers[i], nil)
			return &r.providers[i], nil
		}
	}
	return nil, errors.New("provider not found")
}

// ListSchedulableByGroupIDAndPlatform delegates compatible fixture group lookup
// for a single platform.
func (r schedulerTestOpenAIProviderRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return r.ListSchedulableByGroupIDAndPlatforms(ctx, groupID, []string{platform})
}

// ListSchedulableByPlatform returns compatible fixture values matching the
// requested platform.
func (r schedulerTestOpenAIProviderRepo) ListSchedulableByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	var result []gatewayprovider.ExecutionProvider
	for _, acc := range r.providers {
		if acc.Record.Platform == platform {
			result = append(result, acc)
		}
	}
	return result, nil
}

// ListSchedulableUngroupedByPlatform delegates the permissive compatible
// platform query with memberships retained. This fixture lets selection enforce
// group rules, while tests needing strict repository isolation use the
// group-aware variant.
func (r schedulerTestOpenAIProviderRepo) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

// ListModelAvailabilityCandidates filters active schedulable fixture records by
// platform and optional group membership. Missing request groups with
// includeGrouped false fail the membership predicate, yielding an empty
// diagnosis pool under this fixture.
func (r schedulerTestOpenAIProviderRepo) ListModelAvailabilityCandidates(_ context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]gatewayprovider.ExecutionProvider, error) {
	platformSet := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		platformSet[platform] = struct{}{}
	}
	result := make([]gatewayprovider.ExecutionProvider, 0, len(r.providers))
	for _, provider := range r.providers {
		if _, ok := platformSet[provider.Record.Platform]; !ok || provider.Record.Status != billing.StatusActive || !provider.Record.Schedulable {
			continue
		}
		if groupID != nil && !openAIStickyProviderMatchesGroup(&provider, groupID) {
			continue
		}
		if groupID == nil && !includeGrouped && !openAIStickyProviderMatchesGroup(&provider, nil) {
			continue
		}
		result = append(result, provider)
	}
	return result, nil
}

// ListSchedulableByGroupIDAndPlatform returns platform matches carrying the
// requested stored group membership.
func (r schedulerGroupAwareOpenAIProviderRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	var result []gatewayprovider.ExecutionProvider
	for _, acc := range r.providers {
		if acc.Record.Platform == platform && openAIStickyProviderMatchesGroup(&acc, &groupID) {
			result = append(result, acc)
		}
	}
	return result, nil
}

// ListSchedulableUngroupedByPlatform uses the membership predicate with a nil
// requested group. That predicate rejects every candidate, so this group-aware
// fixture produces an empty ungrouped pool for selection.
func (r schedulerGroupAwareOpenAIProviderRepo) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	var result []gatewayprovider.ExecutionProvider
	for _, acc := range r.providers {
		if acc.Record.Platform == platform && openAIStickyProviderMatchesGroup(&acc, nil) {
			result = append(result, acc)
		}
	}
	return result, nil
}

// AcquireProviderSlot appends the provider to an optional acquisition trace
// before returning its configured outcome. Missing entries admit, and trace
// slice pointers retain updates even through this fixture's value receiver.
func (c schedulerTestConcurrencyCache) AcquireProviderSlot(ctx context.Context, providerID int64, maxConcurrency int, requestID string) (bool, error) {
	if c.acquiredIDs != nil {
		*c.acquiredIDs = append(*c.acquiredIDs, providerID)
	}
	if c.acquireResults != nil {
		if result, ok := c.acquireResults[providerID]; ok {
			return result, nil
		}
	}
	return true, nil
}

// ReleaseProviderSlot appends the provider to an optional release trace and
// returns success. It keeps configured acquisition outcomes unchanged, allowing
// tests to observe cleanup order independently of later slot decisions.
func (c schedulerTestConcurrencyCache) ReleaseProviderSlot(ctx context.Context, providerID int64, requestID string) error {
	if c.releasedIDs != nil {
		*c.releasedIDs = append(*c.releasedIDs, providerID)
	}
	return nil
}

// GetProvidersLoadBatch returns configured compatible load errors or a new
// observation map. Explicit load pointers remain shared; skipDefaultLoad with a
// configured map omits missing IDs, while other misses receive idle default
// entries.
func (c schedulerTestConcurrencyCache) GetProvidersLoadBatch(ctx context.Context, providers []schedulercore.ProviderWithConcurrency) (map[int64]*schedulercore.ProviderLoadInfo, error) {
	if c.loadBatchErr != nil {
		return nil, c.loadBatchErr
	}
	out := make(map[int64]*schedulercore.ProviderLoadInfo, len(providers))
	if c.skipDefaultLoad && c.loadMap != nil {
		for _, acc := range providers {
			if load, ok := c.loadMap[acc.ID]; ok {
				out[acc.ID] = load
			}
		}
		return out, nil
	}
	for _, acc := range providers {
		if c.loadMap != nil {
			if load, ok := c.loadMap[acc.ID]; ok {
				out[acc.ID] = load
				continue
			}
		}
		out[acc.ID] = &schedulercore.ProviderLoadInfo{ProviderID: acc.ID, LoadRate: 0}
	}
	return out, nil
}

// GetProviderWaitingCount returns the configured compatible wait observation or
// zero for a missing entry. Its value is independent of acquisition outcomes,
// so tests can model slot and queue capacity separately.
func (c schedulerTestConcurrencyCache) GetProviderWaitingCount(ctx context.Context, providerID int64) (int, error) {
	if c.waitCounts != nil {
		if count, ok := c.waitCounts[providerID]; ok {
			return count, nil
		}
	}
	return 0, nil
}

// GetSessionProviderID reads a compatible hash binding from the fixture map.
// Missing hashes return a cache-miss error; group and TTL inputs are unused
// because these tests inspect selection affinity.
func (c *schedulerTestGatewayCache) GetSessionProviderID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	if id, ok := c.sessionBindings[sessionHash]; ok {
		return id, nil
	}
	return 0, errors.New("not found")
}

// SetSessionProviderID writes a compatible hash binding, allocating fixture
// storage if needed. Group and TTL inputs are ignored, so subsequent calls
// observe the provider chosen by the scenario without expiration.
func (c *schedulerTestGatewayCache) SetSessionProviderID(ctx context.Context, groupID int64, sessionHash string, providerID int64, ttl time.Duration) error {
	if c.sessionBindings == nil {
		c.sessionBindings = make(map[string]int64)
	}
	c.sessionBindings[sessionHash] = providerID
	return nil
}

// RefreshSessionTTL accepts compatible fixture refresh calls and returns
// success. The fixture stores provider affinity with no expiration state,
// leaving the binding available for later selection and migration assertions.
func (c *schedulerTestGatewayCache) RefreshSessionTTL(ctx context.Context, groupID int64, sessionHash string, ttl time.Duration) error {
	return nil
}

// DeleteSessionProviderID removes a compatible hash binding and records the
// removal attempt. Nil binding maps are ignored; populated maps retain deletion
// counts even when a repeated call finds no binding.
func (c *schedulerTestGatewayCache) DeleteSessionProviderID(ctx context.Context, groupID int64, sessionHash string) error {
	if c.sessionBindings == nil {
		return nil
	}
	if c.deletedSessions == nil {
		c.deletedSessions = make(map[string]int)
	}
	c.deletedSessions[sessionHash]++
	delete(c.sessionBindings, sessionHash)
	return nil
}

// SetSessionOwnerGroupID returns successful owner admission for the compatible
// cache fixture. It stores no owner state, allowing selection tests to bind
// sessions without exercising persistence of user/source ownership.
func (c *schedulerTestGatewayCache) SetSessionOwnerGroupID(ctx context.Context, userID int64, source, sessionHash string, groupID int64, ttl time.Duration) (bool, error) {
	return true, nil
}

// GetSessionOwnerGroupID returns a cache-miss error because the fixture retains
// no owner records. Callers using ownership lookup therefore observe an absent
// binding even after its permissive owner write.
func (c *schedulerTestGatewayCache) GetSessionOwnerGroupID(ctx context.Context, userID int64, source, sessionHash string) (int64, error) {
	return 0, errors.New("not found")
}

// RefreshSessionOwnerTTL accepts owner expiration refresh and returns success
// in this fixture. Owner records are unmodeled, so this optional cleanup
// operation changes no affinity or ownership result returned to selection.
func (c *schedulerTestGatewayCache) RefreshSessionOwnerTTL(ctx context.Context, userID int64, source, sessionHash string, ttl time.Duration) error {
	return nil
}

// newSchedulerTestOpenAIWSV2Config configures a one-hour response affinity
// lifetime for compatible fixtures.
func newSchedulerTestOpenAIWSV2Config() *config.Config {
	cfg := &config.Config{}
	cfg.Scheduling.SessionAffinity.ResponseIDTTLSeconds = 3600
	return cfg
}

// Get wraps the fixture's configured string value in a Setting record. Missing
// keys retain GetValue's error, letting runtime parameter readers apply their
// defaults for unavailable settings.
func (s *advancedSchedulerSettingRepoStub) Get(ctx context.Context, key string) (*settings.Setting, error) {
	value, err := s.GetValue(ctx, key)
	if err != nil {
		return nil, err
	}
	return &settings.Setting{Key: key, Value: value}, nil
}

// GetValue returns a configured string setting or ErrSettingNotFound for absent
// fixture storage or keys. Callers can distinguish missing configuration from
// supplied empty or false values during runtime policy resolution.
func (s *advancedSchedulerSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if s == nil || s.values == nil {
		return "", settings.ErrSettingNotFound
	}
	value, ok := s.values[key]
	if !ok {
		return "", settings.ErrSettingNotFound
	}
	return value, nil
}

// Set panics when a test requests a setting write through this read fixture.
// Scenario setup mutates values directly, so a call identifies an unexpected
// service-side persistence operation.
func (s *advancedSchedulerSettingRepoStub) Set(context.Context, string, string) error {
	panic("unexpected call to Set")
}

// GetMultiple returns the configured subset of requested settings using
// background-context GetValue reads. Missing keys are omitted so runtime
// settings can retain defaults; the fixture does not propagate those lookup
// errors.
func (s *advancedSchedulerSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		// Missing runtime settings leave their configured defaults in effect. Omit
		// those keys from this batch result rather than failing every setting read.
		if value, err := s.GetValue(context.Background(), key); err == nil {
			result[key] = value
		}
	}
	return result, nil
}

// SetMultiple panics when a test requests batch setting persistence through
// this read fixture. Scenarios configure values directly, making any invocation
// an observable unexpected store operation.
func (s *advancedSchedulerSettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	panic("unexpected call to SetMultiple")
}

// GetAll panics for unbounded setting reads through this fixture. Runtime
// parameter tests use key-specific reads, so invoking this method identifies an
// unexpected dependency call in the tested path.
func (s *advancedSchedulerSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected call to GetAll")
}

// Delete panics when setting deletion reaches this read fixture. Tests retain
// their configured values directly, allowing an unexpected service-side
// mutation to fail at the attempted dependency call.
func (s *advancedSchedulerSettingRepoStub) Delete(context.Context, string) error {
	panic("unexpected call to Delete")
}

// newAdvancedSchedulerParametersForTest binds process defaults with optional
// sticky-weight and subscription-priority setting strings. Its compatibility
// string is ignored; the first two variadic values populate those respective
// keys when nonempty.
func newAdvancedSchedulerParametersForTest(cfg *config.Config, _ string, values ...string) *schedulercore.Parameters {
	repo := &advancedSchedulerSettingRepoStub{
		values: map[string]string{},
	}
	if len(values) > 0 && values[0] != "" {
		repo.values[schedulercore.SettingKeyAdvancedSchedulerStickyWeightedEnabled] = values[0]
	}
	if len(values) > 1 && values[1] != "" {
		repo.values[schedulercore.SettingKeyAdvancedSchedulerSubscriptionPriorityEnabled] = values[1]
	}
	return schedulercore.NewParameters(schedulercore.NewSettingsRuntime(schedulercore.Diagnostics{}), repo, diagnosticParameterDefaults(cfg))
}

// GetSnapshot wraps shallow fixture target copies after preparing omitted
// defaults for the bucket group. Empty candidates return no available snapshot;
// populated results retain referenced record data until the snapshot reader
// decodes them.
func (s *openAISnapshotCacheStub) GetSnapshot(ctx context.Context, bucket schedulercore.SchedulerBucket) ([]schedulercore.SnapshotProvider, bool, error) {
	if len(s.snapshotProviders) == 0 {
		return nil, false, nil
	}
	out := make([]schedulercore.SnapshotProvider, 0, len(s.snapshotProviders))
	for _, provider := range s.snapshotProviders {
		if provider == nil {
			continue
		}
		cloned := *provider
		prepareSelectionFixtureProvider(ctx, &cloned, &bucket.GroupID)
		out = append(out, codec.WrapRecord(&cloned.Record))
	}
	return out, true, nil
}

// GetProvider wraps a shallow copy of the configured full target after
// preparing defaults. Missing maps or records return nil without error, and
// referenced credential data remains shared with the fixture input.
func (s *openAISnapshotCacheStub) GetProvider(ctx context.Context, providerID int64) (schedulercore.SnapshotProvider, error) {
	if s.providersByID == nil {
		return nil, nil
	}
	provider := s.providersByID[providerID]
	if provider == nil {
		return nil, nil
	}
	cloned := *provider
	prepareSelectionFixtureProvider(ctx, &cloned, nil)
	return codec.WrapRecord(&cloned.Record), nil
}

// ListSchedulableByGroupIDAndPlatforms prepares omitted defaults on source
// elements before filtering platform and group membership. Returned record
// values share referenced data with the source slice, retaining fixture updates
// for later reads.
func (r schedulerTestOpenAIProviderRepo) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]gatewayprovider.ExecutionProvider, error) {
	var result []gatewayprovider.ExecutionProvider
	for i := range r.providers {
		prepareSelectionFixtureProvider(ctx, &r.providers[i], &groupID)
		if slices.Contains(platforms, r.providers[i].Record.Platform) && openAIStickyProviderMatchesGroup(&r.providers[i], &groupID) {
			result = append(result, r.providers[i])
		}
	}
	return result, nil
}

// ListSchedulableByGroupIDAndPlatforms checks explicit membership and platform
// before preparing the compatible fixture element. It therefore retains group
// isolation even when omitted defaults would otherwise add fixture membership.
func (r schedulerGroupAwareOpenAIProviderRepo) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]gatewayprovider.ExecutionProvider, error) {
	var result []gatewayprovider.ExecutionProvider
	for i := range r.providers {
		if slices.Contains(platforms, r.providers[i].Record.Platform) && openAIStickyProviderMatchesGroup(&r.providers[i], &groupID) {
			prepareSelectionFixtureProvider(ctx, &r.providers[i], &groupID)
			result = append(result, r.providers[i])
		}
	}
	return result, nil
}

// GetByID prepares a shallow matching target copy while restoring its supplied
// GroupIDs afterward. Missing records return an error, and references other
// than the copied struct fields remain shared with the source fixture.
func (r schedulerGroupAwareOpenAIProviderRepo) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	for i := range r.providers {
		if r.providers[i].Record.ID == id {
			copy := r.providers[i]
			groups := copy.Record.GroupIDs
			prepareSelectionFixtureProvider(ctx, &copy, nil)
			copy.Record.GroupIDs = groups
			return &copy, nil
		}
	}
	return nil, errors.New("provider not found")
}

// ListSchedulableByPlatform returns threshold fixture targets matching the
// supplied platform.
func (r *thresholdSelectionProviderRepoStub) ListSchedulableByPlatform(_ context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	filtered := make([]gatewayprovider.ExecutionProvider, 0, len(r.providers))
	for _, provider := range r.providers {
		if provider.Record.Platform == platform {
			filtered = append(filtered, provider)
		}
	}
	return filtered, nil
}

// ListSchedulableByGroupIDAndPlatform delegates threshold fixture lookup and
// ignores the requested group. Platform matches remain permissive so the health
// threshold scenario controls candidate membership independently of its
// mutation recorder.
func (r *thresholdSelectionProviderRepoStub) ListSchedulableByGroupIDAndPlatform(ctx context.Context, _ int64, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

// ListSchedulableUngroupedByPlatform delegates the threshold fixture's
// permissive platform lookup. It retains stored memberships and candidate
// state, letting the scenario observe scheduling-threshold effects through the
// embedded health recorder.
func (r *thresholdSelectionProviderRepoStub) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

// responseSelectionOptions sets one-hour session and response lifetimes for
// continuation fixtures. Scheduling wait fields remain zero until a scenario
// supplies them, allowing tests to choose the specific queue behavior.
func responseSelectionOptions() Options {
	return Options{
		StickyTTL:   time.Hour,
		ResponseTTL: time.Hour,
	}
}

// responseSelectionParameters constructs default scoring settings for
// response-chain fixture selectors.
func responseSelectionParameters() *schedulercore.Parameters {
	return schedulercore.NewParameters(schedulercore.NewSettingsRuntime(schedulercore.Diagnostics{}), nil, diagnosticParameterDefaults(&config.Config{}))
}

// GetByID prepares the matching fixture target in the shared source slice and
// returns its pointer. Missing identifiers return an error, and supplied
// defaults remain visible to later queries despite the value receiver.
func (r selectionProviderFixture) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	for i := range r.providers {
		if r.providers[i].Record.ID == id {
			prepareSelectionFixtureProvider(ctx, &r.providers[i], nil)
			return &r.providers[i], nil
		}
	}
	return nil, errors.New("provider not found")
}

// ListSchedulableByPlatform returns fixture values matching the requested
// compatible platform.
func (r selectionProviderFixture) ListSchedulableByPlatform(_ context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	var out []gatewayprovider.ExecutionProvider
	for _, value := range r.providers {
		if value.Record.Platform == platform {
			out = append(out, value)
		}
	}
	return out, nil
}

// ListSchedulableByGroupIDAndPlatform delegates prepared fixture membership
// lookup for one platform.
func (r selectionProviderFixture) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return r.ListSchedulableByGroupIDAndPlatforms(ctx, groupID, []string{platform})
}

// ListSchedulableUngroupedByPlatform delegates the permissive compatible
// platform query and retains supplied memberships. Selector admission checks or
// a stricter group-aware source establish isolation in scenarios requiring it.
func (r selectionProviderFixture) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

// GetSessionProviderID delegates continuation affinity reads to the embedded
// sticky fixture. Group inputs remain ignored, missing hashes return the same
// cache-miss error, and current/legacy key selection stays with the scheduler
// caller.
func (c *responseCacheFixture) GetSessionProviderID(ctx context.Context, group int64, key string) (int64, error) {
	return c.stickyCacheFixture.GetSessionProviderID(ctx, group, key)
}

// SetSessionProviderID delegates continuation affinity writes to the embedded
// sticky fixture. The provider is stored by hash with group and TTL inputs
// ignored, so later assertions can inspect selection affinity.
func (c *responseCacheFixture) SetSessionProviderID(ctx context.Context, group int64, key string, id int64, ttl time.Duration) error {
	return c.stickyCacheFixture.SetSessionProviderID(ctx, group, key, id, ttl)
}

// RefreshSessionTTL delegates continuation expiration calls to the embedded
// sticky fixture. That cache retains hash bindings and returns success without
// expiration state, leaving the stored provider available to subsequent
// selection reads.
func (c *responseCacheFixture) RefreshSessionTTL(ctx context.Context, group int64, key string, ttl time.Duration) error {
	return c.stickyCacheFixture.RefreshSessionTTL(ctx, group, key, ttl)
}

// DeleteSessionProviderID delegates continuation binding cleanup to the
// embedded sticky fixture. Populated binding maps record the hash's deletion
// attempt and remove affinity, while a nil binding map is ignored.
func (c *responseCacheFixture) DeleteSessionProviderID(ctx context.Context, group int64, key string) error {
	return c.stickyCacheFixture.DeleteSessionProviderID(ctx, group, key)
}

// AcquireProviderSlot returns the fixture's configured provider outcome or
// admits when missing. The inputs for max concurrency and request ID are
// ignored, leaving capacity behavior under scenario control.
func (c selectionConcurrencyFixture) AcquireProviderSlot(_ context.Context, id int64, _ int, _ string) (bool, error) {
	if value, ok := c.acquireResults[id]; ok {
		return value, nil
	}
	return true, nil
}

// ReleaseProviderSlot accepts continuation fixture capacity cleanup and returns
// success. Configured acquisition outcomes remain the source of later
// decisions, with no real occupancy state changed by this fixture call.
func (selectionConcurrencyFixture) ReleaseProviderSlot(context.Context, int64, string) error {
	return nil
}

// GetProviderWaitingCount returns the fixture map's count, using zero for a
// missing provider. This request-local fixture observation is independent of
// configured slot outcomes, allowing scenarios to vary queue capacity
// separately.
func (c selectionConcurrencyFixture) GetProviderWaitingCount(_ context.Context, id int64) (int, error) {
	return c.waitCounts[id], nil
}

// GetProvidersLoadBatch returns configured errors or compatible candidate
// observations. Explicit load pointers are shared; skipDefaultLoad with a map
// retains only known IDs, while other missing candidates receive new idle
// loads.
func (c selectionConcurrencyFixture) GetProvidersLoadBatch(ctx context.Context, providers []schedulercore.ProviderWithConcurrency) (map[int64]*schedulercore.ProviderLoadInfo, error) {
	if c.loadBatchErr != nil {
		return nil, c.loadBatchErr
	}
	out := make(map[int64]*schedulercore.ProviderLoadInfo, len(providers))
	if c.skipDefaultLoad && c.loadMap != nil {
		for _, acc := range providers {
			if load, ok := c.loadMap[acc.ID]; ok {
				out[acc.ID] = load
			}
		}
		return out, nil
	}
	for _, acc := range providers {
		if c.loadMap != nil {
			if load, ok := c.loadMap[acc.ID]; ok {
				out[acc.ID] = load
				continue
			}
		}
		out[acc.ID] = &schedulercore.ProviderLoadInfo{ProviderID: acc.ID, LoadRate: 0}
	}
	return out, nil
}

// GetByID wraps the source execution record for scheduler snapshot hydration.
// It retains the source error with the converted record, allowing preparation
// tests to exercise full-read failures.
func (s hydrationProviderSource) GetByID(ctx context.Context, id int64) (schedulercore.SnapshotProvider, error) {
	value, err := s.source.GetByID(ctx, id)
	return codec.WrapRecord(gatewayprovider.ExecutionRecord(value)), err
}

// ListSchedulableByGroupIDAndPlatforms prepares omitted defaults before
// filtering requested platform and group membership. Returned values share
// referenced record data with the fixture source, letting full reads observe
// the same scenario configuration.
func (r selectionProviderFixture) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]gatewayprovider.ExecutionProvider, error) {
	var result []gatewayprovider.ExecutionProvider
	for i := range r.providers {
		prepareSelectionFixtureProvider(ctx, &r.providers[i], &groupID)
		if slices.Contains(platforms, r.providers[i].Record.Platform) && openAIStickyProviderMatchesGroup(&r.providers[i], &groupID) {
			result = append(result, r.providers[i])
		}
	}
	return result, nil
}

// GetSnapshot prepares default membership on the fixture's candidate pointers
// before wrapping them for a present snapshot. Callers retain those source
// pointers, and the cache reports availability even for an empty candidate
// slice.
func (c *snapshotHydrationCache) GetSnapshot(ctx context.Context, bucket schedulercore.SchedulerBucket) ([]schedulercore.SnapshotProvider, bool, error) {
	out := make([]schedulercore.SnapshotProvider, 0, len(c.snapshot))
	for _, v := range c.snapshot {
		prepareSelectionFixtureProvider(ctx, v, &bucket.GroupID)
		out = append(out, codec.WrapRecord(gatewayprovider.ExecutionRecord(v)))
	}
	return out, true, nil
}

// GetProvider prepares omitted defaults on the configured full target before
// snapshot wrapping. Missing IDs remain nil without error, allowing the
// selection completion path to observe a provider absent during hydration.
func (c *snapshotHydrationCache) GetProvider(ctx context.Context, id int64) (schedulercore.SnapshotProvider, error) {
	prepareSelectionFixtureProvider(ctx, c.providers[id], nil)
	return codec.WrapRecord(gatewayprovider.ExecutionRecord(c.providers[id])), nil
}

// newHydrationSnapshotForTest binds the fixture cache with an optional
// repository-backed provider source. Missing source leaves hydration
// cache-only, while configured not-found errors and gateway diagnostics support
// failure scenarios.
func newHydrationSnapshotForTest(cache *snapshotHydrationCache, source Providers) *schedulercore.SnapshotService {
	var read schedulercore.SnapshotProviderSource
	if source != nil {
		read = hydrationProviderSource{source: source}
	}
	return schedulercore.NewSnapshotService(cache, nil, read, nil, nil, schedulercore.SnapshotBindings{ProviderNotFound: providercore.ErrProviderNotFound, GroupNotFound: routing.ErrGroupNotFound, Diagnostics: schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}})
}

// GetSessionProviderID reads a hash binding and returns a cache-miss error when
// absent. Group inputs are ignored by this fixture, while callers exercise the
// scheduler's current and legacy hash selection rules.
func (c *stickyCacheFixture) GetSessionProviderID(_ context.Context, _ int64, key string) (int64, error) {
	if id, ok := c.sessionBindings[key]; ok {
		return id, nil
	}
	return 0, errors.New("not found")
}

// SetSessionProviderID writes the provider by hash and allocates fixture
// storage when absent. Group and TTL inputs are ignored, so repeated reads
// inspect affinity decisions rather than expiring Redis state.
func (c *stickyCacheFixture) SetSessionProviderID(_ context.Context, _ int64, key string, id int64, _ time.Duration) error {
	if c.sessionBindings == nil {
		c.sessionBindings = map[string]int64{}
	}
	c.sessionBindings[key] = id
	return nil
}

// RefreshSessionTTL accepts sticky fixture refresh calls and returns success.
// The fixture holds hash affinity with no expiration data, allowing current and
// legacy binding tests to inspect the same stored provider afterward.
func (*stickyCacheFixture) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

// DeleteSessionProviderID removes hash affinity while counting deletion
// attempts in the fixture. Missing binding maps return immediately, while
// populated maps record repeated removals even after the hash has no provider.
func (c *stickyCacheFixture) DeleteSessionProviderID(_ context.Context, _ int64, key string) error {
	if c.sessionBindings == nil {
		return nil
	}
	if c.deletedSessions == nil {
		c.deletedSessions = map[string]int{}
	}
	c.deletedSessions[key]++
	delete(c.sessionBindings, key)
	return nil
}
