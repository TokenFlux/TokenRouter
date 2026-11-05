package integration

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
	creativeredis "github.com/TokenFlux/TokenRouter/internal/creative/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	catalogtestkit "github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// Generated output is delivered through production Redis queue and transient
// adapters, while run metadata and funding observations live in memory. One
// sequential scenario creates a task, runs one worker attempt and reads its
// fixed output before acknowledging it. Results has no outbox in this assembly,
// so provider success is followed by inline settlement before RunOnce returns.
//
// The run repository exposes shared pointers and replaces output metadata
// before recording provider success. Those updates model the successful service
// sequence without SQL rollback or persistent recovery. Acknowledgment removes
// temporary bytes and a later read reports expiry; the test does not advance
// the configured TTL or validate the executor's opaque PNG-labelled output as
// an image.

// smokeFakeRunRepo retains mutable run and output objects for the sequential
// smoke scenario.
type smokeFakeRunRepo struct {
	// runs and outputs retain the same mutable objects returned to callers.
	// RunOnce is completed before assertions or queries, and these maps are
	// used sequentially without fixture synchronization.
	runs map[string]*creative.CreativeRun // shared run metadata
	// shared output metadata
	outputs map[string][]*creative.CreativeRunOutput
}

// smokeFakeBillingRepo reports applied funds operations and counts observed
// capture and release calls.
type smokeFakeBillingRepo struct {
	// captureN and releaseN count every corresponding funds call, including
	// repeats. The smoke assertion reads captureN after RunOnce; these
	// observations do not implement billing idempotency.
	captureN int // observed funds capture calls
	releaseN int // observed funds release calls
}

// smokeFakeExecutor supplies a fixed Gemini execution target and opaque output
// bytes.
type smokeFakeExecutor struct{}

// smokeFakeManagedKeyRepo retains one execution key for this user and group
// fixture.
type smokeFakeManagedKeyRepo struct {
	// key retains the caller's object after CreateManagedKey assigns its
	// ID; every later lookup returns that pointer regardless of the
	// supplied scope.
	key *apikey.APIKey // retained managed execution key
}

// smokeFakeUserRepo supplies a minimal identity account for creative access
// checks.
type smokeFakeUserRepo struct{}

// smokeFakeGroupRepo supplies the active image-enabled group used by creative
// request validation.
type smokeFakeGroupRepo struct{}

// smokeFakeProviderRepo supplies one Gemini catalog provider with the fixture
// model mapping.
type smokeFakeProviderRepo struct{}

// smokeFakeRateRepo supplies absent user-group rate overrides for creative
// pricing.
type smokeFakeRateRepo struct{}

// smokeCreativeSettingReader enables the fixture model and its generate and
// edit operations.
type smokeCreativeSettingReader struct{}

// newSmokeFakeRunRepo allocates empty run and output maps for one smoke
// scenario.
func newSmokeFakeRunRepo() *smokeFakeRunRepo {
	return &smokeFakeRunRepo{
		runs:    make(map[string]*creative.CreativeRun),
		outputs: make(map[string][]*creative.CreativeRunOutput),
	}
}

// CreateCreativeRun stores queued run metadata and one pending output per
// requested index, returning the stored run pointer. Callers initialize both
// maps and supply a nonnegative output count; duplicate run IDs replace prior
// entries. Workspace and hold values are copied, while later repository reads
// share the stored objects during sequential fixture use.
func (r *smokeFakeRunRepo) CreateCreativeRun(ctx context.Context, params creative.CreateCreativeRunParams) (*creative.CreativeRun, error) {
	workspaceID := params.WorkspaceID
	run := &creative.CreativeRun{
		RunID:                params.RunID,
		UserID:               params.UserID,
		WorkspaceID:          &workspaceID,
		GroupID:              params.GroupID,
		APIKeyID:             params.APIKeyID,
		Model:                params.Model,
		Platform:             params.Platform,
		Operation:            params.Operation,
		RequestedOutputCount: params.RequestedOutputCount,
		ImageSize:            params.ImageSize,
		Status:               creative.CreativeRunStatusQueued,
		EstimatedCost:        params.EstimatedCost,
		HoldAmount:           &params.HoldAmount,
		BaseUnitPrice:        params.BaseUnitPrice,
	}
	r.runs[run.RunID] = run
	outputs := make([]*creative.CreativeRunOutput, 0, params.RequestedOutputCount)
	for index := range params.RequestedOutputCount {
		outputs = append(outputs, &creative.CreativeRunOutput{RunID: run.RunID, OutputIndex: index, Status: creative.CreativeRunOutputStatusPending})
	}
	r.outputs[run.RunID] = outputs
	return run, nil
}

