package moderation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	moderationadapter "github.com/TokenFlux/TokenRouter/internal/moderation/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// Fixture role labels select regular-account and administrator paths in
// ordinary moderation ban and diagnostic assertions.
const (
	RoleAdmin = "admin" // ordinary admin ban exemption
	RoleUser  = "user"  // ordinary regular actor policy
)

// ErrUserNotFound marks missing fixture users so the test runtime can exercise
// service-level not-found translation.
var ErrUserNotFound = errors.New("user not found")

// contentModerationTestProxyRepo resolves fixture proxies and atomically counts
// lookups while its configured map stays stable.
type contentModerationTestProxyRepo struct {
	// proxies and getByIDErr are configured before lookup begins. Atomic getCalls
	// supports observation, while callers keep the map and pointed proxy values
	// stable during concurrent reads.
	proxies    map[int64]*Proxy // stable fixture proxy pointer map
	getByIDErr error            // configured lookup failure result
	getCalls   atomic.Int64     // atomic lookup invocation count
}

// contentModerationRuntimeSettingRepo provides locked policy storage, read
// counters, and one captured-read gate for publication races.
type contentModerationRuntimeSettingRepo struct {
	// mu protects values, getValueCalls, getMultipleCalls, getMultipleErr,
	// getMultipleStart, and getMultipleWait. GetMultiple copies values and
	// consumes gate references under this lock, then sends or waits after
	// unlocking.
	mu               sync.Mutex        // fixture storage and gate lock
	values           map[string]string // stored fixture setting strings
	getValueCalls    int               // individual read invocation count
	getMultipleCalls int               // batch read invocation count
	getMultipleErr   error             // injected batch read failure
	// getMultipleStart and getMultipleWait gate the next successful batch read. A
	// failed read leaves the gate armed; callers drain the notification and
	// release the wait because these fixture waits ignore context cancellation.
	getMultipleStart chan<- struct{} // captured read notification channel
	getMultipleWait  <-chan struct{} // captured read release channel
}

// contentModerationTestSettingRepo is simple map storage for cases with stable
// worker-visible settings or serial writes.
type contentModerationTestSettingRepo struct {
	// values has no mutex. Cases keep it stable while started workers or untracked
	// refresh callbacks read it, and serialize any fixture settings edits
	// separately.
	values map[string]string // fixture setting string map
}

// contentModerationTestRepo retains shallow ordinary logs and serial Cyber
// policy observations for service assertions.
type contentModerationTestRepo struct {
	mu sync.Mutex // ordinary log collection lock
	// mu protects logs append and snapshot/count reads. Stored value copies still
	// share pointer fields, maps, item slices, and media storage, so cases keep
	// those nested values stable after publication.
	logs []ContentModerationLog // shallow retained ordinary observations
	// cyberWarnings is accessed by serial Cyber cases and has no mutex. Its policy
	// helper reports threshold eligibility without identity row locking, database
	// IDs, or a user status write.
	// serial retained Cyber observations
	cyberWarnings []ContentModerationCyberWarning
}

// contentModerationTestHashCache is mutex-backed membership and command history
// used in place of Redis by service cases.
type contentModerationTestHashCache struct {
	// mu protects hashes, recorded, checked, deleted, hasResult, and hasResultUsed
	// during commands and snapshots. Initial fixture values are bound before
	// concurrent use; command contexts are ignored by this in-memory adapter.
	mu            sync.Mutex          // membership and history lock
	hashes        map[string]struct{} // fixture flagged-member set
	recorded      []string            // record invocation member history
	checked       []string            // membership invocation member history
	deleted       []string            // remove invocation member history
	hasResult     bool                // configured membership override value
	hasResultUsed bool                // membership override selection flag
}

// contentModerationTestUserRepo exposes one fixture account and records
// requested IDs and whole-user replacement observations.
type contentModerationTestUserRepo struct {
	// user, updated, and requestedIDs are used serially. GetByID records the
	// requested ID but returns the one stored account without matching it; tests
	// inspect requestedIDs to verify actor selection.
	user         *User   // one stored fixture account
	updated      []User  // copied whole-account update history
	requestedIDs []int64 // requested account lookup history
}

// contentModerationTestAuthCacheInvalidator records account identities passed
// to authentication invalidation in serial cases.
type contentModerationTestAuthCacheInvalidator struct {
	// userIDs is mutated and read serially by these cases after synchronous side
	// effects. This fixture has no mutex or physical authentication cache to
	// invalidate.
	userIDs []int64 // serial invalidation identity history
}

