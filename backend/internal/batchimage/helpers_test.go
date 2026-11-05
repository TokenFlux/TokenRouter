package batchimage_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchimageprovider "github.com/TokenFlux/TokenRouter/internal/batchimage/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// The batch tests share an in-memory job repository, recording funds store,
// provider streams and queue handles so each service can inspect the same
// task progress. Stored jobs are shared pointers and item replacement copies
// the outer parameter slice while retaining optional field pointers. Each
// test owns these mutable fixtures and configures them before service calls.
//
// The fixtures reproduce selected lifecycle checks and source allocations
// rather than database transactions or Redis token comparisons. Job listing
// follows map iteration, replacement updates fixture ItemCount, and creation
// copies the fields used by these cases. Funding records normalized command
// pointers and request-ID replay state; capture delegates allocation
// calculation to billing.PlanTaskCapture. Construction helpers bind
// production candidate and result adapters around the fake sources, keeping
// provider eligibility and public conversion rules on the production path.

// The encoded image sample is reused in provider rows and assertions that
// inspect whether persisted metadata or diagnostics expose payload content.
const batchImageTestData = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJ"

// Compile-time assertions keep recording fixtures compatible with the domain
// and provider interfaces exercised by shared batch service construction.
var (
	_ batchimage.BatchImageDownloadLimiter = (*fakeBatchImageDownloadLimiter)(nil)
	_ batchimage.BatchImageDownloadPermit  = (*fakeBatchImageDownloadPermit)(nil)

	_ batchProvidersFixtureSource           = (*publicBatchImageProviderRepo)(nil)
	_ batchimage.BatchImageQueue            = (*publicBatchImageQueue)(nil)
	_ batchimageprovider.BatchImageProvider = (*publicBatchImageProvider)(nil)

	_ batchGroupFixtureSource                      = (*publicBatchImageGroupRepo)(nil)
	_ batchimage.BatchImageUserGroupRateRepository = (*publicBatchImageUserGroupRateRepo)(nil)

	_ batchimage.FundingStore = (*fakeBatchImageBillingRepo)(nil)
	_ batchimage.ImagePricer  = (*fakeBatchImagePricingResolver)(nil)
)

// fakeBatchImageDownloadLimiter records slot acquisition and release within a
// single test.
type fakeBatchImageDownloadLimiter struct {
	acquireCount int  // download acquisition invocation count
	releaseCount int  // download or lease release count
	deny         bool // configured download admission rejection
}

// fakeBatchImageDownloadPermit invokes one retained release callback during
// sequential fixture cleanup.
type fakeBatchImageDownloadPermit struct {
	once    bool   // sequential permit release guard
	release func() // fixture resource release callback
}

// fakeBatchImageProviderResolver supplies one provider pointer or error to
// result access.
type fakeBatchImageProviderResolver struct {
	// provider and err describe one retained lookup outcome. An injected err
	// takes precedence in GetByID; otherwise callers receive the shared provider
	// pointer for later binding.
	provider *providercore.Record // retained provider record pointer
	err      error                // injected fixture operation failure
}

// fakeProcessorProvider supplies polling observations and retained result
// streams for processor tests.
type fakeProcessorProvider struct {
	// status and getErr describe the polling outcome. Get records its invocation
	// before returning getErr, then uses a configured status or a new queued
	// observation when status is absent.
	status *batchimage.BatchProviderStatus // retained upstream observation pointer
	getErr error                           // injected provider polling failure
	result string                          // configured provider JSONL or outcome

	getCalled        bool // provider polling was invoked
	openResultCalled bool // provider result was opened
}

// batchProvidersFixtureSource reads provider records used by production
// candidate conversion in tests.
type batchProvidersFixtureSource interface {
	// GetByID supplies the retained provider record for an existing job fixture.
	GetByID(context.Context, int64) (*providercore.Record, error)
	// ListSchedulableByPlatform supplies records for production batch candidate
	// conversion in this fixture.
	ListSchedulableByPlatform(context.Context, string) ([]providercore.Record, error)
	// ListSchedulableByGroupIDAndPlatform supplies the configured group
	// candidate records for public fixture selection.
	ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]providercore.Record, error)
}

// batchGroupFixtureSource supplies permission and pricing group views to
// public test construction.
type batchGroupFixtureSource interface {
	// GetByIDLite supplies a retained group view for public permission and price
	// fixtures.
	GetByIDLite(context.Context, int64) (*batchimage.GroupView, error)
}

// batchProviderFixture converts test provider records through production
// batch candidate preparation.
type batchProviderFixture struct {
	source   batchProvidersFixtureSource                                 // configured fixture source reader
	registry *batchimage.Registry[batchimageprovider.BatchImageProvider] // shared production backend registry
}

// batchGroupReader forwards fixture group views into public permission and
// pricing reads.
type batchGroupReader struct {
	source batchGroupFixtureSource /* retained group fixture source */
}

// publicBatchImageProviderRepo retains mutable provider records used by
// public selection tests.
type publicBatchImageProviderRepo struct {
	providers []providercore.Record // retained provider record slice
}

// publicBatchImageQueue records distinct submitted batch identities and
// injected publication failures.
type publicBatchImageQueue struct {
	enqueued []string // distinct published batch identities
	err      error    // injected fixture operation failure
}

// publicBatchImageGroupRepo retains group views for permission and pricing
// test cases.
type publicBatchImageGroupRepo struct {
	groups map[int64]*batchimage.GroupView // retained routing group views
}

// publicBatchImageUserGroupRateRepo supplies optional wallet rates keyed by
// fixture group identity.
type publicBatchImageUserGroupRateRepo struct {
	rates map[int64]*float64 // optional wallet rates by group
}

// publicBatchImageProvider records submissions, cancellation and cleanup
// around a retained result stream.
type publicBatchImageProvider struct {
	name           string                       // registered fixture backend name
	submits        []batchimage.BatchImageInput // retained expanded submission inputs
	submitErr      error                        // injected upstream submission failure
	cancelCount    int                          // provider cancellation invocation count
	cancelErr      error                        // injected upstream cancellation failure
	result         string                       // configured provider JSONL or outcome
	cleanupTargets []batchimage.CleanupTarget   // observed resource deletion targets
	cleanupErr     error                        // injected provider cleanup failure
}

// fakeBatchImageRepository retains shared task state and recorded lifecycle
// effects for batch service tests.
type fakeBatchImageRepository struct {
	jobs          map[string]*batchimage.BatchImageJob               // shared jobs by batch identity
	items         map[string][]batchimage.CreateBatchImageItemParams // stored item parameter slices
	counts        map[string]batchimage.BatchImageCounts             // reconciled counts by batch
	transitions   map[string][]string                                // recorded status edge sequences
	events        map[string][]string                                // recorded audit event labels
	transitionErr error                                              // injected status mutation failure
	replaceCalls  int                                                // item replacement invocation count
}

// resultProviderFixture returns the retained provider for download and
// cleanup eligibility checks.
type resultProviderFixture struct {
	provider *providercore.Record /* retained result provider pointer */
}

// fakeBatchImagePricingResolver records requested models and supplies
// configured per-image prices or errors.
type fakeBatchImagePricingResolver struct {
	unitPrice     float64         // configured USD image price
	missingModels map[string]bool // models with absent image price
	err           error           // injected fixture operation failure
	models        []string        // observed billing model spellings
}

// fakeBatchImageBillingRepo records task commands and delegates capture
// calculations to billing rules.
type fakeBatchImageBillingRepo struct {
	usableSubscription *billing.UserSubscription   // retained eligible subscription pointer
	subscriptionErr    error                       // injected subscription eligibility failure
	reserves           []*billing.TaskFundsCommand // retained reservation command pointers
	captures           []*billing.TaskFundsCommand // retained capture command pointers
	releases           []*billing.TaskFundsCommand // retained refund command pointers
	// seen and alreadyApplied identify request-ID replays in applyHold. A match
	// reports Applied false; the fixture compares no fingerprint and retains no
	// committed account state.
	seen           map[string]struct{} // applied fixture action identities
	alreadyApplied map[string]bool     // injected previously committed identities

	// reserveErr, captureErr and releaseErr take precedence for their named
	// funding operations. The shared err is considered by applyHold after each
	// operation-specific injection has been checked.
	err        error // injected fixture operation failure
	reserveErr error // injected reservation operation failure
	captureErr error // injected capture operation failure
	releaseErr error // injected refund operation failure
}

// fakeBatchImageQueue records scheduling outcomes for one configured worker
// reservation and lease.
type fakeBatchImageQueue struct {
	reserved     batchimage.ReservedBatchImageJob // configured repeated reservation result
	lockAcquired bool                             // configured lease acquisition outcome
	acked        []string                         // acknowledged fixture batch identities
	requeued     []fakeBatchImageRequeue          // recorded delayed visit requests
	releaseCount int                              // download or lease release count
}

// fakeBatchImageRequeue retains a delayed visit identity and its requested
// wait duration.
type fakeBatchImageRequeue struct {
	batchID string        // delayed visit batch identity
	delay   time.Duration // requested delayed visit duration
}

// fakeBatchImageLock forwards held-visit operations to its configured
// recording queue.
type fakeBatchImageLock struct {
	release func()               // fixture resource release callback
	queue   *fakeBatchImageQueue // retained worker queue fixture
}

// fakeBatchImageProcessor records processed IDs and returns a configured
// scheduling outcome.
type fakeBatchImageProcessor struct {
	result    batchimage.BatchImageProcessResult // configured worker scheduling outcome
	err       error                              // injected fixture operation failure
	processed []string                           // observed processed batch identities
}