// GetCreativeRunByRunID returns the shared stored run object or
// ErrCreativeRunNotFound for an absent ID. Callers in this sequential fixture
// can observe mutations through previously returned pointers.
func (r *smokeFakeRunRepo) GetCreativeRunByRunID(ctx context.Context, runID string) (*creative.CreativeRun, error) {
	run, ok := r.runs[runID]
	if !ok {
		return nil, creative.ErrCreativeRunNotFound
	}
	return run, nil
}

// GetCreativeRunByRunIDForOwner returns shared metadata only when user and
// workspace match the supplied scope. An absent workspace or either ownership
// mismatch returns ErrCreativeRunNotFound like a missing run.
func (r *smokeFakeRunRepo) GetCreativeRunByRunIDForOwner(ctx context.Context, scope creative.CreativeRunScope, runID string) (*creative.CreativeRun, error) {
	run, err := r.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.UserID != scope.UserID || run.WorkspaceID == nil || *run.WorkspaceID != scope.WorkspaceID {
		return nil, creative.ErrCreativeRunNotFound
	}
	return run, nil
}

// GetCreativeRunByIdempotencyKey always reports ErrCreativeRunNotFound for the
// smoke fixture. One task is created in this scenario, so repeated
// idempotency-key lookup is not modeled by this repository.
func (r *smokeFakeRunRepo) GetCreativeRunByIdempotencyKey(ctx context.Context, scope creative.CreativeRunScope, key string) (*creative.CreativeRun, error) {
	return nil, creative.ErrCreativeRunNotFound
}

// ListCreativeRunsForOwner returns an empty successful listing for this smoke
// fixture.
func (r *smokeFakeRunRepo) ListCreativeRunsForOwner(ctx context.Context, scope creative.CreativeRunScope, filter creative.CreativeRunFilter) ([]*creative.CreativeRun, error) {
	return nil, nil
}

// TransitionCreativeRunStatus checks the domain transition table before
// mutating a stored run. It reports missing or invalid transitions and retains
// a nonempty release target from the supplied options.
func (r *smokeFakeRunRepo) TransitionCreativeRunStatus(ctx context.Context, runID, toStatus string, opts creative.CreativeRunTransitionOptions) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if !creative.CanTransitionCreativeRun(run.Status, toStatus) {
		return creative.ErrCreativeInvalidTransition
	}
	run.Status = toStatus
	if opts.ReleaseTargetStatus != "" {
		run.ReleaseTargetStatus = opts.ReleaseTargetStatus
	}
	return nil
}

// MarkCreativeRunRunning validates entry into running and stores its initial
// time and positive provider ID. Already-running calls succeed immediately,
// retaining the previously observed provider and start time.
func (r *smokeFakeRunRepo) MarkCreativeRunRunning(ctx context.Context, runID string, providerID int64, now time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if run.Status == creative.CreativeRunStatusRunning {
		return nil
	}
	if !creative.CanTransitionCreativeRun(run.Status, creative.CreativeRunStatusRunning) {
		return creative.ErrCreativeInvalidTransition
	}
	run.Status = creative.CreativeRunStatusRunning
	if providerID > 0 {
		run.ProviderID = &providerID
	}
	run.StartedAt = &now
	return nil
}