type (
	// User is the fixture name for moderation account snapshot values used in
	// ordinary status and actor-attribution assertions.
	User = UserSnapshot
	// Proxy is the fixture name for stored egress proxy values resolved by the
	// test adapter into moderation connection observations.
	Proxy = egress.Proxy
	// UserUpdateFields carries the selector passed by fixture SetStatus, while
	// fixture Update replaces the complete supplied account value.
	UserUpdateFields struct {
		// Status is passed as a status-write marker, but Update ignores this selector
		// and stores the complete provided account copy regardless of its value.
		Status bool // requested status field marker
	}
)

// testModerationRuntime supplies production HTTP, image, and Cyber parsing
// adapters for fixtures. Background work launches through an untracked
// goroutine; tests that observe refresh results provide their own gates or
// polling instead of an application task owner.
func testModerationRuntime() Runtime {
	return Runtime{Audit: moderationadapter.NewAuditClient(), SnapshotMedia: moderationadapter.SnapshotMedia, Background: func(_ string, fn func()) bool {
		// Run the reload separately so the stale-snapshot Check can return while
		// GetMultiple waits on the testcase gate; inline execution would block that
		// release path.
		go fn()
		return true
	}, CyberText: openai.IsOpenAICyberWarningText, CyberPolicy: openai.DetectOpenAICyberPolicy, MissingRow: func(e error) bool { return errors.Is(e, sql.ErrNoRows) }, MissingUser: func(e error) bool { return errors.Is(e, ErrUserNotFound) }}
}

// buildTextLogForTest normalizes plain text into structured input before
// delegating log construction. It retains full item text for review; the shared
// builder applies summary redaction, excerpt limits, and the supplied retention
// policy.
func (s *ContentModerationService) buildTextLogForTest(input ContentModerationCheckInput, cfg *ContentModerationConfig, action string, flagged bool, highestCategory string, highestScore float64, scores map[string]float64, text string, latency *int, queueDelay *int, errText string) *ContentModerationLog {
	content := ContentModerationInput{Text: text}
	content.Normalize()
	return s.buildStructuredLog(input, cfg, action, flagged, highestCategory, highestScore, scores, content, latency, queueDelay, errText, nil)
}

// newTestModeration constructs the service with production transport, snapshot,
// and upstream classifiers plus an untracked background launcher. Construction
// starts no workers; callers explicitly Start and Stop any runtime they choose
// to launch.
func newTestModeration(settings SettingRepository, repo ContentModerationRepository, hash ContentModerationHashCache, groups GroupRepository, users UserCommands, auth APIKeyAuthCacheInvalidator, email RiskSender) *ContentModerationService {
	return NewContentModerationService(settings, repo, hash, groups, users, auth, email, testModerationRuntime())
}

// newExpandedModerationTestService constructs and starts a pre-block service
// with one key and no audit retry. It uses immutable serialized fixture
// settings and returns the log observer; Stop is not registered here, so
// callers own the started runtime lifetime.
func newExpandedModerationTestService(t *testing.T, baseURL string, recordNonHits bool) (*ContentModerationService, *contentModerationTestRepo) {
	t.Helper()
	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModePreBlock
	cfg.BaseURL = baseURL
	cfg.APIKeys = []string{"sk-test"}
	cfg.RetryCount = 0
	cfg.RecordNonHits = recordNonHits
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationTestRepo{}
	service := newTestModeration(
		&contentModerationTestSettingRepo{values: map[string]string{
			SettingKeyRiskControlEnabled:      "true",
			SettingKeyContentModerationConfig: string(rawCfg),
		}},
		repo,
		&contentModerationTestHashCache{},
		nil,
		nil,
		nil,
		nil,
	)
	service.Start()
	return service, repo
}

// decodeModerationRawInput decodes the raw input field from an audit fixture
// request and fails on JSON errors. Callers supply valid expected moderation
// request shapes; the returned RawMessage retains the decoded field bytes for
// further fixture inspection.
func decodeModerationRawInput(t *testing.T, r *http.Request) json.RawMessage {
	t.Helper()
	var payload struct {
		Input json.RawMessage `json:"input"` // JSON audit input field value
	}
	require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
	return payload.Input
}