// newTestBatchImageDownloadService builds a completed job with two successful
// custom IDs, one failure and a recording permit limiter. Its shared
// repository and result backend let cases inspect multi-image downloads and
// archive diagnostics.
func newTestBatchImageDownloadService() (*batchimage.Download, *fakeBatchImageRepository, *fakeBatchImageDownloadLimiter) {
	repo := newFakeBatchImageRepository()
	apiKeyID := int64(22)
	providerID := int64(101)
	repo.jobs["imgbatch_download"] = &batchimage.BatchImageJob{
		BatchID:           "imgbatch_download",
		UserID:            11,
		APIKeyID:          &apiKeyID,
		ProviderID:        &providerID,
		Platform:          batchimage.BatchImageProviderGeminiAPI,
		Model:             "gemini-2.5-flash-image",
		Status:            batchimage.BatchImageJobStatusCompleted,
		ProviderJobName:   batchimage.BatchImageStringPtr("providers/internal/job"),
		ProviderOutputRef: batchimage.BatchImageStringPtr("gs://bucket/internal/output.jsonl"),
		ItemCount:         3,
		SuccessCount:      2,
		FailCount:         1,
		CreatedAt:         time.Now(),
	}
	mime := "image/png"
	ext := "png"
	webp := "image/webp"
	webpExt := "webp"
	code := "SAFETY_BLOCKED"
	msg := "blocked in gs://bucket/internal/output.jsonl"
	repo.items["imgbatch_download"] = []batchimage.CreateBatchImageItemParams{
		{JobID: "imgbatch_download", CustomID: "cover/../001", Status: batchimage.BatchImageItemStatusSuccess, MimeType: &mime, FileExtension: &ext, ImageCount: 2},
		{JobID: "imgbatch_download", CustomID: "bad", Status: batchimage.BatchImageItemStatusFailed, ErrorCode: &code, ErrorMessage: &msg},
		{JobID: "imgbatch_download", CustomID: "ok_2", Status: batchimage.BatchImageItemStatusSuccess, MimeType: &webp, FileExtension: &webpExt, ImageCount: 1},
	}
	platform := &publicBatchImageProvider{name: batchimage.BatchImageProviderGeminiAPI, result: batchImageDownloadResultJSONL()}
	limiter := &fakeBatchImageDownloadLimiter{}
	svc := newBatchDownloadFixture(repo, batchimage.NewRegistry[batchimageprovider.BatchImageProvider](platform), &resultProviderFixture{provider: &providercore.Record{ID: providerID, Platform: capability.PlatformGemini, Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true}}, limiter, &config.Config{BatchImage: config.BatchImageConfig{MaxDownloadItemsZip: 10, MaxDownloadDurationSeconds: 60}})
	return svc, repo, limiter
}

// batchImageDownloadResultJSONL returns three rows containing the fixture's
// image parts and safety failure.
func batchImageDownloadResultJSONL() string {
	return strings.Join([]string{
		`{"key":"cover/../001","response":{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"Zmlyc3Q="}},{"inlineData":{"mimeType":"image/jpeg","data":"c2Vjb25k"}}]}}]}}`,
		`{"key":"bad","error":{"code":"SAFETY","message":"blocked"}}`,
		`{"key":"ok_2","candidates":[{"content":{"parts":[{"inline_data":{"mime_type":"image/webp","data":"dGhpcmQ="}}]}}]}`,
	}, "\n") + "\n"
}

// readZipFiles decodes an in-memory archive and returns entry bytes keyed by
// filename, failing the test on open, read or close errors. Repeated
// filenames replace earlier map entries for fixture assertions.
func readZipFiles(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	out := make(map[string][]byte, len(reader.File))
	for _, file := range reader.File {
		rc, err := file.Open()
		require.NoError(t, err)
		body, err := io.ReadAll(rc)
		require.NoError(t, err)
		// Archive entries are opened independently; close each reader after
		// retaining its bytes so the fixture helper does not keep entry resources
		// alive.
		require.NoError(t, rc.Close())
		out[file.Name] = body
	}
	return out
}

// mapValues collects retained byte slices from the supplied map for
// joined-content assertions. Map iteration determines their order, and the
// returned outer slice still shares each stored entry's bytes.
func mapValues(in map[string][]byte) [][]byte {
	out := make([][]byte, 0, len(in))
	for _, value := range in {
		out = append(out, value)
	}
	return out
}

// Acquire counts the fixture request and returns a denial or a permit linked
// to releaseCount. Successful callers close their content Reader or finish
// the ZIP call to invoke that shared release callback.
func (l *fakeBatchImageDownloadLimiter) Acquire(context.Context, string, string) (batchimage.BatchImageDownloadPermit, error) {
	l.acquireCount++
	if l.deny {
		return nil, batchimage.ErrBatchImageDownloadLimited
	}
	return &fakeBatchImageDownloadPermit{release: func() { l.releaseCount++ }}, nil
}

// Release runs the configured callback once for this single-test permit and
// returns successfully. The boolean guard supports repeated closes in
// sequential fixture use; it provides no synchronization for concurrent
// callers.
func (p *fakeBatchImageDownloadPermit) Release(context.Context) error {
	if p.once {
		return nil
	}
	p.once = true
	if p.release != nil {
		p.release()
	}
	return nil
}

// newBatchProcessorFixture constructs polling and indexing dependencies
// around the supplied repository and funding store. Present registry and
// provider readers install production ResultAccess resolution, while an
// optional auth invalidator receives successful refund cache refreshes.
func newBatchProcessorFixture(repo batchimage.BatchImageRepository, registry *batchimage.Registry[batchimageprovider.BatchImageProvider], providers batchimageprovider.ResultProviders, indexer *batchimage.ResultIndexer, funds batchimage.FundingStore, auth apikey.APIKeyAuthCacheInvalidator, delay time.Duration) *batchimage.ProviderProcessor {
	core := &batchimage.ProviderProcessor{Repo: repo, Funding: nativeTaskFundingFixture(funds), DefaultRequeue: delay, Indexer: indexer}
	if registry != nil && providers != nil {
		core.ResolveProvider = (batchimageprovider.ResultAccess{Registry: registry, Providers: providers}).Process
	}
	if auth != nil {
		core.InvalidateAuth = auth.InvalidateAuthCacheByUserID
	}
	return core
}

// newBatchSettlementFixture binds task funds, legacy quote lookup, retention
// and optional usage or auth callbacks. The usage callback runs production
// snapshot recording on a cloned log, while absent dependencies retain their
// unconfigured behavior.
func newBatchSettlementFixture(repo batchimage.BatchImageRepository, funds batchimage.FundingStore, logs usage.UsageLogRepository, prices batchimage.ImagePricer, auth apikey.APIKeyAuthCacheInvalidator, cfg *config.Config) *batchimage.Settlement {
	core := &batchimage.Settlement{Repo: repo, Funding: nativeTaskFundingFixture(funds)}
	if cfg != nil {
		core.Retention = time.Duration(cfg.BatchImage.OutputRetentionAfterTerminalHours) * time.Hour
	}
	if prices != nil {
		core.Quote = func(ctx context.Context, model string, group *int64, size string) (float64, error) {
			return prices.BatchImageUnitPrice(ctx, batchimage.BatchImagePriceInput{Model: model, GroupID: group, ImageSize: size})
		}
	}
	if auth != nil {
		core.InvalidateAuth = auth.InvalidateAuthCacheByUserID
	}
	if logs != nil {
		core.RecordUsage = func(ctx context.Context, row *usage.UsageLog) {
			completion.NewRecorder(completion.Dependencies{Logs: completion.SnapshotLogWriter(logs), Observe: func(component, message string) { logging.LegacyPrintf(component, "%s", message) }}, completion.RecorderOptions{}).WriteUsage(ctx, querycache.Clone(row), "service.batch_image_settlement")
		}
	}
	return core
}

// nativeTaskFundingFixture retains the supplied store in the batch task
// funding adapter.
func nativeTaskFundingFixture(store batchimage.FundingStore) batchimage.Funding {
	return batchimage.Funding{Store: store}
}

// GetByID returns the fixture's retained provider pointer unless its
// configured error is present. The requested identity is ignored, allowing
// processor and result-access cases to inject one provider lookup outcome.
func (r *fakeBatchImageProviderResolver) GetByID(context.Context, int64) (*providercore.Record, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.provider, nil
}

// Name returns the fixed backend label used by processor-only fixtures.
func (p *fakeProcessorProvider) Name() string { return "fake" }

// SupportsSubmission reuses the fixture credential rules for new jobs.
// Tests inject provider protocol selections through candidate preparation, while
// this adapter supplies deterministic local execution without deployment storage.
func (p *fakeProcessorProvider) SupportsSubmission(provider *providercore.Record) bool {
	return p.SupportsProvider(provider)
}

// SupportsProvider accepts each supplied record for the processor backend
// fixture.
func (p *fakeProcessorProvider) SupportsProvider(*providercore.Record) bool {
	return true
}

// Submit panics when processor-only work unexpectedly creates another
// upstream task.
func (p *fakeProcessorProvider) Submit(context.Context, *batchimage.BatchImageJob, *providercore.Record, batchimage.BatchImageInput) (*batchimage.BatchProviderJob, error) {
	panic("Submit must not be called by PR5 processor")
}

// Get records a polling call and returns an injected error, stored status or
// default queued observation. A configured status pointer remains shared with
// the fixture for the caller's assertions.
func (p *fakeProcessorProvider) Get(context.Context, *batchimage.BatchImageJob, *providercore.Record) (*batchimage.BatchProviderStatus, error) {
	p.getCalled = true
	if p.getErr != nil {
		return nil, p.getErr
	}
	if p.status == nil {
		return &batchimage.BatchProviderStatus{InternalState: batchimage.BatchProviderStateQueued}, nil
	}
	return p.status, nil
}