// SetCreativeRunExecution updates provider identity and platform together when
// providerID is positive. Nonpositive IDs leave both values unchanged, and
// missing runs report ErrCreativeRunNotFound to the worker.
func (r *smokeFakeRunRepo) SetCreativeRunExecution(ctx context.Context, runID string, providerID int64, provider string, now time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if providerID > 0 {
		run.ProviderID = &providerID
		run.Platform = provider
	}
	return nil
}

// MarkCreativeRunSucceeded records actual cost and completion time for running
// or settlement states. Other states return ErrCreativeInvalidTransition,
// allowing the smoke sequence to detect an unexpected completion order.
func (r *smokeFakeRunRepo) MarkCreativeRunSucceeded(ctx context.Context, runID string, actualCost float64, now time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if run.Status != creative.CreativeRunStatusRunning && run.Status != creative.CreativeRunStatusProviderSucceeded && run.Status != creative.CreativeRunStatusSettlementPending {
		return creative.ErrCreativeInvalidTransition
	}
	run.Status = creative.CreativeRunStatusSucceeded
	run.ActualCost = &actualCost
	run.CompletedAt = &now
	return nil
}

// UpdateCreativeRunOutput replaces metadata for a matching output while
// retaining the supplied expiry pointer. Acknowledged outputs succeed
// unchanged; missing indexes return ErrCreativeOutputNotFound, so later
// delivery cannot overwrite client receipt.
func (r *smokeFakeRunRepo) UpdateCreativeRunOutput(ctx context.Context, runID string, outputIndex int, status, mimeType string, byteSize int64, transientExpiresAt *time.Time, errorCode, errorMessage string) error {
	for _, output := range r.outputs[runID] {
		if output.OutputIndex == outputIndex {
			if output.Status == creative.CreativeRunOutputStatusAcked {
				return nil
			}
			output.Status = status
			output.MimeType = &mimeType
			output.ByteSize = &byteSize
			output.TransientExpiresAt = transientExpiresAt
			output.ErrorCode = &errorCode
			output.ErrorMessage = &errorMessage
			return nil
		}
	}
	return creative.ErrCreativeOutputNotFound
}

// GetCreativeRunOutput returns the shared output object at the requested index
// or ErrCreativeOutputNotFound. The returned metadata remains mutable through
// repository updates used later in this scenario.
func (r *smokeFakeRunRepo) GetCreativeRunOutput(ctx context.Context, runID string, outputIndex int) (*creative.CreativeRunOutput, error) {
	for _, output := range r.outputs[runID] {
		if output.OutputIndex == outputIndex {
			return output, nil
		}
	}
	return nil, creative.ErrCreativeOutputNotFound
}

// ListCreativeRunOutputs returns the stored slice and its shared output
// pointers for settlement. Missing run IDs yield a nil successful slice, and
// callers keep the fixture's sequential access order.
func (r *smokeFakeRunRepo) ListCreativeRunOutputs(ctx context.Context, runID string) ([]*creative.CreativeRunOutput, error) {
	return r.outputs[runID], nil
}

// MarkCreativeRunOutputAcked records receipt only for a succeeded output,
// retaining the supplied acknowledgment time. Repeated acknowledgment succeeds
// unchanged; missing or unready output metadata keeps its domain error.
func (r *smokeFakeRunRepo) MarkCreativeRunOutputAcked(ctx context.Context, runID string, outputIndex int, now time.Time) error {
	output, err := r.GetCreativeRunOutput(ctx, runID, outputIndex)
	if err != nil {
		return err
	}
	if output.Status == creative.CreativeRunOutputStatusAcked {
		return nil
	}
	if output.Status != creative.CreativeRunOutputStatusSucceeded {
		return creative.ErrCreativeOutputNotReady
	}
	output.Status = creative.CreativeRunOutputStatusAcked
	output.AckedAt = &now
	return nil
}