// decodeModerationTextInputs accepts a decoded string batch or one scalar
// string from the request fixture. Batch decoding is tried first; scalar
// decoding errors fail the test, and callers using element zero supply nonempty
// text input.
func decodeModerationTextInputs(t *testing.T, r *http.Request) []string {
	t.Helper()
	raw := decodeModerationRawInput(t, r)
	var batch []string
	// Batch input and scalar input have separate JSON shapes; retry scalar decode
	// only after the batch shape fails, matching the API input selected by the caller.
	if err := json.Unmarshal(raw, &batch); err == nil {
		return batch
	}
	var single string
	require.NoError(t, json.Unmarshal(raw, &single))
	return []string{single}
}

// GetByID records one lookup and returns a configured proxy pointer, injected
// failure, or missing-proxy error. It ignores context, and returned proxy
// storage remains shared with the stable fixture map.
func (r *contentModerationTestProxyRepo) GetByID(ctx context.Context, id int64) (*Proxy, error) {
	r.getCalls.Add(1)
	if r.getByIDErr != nil {
		return nil, r.getByIDErr
	}
	if px, ok := r.proxies[id]; ok {
		return px, nil
	}
	return nil, errors.New("proxy not found")
}

// moderationProxyIDPtr allocates an independent integer pointer for explicit
// fixture proxy-selection arguments.
func moderationProxyIDPtr(v int64) *int64 { return &v }

// Get returns a new Setting for a stored fixture value under its mutex. Missing
// keys return ErrSettingNotFound, context is ignored, and this method does not
// contribute to the GetValue read counter.
func (r *contentModerationRuntimeSettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value}, nil
}

// GetValue records and returns an individual fixture setting read under its
// mutex. Missing keys return ErrSettingNotFound after incrementing the counter;
// context cancellation is ignored by this memory operation.
func (r *contentModerationRuntimeSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getValueCalls++
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

// Set writes one fixture setting and initializes missing map storage under its
// mutex. It ignores context and returns nil, allowing dedicated updates to
// change stored policy while a previously captured batch read is gated.
func (r *contentModerationRuntimeSettingRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}

// GetMultiple captures present selected values and one pending gate while
// holding the fixture mutex. It increments the read count first; an injected
// error returns before consuming the gate. The copied map is independent of
// later setting updates.
//
// The notification and wait run after unlocking, so a concurrent dedicated
// update can save new policy while this read still returns its old captured
// values. Context is ignored for both gate operations; the testcase drains
// start and releases wait to complete the read. Clearing gate fields before
// unlocking restricts the gate to that captured successful read.
func (r *contentModerationRuntimeSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.Lock()
	r.getMultipleCalls++
	if err := r.getMultipleErr; err != nil {
		r.mu.Unlock()
		return nil, err
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	start := r.getMultipleStart
	wait := r.getMultipleWait
	r.getMultipleStart = nil
	r.getMultipleWait = nil
	r.mu.Unlock()
	// After unlocking, notify that older values have been captured and await the
	// testcase release. Clearing both gate fields earlier lets later reads proceed
	// independently while this context-ignoring read remains held.
	if start != nil {
		start <- struct{}{}
	}
	if wait != nil {
		<-wait
	}
	return out, nil
}

// SetMultiple merges supplied setting strings into fixture storage under its
// mutex. It initializes missing storage, ignores context, and returns nil;
// omitted keys retain their prior fixture values.
func (r *contentModerationRuntimeSettingRepo) SetMultiple(_ context.Context, values map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = make(map[string]string)
	}
	maps.Copy(r.values, values)
	return nil
}

// GetAll returns an independent map copy of fixture settings while holding its
// mutex. It ignores context and performs no read-count increment, keeping this
// interface method separate from snapshot-load observations.
func (r *contentModerationRuntimeSettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.values))
	maps.Copy(out, r.values)
	return out, nil
}

// Delete removes one fixture key under its mutex and returns nil. Missing
// entries are accepted, context is ignored, and subsequent individual reads
// report ErrSettingNotFound for that key.
func (r *contentModerationRuntimeSettingRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.values, key)
	return nil
}

// calls returns individual and batch fixture read counters from one locked
// observation. Callers can compare them while an untracked background refresh
// proceeds, instead of directly reading mutable counter fields.
func (r *contentModerationRuntimeSettingRepo) calls() (getValue, getMultiple int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.getValueCalls, r.getMultipleCalls
}

// failMultiple installs the batch-read error under the fixture mutex. Passing
// nil restores successful reads; an already captured successful read keeps its
// earlier copied values and gate behavior.
func (r *contentModerationRuntimeSettingRepo) failMultiple(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getMultipleErr = err
}