// Cancel accepts cancellation requests made against the processor backend
// fixture.
func (p *fakeProcessorProvider) Cancel(context.Context, *batchimage.BatchImageJob, *providercore.Record) error {
	return nil
}

// OpenResult records access and returns a closable reader over the fixture
// result string. The indexer or download consumer owns closing that reader
// while the provider retains its encoded source text.
func (p *fakeProcessorProvider) OpenResult(context.Context, *batchimage.BatchImageJob, *providercore.Record) (io.ReadCloser, string, error) {
	p.openResultCalled = true
	return io.NopCloser(strings.NewReader(p.result)), "application/jsonl", nil
}

// Cleanup accepts resource removal requests against the processor backend
// fixture.
func (p *fakeProcessorProvider) Cleanup(context.Context, *batchimage.BatchImageJob, *providercore.Record, batchimage.CleanupTarget) error {
	return nil
}

// resultObserve forwards batch fixture diagnostics to the configured legacy
// logging helper.
func resultObserve(event string, values ...any) {
	logging.LegacyPrintf("service.batch_image", "%s %v", event, values)
}

// project converts a cloned provider record through production batch
// candidate preparation. The fixture registry and model trace observer remain
// attached, so injected records use the same selection callbacks as public
// operations.
func (r *batchProviderFixture) project(value *providercore.Record) *batchimage.Candidate {
	return (&batchimageprovider.Candidates{Registry: r.registry, ObserveModel: modeltrace.RegisterStage}).Project(providercore.CloneRecord(value))
}

// GetByID converts the configured source lookup into a batch provider
// candidate.
func (r *batchProviderFixture) GetByID(ctx context.Context, id int64) (*batchimage.Candidate, error) {
	v, err := r.source.GetByID(ctx, id)
	return r.project(v), err
}

// values converts the supplied provider records into a newly allocated
// candidate slice.
func (r *batchProviderFixture) values(rows []providercore.Record) []batchimage.Candidate {
	out := make([]batchimage.Candidate, len(rows))
	for i := range rows {
		out[i] = *r.project(&rows[i])
	}
	return out
}

// ListSchedulableByPlatform converts source records for the requested routing
// platform into candidates.
func (r *batchProviderFixture) ListSchedulableByPlatform(ctx context.Context, p string) ([]batchimage.Candidate, error) {
	v, err := r.source.ListSchedulableByPlatform(ctx, p)
	return r.values(v), err
}

// ListSchedulableByGroupIDAndPlatform converts the configured
// group-and-platform source listing into candidates.
func (r *batchProviderFixture) ListSchedulableByGroupIDAndPlatform(ctx context.Context, id int64, p string) ([]batchimage.Candidate, error) {
	v, err := r.source.ListSchedulableByGroupIDAndPlatform(ctx, id, p)
	return r.values(v), err
}

// GetByIDLite forwards the group lookup and returns the same view through
// batchGroupProjection. The fixture retains that pointer, so caller mutations
// change subsequent permission and pricing reads from the shared source.
func (r batchGroupReader) GetByIDLite(ctx context.Context, id int64) (*batchimage.GroupView, error) {
	v, err := r.source.GetByIDLite(ctx, id)
	return batchGroupProjection(v), err
}

// batchGroupProjection returns its supplied group pointer unchanged, including
// nil. Callers changing a present view also change the fixture's retained value;
// this helper introduces no independent group storage for service reads.
func batchGroupProjection(v *batchimage.GroupView) *batchimage.GroupView { return v }

// taskFixtureBilling returns the shared funds store attached to a public
// fixture.
func taskFixtureBilling(core *batchimage.Public) batchimage.FundingStore { return core.Funding.Store }

// newBatchPublicFixture constructs public operations with production
// candidate preparation and supplied recording dependencies. Missing group
// readers receive an enabled group-seven view with the standard batch rates.
// Configuration supplies request caps, while the registry defines available
// backends.
//
// Model trace callbacks retain client and routing identities. Subscription
// callbacks resolve sources through the injected funds store, and the
// multiplier callback uses shared completion pricing rules. Optional
// authentication invalidation is installed only when supplied. Tests own the
// mutable fake records and callbacks before invoking this service;
// construction does not start background worker loops.
func newBatchPublicFixture(repo batchimage.BatchImageRepository, providers batchProvidersFixtureSource, pricingConfigs *routing.PricingConfigService, groups batchGroupFixtureSource, rates batchimage.BatchImageUserGroupRateRepository, queue batchimage.BatchImageQueue, registry *batchimage.Registry[batchimageprovider.BatchImageProvider], prices batchimage.ImagePricer, funds batchimage.FundingStore, auth apikey.APIKeyAuthCacheInvalidator, cfg *config.Config) *batchimage.Public {
	core := &batchimage.Public{Attributes: &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}, Repo: repo, UserGroupRateRepo: rates, Queue: queue, Pricing: prices, Funding: nativeTaskFundingFixture(funds), Observe: resultObserve}
	if providers != nil {
		core.ProviderRepo = &batchProviderFixture{source: providers, registry: registry}
	}
	if groups == nil {
		// A missing group source uses the enabled default test group, allowing
		// cases focused on other dependencies to pass the public permission gate.
		groups = &publicBatchImageGroupRepo{groups: map[int64]*batchimage.GroupView{7: {ID: 7, AllowBatchImageGeneration: true, RateMultiplier: 1, BatchImageDiscountMultiplier: 0.5, BatchImageHoldMultiplier: 0.6}}}
	}
	core.GroupRepo = batchGroupReader{groups}
	if pricingConfigs != nil {
		core.PricingConfigService = pricingConfigs
	}
	if cfg != nil {
		c := cfg.BatchImage
		core.Options = batchimage.PublicOptions{
			StaleActiveAfterSeconds:       c.Queue.StaleActiveAfterSeconds,
			MaxItemsPerJobDefault:         c.MaxItemsPerJob,
			MaxOutputImagesPerJob:         c.MaxOutputImagesPerJob,
			MaxOutputImagesPerItem:        c.MaxOutputImagesPerItem,
			MaxPromptCharsPerItem:         c.MaxPromptBytesPerItem,
			MaxReferenceImagesPerJob:      c.MaxReferenceImagesPerJob,
			MaxReferenceInlineBytesPerJob: c.MaxReferenceInlineBytesPerJob,
			DefaultResponseMimeType:       c.DefaultResponseMimeType,
			DefaultImageSize:              c.DefaultImageSize,
		}
	}
	core.ProviderExists = func(name string) bool {
		value, ok := registry.Get(name)
		return ok && value != nil
	}
	if auth != nil {
		core.InvalidateAuth = auth.InvalidateAuthCacheByUserID
	}
	core.ClientModel = func(ctx context.Context) string {
		v, _ := ctx.Value(telemetry.ClientModel).(string)
		return v
	}
	core.WithModelTrace = func(ctx context.Context, m routing.GroupMappingResult, requested string) routing.GroupMappingResult {
		return modeltrace.WithGroupRedirect(m, ctx, requested)
	}
	core.RegisterModel = modeltrace.RegisterStage
	core.AutoSubscription = func(ctx context.Context, userID int64, groupID *int64) *billing.UserSubscription {
		reader, _ := taskFixtureBilling(core).(completion.SubscriptionReader)
		return completion.ResolveSubscription(ctx, nil, reader, userID, groupID)
	}
	core.PreferredSubscription = func(ctx context.Context, userID, id int64, groupID *int64) *billing.UserSubscription {
		reader, _ := taskFixtureBilling(core).(billing.PreferredSubscriptionReader)
		return billing.ResolvePreferredSubscription(ctx, reader, userID, id, groupID)
	}
	core.SubscriptionMultiplier = func(ctx context.Context, owner batchimage.BatchImageOwner, group *batchimage.GroupView, fallback float64, sub *billing.UserSubscription) float64 {
		var projected *completion.GroupSnapshot
		if group != nil {
			projected = &completion.GroupSnapshot{ID: group.ID, RateMultiplier: group.RateMultiplier}
		}
		return completion.ResolveUsageRateMultiplier(ctx, owner.EffectiveBillingUserID(), owner.GroupID, projected, fallback, sub, nil)
	}
	return core
}

// validBatchImageSubmitRequest creates a two-prompt Gemini batch with
// oversized metadata for sanitation cases.
func validBatchImageSubmitRequest() batchimage.BatchImageSubmitRequest {
	return batchimage.BatchImageSubmitRequest{
		Model:            "gemini-2.5-flash-image",
		Platform:         batchimage.BatchImageProviderGeminiAPI,
		ResponseMimeType: "image/png",
		AspectRatio:      "1:1",
		ImageSize:        "1K",
		Metadata:         map[string]string{"project": "campaign-a", "secret": strings.Repeat("x", 300)},
		Items: []batchimage.BatchImageSubmitItem{
			{CustomID: "cover_001", Prompt: "hero"},
			{CustomID: "cover_002", Prompt: "clean"},
		},
	}
}

// testBatchImageProvider creates a schedulable Gemini record with fixture
// credentials and batch capability.
func testBatchImageProvider(id int64, providerType string) providercore.Record {
	return providercore.Record{
		ID:            id,
		Platform:      capability.PlatformGemini,
		Type:          providerType,
		Status:        billing.StatusActive,
		Schedulable:   true,
		Priority:      int(id),
		Credentials:   map[string]any{"api_key": "test-secret"},
		Concurrency:   1,
		RateLimitedAt: nil,
	}
}