// ListCreativeRunsDueForTransientCleanup returns no cleanup candidates in the
// single-run smoke fixture.
func (r *smokeFakeRunRepo) ListCreativeRunsDueForTransientCleanup(ctx context.Context, cutoff time.Time, limit int) ([]*creative.CreativeRun, error) {
	return nil, nil
}

// IncrementCreativeRunAttempt increases and returns the execution attempt count
// for a stored run. An absent run reports ErrCreativeRunNotFound; this memory
// update participates only in the sequential smoke attempt.
func (r *smokeFakeRunRepo) IncrementCreativeRunAttempt(ctx context.Context, runID string) (int, error) {
	run, ok := r.runs[runID]
	if !ok {
		return 0, creative.ErrCreativeRunNotFound
	}
	run.AttemptCount++
	return run.AttemptCount, nil
}

// IncrementCreativeRunSettlementAttempt increases and returns the stored
// settlement attempt count for a run. Missing metadata reports
// ErrCreativeRunNotFound, while the successful fixture retains that observation
// for later service reads.
func (r *smokeFakeRunRepo) IncrementCreativeRunSettlementAttempt(ctx context.Context, runID string) (int, error) {
	run, ok := r.runs[runID]
	if !ok {
		return 0, creative.ErrCreativeRunNotFound
	}
	run.SettlementAttemptCount++
	return run.SettlementAttemptCount, nil
}

// IncrementCreativeRunReleaseAttempt increases and returns the release attempt
// count on a stored run. Missing run metadata returns ErrCreativeRunNotFound;
// the fixture records counts without a separate persistent recovery action.
func (r *smokeFakeRunRepo) IncrementCreativeRunReleaseAttempt(ctx context.Context, runID string) (int, error) {
	run, ok := r.runs[runID]
	if !ok {
		return 0, creative.ErrCreativeRunNotFound
	}
	run.ReleaseAttemptCount++
	return run.ReleaseAttemptCount, nil
}

// SetCreativeRunProvisioningPhase replaces the stored phase text for a present
// run. Missing metadata returns ErrCreativeRunNotFound, and the supplied phase
// is accepted directly by this in-memory fixture.
func (r *smokeFakeRunRepo) SetCreativeRunProvisioningPhase(ctx context.Context, runID, phase string) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	run.ProvisioningPhase = phase
	return nil
}

// MarkCreativeRunProviderSucceeded stores provider receipt time and a positive
// provider ID. Only a currently running task advances to provider_succeeded;
// other states keep their status while receipt metadata is updated.
func (r *smokeFakeRunRepo) MarkCreativeRunProviderSucceeded(ctx context.Context, runID string, providerID int64, now time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if providerID > 0 {
		run.ProviderID = &providerID
	}
	run.ProviderResultRecordedAt = &now
	if run.Status == creative.CreativeRunStatusRunning {
		run.Status = creative.CreativeRunStatusProviderSucceeded
	}
	return nil
}

// SetCreativeRunReconcileError stores reconciliation diagnostics for an
// existing run. Empty message and zero next time clear their respective
// pointers, letting the fixture represent cleared error or scheduling metadata.
func (r *smokeFakeRunRepo) SetCreativeRunReconcileError(ctx context.Context, runID, message string, next time.Time) error {
	run, ok := r.runs[runID]
	if !ok {
		return creative.ErrCreativeRunNotFound
	}
	if message == "" {
		run.LastReconcileError = nil
	} else {
		run.LastReconcileError = &message
	}
	if next.IsZero() {
		run.NextReconcileAt = nil
	} else {
		run.NextReconcileAt = &next
	}
	return nil
}

// Reserve reports a nonnil command's hold as applied balance funding and
// returns matching hold and estimate amounts. The fixture returns observations
// directly, so the smoke test does not maintain a wallet ledger.
func (r *smokeFakeBillingRepo) Reserve(ctx context.Context, cmd *billingcore.TaskFundsCommand) (*billingcore.TaskFundsResult, error) {
	return &billingcore.TaskFundsResult{Applied: true, HoldAmountUSD: cmd.HoldAmount, EstimatedAmountUSD: cmd.HoldAmount, BalanceAmountUSD: cmd.HoldAmount}, nil
}