// blockNextMultiple arms notification and release channels for the next
// successful batch read. Callers provide a receivable start channel and release
// wait themselves; failed reads leave this gate armed and cancellation does not
// release it.
func (r *contentModerationRuntimeSettingRepo) blockNextMultiple(start chan<- struct{}, wait <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getMultipleStart = start
	r.getMultipleWait = wait
}

// runtimeCacheTestConfig serializes enabled pre-block keyword-only settings for
// the supplied ordered keywords. JSON failures stop the test, and the resulting
// fixture avoids API transport work while exercising runtime snapshot
// publication.
func runtimeCacheTestConfig(t *testing.T, keywords ...string) string {
	t.Helper()
	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModePreBlock
	cfg.KeywordBlockingMode = ContentModerationKeywordModeKeywordOnly
	cfg.BlockedKeywords = keywords
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	return string(raw)
}

// runtimeCacheTestService binds locked setting storage and a log fixture with
// the supplied snapshot TTL. It starts no workers and has no queue; keyword
// records therefore use inline persistence, while reloads use the test runtime
// background launcher.
func runtimeCacheTestService(repo *contentModerationRuntimeSettingRepo, ttl time.Duration) *ContentModerationService {
	return &ContentModerationService{
		runtime:         testModerationRuntime(),
		settingRepo:     repo,
		repo:            &contentModerationTestRepo{},
		runtimeCacheTTL: ttl,
	}
}

// runtimeCacheTestInput embeds text directly inside a Chat JSON fixture and
// fixes the requested model label. Callers supply JSON-safe text without quote,
// escape, or control sequences because this helper does not encode the text
// separately.
func runtimeCacheTestInput(text string) ContentModerationCheckInput {
	return ContentModerationCheckInput{
		Protocol: ContentModerationProtocolOpenAIChat,
		Model:    "risk-cache-test",
		Body:     []byte(`{"messages":[{"role":"user","content":"` + text + `"}]}`),
	}
}

// Get returns a new Setting value from the simple fixture map or
// ErrSettingNotFound. It ignores context and has no synchronization; cases
// serialize mutation or keep the map stable while readers run.
func (r *contentModerationTestSettingRepo) Get(ctx context.Context, key string) (*Setting, error) {
	if value, ok := r.values[key]; ok {
		return &Setting{Key: key, Value: value}, nil
	}
	return nil, ErrSettingNotFound
}

// GetValue reads one simple fixture string or returns ErrSettingNotFound.
// Context is ignored and no mutex is used, so callers keep values stable during
// worker or untracked snapshot-refresh reads.
func (r *contentModerationTestSettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}

// Set initializes and writes the simple fixture map, ignoring context and
// returning nil. Callers serialize edits because this adapter has no mutex and
// can also be used by service readers.
func (r *contentModerationTestSettingRepo) Set(ctx context.Context, key, value string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	r.values[key] = value
	return nil
}