// GetByID returns a pointer into the retained provider slice when its
// identity matches. Unknown IDs return the fixture's lookup error, and
// callers modifying the record alter later fixture reads.
func (r *publicBatchImageProviderRepo) GetByID(_ context.Context, id int64) (*providercore.Record, error) {
	for i := range r.providers {
		if r.providers[i].ID == id {
			return &r.providers[i], nil
		}
	}
	return nil, errors.New("provider not found")
}

// ListSchedulableByPlatform copies records of the requested platform into a
// fresh outer slice, while credential maps and optional pointers stay shared.
// The fixture does not filter scheduling flags here;
// production candidate checks evaluate those retained fields later.
func (r *publicBatchImageProviderRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]providercore.Record, error) {
	out := make([]providercore.Record, 0, len(r.providers))
	for _, provider := range r.providers {
		if provider.Platform == platform {
			out = append(out, provider)
		}
	}
	return out, nil
}

// ListSchedulableByGroupIDAndPlatform delegates to the fixture's platform
// listing and ignores the group identity. Public bound-group checks still run
// in the service, while fixture records are available for any positive group.
func (r *publicBatchImageProviderRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, _ int64, platform string) ([]providercore.Record, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

// Enqueue records a new batch identity unless an injected error is
// configured. Repeated recorded IDs return ErrBatchImageAlreadyQueued,
// letting submission replay exercise duplicate queue publication through this
// fixture.
func (q *publicBatchImageQueue) Enqueue(_ context.Context, batchID string) error {
	if q.err != nil {
		return q.err
	}
	if slices.Contains(q.enqueued, batchID) {
		return batchimage.ErrBatchImageAlreadyQueued
	}
	q.enqueued = append(q.enqueued, batchID)
	return nil
}

// Reserve reports an empty queue for the public submission recording fixture.
func (q *publicBatchImageQueue) Reserve(context.Context, time.Duration) (batchimage.ReservedBatchImageJob, error) {
	return batchimage.ReservedBatchImageJob{}, batchimage.ErrBatchImageQueueEmpty
}

// RequeueAfter accepts delayed scheduling requests for the public queue
// fixture.
func (q *publicBatchImageQueue) RequeueAfter(context.Context, string, time.Duration) error {
	return nil
}

// Ack accepts acknowledgement requests for the public submission queue
// fixture.
func (q *publicBatchImageQueue) Ack(context.Context, string) error {
	return nil
}

// Heartbeat accepts activity requests for the public submission queue
// fixture.
func (q *publicBatchImageQueue) Heartbeat(context.Context, string) error {
	return nil
}

// MoveDueDelayedToReady reports no delayed entries in the public queue
// fixture.
func (q *publicBatchImageQueue) MoveDueDelayedToReady(context.Context, int) (int, error) {
	return 0, nil
}

// RecoverStaleActive reports no abandoned entries in the public queue
// fixture.
func (q *publicBatchImageQueue) RecoverStaleActive(context.Context, time.Duration, int) (int, error) {
	return 0, nil
}

// TryAcquireJobLock reports no acquired lease for the public queue fixture.
func (q *publicBatchImageQueue) TryAcquireJobLock(context.Context, string, time.Duration) (batchimage.BatchImageJobLock, bool, error) {
	return nil, false, nil
}

// GetByIDLite returns the retained group pointer when its identity is
// configured. A nil repository, absent map or missing group returns
// routing.ErrGroupNotFound for permission and pricing validation cases.
func (r *publicBatchImageGroupRepo) GetByIDLite(_ context.Context, id int64) (*batchimage.GroupView, error) {
	if r != nil && r.groups != nil {
		if group, ok := r.groups[id]; ok {
			return group, nil
		}
	}
	return nil, routing.ErrGroupNotFound
}

// GetByUserAndGroup reads the optional retained rate for the supplied group
// identity and ignores the user argument. Missing fixture maps produce nil
// successfully, allowing public pricing to keep the group wallet rate.
func (r *publicBatchImageUserGroupRateRepo) GetByUserAndGroup(_ context.Context, _ int64, groupID int64) (*float64, error) {
	if r != nil && r.rates != nil {
		return r.rates[groupID], nil
	}
	return nil, nil
}

// Name returns the configured registry label of the public provider fixture.
func (p *publicBatchImageProvider) Name() string { return p.name }

// SupportsSubmission matches fixture backends to their supported authentication
// types. Production candidate preparation checks protocol selections before this
// fake admits locally recorded submissions, allowing tests to compare both backends.
func (p *publicBatchImageProvider) SupportsSubmission(provider *providercore.Record) bool {
	if p.name == batchimage.BatchImageProviderVertex {
		return provider.Type == capability.ProviderTypeServiceAccount
	}
	return provider.Type == capability.ProviderTypeAPIKey
}

// SupportsProvider accepts all records for the public submission backend
// fixture.
func (p *publicBatchImageProvider) SupportsProvider(*providercore.Record) bool { return true }

// Submit records the expanded provider input before returning an injected
// error or fixed resource references. Input slices and byte buffers remain
// shared with the fixture so tests inspect what submission passed upstream.
func (p *publicBatchImageProvider) Submit(_ context.Context, _ *batchimage.BatchImageJob, _ *providercore.Record, input batchimage.BatchImageInput) (*batchimage.BatchProviderJob, error) {
	p.submits = append(p.submits, input)
	if p.submitErr != nil {
		return nil, p.submitErr
	}
	return &batchimage.BatchProviderJob{
		ProviderJobName:   "providers/" + p.name + "/job",
		ProviderInputRef:  "files/" + p.name + "/input",
		ProviderOutputRef: "files/" + p.name + "/output",
	}, nil
}

// Get returns a queued upstream observation for the public provider fixture.
func (p *publicBatchImageProvider) Get(context.Context, *batchimage.BatchImageJob, *providercore.Record) (*batchimage.BatchProviderStatus, error) {
	return &batchimage.BatchProviderStatus{InternalState: batchimage.BatchProviderStateQueued}, nil
}

// Cancel counts the upstream request and returns the configured cancellation
// error.
func (p *publicBatchImageProvider) Cancel(context.Context, *batchimage.BatchImageJob, *providercore.Record) error {
	p.cancelCount++
	return p.cancelErr
}

// OpenResult returns a closable reader over the public provider fixture's
// result text and JSONL MIME. Each consumer owns closing its reader, while
// the underlying fixture retains the same source string.
func (p *publicBatchImageProvider) OpenResult(context.Context, *batchimage.BatchImageJob, *providercore.Record) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader(p.result)), "application/jsonl", nil
}

// Cleanup records the selected resource target and returns its configured
// error.
func (p *publicBatchImageProvider) Cleanup(_ context.Context, _ *batchimage.BatchImageJob, _ *providercore.Record, target batchimage.CleanupTarget) error {
	p.cleanupTargets = append(p.cleanupTargets, target)
	return p.cleanupErr
}

// newFakeBatchImageRepository allocates empty job, result, count, transition
// and event maps.
func newFakeBatchImageRepository() *fakeBatchImageRepository {
	return &fakeBatchImageRepository{
		jobs:        make(map[string]*batchimage.BatchImageJob),
		items:       make(map[string][]batchimage.CreateBatchImageItemParams),
		counts:      make(map[string]batchimage.BatchImageCounts),
		transitions: make(map[string][]string),
		events:      make(map[string][]string),
	}
}

// CreateBatchImageJob copies the selected parameter fields used by these
// tests into a shared record and sets its creation time. It stores that
// record by BatchID; duplicate IDs replace the fixture entry. Payer/team,
// funding mode, parent batch and several provider fields are omitted, so tests
// needing those snapshots seed their records directly.
func (r *fakeBatchImageRepository) CreateBatchImageJob(_ context.Context, params batchimage.CreateBatchImageJobParams) (*batchimage.BatchImageJob, error) {
	job := &batchimage.BatchImageJob{
		BatchID:                     params.BatchID,
		UserID:                      params.UserID,
		APIKeyID:                    params.APIKeyID,
		ProviderID:                  params.ProviderID,
		GroupID:                     params.GroupID,
		Status:                      params.Status,
		Platform:                    params.Platform,
		Model:                       params.Model,
		RequestedModel:              params.RequestedModel,
		InternalModel:               params.InternalModel,
		TaskName:                    params.TaskName,
		ProviderJobName:             params.ProviderJobName,
		ItemCount:                   params.ItemCount,
		EstimatedCost:               params.EstimatedCost,
		HoldAmount:                  params.HoldAmount,
		BalanceHoldAmount:           params.BalanceHoldAmount,
		SubscriptionHoldAllocations: cloneResultAllocations(params.SubscriptionHoldAllocations),
		SubscriptionRateMultiplier:  params.SubscriptionRateMultiplier,
		BalanceRateMultiplier:       params.BalanceRateMultiplier,
		PlanGroupRateEnabled:        params.PlanGroupRateEnabled,
		HoldID:                      params.HoldID,
		BaseUnitPrice:               params.BaseUnitPrice,
		GroupRateMultiplier:         params.GroupRateMultiplier,
		ProviderRateMultiplier:      params.ProviderRateMultiplier,
		BatchDiscountMultiplier:     params.BatchDiscountMultiplier,
		HoldMultiplier:              params.HoldMultiplier,
		BillableUnitPrice:           params.BillableUnitPrice,
		HoldUnitPrice:               params.HoldUnitPrice,
		PricingSnapshotVersion:      params.PricingSnapshotVersion,
		Currency:                    params.Currency,
		IdempotencyKey:              params.IdempotencyKey,
		RequestHash:                 params.RequestHash,
		SessionID:                   params.SessionID,
		CreatedAt:                   time.Now(),
	}
	r.jobs[job.BatchID] = job
	return job, nil
}