// Capture counts each invocation and reports a nonnil command's requested
// base amount as applied actual funding. Repeated calls increment again
// because this fixture does not store billing idempotency records.
func (r *smokeFakeBillingRepo) Capture(ctx context.Context, cmd *billingcore.TaskFundsCommand) (*billingcore.TaskFundsResult, error) {
	r.captureN++
	return &billingcore.TaskFundsResult{Applied: true, ActualAmountUSD: cmd.ActualBaseAmountUSD}, nil
}

// Release counts each invocation and reports a successful applied release. The
// counter records observed calls, while balance restoration and billing
// deduplication belong to the funds implementation beyond this fixture.
func (r *smokeFakeBillingRepo) Release(ctx context.Context, cmd *billingcore.TaskFundsCommand) (*billingcore.TaskFundsResult, error) {
	r.releaseN++
	return &billingcore.TaskFundsResult{Applied: true}, nil
}

// Prepare supplies provider 55, Gemini platform and the run's model for the
// fixed executor. Its release callback is empty because this fixture acquires
// no scheduler capacity or provider resource.
func (e *smokeFakeExecutor) Prepare(ctx context.Context, run creative.CreativeRun) (*creative.CreativeExecution, error) {
	return &creative.CreativeExecution{
		ProviderID:    55,
		Platform:      creative.PlatformGemini,
		Target:        e,
		UpstreamModel: run.Model,
		ReleaseFunc:   func() {},
	}, nil
}

// Execute returns one nonempty output labelled image/png with provider 55. The
// bytes are an opaque transport fixture; the smoke assertion compares their
// content and MIME instead of decoding a generated image.
func (e *smokeFakeExecutor) Execute(ctx context.Context, run creative.CreativeRun, payload creative.CreativeRunPayload) (*creative.CreativeExecuteResult, error) {
	return &creative.CreativeExecuteResult{
		Outputs:    []creative.CreativeOutput{{Index: 0, Bytes: []byte("smoke-image-bytes"), Mime: "image/png"}},
		ProviderID: 55,
	}, nil
}

// IsRetryable reports every executor error as permanent for this smoke fixture.
func (e *smokeFakeExecutor) IsRetryable(err error) bool { return false }

// GetManagedKeyByUserAndGroup returns the retained key or ErrAPIKeyNotFound
// before its creation. User, group and manager filters are ignored because this
// fixture stores one execution key for the scenario.
func (r *smokeFakeManagedKeyRepo) GetManagedKeyByUserAndGroup(ctx context.Context, userID, groupID int64, managedBy string) (*apikey.APIKey, error) {
	if r.key != nil {
		return r.key, nil
	}
	return nil, apikey.ErrAPIKeyNotFound
}

// CreateManagedKey assigns fixture ID 900 to the supplied nonnil key and
// retains that same pointer. Later reads observe the caller's object rather
// than an independently loaded database record.
func (r *smokeFakeManagedKeyRepo) CreateManagedKey(ctx context.Context, key *apikey.APIKey) error {
	key.ID = 900
	r.key = key
	return nil
}

// GetByID returns a new minimal user with the requested identifier for access
// checks. It accepts every ID and supplies no stored balance or account-status
// lookup in this fixture.
func (r *smokeFakeUserRepo) GetByID(ctx context.Context, id int64) (creative.UserAccess, error) {
	return &identity.User{ID: id}, nil
}

// GetByIDLite returns an active image-enabled group using the requested
// identifier and unit rate. Generate and edit are allowed for Gemini, while the
// fixture accepts every group lookup.
func (r *smokeFakeGroupRepo) GetByIDLite(ctx context.Context, id int64) (*creative.GroupView, error) {
	return &creative.GroupView{ID: id, Name: "Smoke Group", Active: true, AllowImageGeneration: true, RateMultiplier: 1, Operations: map[string][]string{creative.PlatformGemini: {creative.CreativeOperationGenerate, creative.CreativeOperationEdit}}}, nil
}