// GetMultiple copies present selected keys into a new fixture result map and
// omits missing keys. It ignores context and requires stable source storage
// while reading because this simple adapter supplies no mutex.
func (r *contentModerationTestSettingRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

// SetMultiple merges supplied strings into the simple fixture map and
// initializes it when absent. It ignores context and returns nil; callers
// serialize this mutation against all fixture reads.
func (r *contentModerationTestSettingRepo) SetMultiple(ctx context.Context, settings map[string]string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	maps.Copy(r.values, settings)
	return nil
}

// GetAll returns a new map containing the current simple fixture strings. It
// ignores context and requires stable source storage during copying; callers
// can change the returned map independently afterward.
func (r *contentModerationTestSettingRepo) GetAll(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	maps.Copy(out, r.values)
	return out, nil
}

// Delete removes one simple fixture key and returns nil even if it was absent.
// It ignores context; callers serialize mutations because storage is shared
// with the same unsynchronized read methods.
func (r *contentModerationTestSettingRepo) Delete(ctx context.Context, key string) error {
	delete(r.values, key)
	return nil
}

// CreateLog appends a nonnil log value under the ordinary fixture mutex and
// ignores context. It does not assign database ID or creation time; nested
// pointers, maps, and slices remain shared, so cases leave them stable after
// publication.
func (r *contentModerationTestRepo) CreateLog(ctx context.Context, log *ContentModerationLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if log != nil {
		r.logs = append(r.logs, *log)
	}
	return nil
}

// ListLogs is an inert interface fixture returning nil rows, nil pagination,
// and nil error. It ignores context and filters; current cases inspect
// snapshotLogs instead, and this result cannot support a handler expecting page
// metadata.
func (r *contentModerationTestRepo) ListLogs(ctx context.Context, filter ContentModerationLogFilter) ([]ContentModerationLog, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

// CountFlaggedByUserSince counts mutex-protected actor matches that are
// flagged, timestamped at or after since, and not hash blocks. It ignores
// context and does not implement PostgreSQL latest-auto-ban resetting, so cases
// supply the history needed for their selected assertion.
func (r *contentModerationTestRepo) CountFlaggedByUserSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, log := range r.logs {
		if log.UserID == nil || *log.UserID != userID || !log.Flagged || log.Action == ContentModerationActionHashBlock {
			continue
		}
		if log.CreatedAt.IsZero() || log.CreatedAt.Before(since) {
			continue
		}
		count++
	}
	return count, nil
}

// CreateCyberWarning appends a nonnil warning in serial fixture use and fills
// absent creation time. It ignores context and keeps nested references shared;
// database identity, media insertion, and transaction behavior are outside this
// fixture.
func (r *contentModerationTestRepo) CreateCyberWarning(ctx context.Context, warning *ContentModerationCyberWarning) error {
	if warning != nil {
		if warning.CreatedAt.IsZero() {
			warning.CreatedAt = time.Now()
		}
		r.cyberWarnings = append(r.cyberWarnings, *warning)
	}
	return nil
}

// CreateCyberWarningAndApplyUserBan computes a window count, appends one serial
// fixture warning, and returns its AutoBanned value. Missing warnings return
// false, nil. The policy can set that value true; an incoming true value
// remains true even when this policy does not select a ban.
//
// This fixture performs no identity row locking or status command and can
// report a ban with no actor. It assigns missing creation time and keeps nested
// warning references shared, but assigns no database ID. Service cases supply
// fresh warning values and inspect post-return invalidation and metadata; this
// helper does not establish a durable user status change.
func (r *contentModerationTestRepo) CreateCyberWarningAndApplyUserBan(ctx context.Context, warning *ContentModerationCyberWarning, policy ContentModerationCyberWarningPolicy) (bool, error) {
	if warning == nil {
		return false, nil
	}
	count := 1
	if warning.UserID != nil && policy.WindowHours > 0 {
		// Convert policy hours to a cutoff before reading existing fixture
		// observations.
		since := time.Now().Add(-time.Duration(policy.WindowHours) * time.Hour)
		// This in-memory counter reports computed history with nil error; the fake
		// policy adds this warning without simulating database count failures or identity locking.
		n, _ := r.CountCyberWarningsByUserSince(ctx, *warning.UserID, since)
		count = n + 1
	}
	warning.ViolationCount = count
	if policy.AutoBanEnabled && policy.BanThreshold > 0 && count >= policy.BanThreshold {
		warning.AutoBanned = true
	}
	if warning.CreatedAt.IsZero() {
		warning.CreatedAt = time.Now()
	}
	r.cyberWarnings = append(r.cyberWarnings, *warning)
	return warning.AutoBanned, nil
}

// ListCyberWarnings is an inert fixture returning nil rows, nil pagination, and
// nil error. It ignores filters and context; serial warning cases inspect the
// retained cyberWarnings collection instead of this list capability.
func (r *contentModerationTestRepo) ListCyberWarnings(ctx context.Context, filter ContentModerationCyberWarningFilter) ([]ContentModerationCyberWarning, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

// CountCyberWarningsByUserSince counts serial fixture rows for a matching
// pointed actor strictly after since. It ignores context and has no
// latest-auto-ban reset or mutex; callers avoid concurrent warning writes.
func (r *contentModerationTestRepo) CountCyberWarningsByUserSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	count := 0
	for _, warning := range r.cyberWarnings {
		if warning.UserID != nil && *warning.UserID == userID && warning.CreatedAt.After(since) {
			count++
		}
	}
	return count, nil
}

// GetCyberSummary returns an empty summary fixture and ignores the supplied
// filter and context. It performs no grouping or counts, so current recording
// cases inspect stored warning observations directly.
func (r *contentModerationTestRepo) GetCyberSummary(ctx context.Context, filter ContentModerationCyberWarningFilter) (*ContentModerationCyberSummary, error) {
	return &ContentModerationCyberSummary{}, nil
}

// MarkCyberWarningEmailSent is a successful no-op fixture method for every ID
// and context. It does not mutate retained warning rows, so this adapter cannot
// establish persistence of an email marker.
func (r *contentModerationTestRepo) MarkCyberWarningEmailSent(ctx context.Context, id int64) error {
	return nil
}

// CleanupExpiredLogs returns a zero-delete fixture result with zero FinishedAt
// and ignores context and cutoffs. It does not alter retained logs or warnings,
// so retention deletion is outside these service fixtures.
func (r *contentModerationTestRepo) CleanupExpiredLogs(ctx context.Context, hitBefore time.Time, nonHitBefore time.Time) (*ContentModerationCleanupResult, error) {
	return &ContentModerationCleanupResult{}, nil
}

// snapshotLogs copies the outer ordinary log slice under its mutex for polling
// assertions. Nested pointers, maps, items, and media remain shared with
// retained logs; callers keep those values stable during asynchronous
// observation.
func (r *contentModerationTestRepo) snapshotLogs() []ContentModerationLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ContentModerationLog, len(r.logs))
	copy(out, r.logs)
	return out
}