// GetBatchImageJobByBatchID returns the retained job pointer for the
// requested batch identity. Missing records return the domain not-found
// error, and fixture callers can mutate a returned record for later service
// visits.
func (r *fakeBatchImageRepository) GetBatchImageJobByBatchID(_ context.Context, batchID string) (*batchimage.BatchImageJob, error) {
	job, ok := r.jobs[batchID]
	if !ok {
		return nil, batchimage.ErrBatchImageJobNotFound
	}
	return job, nil
}

// GetBatchImageJobByIdempotencyKey scans retained jobs for matching user, key
// and trimmed stored retry key. It returns the shared record or the domain
// not-found error used by public submission replay.
func (r *fakeBatchImageRepository) GetBatchImageJobByIdempotencyKey(_ context.Context, userID, apiKeyID int64, key string) (*batchimage.BatchImageJob, error) {
	for _, job := range r.jobs {
		if job.UserID == userID && job.APIKeyID != nil && *job.APIKeyID == apiKeyID && batchimage.BatchImageDerefString(job.IdempotencyKey) == key {
			return job, nil
		}
	}
	return nil, batchimage.ErrBatchImageJobNotFound
}

// GetBatchImageJobByBatchIDForOwner returns the shared record only when user
// and key ownership both match. Missing jobs or mismatched identities use the
// same domain not-found result for public operation cases.
func (r *fakeBatchImageRepository) GetBatchImageJobByBatchIDForOwner(_ context.Context, userID, apiKeyID int64, batchID string) (*batchimage.BatchImageJob, error) {
	job, ok := r.jobs[batchID]
	if !ok || job.UserID != userID || job.APIKeyID == nil || *job.APIKeyID != apiKeyID {
		return nil, batchimage.ErrBatchImageJobNotFound
	}
	return job, nil
}

// ListBatchImageJobsForOwner filters retained jobs by owner and requested
// state, title, download and time conditions. Pagination follows map
// iteration order, normalizes its limit and offset, and returns shared record
// pointers.
func (r *fakeBatchImageRepository) ListBatchImageJobsForOwner(_ context.Context, userID, apiKeyID int64, filter batchimage.BatchImageJobFilter) ([]*batchimage.BatchImageJob, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset := max(filter.Offset, 0)
	var jobs []*batchimage.BatchImageJob
	for _, job := range r.jobs {
		if job.UserID != userID || job.APIKeyID == nil || *job.APIKeyID != apiKeyID {
			continue
		}
		if filter.Status != "" && job.Status != filter.Status {
			continue
		}
		if filter.TaskNameLike != "" && !strings.Contains(strings.ToLower(job.TaskName), strings.ToLower(filter.TaskNameLike)) {
			continue
		}
		if filter.ExcludeDeleted && job.UserDeletedAt != nil {
			continue
		}
		if filter.Downloaded != nil {
			downloaded := job.DownloadedAt != nil
			if downloaded != *filter.Downloaded {
				continue
			}
		}
		if filter.CreatedAfter != nil && job.CreatedAt.Before(*filter.CreatedAfter) {
			continue
		}
		if filter.CreatedBefore != nil && !job.CreatedAt.Before(*filter.CreatedBefore) {
			continue
		}
		if offset > 0 {
			offset--
			continue
		}
		jobs = append(jobs, job)
		if len(jobs) >= limit {
			break
		}
	}
	return jobs, nil
}

// GetBatchImageJobByID scans the retained records for a numeric identity and
// returns its shared pointer. A missing identity produces the job not-found
// category for tests using internal lookup.
func (r *fakeBatchImageRepository) GetBatchImageJobByID(_ context.Context, id int64) (*batchimage.BatchImageJob, error) {
	for _, job := range r.jobs {
		if job.ID == id {
			return job, nil
		}
	}
	return nil, batchimage.ErrBatchImageJobNotFound
}