// ListActive returns no catalog groups for the direct-request smoke fixture.
func (r *smokeFakeGroupRepo) ListActive(context.Context) ([]creative.GroupView, error) {
	return nil, nil
}

// ListSchedulableByGroupIDAndPlatform returns one active Gemini provider whose
// model maps to itself. The supplied filters are ignored, keeping this fixture
// focused on the scenario's single configured route.
func (r *smokeFakeProviderRepo) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]creative.CatalogProvider, error) {
	return []creative.CatalogProvider{creativeprovider.CatalogProvider(&provider.Record{ID: 55, Platform: "gemini", Type: "apikey", Status: billingcore.StatusActive, Schedulable: true, Credentials: map[string]any{"model_mapping": map[string]any{"gemini-3.1-flash-image": "gemini-3.1-flash-image"}}})}, nil
}

// GetByUserAndGroup returns no user-group rate override and no error for any
// identifiers. Creative pricing then observes an absent override alongside the
// fixture group's configured rate.
func (r *smokeFakeRateRepo) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error) {
	return nil, nil
}

// IsCreativeEnabled reports creative access enabled for every request in the
// fixture.
func (smokeCreativeSettingReader) IsCreativeEnabled(context.Context) bool { return true }

// GetCreativeModelSettings returns a fresh list enabling the fixture model in
// group 12. Both generate and edit are advertised, while this smoke scenario
// submits one generate request.
func (smokeCreativeSettingReader) GetCreativeModelSettings(context.Context) []creative.CreativeModelSetting {
	return []creative.CreativeModelSetting{{
		GroupID:    12,
		Model:      "gemini-3.1-flash-image",
		Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit},
	}}
}