// requireContentModerationLogCount polls shallow fixture snapshots for the
// exact expected publication count. It fails after the one-second fixture
// budget and returns the observed rows; matching the count is not a join of all
// service workers.
func requireContentModerationLogCount(t *testing.T, repo *contentModerationTestRepo, want int) []ContentModerationLog {
	t.Helper()
	var logs []ContentModerationLog
	// Bound fixture publication waiting independently of the service drain; the
	// observed row count can become ready before a worker finishes all completion bookkeeping.
	require.Eventually(t, func() bool {
		logs = repo.snapshotLogs()
		return len(logs) == want
	}, time.Second, 10*time.Millisecond)
	return logs
}

// requireRecordedHashCount polls copied fixture record history for the exact
// expected count. Its one-second budget bounds the assertion wait, while hash
// publication can precede log insertion and does not establish full task
// completion.
func requireRecordedHashCount(t *testing.T, cache *contentModerationTestHashCache, want int) []string {
	t.Helper()
	var hashes []string
	// Hash recording occurs before log insertion, so its publication assertion
	// owns a separate wait and does not imply that the complete task has finished.
	require.Eventually(t, func() bool {
		hashes = cache.snapshotRecorded()
		return len(hashes) == want
	}, time.Second, 10*time.Millisecond)
	return hashes
}

// GetByID records the requested account ID and returns a copy of the one stored
// fixture user. Missing users return ErrUserNotFound; it ignores context and
// does not match the requested ID against that stored user.
func (r *contentModerationTestUserRepo) GetByID(ctx context.Context, id int64) (*User, error) {
	r.requestedIDs = append(r.requestedIDs, id)
	if r.user == nil {
		return nil, ErrUserNotFound
	}
	clone := *r.user
	return &clone, nil
}

// Update replaces the serial fixture user with a copied supplied value and
// records that copy. Nil users are accepted, context and fields selector are
// ignored, and omitted values in the supplied user remain omitted in the
// replacement.
func (r *contentModerationTestUserRepo) Update(ctx context.Context, user *User, fields UserUpdateFields) error {
	if user == nil {
		return nil
	}
	clone := *user
	r.updated = append(r.updated, clone)
	r.user = &clone
	return nil
}

// InvalidateAuthCacheByUserID appends the supplied identity to a serial fixture
// observation list. It ignores context and performs no cache operation; cases
// inspect the list after the synchronous side effect returns.
func (i *contentModerationTestAuthCacheInvalidator) InvalidateAuthCacheByUserID(ctx context.Context, userID int64) {
	i.userIDs = append(i.userIDs, userID)
}

// RecordFlaggedInputHash initializes membership, adds the literal member, and
// records every invocation under its mutex. It ignores context and performs no
// trim or hash validation, unlike normalization applied by production adapter
// or service callers.
func (c *contentModerationTestHashCache) RecordFlaggedInputHash(ctx context.Context, inputHash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hashes == nil {
		c.hashes = map[string]struct{}{}
	}
	c.hashes[inputHash] = struct{}{}
	c.recorded = append(c.recorded, inputHash)
	return nil
}

// HasFlaggedInputHash records the queried literal member under its mutex and
// returns configured override or current membership. Context is ignored;
// hasResultUsed selects the override even when that override is false.
func (c *contentModerationTestHashCache) HasFlaggedInputHash(ctx context.Context, inputHash string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checked = append(c.checked, inputHash)
	if c.hasResultUsed {
		return c.hasResult, nil
	}
	_, ok := c.hashes[inputHash]
	return ok, nil
}