// TransitionBatchImageJobStatus checks the production transition predicate
// before any injected error or mutation. Successful updates assign the supplied
// diagnostic pointers, including nil, and record transition and event labels
// on shared fixture state. PostgreSQL updates diagnostics on failed transitions;
// this fixture applies the supplied pointers on every accepted edge.
func (r *fakeBatchImageRepository) TransitionBatchImageJobStatus(_ context.Context, batchID, toStatus string, opts batchimage.BatchImageTransitionOptions) error {
	job, ok := r.jobs[batchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	if !batchimage.CanTransitionBatchImageJob(job.Status, toStatus) {
		return batchimage.ErrBatchImageInvalidTransition
	}
	if r.transitionErr != nil {
		return r.transitionErr
	}
	// The fixture applies the guarded state before recording its transition and
	// event labels, letting later service visits inspect the same shared job
	// outcome.
	job.Status = toStatus
	job.LastErrorCode = opts.ErrorCode
	job.LastErrorMessage = opts.ErrorMessage
	r.transitions[batchID] = append(r.transitions[batchID], toStatus)
	if opts.EventType != "" {
		r.events[batchID] = append(r.events[batchID], opts.EventType)
	}
	return nil
}

// TouchBatchImageJobSubmitting updates activity for retained created or
// uploading jobs using the system clock. Missing jobs fail, while later
// statuses stay unchanged so submission heartbeat cases can distinguish
// eligible records.
func (r *fakeBatchImageRepository) TouchBatchImageJobSubmitting(_ context.Context, batchID string) error {
	job, ok := r.jobs[batchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	if job.Status == batchimage.BatchImageJobStatusCreated || job.Status == batchimage.BatchImageJobStatusUploading {
		job.UpdatedAt = time.Now()
	}
	return nil
}

// FailStaleUnsubmittedBatchImageJob rechecks created or uploading status,
// trimmed provider name and activity at or before cutoff. Eligible shared
// records become failed with recovery diagnostics and event labels;
// ineligible records return false.
func (r *fakeBatchImageRepository) FailStaleUnsubmittedBatchImageJob(_ context.Context, batchID string, cutoff time.Time, code, message string) (bool, error) {
	job, ok := r.jobs[batchID]
	if !ok {
		return false, batchimage.ErrBatchImageJobNotFound
	}
	if job.Status != batchimage.BatchImageJobStatusCreated && job.Status != batchimage.BatchImageJobStatusUploading {
		return false, nil
	}
	if batchimage.BatchImageDerefString(job.ProviderJobName) != "" || job.UpdatedAt.After(cutoff) {
		return false, nil
	}
	job.Status = batchimage.BatchImageJobStatusFailed
	job.LastErrorCode = batchimage.BatchImageStringPtr(code)
	job.LastErrorMessage = batchimage.BatchImageStringPtr(message)
	job.UpdatedAt = time.Now()
	r.transitions[batchID] = append(r.transitions[batchID], batchimage.BatchImageJobStatusFailed)
	r.events[batchID] = append(r.events[batchID], "billing_hold_recovery_failed_unsubmitted")
	return true, nil
}

// UpdateBatchImageJobProviderOutputRef replaces the retained output pointer
// for an existing fixture job. Missing records return the job not-found
// category, so processor cases can detect failures before updating their
// local reference.
func (r *fakeBatchImageRepository) UpdateBatchImageJobProviderOutputRef(_ context.Context, batchID, providerOutputRef string) error {
	job, ok := r.jobs[batchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	job.ProviderOutputRef = &providerOutputRef
	return nil
}

// UpdateBatchImageJobProviderSubmit checks submission eligibility, saves
// returned provider references and marks the shared job submitted. It records
// the system submission time, state edge and provider-submitted event for
// later fixture assertions.
func (r *fakeBatchImageRepository) UpdateBatchImageJobProviderSubmit(_ context.Context, params batchimage.UpdateBatchImageJobProviderSubmitParams) error {
	job, ok := r.jobs[params.BatchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	if !batchimage.CanTransitionBatchImageJob(job.Status, batchimage.BatchImageJobStatusSubmitted) {
		return batchimage.ErrBatchImageInvalidTransition
	}
	job.Status = batchimage.BatchImageJobStatusSubmitted
	job.ProviderJobName = batchimage.BatchImageOptionalStringPtr(params.ProviderJobName)
	job.ProviderInputRef = batchimage.BatchImageOptionalStringPtr(params.ProviderInputRef)
	job.ProviderOutputRef = batchimage.BatchImageOptionalStringPtr(params.ProviderOutputRef)
	job.GCSInputURI = batchimage.BatchImageOptionalStringPtr(params.GCSInputURI)
	job.GCSOutputURI = batchimage.BatchImageOptionalStringPtr(params.GCSOutputURI)
	now := time.Now()
	job.SubmittedAt = &now
	r.transitions[params.BatchID] = append(r.transitions[params.BatchID], batchimage.BatchImageJobStatusSubmitted)
	r.events[params.BatchID] = append(r.events[params.BatchID], "provider_submitted")
	return nil
}

// RecordBatchImageJobSubmitFailure stores optional diagnostics and chooses
// the submit or queue event label. markFailed changes the shared status; a
// queue failure leaves the submitted state available for another publication
// attempt.
func (r *fakeBatchImageRepository) RecordBatchImageJobSubmitFailure(_ context.Context, batchID, code, message string, markFailed bool) error {
	job, ok := r.jobs[batchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	if markFailed {
		job.Status = batchimage.BatchImageJobStatusFailed
	}
	job.LastErrorCode = batchimage.BatchImageOptionalStringPtr(code)
	job.LastErrorMessage = batchimage.BatchImageOptionalStringPtr(message)
	eventType := "submit_failed"
	if !markFailed {
		eventType = "queue_failed"
	}
	r.events[batchID] = append(r.events[batchID], eventType)
	return nil
}

// MarkBatchImageJobSettled checks settling state and any retained manifest
// before completing the fixture job. It saves cost and time, retains a
// present output expiry and records the completion event; already completed
// records return a conflict.
func (r *fakeBatchImageRepository) MarkBatchImageJobSettled(_ context.Context, params batchimage.MarkBatchImageJobSettledParams) error {
	job, ok := r.jobs[params.BatchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	if job.Status != batchimage.BatchImageJobStatusSettling {
		if job.Status == batchimage.BatchImageJobStatusCompleted {
			return batchimage.ErrBatchImageAlreadySettled
		}
		return batchimage.ErrBatchImageSettlementInvalidStatus
	}
	if batchimage.BatchImageDerefString(job.ManifestHash) != "" && batchimage.BatchImageDerefString(job.ManifestHash) != params.ManifestHash {
		return batchimage.ErrBatchImageSettlementManifestConflict
	}
	now := time.Now()
	job.Status = batchimage.BatchImageJobStatusCompleted
	job.ActualCost = &params.ActualCost
	job.ManifestHash = &params.ManifestHash
	job.SettledAt = &now
	if job.OutputExpiresAt == nil && params.OutputExpiresAt != nil {
		job.OutputExpiresAt = params.OutputExpiresAt
	}
	r.transitions[params.BatchID] = append(r.transitions[params.BatchID], batchimage.BatchImageJobStatusCompleted)
	r.events[params.BatchID] = append(r.events[params.BatchID], "settlement_completed")
	return nil
}

// SetBatchImageJobSettlementFailed saves diagnostics, increments RetryCount
// and records the failure event for an existing fixture job. It returns the
// new count or a not-found error used by bounded settlement recovery.
func (r *fakeBatchImageRepository) SetBatchImageJobSettlementFailed(_ context.Context, batchID, code, message string) (int, error) {
	job, ok := r.jobs[batchID]
	if !ok {
		return 0, batchimage.ErrBatchImageJobNotFound
	}
	job.LastErrorCode = batchimage.BatchImageStringPtr(code)
	job.LastErrorMessage = batchimage.BatchImageOptionalStringPtr(message)
	job.RetryCount++
	r.events[batchID] = append(r.events[batchID], "settlement_failed")
	return job.RetryCount, nil
}

// CreateBatchImageItem appends the supplied parameter value to its owning
// batch and returns minimal item metadata. Optional pointers remain retained
// in fixture storage, and this method performs no duplicate-ID validation.
func (r *fakeBatchImageRepository) CreateBatchImageItem(_ context.Context, params batchimage.CreateBatchImageItemParams) (*batchimage.BatchImageItem, error) {
	r.items[params.JobID] = append(r.items[params.JobID], params)
	return &batchimage.BatchImageItem{JobID: params.JobID, CustomID: params.CustomID, Status: params.Status}, nil
}

// BulkCreateBatchImageItems appends each supplied item through the fixture
// creation method.
func (r *fakeBatchImageRepository) BulkCreateBatchImageItems(ctx context.Context, params []batchimage.CreateBatchImageItemParams) error {
	for _, param := range params {
		if _, err := r.CreateBatchImageItem(ctx, param); err != nil {
			return err
		}
	}
	return nil
}

// ReplaceBatchImageItemsForJob replaces fixture outcomes after checking indexing
// state for a registered job. Unregistered indexer fixtures can write results
// too. The outer input slice is copied before its JobID values are overwritten,
// so that rewrite leaves the caller's parameter values intact.
//
// Optional item pointers remain shared. The fixture stores supplied counts and,
// when the job exists, writes SuccessCount, FailCount and ItemCount using the
// replacement length. It keeps no transaction or rollback state, so tests inspect
// this single-call mutation rather than PostgreSQL commit behavior.
func (r *fakeBatchImageRepository) ReplaceBatchImageItemsForJob(_ context.Context, batchID string, items []batchimage.CreateBatchImageItemParams, counts batchimage.BatchImageCounts) error {
	// Registered jobs can rebuild items only while indexing.
	// Unregistered jobs constructed directly by tests can also write items.
	if job, ok := r.jobs[batchID]; ok && job.Status != batchimage.BatchImageJobStatusIndexing {
		return batchimage.ErrBatchImageIndexStateConflict
	}
	r.replaceCalls++
	// Copy the outer replacement slice before setting owning batch IDs; optional
	// item pointers stay shared with these single-test parameter values.
	copied := append([]batchimage.CreateBatchImageItemParams(nil), items...)
	for idx := range copied {
		copied[idx].JobID = batchID
	}
	r.items[batchID] = copied
	r.counts[batchID] = counts
	if job, ok := r.jobs[batchID]; ok {
		job.SuccessCount = counts.SuccessCount
		job.FailCount = counts.FailCount
		job.ItemCount = len(copied)
	}
	return nil
}

// ListBatchImageItems filters retained parameters by status and normalized
// pagination, returning fresh outer item records. Optional field pointers
// remain shared with fixture storage, letting service conversion observe the
// same stored metadata.
func (r *fakeBatchImageRepository) ListBatchImageItems(_ context.Context, batchID string, filter batchimage.BatchImageItemFilter) ([]*batchimage.BatchImageItem, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset := max(filter.Offset, 0)
	var result []*batchimage.BatchImageItem
	for _, item := range r.items[batchID] {
		if filter.Status != "" && item.Status != filter.Status {
			continue
		}
		if offset > 0 {
			offset--
			continue
		}
		result = append(result, &batchimage.BatchImageItem{
			JobID:                item.JobID,
			CustomID:             item.CustomID,
			Status:               item.Status,
			RequestHash:          item.RequestHash,
			PromptPreview:        item.PromptPreview,
			ProviderSourceObject: item.ProviderSourceObject,
			SourceLineNumber:     item.SourceLineNumber,
			SourceByteOffset:     item.SourceByteOffset,
			SourceByteLength:     item.SourceByteLength,
			MimeType:             item.MimeType,
			FileExtension:        item.FileExtension,
			ImageCount:           item.ImageCount,
			ErrorCode:            item.ErrorCode,
			ErrorMessage:         item.ErrorMessage,
			BilledAmount:         item.BilledAmount,
			IndexedAt:            item.IndexedAt,
		})
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

// ListBatchImageItemsForOwner checks fixture user and key ownership before
// delegating to item listing. A missing or mismatched job rejects access,
// while the returned item fields retain their existing optional pointer
// sharing.
func (r *fakeBatchImageRepository) ListBatchImageItemsForOwner(ctx context.Context, userID, apiKeyID int64, batchID string, filter batchimage.BatchImageItemFilter) ([]*batchimage.BatchImageItem, error) {
	if _, err := r.GetBatchImageJobByBatchIDForOwner(ctx, userID, apiKeyID, batchID); err != nil {
		return nil, err
	}
	return r.ListBatchImageItems(ctx, batchID, filter)
}

// GetBatchImageJobForDownload delegates to fixture user-and-key authorization
// and returns the shared job record. Missing or mismatched ownership uses the
// not-found result before Download checks completion or deletion state.
func (r *fakeBatchImageRepository) GetBatchImageJobForDownload(ctx context.Context, userID, apiKeyID int64, batchID string) (*batchimage.BatchImageJob, error) {
	return r.GetBatchImageJobByBatchIDForOwner(ctx, userID, apiKeyID, batchID)
}

// GetBatchImageItemForDownload finds one custom ID and returns a fresh item
// record with retained optional field pointers. Missing rows use the item
// not-found category before download validates state and image index.
func (r *fakeBatchImageRepository) GetBatchImageItemForDownload(_ context.Context, batchID, customID string) (*batchimage.BatchImageItem, error) {
	for _, item := range r.items[batchID] {
		if item.CustomID != customID {
			continue
		}
		return &batchimage.BatchImageItem{
			JobID:                item.JobID,
			CustomID:             item.CustomID,
			Status:               item.Status,
			RequestHash:          item.RequestHash,
			PromptPreview:        item.PromptPreview,
			ProviderSourceObject: item.ProviderSourceObject,
			SourceLineNumber:     item.SourceLineNumber,
			SourceByteOffset:     item.SourceByteOffset,
			SourceByteLength:     item.SourceByteLength,
			MimeType:             item.MimeType,
			FileExtension:        item.FileExtension,
			ImageCount:           item.ImageCount,
			ErrorCode:            item.ErrorCode,
			ErrorMessage:         item.ErrorMessage,
			BilledAmount:         item.BilledAmount,
			IndexedAt:            item.IndexedAt,
		}, nil
	}
	return nil, batchimage.ErrBatchImageItemNotFound
}

// ListBatchImageItemsForDownload delegates status and page-size normalization
// to fixture listing. It returns fresh outer item records whose optional
// pointers remain shared with fixture storage, so caller edits to pointed-to
// metadata affect later reads.
func (r *fakeBatchImageRepository) ListBatchImageItemsForDownload(ctx context.Context, batchID string, status string, limit int) ([]*batchimage.BatchImageItem, error) {
	return r.ListBatchImageItems(ctx, batchID, batchimage.BatchImageItemFilter{Status: status, Limit: limit})
}

// ListBatchImageJobsDueForInputCleanup selects terminal fixture jobs
// retaining input references before cutoff. Finish time takes precedence over
// settlement and activity, and the returned shared records are bounded by the
// requested or default limit.
func (r *fakeBatchImageRepository) ListBatchImageJobsDueForInputCleanup(_ context.Context, cutoff time.Time, limit int) ([]*batchimage.BatchImageJob, error) {
	if limit <= 0 {
		limit = 100
	}
	var jobs []*batchimage.BatchImageJob
	for _, job := range r.jobs {
		if job.InputDeletedAt != nil || batchimage.BatchImageDerefString(job.ProviderInputRef) == "" || !batchimage.IsTerminalBatchImageJobStatus(job.Status) {
			continue
		}
		at := job.FinishedAt
		if at == nil {
			at = job.SettledAt
		}
		if at == nil {
			at = &job.UpdatedAt
		}
		if at != nil && at.After(cutoff) {
			continue
		}
		jobs = append(jobs, job)
		if len(jobs) >= limit {
			break
		}
	}
	return jobs, nil
}

// ListBatchImageJobsDueForOutputCleanup selects completed fixture jobs with
// undeleted output references and elapsed explicit expiry. It returns shared
// records up to its positive limit or the one-hundred default for cleanup
// cases.
func (r *fakeBatchImageRepository) ListBatchImageJobsDueForOutputCleanup(_ context.Context, now time.Time, limit int) ([]*batchimage.BatchImageJob, error) {
	if limit <= 0 {
		limit = 100
	}
	var jobs []*batchimage.BatchImageJob
	for _, job := range r.jobs {
		if job.OutputDeletedAt != nil || batchimage.BatchImageDerefString(job.ProviderOutputRef) == "" || job.Status != batchimage.BatchImageJobStatusCompleted || job.OutputExpiresAt == nil || job.OutputExpiresAt.After(now) {
			continue
		}
		jobs = append(jobs, job)
		if len(jobs) >= limit {
			break
		}
	}
	return jobs, nil
}

// ListStaleUnsubmittedBatchImageJobs selects old created or uploading fixture
// jobs with positive holds and empty trimmed provider names. The scan follows
// map order and returns shared records for the later conditional failure
// check.
func (r *fakeBatchImageRepository) ListStaleUnsubmittedBatchImageJobs(_ context.Context, cutoff time.Time, limit int) ([]*batchimage.BatchImageJob, error) {
	if limit <= 0 {
		limit = 100
	}
	jobs := make([]*batchimage.BatchImageJob, 0, limit)
	for _, job := range r.jobs {
		if len(jobs) >= limit {
			break
		}
		if job.Status != batchimage.BatchImageJobStatusCreated && job.Status != batchimage.BatchImageJobStatusUploading {
			continue
		}
		if batchimage.BatchImageDerefString(job.ProviderJobName) != "" {
			continue
		}
		holdAmount := job.EstimatedCost
		if job.HoldAmount != nil {
			holdAmount = *job.HoldAmount
		}
		if holdAmount <= 0 || job.UpdatedAt.After(cutoff) {
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// MarkBatchImageInputDeleted saves the first supplied input deletion time and
// appends its fixture event. Missing jobs return not-found, while repeated
// marking retains the first timestamp on the shared record.
func (r *fakeBatchImageRepository) MarkBatchImageInputDeleted(_ context.Context, batchID string, deletedAt time.Time) error {
	job, ok := r.jobs[batchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	if job.InputDeletedAt == nil {
		job.InputDeletedAt = &deletedAt
	}
	r.events[batchID] = append(r.events[batchID], "input_cleanup_completed")
	return nil
}

// MarkBatchImageOutputDeleted saves the first output deletion time and
// converts completed fixture jobs to output_deleted. Failed and cancelled
// statuses remain, and every successful invocation records the cleanup event
// label.
func (r *fakeBatchImageRepository) MarkBatchImageOutputDeleted(_ context.Context, batchID string, deletedAt time.Time) error {
	job, ok := r.jobs[batchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	if job.OutputDeletedAt == nil {
		job.OutputDeletedAt = &deletedAt
	}
	if job.Status == batchimage.BatchImageJobStatusCompleted {
		job.Status = batchimage.BatchImageJobStatusOutputDeleted
	}
	r.events[batchID] = append(r.events[batchID], "output_cleanup_completed")
	return nil
}

// MarkBatchImageDownloaded saves the first download acknowledgement on a
// retained fixture record and appends its event. Later acknowledgements
// retain that timestamp, while an absent batch returns the domain not-found
// category.
func (r *fakeBatchImageRepository) MarkBatchImageDownloaded(_ context.Context, batchID string, downloadedAt time.Time) error {
	job, ok := r.jobs[batchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	if job.DownloadedAt == nil {
		job.DownloadedAt = &downloadedAt
	}
	r.events[batchID] = append(r.events[batchID], "download_completed")
	return nil
}

// MarkBatchImageJobUserDeleted authorizes user and key before the fixture's
// processor-done check, which accepts settling. It retains the first hiding
// timestamp and records its event; production PostgreSQL has a narrower
// terminal-state guard.
func (r *fakeBatchImageRepository) MarkBatchImageJobUserDeleted(_ context.Context, userID, apiKeyID int64, batchID string, deletedAt time.Time) error {
	job, ok := r.jobs[batchID]
	if !ok || job.UserID != userID || job.APIKeyID == nil || *job.APIKeyID != apiKeyID {
		return batchimage.ErrBatchImageJobNotFound
	}
	if !batchimage.IsBatchImageProcessorDoneStatus(job.Status) {
		return batchimage.ErrBatchImageRecordDeleteNotReady
	}
	if job.UserDeletedAt == nil {
		job.UserDeletedAt = &deletedAt
	}
	r.events[batchID] = append(r.events[batchID], "user_record_deleted")
	return nil
}

// SetBatchImageOutputExpiresAt fills a missing output expiry on the retained
// fixture record. A present expiry stays unchanged, and absent batches return
// the not-found category for internal retention cases.
func (r *fakeBatchImageRepository) SetBatchImageOutputExpiresAt(_ context.Context, batchID string, expiresAt time.Time) error {
	job, ok := r.jobs[batchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	if job.OutputExpiresAt == nil {
		job.OutputExpiresAt = &expiresAt
	}
	return nil
}

// RecordBatchImageCleanupFailure stores diagnostics and records a cleanup
// failure event on an existing fixture job. Missing records return the domain
// not-found category while cleanup continues its public error handling.
func (r *fakeBatchImageRepository) RecordBatchImageCleanupFailure(_ context.Context, batchID, code, message string) error {
	job, ok := r.jobs[batchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	job.LastErrorCode = batchimage.BatchImageStringPtr(code)
	job.LastErrorMessage = batchimage.BatchImageOptionalStringPtr(message)
	r.events[batchID] = append(r.events[batchID], "output_cleanup_failed")
	return nil
}

// AppendBatchImageEvent records the event label while ignoring the fixture
// payload value.
func (r *fakeBatchImageRepository) AppendBatchImageEvent(_ context.Context, batchID, eventType string, _ any) error {
	r.events[batchID] = append(r.events[batchID], eventType)
	return nil
}

// testBatchImageOwner returns the user, API key and group identities used by
// public fixtures.
func testBatchImageOwner() batchimage.BatchImageOwner {
	groupID := int64(7)
	return batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID}
}

// requireBatchImagePublicJSONHasNoInternals fails the calling test when known
// internal field names or resource markers appear in output text. It screens
// the shared fixture strings used by public JSON and archive assertions.
func requireBatchImagePublicJSONHasNoInternals(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{
		"provider_job_name",
		"provider_input_ref",
		"provider_output_ref",
		"gcs_input_uri",
		"gcs_output_uri",
		"provider_id",
		"service_account",
		"api_key",
		"download_url",
		"providers/",
		"files/",
		"gs://",
	} {
		require.NotContains(t, body, forbidden)
	}
}

// GetByID returns the retained provider pointer and ignores the requested ID
// and context. Callers can alter that shared record before later download or
// cleanup operations resolve their fixture binding.
func (r *resultProviderFixture) GetByID(context.Context, int64) (*providercore.Record, error) {
	return r.provider, nil
}

// newBatchDownloadFixture binds production result access to fixture storage,
// limiter and configured ZIP limits. Its caller supplies nonnil configuration
// and retained dependencies before invoking authorized item or archive
// downloads.
func newBatchDownloadFixture(repo batchimage.BatchImageRepository, registry *batchimage.Registry[batchimageprovider.BatchImageProvider], providers batchimageprovider.ResultProviders, limiter batchimage.BatchImageDownloadLimiter, cfg *config.Config) *batchimage.Download {
	return &batchimage.Download{Repo: repo, Limiter: limiter, ResolveProvider: (batchimageprovider.ResultAccess{Registry: registry, Providers: providers}).Download, Options: batchimage.DownloadOptions{MaxItems: cfg.BatchImage.MaxDownloadItemsZip, MaxBytes: cfg.BatchImage.MaxDownloadBytesPerRequest, Duration: time.Duration(cfg.BatchImage.MaxDownloadDurationSeconds) * time.Second}}
}

// newBatchCleanupFixture binds production provider cleanup to fixture storage
// and configured retention, scan interval and batch size. Callers supply
// nonnil configuration and own the service runtime when running periodic
// cleanup.
func newBatchCleanupFixture(repo batchimage.BatchImageRepository, registry *batchimage.Registry[batchimageprovider.BatchImageProvider], providers batchimageprovider.ResultProviders, cfg *config.Config) *batchimage.Cleanup {
	return &batchimage.Cleanup{Repo: repo, ResolveProvider: (batchimageprovider.ResultAccess{Registry: registry, Providers: providers}).Cleanup, Options: batchimage.CleanupOptions{InputRetention: time.Duration(cfg.BatchImage.InputRetentionAfterTerminalHours) * time.Hour, Interval: time.Duration(cfg.BatchImage.CleanupIntervalMinutes) * time.Minute, BatchSize: cfg.BatchImage.CleanupBatchSize}}
}

// cloneResultAllocations copies nonempty allocation slices through
// billing.CloneBillingAllocation, including optional source identity
// pointers. Nil and empty inputs return nil, while scalar base costs and rate
// multipliers remain as recorded.
func cloneResultAllocations(values []billing.BillingAllocation) []billing.BillingAllocation {
	if len(values) == 0 {
		return nil
	}
	out := make([]billing.BillingAllocation, len(values))
	for i, value := range values {
		out[i] = billing.CloneBillingAllocation(value, value.AmountUSD)
	}
	return out
}

// BatchImageUnitPrice records nonblank pricing models and returns an injected
// error, model-specific missing-price error or configured unit price. The
// fixture accepts zero prices, letting catalogue and submission tests inspect
// pricing admission.
func (r *fakeBatchImagePricingResolver) BatchImageUnitPrice(_ context.Context, input batchimage.BatchImagePriceInput) (float64, error) {
	if input.Model != "" {
		r.models = append(r.models, input.Model)
	}
	if r.err != nil {
		return 0, r.err
	}
	if input.Model != "" && r.missingModels[input.Model] {
		return 0, batchimage.ErrBatchImageSettlementPricingMissing
	}
	return r.unitPrice, nil
}

// Reserve records and normalizes the command through applyHold unless a
// reserve error was injected. Successful fixture results reserve the supplied
// hold as balance and derive newer estimated cost from its settlement ratio.
func (r *fakeBatchImageBillingRepo) Reserve(_ context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	if r.reserveErr != nil {
		r.reserves = append(r.reserves, cmd)
		return nil, r.reserveErr
	}
	result, err := r.applyHold(cmd, &r.reserves)
	if err == nil && result != nil {
		result.BalanceAmountUSD = cmd.HoldAmount
		result.HoldAmountUSD = cmd.HoldAmount
		result.EstimatedAmountUSD = cmd.HoldAmount
		if cmd.PricingSnapshotVersion >= 2 {
			result.EstimatedAmountUSD = cmd.HoldAmount * cmd.SettlementRateScale
		}
		if cmd.HoldAmount > 0 {
			result.BillingAllocations = []billing.BillingAllocation{{Type: billing.BillingAllocationTypeBalance, AmountUSD: cmd.HoldAmount}}
		}
	}
	return result, err
}

// Capture records the command through applyHold and computes source amounts
// through billing.PlanTaskCapture. An injected capture error records the command
// before returning. Planning also runs on an Applied-false replay, so the
// fixture reconstructs costs from the supplied snapshot each time.
//
// The claim is stored before planning, and a planning error keeps that fixture
// identity recorded. Successful planning replaces the command's ActualAmount
// and returns its allocation details. Tests can inspect replay keys and planned
// amounts here; this fixture does not model a funding transaction or an account
// balance mutation.
func (r *fakeBatchImageBillingRepo) Capture(_ context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	if r.captureErr != nil {
		r.captures = append(r.captures, cmd)
		return nil, r.captureErr
	}
	result, err := r.applyHold(cmd, &r.captures)
	if err != nil || result == nil {
		return result, err
	}
	plan, err := billing.PlanTaskCapture(cmd)
	if err != nil {
		return nil, err
	}
	cmd.ActualAmount = plan.ActualAmountUSD
	result.SubscriptionAmountUSD = plan.SubscriptionAmountUSD
	result.BalanceAmountUSD = plan.BalanceAmountUSD
	result.ActualAmountUSD = plan.ActualAmountUSD
	result.BillingAllocations = plan.BillingAllocations
	return result, nil
}

// Release returns an injected refund error or delegates recording and
// request-ID replay to applyHold. Recorded command pointers remain shared
// with the fixture, allowing terminal recovery cases to compare release
// identities and fingerprints.
func (r *fakeBatchImageBillingRepo) Release(_ context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	if r.releaseErr != nil {
		r.releases = append(r.releases, cmd)
		return nil, r.releaseErr
	}
	return r.applyHold(cmd, &r.releases)
}

// applyHold records command pointers and claims request IDs for fixture replay.
// An injected error returns before claiming an ID; nil commands are still
// recorded and receive an Applied-true result. Present commands are normalized
// in place before their identity is inspected.
//
// The seen and alreadyApplied sets make repeated IDs return Applied false.
// Fingerprint comparison and saved financial results are not modeled: a new ID
// is retained before the caller continues planning or completion persistence.
// Each test owns these mutable maps and command pointers.
func (r *fakeBatchImageBillingRepo) applyHold(cmd *billing.TaskFundsCommand, calls *[]*billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	if r.seen == nil {
		r.seen = make(map[string]struct{})
	}
	if r.err != nil {
		*calls = append(*calls, cmd)
		return nil, r.err
	}
	if cmd != nil {
		cmd.Normalize()
		if _, ok := r.seen[cmd.RequestID]; ok || r.alreadyApplied[cmd.RequestID] {
			*calls = append(*calls, cmd)
			return &billing.TaskFundsResult{Applied: false}, nil
		}
		// Keep the accepted action ID before returning the fixture result so later
		// visits can simulate funding replay even when completion persistence
		// fails.
		r.seen[cmd.RequestID] = struct{}{}
	}
	*calls = append(*calls, cmd)
	return &billing.TaskFundsResult{Applied: true}, nil
}

// ResolveUsableSubscriptionForGroup returns the retained subscription pointer
// and configured eligibility error, ignoring user and group arguments. Tests
// can mutate that subscription before later pricing calls observe the same
// fixture source.
func (r *fakeBatchImageBillingRepo) ResolveUsableSubscriptionForGroup(context.Context, int64, int64) (*billing.UserSubscription, error) {
	return r.usableSubscription, r.subscriptionErr
}

// newFakeBatchImageQueue configures one reservation and a successful fixture
// lease acquisition.
func newFakeBatchImageQueue(batchID string) *fakeBatchImageQueue {
	return &fakeBatchImageQueue{
		reserved:     batchimage.ReservedBatchImageJob{BatchID: batchID},
		lockAcquired: true,
	}
}

// Enqueue accepts publication requests for the single-visit worker queue
// fixture.
func (q *fakeBatchImageQueue) Enqueue(context.Context, string) error {
	return nil
}

// Reserve returns the configured reservation on every call without consuming
// fixture state. Single-visit worker tests use that retained identity, while
// runtime tests use a separate queue whose reserve waits for cancellation.
func (q *fakeBatchImageQueue) Reserve(context.Context, time.Duration) (batchimage.ReservedBatchImageJob, error) {
	return q.reserved, nil
}

// RequeueAfter records the batch identity and requested delay for worker
// scheduling assertions.
func (q *fakeBatchImageQueue) RequeueAfter(_ context.Context, batchID string, delay time.Duration) error {
	q.requeued = append(q.requeued, fakeBatchImageRequeue{batchID: batchID, delay: delay})
	return nil
}

// Ack records the batch identity acknowledged by the worker queue fixture.
func (q *fakeBatchImageQueue) Ack(_ context.Context, batchID string) error {
	q.acked = append(q.acked, batchID)
	return nil
}

// Heartbeat accepts activity updates from the single-visit worker queue
// fixture.
func (q *fakeBatchImageQueue) Heartbeat(context.Context, string) error {
	return nil
}

// MoveDueDelayedToReady reports no delayed jobs in the worker queue fixture.
func (q *fakeBatchImageQueue) MoveDueDelayedToReady(context.Context, int) (int, error) {
	return 0, nil
}

// RecoverStaleActive reports no abandoned jobs in the worker queue fixture.
func (q *fakeBatchImageQueue) RecoverStaleActive(context.Context, time.Duration, int) (int, error) {
	return 0, nil
}

// TryAcquireJobLock returns contention when lockAcquired is false, otherwise
// a handle linked to this fixture queue. Releasing that handle invokes the
// shared release counter, while queue outcomes use the configured reservation
// identity.
func (q *fakeBatchImageQueue) TryAcquireJobLock(context.Context, string, time.Duration) (batchimage.BatchImageJobLock, bool, error) {
	if !q.lockAcquired {
		return nil, false, nil
	}
	return fakeBatchImageLock{release: func() { q.releaseCount++ }, queue: q}, true, nil
}

// Release invokes the optional fixture callback that records lease cleanup.
func (l fakeBatchImageLock) Release(context.Context) error {
	if l.release != nil {
		l.release()
	}
	return nil
}

// Process records the batch identity and returns its configured scheduling
// result or error.
func (p *fakeBatchImageProcessor) Process(_ context.Context, batchID string) (batchimage.BatchImageProcessResult, error) {
	p.processed = append(p.processed, batchID)
	return p.result, p.err
}

// Heartbeat forwards activity to the fixture queue for its configured
// reserved batch.
func (l fakeBatchImageLock) Heartbeat(ctx context.Context) error {
	return l.queue.Heartbeat(ctx, l.queue.reserved.BatchID)
}

// Ack forwards acknowledgement to the fixture queue for its configured
// reserved batch.
func (l fakeBatchImageLock) Ack(ctx context.Context) error {
	return l.queue.Ack(ctx, l.queue.reserved.BatchID)
}

// RequeueAfter forwards the delay to the fixture queue for its configured
// reservation.
func (l fakeBatchImageLock) RequeueAfter(ctx context.Context, d time.Duration) error {
	return l.queue.RequeueAfter(ctx, l.queue.reserved.BatchID, d)
}