// TestCreativeFullChainSmoke creates one queued run and processes it through
// production Redis adapters on miniredis. It requires one capture, retrievable
// bytes and MIME, acknowledgment deletion, and ErrCreativeOutputExpired on a
// later read.
func TestCreativeFullChainSmoke(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	// Release the Redis client on every test exit, including assertion
	// failures. Normal worker processing joins its heartbeat before the
	// later stored-state checks; this cleanup discards close errors.
	t.Cleanup(func() { _ = client.Close() })

	cfg := &config.Config{
		Creative: config.CreativeConfig{
			Enabled:               true,
			QueueWorkerEnabled:    true,
			TransientTTLSeconds:   1800,
			MaxAssetBytes:         33554432,
			MaxTotalInputBytes:    67108864,
			MaxPromptChars:        8000,
			DefaultImageSize:      "1K",
			ExecuteTimeoutSeconds: 300,
			MaxExecuteAttempts:    3,
			Queue: config.TaskQueueConfig{
				JobLockTTLSeconds:       300,
				StaleActiveAfterSeconds: 600,
				DelayedMoveLimit:        100,
				RecoverLimit:            100,
			},
		},
		APIKey: config.APIKeyConfig{Prefix: "sk-"},
	}

	repo := newSmokeFakeRunRepo()
	billing := &smokeFakeBillingRepo{}
	queue := creativeredis.NewCreativeQueue(client, &creativeredis.QueueOptions{
		InflightKeyPrefix:  cfg.Creative.Queue.InflightKeyPrefix,
		InflightTTLSeconds: cfg.Creative.Queue.InflightTTLSeconds,
		JobLockTTLSeconds:  cfg.Creative.Queue.JobLockTTLSeconds,
		LockKeyPrefix:      cfg.Creative.Queue.LockKeyPrefix,
		QueueActiveKey:     cfg.Creative.Queue.ActiveKey,
		QueueDelayedKey:    cfg.Creative.Queue.DelayedKey,
		QueueReadyKey:      cfg.Creative.Queue.ReadyKey,
	})
	store := creativeredis.NewCreativeTransientStore(client, &creativeredis.TransientOptions{TransientTTLSeconds: cfg.Creative.TransientTTLSeconds})
	// Omit a persistent outbox so this successful attempt settles inline.
	// Convert configured seconds for result metadata; its duration and the
	// Redis store receive the same retention setting.
	results := &creative.Results{Repo: repo, TransientStore: store, Queue: queue, Funding: creative.Funding{Store: billing}, TransientTTL: time.Duration(cfg.Creative.TransientTTLSeconds) * time.Second}
	managed := apikey.ManagedKeys{Store: &smokeFakeManagedKeyRepo{}, Prefix: cfg.APIKey.Prefix, ManagedBy: creative.CreativeManagedBy, NamePrefix: "creative-studio"}
	// Use a fixed image price to exercise nonzero funding capture.
	price := 0.02
	prices := billingtestkit.SharedPriceResolver(billingtestkit.Calculator(nil, nil), 12, pricing.DefaultBillingSettings(), []routing.ModelPricingEntry{{Models: []string{"*"}, BillingMode: routing.BillingModeImage, PerRequestPrice: &price}})
	svc := &creative.Public{
		Attributes: &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: catalogtestkit.OfflineView}},
		Repo:       repo, UserRepo: &smokeFakeUserRepo{}, ProviderRepo: &smokeFakeProviderRepo{}, GroupRepo: &smokeFakeGroupRepo{}, UserGroupRateRepo: &smokeFakeRateRepo{}, Queue: queue, TransientStore: store, Results: results, Settings: smokeCreativeSettingReader{}, UserNotFound: identity.ErrUserNotFound,
		Options: creative.PublicOptions{Enabled: cfg.Creative.Enabled, MaxAssetBytes: cfg.Creative.MaxAssetBytes, MaxTotalInputBytes: cfg.Creative.MaxTotalInputBytes, MaxPromptChars: cfg.Creative.MaxPromptChars, DefaultImageSize: cfg.Creative.DefaultImageSize},
		EnsureKey: func(ctx context.Context, u, g int64) (int64, error) {
			key, err := managed.Ensure(ctx, u, g)
			if err != nil {
				return 0, err
			}
			return key.ID, nil
		},
		ImageUnitPrice: func(ctx context.Context, g *creative.GroupView, m, size string) (float64, bool) {
			price, err := prices.ResolveImageUnitPrice(ctx, billingcore.PricingInput{Model: m, GroupID: &g.ID}, size)
			// A resolver error becomes an unavailable quote through
			// the boolean result, so public creation rejects
			// missing pricing even when an amount was returned.
			return price, err == nil
		},
		SubscriptionMultiplier: func(context.Context, int64, *creative.GroupView, float64) (float64, bool) { return 0, false },
	}
	worker := creative.NewCreativeRunWorker(queue, repo, store, &smokeFakeExecutor{}, results, creative.CreativeWorkerOptions{ReserveBlockTimeout: time.Second, JobLockTTL: time.Minute, LockConflictDelay: time.Millisecond, ErrorRetryDelay: time.Millisecond, ErrorBackoff: time.Millisecond, StaleActiveAfter: time.Minute, MaxAttempts: 3}, creative.WorkerPorts{})
	ctx := context.Background()

	// A valid PNG input reaches the public validation and provisioning
	// sequence, while the idempotency lookup stub always reports missing
	// for this first submitted task.
	png := smokeTestPNG(t)
	scope := creative.CreativeRunScope{UserID: 7, WorkspaceID: "11111111-1111-4111-8111-111111111111"}
	created, err := svc.CreateRun(ctx, scope, creative.CreateCreativeRunParamsPublic{
		GroupID:      12,
		Model:        "gemini-3.1-flash-image",
		Operation:    creative.CreativeOperationGenerate,
		Prompt:       "smoke prompt",
		SourceImages: []creative.CreativeInputImage{{Bytes: png, Mime: "image/png"}},
		ImageSize:    "1K",
	}, "smoke-idem-key")
	require.NoError(t, err)
	require.Equal(t, creative.CreativeRunStatusQueued, created.Status)
	runID := created.ID
	require.Equal(t, creative.PlatformGemini, repo.runs[runID].Platform)

	// RunOnce reserves the queued task and joins its heartbeat before
	// returning; the following shared-memory assertions therefore observe
	// the completed successful attempt and its inline capture.
	require.NoError(t, worker.RunOnce(ctx))
	require.Equal(t, creative.CreativeRunStatusSucceeded, repo.runs[runID].Status)
	require.Equal(t, 1, billing.captureN)
	require.Equal(t, creative.CreativeRunOutputStatusSucceeded, repo.outputs[runID][0].Status)

	// Retrieve the generated output bytes.
	content, err := svc.GetOutputContent(ctx, scope, runID, 0)
	require.NoError(t, err)
	require.Equal(t, []byte("smoke-image-bytes"), content.Content)
	require.Equal(t, "image/png", content.ContentType)

	// Check acknowledgment metadata before reading Redis directly so the
	// scenario observes both recorded client receipt and removal of the
	// temporary output bytes.
	require.NoError(t, svc.AckOutput(ctx, scope, runID, 0))
	require.Equal(t, creative.CreativeRunOutputStatusAcked, repo.outputs[runID][0].Status)
	_, err = store.LoadOutput(ctx, runID, 0)
	require.Error(t, err, "ack 后临时输出必须已删除")

	// The reread reports expiry because the output is acknowledged,
	// establishing receipt behavior without advancing Redis time or waiting
	// for the configured transient TTL.
	_, err = svc.GetOutputContent(ctx, scope, runID, 0)
	require.ErrorIs(t, err, creative.ErrCreativeOutputExpired)
}