// DeleteFlaggedInputHash records the literal delete request and removes an
// existing fixture member under its mutex. Missing sets or members return
// false; context and real Redis retries are outside this in-memory operation.
func (c *contentModerationTestHashCache) DeleteFlaggedInputHash(ctx context.Context, inputHash string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleted = append(c.deleted, inputHash)
	if c.hashes == nil {
		return false, nil
	}
	if _, ok := c.hashes[inputHash]; !ok {
		return false, nil
	}
	delete(c.hashes, inputHash)
	return true, nil
}

// ClearFlaggedInputHashes samples and replaces fixture membership under one
// mutex, returning its prior count. It ignores context; this single locked
// fixture operation does not model the production SCARD-before-DEL
// interleaving.
func (c *contentModerationTestHashCache) ClearFlaggedInputHashes(ctx context.Context) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	deleted := int64(len(c.hashes))
	c.hashes = map[string]struct{}{}
	return deleted, nil
}

// CountFlaggedInputHashes returns fixture cardinality under its mutex and
// ignores context. Nil membership counts as zero, and this adapter introduces
// no Redis availability or command retry errors.
func (c *contentModerationTestHashCache) CountFlaggedInputHashes(ctx context.Context) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return int64(len(c.hashes)), nil
}

// snapshotRecorded returns a new string slice containing record invocation
// history under the fixture mutex. Repeated member recordings remain separate
// observations, and callers can modify the returned slice independently.
func (c *contentModerationTestHashCache) snapshotRecorded() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.recorded))
	copy(out, c.recorded)
	return out
}

// snapshotChecked returns a new string slice containing membership invocation
// history under the fixture mutex. It counts reads rather than unique members,
// allowing cases to observe exactly which query arguments were used.
func (c *contentModerationTestHashCache) snapshotChecked() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.checked))
	copy(out, c.checked)
	return out
}

// hasHash reads literal fixture membership under its mutex for administrator
// removal assertions. Missing sets return false, and this helper does not add a
// membership invocation to checked history.
func (c *contentModerationTestHashCache) hasHash(inputHash string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.hashes[inputHash]
	return ok
}

// snapshotDeleted returns a new slice of delete invocation arguments under its
// mutex. Failed or repeated removals still appear in that history, letting
// cases separate invocation observation from successful removal.
func (c *contentModerationTestHashCache) snapshotDeleted() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.deleted))
	copy(out, c.deleted)
	return out
}

// defaultContentModerationModelFilterTestConfig returns enabled pre-block
// policy with a fixed blocked keyword and otherwise default scope. Cases
// replace ModelFilter before serializing this policy to isolate requested-model
// admission.
func defaultContentModerationModelFilterTestConfig() *ContentModerationConfig {
	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModePreBlock
	cfg.BlockedKeywords = []string{"secret-token"}
	return cfg
}

// newContentModerationModelFilterTestService serializes supplied policy and
// starts a service with ordinary log and hash fixtures. It registers no stop
// cleanup; callers own the returned runtime and observe queued records through
// the log fixture.
func newContentModerationModelFilterTestService(t *testing.T, cfg *ContentModerationConfig) (*ContentModerationService, *contentModerationTestRepo) {
	t.Helper()
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationTestRepo{}
	svc := newTestModeration(
		&contentModerationTestSettingRepo{values: map[string]string{
			SettingKeyRiskControlEnabled:      "true",
			SettingKeyContentModerationConfig: string(rawCfg),
		}},
		repo,
		&contentModerationTestHashCache{},
		nil,
		nil,
		nil,
		nil,
	)
	svc.Start()
	return svc, repo
}

// newContentModerationFlaggedLog constructs a timestamped ordinary flagged
// fixture record for the supplied actor. Its block action counts toward
// ordinary policy, while hash-block history is supplied by other cases and
// additional attribution can be set before persistence.
func newContentModerationFlaggedLog(userID int64) *ContentModerationLog {
	return &ContentModerationLog{
		UserID:          &userID,
		Action:          ContentModerationActionBlock,
		Flagged:         true,
		HighestCategory: "sexual",
		HighestScore:    0.9,
		CreatedAt:       time.Now(),
	}
}

// contentModerationIntPtr allocates an independent integer pointer used for
// fixture timing arguments.
func contentModerationIntPtr(v int) *int {
	return &v
}