// smokeTestPNG encodes a two-by-two PNG used as the smoke request's source
// image. An encoding failure terminates the fixture through require.NoError
// before public creation is called; the generated bytes satisfy the input
// format check.
func smokeTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// RecordProviderOutcome replaces output snapshots before recording provider
// receipt on a stored run. An existing receipt returns success without
// replacing those snapshots, supporting the delivery helper's repeated
// observation of the same provider outcome.
//
// Each output receives a separate shallow struct copy; pointer-valued metadata
// remains shared with the supplied slice entries. These are ordered memory
// updates for a sequential scenario, and callers use PostgreSQL tests to
// examine transaction or recovery behavior beyond that fixture sequence.
func (r *smokeFakeRunRepo) RecordProviderOutcome(ctx context.Context, id string, providerID int64, outputs []creative.CreativeRunOutput, now time.Time) error {
	run, err := r.GetCreativeRunByRunID(ctx, id)
	if err != nil {
		return err
	}
	if run.ProviderResultRecordedAt != nil {
		return nil
	}
	snapshots := make([]*creative.CreativeRunOutput, len(outputs))
	for i := range outputs {
		value := outputs[i]
		snapshots[i] = &value
	}
	// Replace the output collection before provider receipt is marked so
	// the following delivery phase reads this outcome's metadata when it
	// saves transient bytes.
	r.outputs[id] = snapshots
	return r.MarkCreativeRunProviderSucceeded(ctx, id, providerID, now)
}

// CompleteProviderOutcome records successful completion before selecting
// result_lost when delivery is lost. Invalid completion returns first, while
// the lost flag directly replaces the completed memory status for this fixture.
func (r *smokeFakeRunRepo) CompleteProviderOutcome(ctx context.Context, id string, cost float64, lost bool, now time.Time) error {
	if err := r.MarkCreativeRunSucceeded(ctx, id, cost, now); err != nil {
		return err
	}
	// Completion already stored cost and time; a lost delivery then changes
	// only this fixture's terminal status so callers can inspect completed
	// provider work with missing output.
	if lost {
		r.runs[id].Status = creative.CreativeRunStatusResultLost
	}
	return nil
}