// newCreativeNoMediaRetentionModerationService starts an observe fixture that
// flags serialized input containing image_url and assigns a safe score to other
// input. The selected cases submit separate text and image units, and a valid
// inline PNG permits production snapshot checks without remote URL fetching.
//
// LIFO cleanup runs service Stop before the HTTP server closes, allowing
// accepted queue work to finish against that endpoint. Fixture decode, marshal,
// and response writes ignore their errors; malformed input or marker text can
// affect this simple classifier, so callers supply the intended unit shapes.
func newCreativeNoMediaRetentionModerationService(t *testing.T) (*ContentModerationService, *contentModerationTestRepo, *contentModerationTestHashCache, string) {
	t.Helper()
	pngBytes := makeTestPNG(t, 2, 2)
	imageDataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Input any `json:"input"` // JSON audit input field value
		}
		// Decode and reserialize the controlled request for literal marker matching;
		// ignored errors can produce a non-image classification instead of rejecting fixture input.
		_ = json.NewDecoder(r.Body).Decode(&request)
		rawInput, _ := json.Marshal(request.Input)
		if strings.Contains(string(rawInput), "image_url") {
			// Emit the image-marker score used by retained-snapshot assertions; write
			// errors are ignored here and can affect the service outcome observed by the case.
			_ = json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{
				{CategoryScores: map[string]float64{"sexual": 0.99}},
			}})
			return
		}
		// Emit the ordinary text score for input lacking the literal marker; this
		// fixture ignores writer errors and leaves result observation to the service assertions.
		_ = json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{
			{CategoryScores: map[string]float64{"sexual": 0.01}},
		}})
	}))
	t.Cleanup(server.Close)

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModeObserve
	cfg.BaseURL = server.URL
	cfg.APIKeys = []string{"sk-test"}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	repo := &contentModerationTestRepo{}
	hashCache := &contentModerationTestHashCache{}
	svc := newTestModeration(
		&contentModerationTestSettingRepo{values: map[string]string{
			SettingKeyRiskControlEnabled:      "true",
			SettingKeyContentModerationConfig: string(rawCfg),
		}},
		repo,
		hashCache,
		nil,
		nil,
		nil,
		nil,
	)
	// Register stop after server cleanup so LIFO teardown joins accepted service
	// work while its HTTP fixture remains available, then closes the server resource.
	svc.Start()
	t.Cleanup(func() { require.NoError(t, svc.Stop()) })
	return svc, repo, hashCache, imageDataURL
}

// SetStatus replaces the serial fixture user with the requested ID/status and
// existing role. A stored user is required for role lookup; the replacement
// omits email, and Update ignores the supplied Status selector.
func (r *contentModerationTestUserRepo) SetStatus(ctx context.Context, id int64, status string) error {
	return r.Update(ctx, &User{ID: id, Role: r.user.Role, Status: status}, UserUpdateFields{Status: true})
}

// Lookup converts the shared fixture proxy to moderation values using the
// supplied expiry instant. It delegates lookup counting and errors, keeps URL
// credentials for dialing, and constructs a separate address label for
// diagnostics.
func (r *contentModerationTestProxyRepo) Lookup(ctx context.Context, id int64, now time.Time) (ProxyInfo, error) {
	v, e := r.GetByID(ctx, id)
	if e != nil {
		return ProxyInfo{}, e
	}
	return ProxyInfo{URL: v.URL(), Name: v.Name, Status: v.Status, Address: fmt.Sprintf("%s://%s:%d", v.Protocol, v.Host, v.Port), Active: v.IsActive(), Expired: v.IsExpired(now)}, nil
}

// IsOpenAICyberWarningText delegates fixture recognition to the shared upstream
// OpenAI warning-text classifier.
func IsOpenAICyberWarningText(text string) bool { return openai.IsOpenAICyberWarningText(text) }

// makeTestPNG returns encoded bytes for a transparent RGBA image with
// caller-supplied positive dimensions. Encoding errors fail the testcase; the
// local buffer remains reachable through the returned byte slice for inline
// snapshot fixtures.
func makeTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// splitContentModerationText collects the chunk iterator output into a fixture
// slice in original chunk order. Blank text yields empty output, while chunk
// size and overlap follow the iterator normalization used by production
// batching.
func splitContentModerationText(text string, chunkSize int, overlap int) []string {
	chunks := make([]string, 0, countContentModerationTextChunks(text, chunkSize, overlap))
	forEachContentModerationTextBatch(text, chunkSize, overlap, contentModerationTextBatchSize, func(_ int, batch []string) {
		chunks = append(chunks, batch...)
	})
	return chunks
}
