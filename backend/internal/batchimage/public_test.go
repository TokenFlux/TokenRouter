package batchimage_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchimageprovider "github.com/TokenFlux/TokenRouter/internal/batchimage/provider"
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
)

// Batch admission combines group permission, concrete catalogue names,
// provider mapping and per-image pricing before upstream submission. The
// public fixtures expose stored snapshots and recorded funding, provider and
// queue calls, so cases can inspect model identities, expanded requests,
// failure compensation and owner-scoped result conversion.

// batchCataloguePolicyReads counts catalogue policy and pricing-source reads
// while supplying configured names.
type batchCataloguePolicyReads struct {
	*routing.PricingConfigService          // delegated routing policy implementation
	policyReads, pricingReads     int      // policyReads: group policy lookup count; pricingReads: pricing source lookup count
	modelsList                    []string // injected concrete group catalogue
}

// changingMediaPricingGroupRepo returns successive group views to expose
// extra snapshot reads.
type changingMediaPricingGroupRepo struct {
	calls int // group view lookup count
	// oldGroup and newGroup are shared fixture views. GetByIDLite returns oldGroup
	// on its first call and newGroup thereafter so the test can expose accidental
	// rereads.
	oldGroup, newGroup *batchimage.GroupView // oldGroup: first group rate snapshot; newGroup: later group rate snapshot
}

// publicPricingConfigFixture supplies one price configuration and group
// policy to shared routing code.
type publicPricingConfigFixture struct {
	routing.PricingConfigRepository                            // unused pricing repository methods
	pricingConfig                   routing.PricingConfig      // linked image pricing configuration
	platforms                       map[int64]string           // fixture group platform map
	policy                          routing.GroupRoutingPolicy // fixture group routing policy
}

// fakeBatchImageAuthCacheInvalidator records key, user and group invalidation
// requests from public operations.
type fakeBatchImageAuthCacheInvalidator struct {
	keys     []string // invalidated API key spellings
	userIDs  []int64  // invalidated payer user identities
	groupIDs []int64  // invalidated group identity values
}

// submitCompensationRepository records failed submission writes and injects persistence failures.
type submitCompensationRepository struct {
	*fakeBatchImageRepository                               // delegated in-memory job storage
	onCall                    func(context.Context, string) // ordered operation observation
	cancel                    context.CancelFunc            // submission cancellation at failure
	itemErr                   error                         // injected item creation error
	recordErr                 error                         // injected failure record error
	hideErr                   error                         // injected record hiding error
	message                   string                        // attempted diagnostic message text
}

// submitCompensationFunding observes refund attempts through the existing funding fixture.
type submitCompensationFunding struct {
	*fakeBatchImageBillingRepo                               // delegated task funding fixture
	onCall                     func(context.Context, string) // ordered operation observation
}

// submitCompensationQueue observes billing retry publication through the existing queue fixture.
type submitCompensationQueue struct {
	*publicBatchImageQueue                               // delegated batch queue fixture
	onCall                 func(context.Context, string) // ordered operation observation
}

// submitCompensationProvider supplies unusable submission outcomes after cancelling the caller context.
type submitCompensationProvider struct {
	*publicBatchImageProvider                               // delegated provider fixture methods
	onCall                    func(context.Context, string) // ordered operation observation
	cancel                    context.CancelFunc            // cancellation before failed return
	result                    *batchimage.BatchProviderJob  // injected upstream job result
}

// submitCompensationError observes diagnostic formatting while retaining the underlying error category.
type submitCompensationError struct {
	err     error  // underlying submission failure
	onError func() // diagnostic formatting observation
}

// admissionProvider combines real creation eligibility with recorded local submissions.
type admissionProvider struct {
	*publicBatchImageProvider                                       // local execution and observation
	readiness                 batchimageprovider.BatchImageProvider // real credential and storage checks
}

// SupportsSubmission delegates creation eligibility to the real backend adapter.
// Recorded execution stays local so an admission regression is observable without
// making a network request or exchanging service-account credentials.
func (p *admissionProvider) SupportsSubmission(record *providercore.Record) bool {
	return p.readiness.SupportsSubmission(record)
}

// TestBatchImageCatalogueAndSubmissionAdmission closes a provider protocol or omits
// Vertex storage. Unavailable routes must be rejected before any job side
// effects, while a configured Gemini route remains usable without Vertex storage.
func TestBatchImageCatalogueAndSubmissionAdmission(t *testing.T) {
	for _, scenario := range []string{"protocol closed", "Vertex storage missing", "Gemini without Vertex storage"} {
		t.Run(scenario, func(t *testing.T) {
			svc, repo, queue, gemini, vertex, _ := newTestBatchImagePublicService()
			fixture := testassert.MustType[*batchProviderFixture](svc.ProviderRepo)
			source := testassert.MustType[*publicBatchImageProviderRepo](fixture.source)
			record := testBatchImageMappedProvider(101, capability.ProviderTypeAPIKey, map[string]any{
				"gemini-2.5-flash-image": "gemini-2.5-flash-image",
			})
			if scenario == "protocol closed" {
				record.Credentials["upstream_protocols"] = []string{}
			} else {
				record.Type = capability.ProviderTypeServiceAccount
				record.Credentials["service_account_json"] = map[string]any{
					"type": "service_account", "project_id": "test-project",
					"private_key": "fixture-key", "client_email": "test@example.com",
				}
			}
			source.providers = []providercore.Record{record}
			vertexAdapter := batchimageprovider.NewVertexBatchImageProvider(batchimageprovider.VertexBatchImageProviderOptions{}, nil, nil, nil)
			if scenario != "protocol closed" {
				require.True(t, vertexAdapter.SupportsProvider(&record))
			}
			if scenario == "Gemini without Vertex storage" {
				source.providers = append(source.providers, testBatchImageMappedProvider(202, capability.ProviderTypeAPIKey, map[string]any{
					"gemini-2.5-flash-image": "gemini-2.5-flash-image",
				}))
			}
			fixture.registry = batchimage.NewRegistry[batchimageprovider.BatchImageProvider](
				&admissionProvider{gemini, batchimageprovider.NewGeminiAPIBatchImageProvider(nil)},
				&admissionProvider{vertex, vertexAdapter},
			)
			models, err := svc.ListModels(context.Background(), testBatchImageOwner())
			require.NoError(t, err)
			if scenario == "Gemini without Vertex storage" {
				require.Len(t, models.Data, 1)
				require.Equal(t, batchimage.BatchImageProviderGeminiAPI, models.Data[0].Platform)
				job, err := svc.Submit(context.Background(), testBatchImageOwner(), validBatchImageSubmitRequest(), "")
				require.NoError(t, err)
				require.Equal(t, batchimage.BatchImageProviderGeminiAPI, job.Platform)
				require.Len(t, gemini.submits, 1)
				require.Empty(t, vertex.submits)
				return
			}
			require.Empty(t, models.Data)
			_, err = svc.Submit(context.Background(), testBatchImageOwner(), validBatchImageSubmitRequest(), "")
			require.ErrorIs(t, err, batchimage.ErrBatchImageNoProviderAvailable)
			require.Empty(t, repo.jobs)
			require.Empty(t, testassert.MustType[*fakeBatchImageBillingRepo](taskFixtureBilling(svc)).reserves)
			require.Empty(t, queue.enqueued)
			require.Empty(t, gemini.submits)
			require.Empty(t, vertex.submits)
		})
	}
}

// TestBatchImageSubmissionRanksBothBackends supplies competing Gemini and Vertex
// records. New jobs follow ascending priority and ID across authentication types,
// while an explicit platform restricts selection to that backend.
func TestBatchImageSubmissionRanksBothBackends(t *testing.T) {
	for _, tc := range []struct {
		name           string // selection scenario name
		geminiPriority int    // API key numeric priority
		vertexPriority int    // service account numeric priority
		geminiID       int64  // API key provider identity
		vertexID       int64  // service account provider identity
		platform       string // requested backend restriction
		wantID         int64  // expected bound provider identity
		wantPlatform   string // expected bound backend identity
	}{
		{"Vertex priority", 20, 10, 101, 202, "", 202, batchimage.BatchImageProviderVertex},
		{"Gemini priority", 10, 20, 101, 202, "", 101, batchimage.BatchImageProviderGeminiAPI},
		{"ID tie", 10, 10, 202, 101, "", 101, batchimage.BatchImageProviderVertex},
		{"explicit Gemini", 20, 10, 101, 202, batchimage.BatchImageProviderGeminiAPI, 101, batchimage.BatchImageProviderGeminiAPI},
		{"explicit Vertex", 10, 20, 101, 202, batchimage.BatchImageProviderVertex, 202, batchimage.BatchImageProviderVertex},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, _, _, _, _ := newTestBatchImagePublicService()
			source := testassert.MustType[*publicBatchImageProviderRepo](testassert.MustType[*batchProviderFixture](svc.ProviderRepo).source)
			apiKey := testBatchImageProvider(tc.geminiID, capability.ProviderTypeAPIKey)
			apiKey.Priority = tc.geminiPriority
			serviceAccount := testBatchImageProvider(tc.vertexID, capability.ProviderTypeServiceAccount)
			serviceAccount.Priority = tc.vertexPriority
			source.providers = []providercore.Record{serviceAccount, apiKey}
			req := validBatchImageSubmitRequest()
			req.Platform = tc.platform
			job, err := svc.Submit(context.Background(), testBatchImageOwner(), req, "")
			require.NoError(t, err)
			require.Equal(t, tc.wantPlatform, job.Platform)
			require.Equal(t, tc.wantID, *repo.jobs[job.ID].ProviderID)
		})
	}
}

// TestBatchImagePublicService_SubmitCompensation exercises item persistence errors,
// upstream errors and unusable upstream names after funds have been reserved.
// It checks compensation order, cancelled context reuse, error precedence and
// the visibility of records retained for billing retries.
func TestBatchImagePublicService_SubmitCompensation(t *testing.T) {
	stages := []struct {
		name        string                       // scenario display name
		itemErr     error                        // injected item persistence failure
		providerErr error                        // injected upstream submission error
		providerJob *batchimage.BatchProviderJob // returned unusable upstream identity
		wantErr     error                        // expected public error category
		wantCode    string                       // expected persisted diagnostic code
		wantMessage string                       // expected sanitized diagnostic text
	}{
		{
			name:        "item creation",
			itemErr:     errors.New("projects/private/item persistence failed"),
			wantErr:     batchimage.ErrBatchImageQueueFailed,
			wantCode:    "ITEM_CREATE_FAILED",
			wantMessage: "upstream platform operation failed",
		},
		{
			name:        "upstream error",
			providerErr: errors.New("projects/private/upstream failed"),
			wantErr:     batchimage.ErrBatchImageProviderSubmitFailed,
			wantCode:    "PROVIDER_SUBMIT_FAILED",
			wantMessage: "upstream platform operation failed",
		},
		{
			name:        "upstream credential error",
			providerErr: batchimage.ErrBatchImageProviderMissingServiceAccount,
			wantErr:     batchimage.ErrBatchImageProviderMissingServiceAccount,
			wantCode:    "BATCH_IMAGE_PROVIDER_MISSING_SERVICE_ACCOUNT",
			wantMessage: batchimage.ErrBatchImageProviderMissingServiceAccount.Error(),
		},
		{
			name:        "blank upstream name",
			providerJob: &batchimage.BatchProviderJob{ProviderJobName: " \t "},
			wantErr:     batchimage.ErrBatchImageProviderSubmitFailed,
			wantCode:    "PROVIDER_SUBMIT_FAILED",
			wantMessage: "platform job name missing",
		},
		{
			name:        "nil upstream job",
			wantErr:     batchimage.ErrBatchImageProviderSubmitFailed,
			wantCode:    "PROVIDER_SUBMIT_FAILED",
			wantMessage: "platform job name missing",
		},
	}
	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			for _, failure := range []struct {
				name       string // compensation failure scenario name
				releaseErr error  // injected funding release failure
				recordErr  error  // injected diagnostic persistence failure
				hideErr    error  // injected record hiding failure
				queueErr   error  // injected retry publication failure
			}{
				{name: "success"},
				{name: "record failure", recordErr: errors.New("record unavailable")},
				{name: "hide failure", hideErr: errors.New("hide unavailable")},
				{name: "release failure", releaseErr: errors.New("funding unavailable")},
				{name: "release and record failure", releaseErr: errors.New("funding unavailable"), recordErr: errors.New("record unavailable")},
				{name: "release and queue failure", releaseErr: errors.New("funding unavailable"), queueErr: errors.New("queue unavailable")},
			} {
				t.Run(failure.name, func(t *testing.T) {
					svc, repo, queue, gemini, _, authCache := newTestBatchImagePublicService()
					ctx, cancel := context.WithCancel(routing.WithModelAttributes(context.Background(), svc.Attributes))
					defer cancel()
					var calls []string
					onCall := func(gotCtx context.Context, operation string) {
						require.Same(t, ctx, gotCtx)
						if operation != "items" && operation != "submit" {
							require.ErrorIs(t, gotCtx.Err(), context.Canceled)
						}
						calls = append(calls, operation)
					}
					observeError := func(err error) error {
						if err == nil {
							return nil
						}
						return submitCompensationError{err: err, onError: func() { onCall(ctx, "describe") }}
					}
					writes := &submitCompensationRepository{
						fakeBatchImageRepository: repo,
						onCall:                   onCall,
						cancel:                   cancel,
						itemErr:                  observeError(stage.itemErr),
						recordErr:                failure.recordErr,
						hideErr:                  failure.hideErr,
					}
					svc.Repo = writes
					funds := testassert.MustType[*fakeBatchImageBillingRepo](taskFixtureBilling(svc))
					funds.releaseErr = failure.releaseErr
					svc.Funding.Store = &submitCompensationFunding{fakeBatchImageBillingRepo: funds, onCall: onCall}
					queue.err = failure.queueErr
					svc.Queue = &submitCompensationQueue{publicBatchImageQueue: queue, onCall: onCall}
					gemini.submitErr = observeError(stage.providerErr)
					provider := &submitCompensationProvider{
						publicBatchImageProvider: gemini,
						onCall:                   onCall,
						cancel:                   cancel,
						result:                   stage.providerJob,
					}
					providers := testassert.MustType[*batchProviderFixture](svc.ProviderRepo)
					providers.registry = batchimage.NewRegistry[batchimageprovider.BatchImageProvider](provider)
					req := validBatchImageSubmitRequest()
					req.TaskName = "compensation"

					got, err := svc.Submit(ctx, testBatchImageOwner(), req, "")

					require.Nil(t, got)
					wantErr := stage.wantErr
					wantCode := stage.wantCode
					wantCalls := []string{"items"}
					if stage.itemErr == nil {
						wantCalls = append(wantCalls, "submit")
					}
					wantCalls = append(wantCalls, "release")
					if failure.releaseErr != nil {
						wantErr = batchimage.ErrBatchImageBillingHoldFailed
						wantCode = "BILLING_RELEASE_FAILED"
						wantCalls = append(wantCalls, "record:"+wantCode, "enqueue")
						require.Len(t, authCache.userIDs, 1)
					} else {
						if stage.itemErr != nil || stage.providerErr != nil {
							wantCalls = append(wantCalls, "describe")
						}
						wantCalls = append(wantCalls, "record:"+wantCode, "hide")
						require.Equal(t, stage.wantMessage, writes.message)
						require.Len(t, authCache.userIDs, 2)
					}
					require.ErrorIs(t, err, wantErr)
					require.Equal(t, wantCalls, calls)
					require.Len(t, funds.reserves, 1)
					require.Len(t, funds.releases, 1)
					require.Equal(t, funds.reserves[0].RequestPayloadHash, funds.releases[0].RequestPayloadHash)
					require.Len(t, repo.jobs, 1)
					for _, job := range repo.jobs {
						require.Nil(t, job.ProviderJobName)
						require.Equal(t, failure.releaseErr != nil, job.AllowanceReserved)
						require.Equal(t, batchimage.BatchImageReleaseRequestID(job.BatchID), funds.releases[0].RequestID)
						if failure.recordErr == nil {
							require.Equal(t, batchimage.BatchImageJobStatusFailed, job.Status)
							require.Equal(t, wantCode, batchimage.BatchImageDerefString(job.LastErrorCode))
						} else {
							wantStatus := batchimage.BatchImageJobStatusUploading
							if stage.itemErr != nil {
								wantStatus = batchimage.BatchImageJobStatusCreated
							}
							require.Equal(t, wantStatus, job.Status)
							require.Nil(t, job.LastErrorCode)
						}
						if failure.releaseErr == nil && failure.recordErr == nil && failure.hideErr == nil {
							require.NotNil(t, job.UserDeletedAt)
						} else {
							require.Nil(t, job.UserDeletedAt)
						}
						if failure.releaseErr != nil && failure.queueErr == nil {
							require.Equal(t, []string{job.BatchID}, queue.enqueued)
						} else {
							require.Empty(t, queue.enqueued)
						}
					}
				})
			}
		})
	}
}

// BulkCreateBatchImageItems records item persistence and returns the configured error.
// A failing write cancels the request before compensation starts so tests can
// detect a replacement context in later operations.
func (r *submitCompensationRepository) BulkCreateBatchImageItems(ctx context.Context, params []batchimage.CreateBatchImageItemParams) error {
	r.onCall(ctx, "items")
	if r.itemErr != nil {
		r.cancel()
		return r.itemErr
	}
	return r.fakeBatchImageRepository.BulkCreateBatchImageItems(ctx, params)
}

// RecordBatchImageJobSubmitFailure captures the attempted diagnostic before applying
// the configured persistence outcome. A configured error leaves the stored job
// unchanged so later hiding attempts still exercise its state check.
func (r *submitCompensationRepository) RecordBatchImageJobSubmitFailure(ctx context.Context, batchID, code, message string, markFailed bool) error {
	r.onCall(ctx, "record:"+code)
	r.message = message
	if r.recordErr != nil {
		return r.recordErr
	}
	return r.fakeBatchImageRepository.RecordBatchImageJobSubmitFailure(ctx, batchID, code, message, markFailed)
}

// MarkBatchImageJobUserDeleted records every hiding attempt before returning a
// configured rejection or delegating the ownership and terminal-state checks.
// Failed writes leave the fixture's user deletion timestamp absent.
func (r *submitCompensationRepository) MarkBatchImageJobUserDeleted(ctx context.Context, userID, apiKeyID int64, batchID string, deletedAt time.Time) error {
	r.onCall(ctx, "hide")
	if r.hideErr != nil {
		return r.hideErr
	}
	return r.fakeBatchImageRepository.MarkBatchImageJobUserDeleted(ctx, userID, apiKeyID, batchID, deletedAt)
}

// Release records refund ordering before forwarding the supplied funding command.
func (f *submitCompensationFunding) Release(ctx context.Context, cmd *billingcore.TaskFundsCommand) (*billingcore.TaskFundsResult, error) {
	f.onCall(ctx, "release")
	return f.fakeBatchImageBillingRepo.Release(ctx, cmd)
}

// Enqueue records retry publication before forwarding the supplied batch identity.
func (q *submitCompensationQueue) Enqueue(ctx context.Context, batchID string) error {
	q.onCall(ctx, "enqueue")
	return q.publicBatchImageQueue.Enqueue(ctx, batchID)
}

// Submit returns the configured failed upstream outcome and cancels its request.
// Cancellation precedes compensation so every subsequent release and repository
// write can be checked against the same cancelled context.
func (p *submitCompensationProvider) Submit(ctx context.Context, _ *batchimage.BatchImageJob, _ *providercore.Record, input batchimage.BatchImageInput) (*batchimage.BatchProviderJob, error) {
	p.onCall(ctx, "submit")
	p.submits = append(p.submits, input)
	p.cancel()
	return p.result, p.submitErr
}

// Error records diagnostic evaluation before returning the underlying failure text.
func (e submitCompensationError) Error() string {
	e.onError()
	return e.err.Error()
}

// Unwrap exposes the underlying category for submission error classification.
func (e submitCompensationError) Unwrap() error {
	return e.err
}

// TestBatchImageCatalogueIncludesGroupMappingTargets enumerates concrete
// group mapping sources and targets with valid image pricing. It checks the
// visible names before requested-stage restrictions or provider allowlists
// remove otherwise configured targets.
func TestBatchImageCatalogueIncludesGroupMappingTargets(t *testing.T) {
	for _, source := range []string{"draw-alias", "draw-*"} {
		t.Run(source, func(t *testing.T) {
			service, _, _, _, _, _ := newTestBatchImagePublicService()
			service.ProviderExists = func(name string) bool { return name == batchimage.BatchImageProviderGeminiAPI }
			owner := testBatchImageOwner()
			value := testBatchImageMappedProvider(303, "apikey", nil)
			service.ProviderRepo = rebindBatchFixtureProviders(service, &publicBatchImageProviderRepo{providers: []providercore.Record{value}})
			policy := makePublicPricingConfigFixture(routing.PricingConfig{ID: 1, Status: "active", GroupIDs: []int64{*owner.GroupID}}, nil, routing.GroupRoutingPolicy{Enabled: true, ModelMapping: map[string]string{source: "custom-image"}})
			service.PricingConfigService = newPublicPricingConfigFixture(policy)
			service.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1}
			service.Attributes = &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{Lookup: func(model string) modelcatalog.Attributes {
				image := []string{"image"}
				if model == "custom-image" {
					return modelcatalog.Attributes{OutputModalities: &image}
				}
				return modelcatalog.Attributes{}
			}}}
			result, err := service.ListModels(context.Background(), owner)
			require.NoError(t, err)
			var ids []string
			for _, model := range result.Data {
				ids = append(ids, model.ID)
			}
			want := []string{"custom-image"}
			if source == "draw-alias" {
				want = append(want, source)
			}
			require.ElementsMatch(t, want, ids)

			policy.policy.RestrictModels = true
			policy.policy.RestrictionModelSource = routing.BillingModelSourceRequested
			policy.policy.AllowedModels = []string{source}
			result, err = service.ListModels(context.Background(), owner)
			require.NoError(t, err)
			for _, model := range result.Data {
				require.NotEqual(t, "custom-image", model.ID)
			}

			policy.policy.RestrictModels = false
			value.Credentials["model_whitelist"] = []string{"other-model"}
			result, err = service.ListModels(context.Background(), owner)
			require.NoError(t, err)
			for _, model := range result.Data {
				require.NotEqual(t, "custom-image", model.ID)
			}
		})
	}
}

// TestBatchImageUnboundKeyCannotUseGlobalProviders submits an owner with no
// group while providers are available. Permission rejection occurs before job
// creation and provider submission, protecting the bound-group requirement
// for batch admission.
func TestBatchImageUnboundKeyCannotUseGlobalProviders(t *testing.T) {
	svc, repo, _, platform, _, _ := newTestBatchImagePublicService()
	owner := testBatchImageOwner()
	owner.GroupID = nil
	_, err := svc.Submit(context.Background(), owner, validBatchImageSubmitRequest(), "")
	require.ErrorIs(t, err, batchimage.ErrBatchImageGroupDisabled)
	require.Empty(t, repo.jobs)
	require.Empty(t, platform.submits)
}

// TestBatchImageCatalogueResolvesBeforeModalities combines wildcard and
// staged mappings with image, text and unknown final modalities. It checks
// visible request names and final pricing models while provider restrictions
// exclude unsupported candidates.
func TestBatchImageCatalogueResolvesBeforeModalities(t *testing.T) {
	for _, tc := range []struct {
		name            string            // scenario display name
		groupMapping    map[string]string // group model mapping
		providerMapping map[string]any    // provider model mapping
		whitelist       []string          // allowed upstream models
		final           string            // resolved upstream model
		visible         bool              // expected catalogue visibility
	}{
		{name: "provider wildcard", providerMapping: map[string]any{"gpt-*": "catalog-image"}, whitelist: []string{"catalog-image"}, final: "catalog-image", visible: true},
		{name: "group wildcard", groupMapping: map[string]string{"gpt-*": "catalog-image"}, whitelist: []string{"catalog-image"}, final: "catalog-image", visible: true},
		{name: "two mapping stages", groupMapping: map[string]string{"gpt-*": "route-model"}, providerMapping: map[string]any{"route-*": "catalog-image"}, whitelist: []string{"catalog-image"}, final: "catalog-image", visible: true},
		{name: "unknown configured final", providerMapping: map[string]any{"gpt-*": "nano-banana-pro"}, whitelist: []string{"nano-banana-pro"}, final: "nano-banana-pro", visible: false},
		{name: "mapped text final", providerMapping: map[string]any{"gpt-*": "catalog-text"}, whitelist: []string{"catalog-text"}, final: "catalog-text"},
		{name: "provider restriction", providerMapping: map[string]any{"gpt-*": "catalog-image"}, whitelist: []string{"catalog-text"}, final: "catalog-image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _, _, _, _ := newTestBatchImagePublicService()
			owner := testBatchImageOwner()
			group := *owner.GroupID
			value := testBatchImageMappedProvider(303, "apikey", tc.providerMapping)
			value.Credentials["model_whitelist"] = tc.whitelist
			svc.ProviderRepo = rebindBatchFixtureProviders(svc, &publicBatchImageProviderRepo{providers: []providercore.Record{value}})
			image, text := []string{"image"}, []string{"text"}
			svc.Attributes = nil
			svc.ModelOutputModalities = func(id string) *[]string {
				switch id {
				case "catalog-image":
					return &image
				case "gpt-5.4", "catalog-text":
					return &text
				default:
					return nil
				}
			}
			policy := newPublicPricingConfigFixture(makePublicPricingConfigFixture(routing.PricingConfig{ID: 1, Status: "active", GroupIDs: []int64{group}, BillingModelSource: routing.BillingModelSourceUpstream}, nil, routing.GroupRoutingPolicy{Enabled: true, AllowedModels: []string{"gpt-5.4"}, ModelMapping: tc.groupMapping}))
			svc.PricingConfigService = policy
			price := &fakeBatchImagePricingResolver{unitPrice: 0.1}
			svc.Pricing = price
			result, err := svc.ListModels(context.Background(), owner)
			require.NoError(t, err)
			ids := []string{}
			for _, model := range result.Data {
				ids = append(ids, model.ID)
			}
			if tc.visible {
				require.Contains(t, ids, "gpt-5.4")
				require.Contains(t, price.models, tc.final)
			} else {
				require.NotContains(t, ids, "gpt-5.4")
			}
			require.NotContains(t, ids, "unconfigured-unknown")
			require.NotContains(t, ids, "catalog-text")
		})
	}
}

// TestBatchImageCatalogueUsesOnePolicySnapshot queries each restriction stage
// through counting policy and price-source readers. It checks one read of
// each source and excludes text models while retaining the mapped image
// request.
func TestBatchImageCatalogueUsesOnePolicySnapshot(t *testing.T) {
	for _, stage := range []string{routing.BillingModelSourceRequested, routing.BillingModelSourceGroupMapped, routing.BillingModelSourceUpstream} {
		t.Run(stage, func(t *testing.T) {
			svc, _, _, _, _, _ := newTestBatchImagePublicService()
			owner := testBatchImageOwner()
			group := *owner.GroupID
			allowed := map[string]string{routing.BillingModelSourceRequested: "gpt-5.4", routing.BillingModelSourceGroupMapped: "route-model", routing.BillingModelSourceUpstream: "catalog-image"}[stage]
			policies := &batchCataloguePolicyReads{PricingConfigService: newPublicPricingConfigFixture(makePublicPricingConfigFixture(routing.PricingConfig{ID: 1, Status: "active", GroupIDs: []int64{group}, BillingModelSource: routing.BillingModelSourceUpstream}, nil, routing.GroupRoutingPolicy{Enabled: true, ModelMapping: map[string]string{"gpt-*": "route-model", "gpt-5.4": "route-model"}, RestrictModels: true, RestrictionModelSource: stage, AllowedModels: []string{allowed}}))}
			svc.PricingConfigService = policies
			value := testBatchImageMappedProvider(303, "apikey", map[string]any{"route-*": "catalog-image"})
			value.Credentials["model_whitelist"] = []string{"catalog-image"}
			svc.ProviderRepo = rebindBatchFixtureProviders(svc, &publicBatchImageProviderRepo{providers: []providercore.Record{value}})

			image, text := []string{"image"}, []string{"text"}
			svc.Attributes = nil
			svc.ModelOutputModalities = func(id string) *[]string {
				if id == "catalog-image" {
					return &image
				}
				return &text
			}
			svc.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1}
			result, err := svc.ListModels(context.Background(), owner)
			require.NoError(t, err)
			ids := []string{}
			for _, model := range result.Data {
				ids = append(ids, model.ID)
			}
			require.Contains(t, ids, "gpt-5.4")
			require.NotContains(t, ids, "text-0")
			require.Equal(t, 1, policies.policyReads)
			require.Equal(t, 1, policies.pricingReads)
		})
	}
}

// TestBatchImageCatalogueRejectsMissingPrice makes a mapped final model
// image-capable but injects missing image pricing. The catalogue still
// returns successfully with no entries, protecting price admission alongside
// output-modality checks.
func TestBatchImageCatalogueRejectsMissingPrice(t *testing.T) {
	svc, _, _, _, _, _ := newTestBatchImagePublicService()
	value := testBatchImageMappedProvider(303, "apikey", map[string]any{"gpt-*": "catalog-image"})
	svc.ProviderRepo = rebindBatchFixtureProviders(svc, &publicBatchImageProviderRepo{providers: []providercore.Record{value}})
	image := []string{"image"}
	svc.Attributes = nil
	svc.ModelOutputModalities = func(string) *[]string { return &image }
	svc.Pricing = &fakeBatchImagePricingResolver{err: batchimage.ErrBatchImageSettlementPricingMissing}
	result, err := svc.ListModels(context.Background(), testBatchImageOwner())
	require.NoError(t, err)
	require.Empty(t, result.Data)
}

// TestBatchImageAutomaticCandidates supplies unconfigured catalogue names and
// a configured image name mapped to text. The catalogue remains empty,
// protecting finite configured candidates and checks against the final
// model's output.
func TestBatchImageAutomaticCandidates(t *testing.T) {
	svc, _, _, _, _, _ := newTestBatchImagePublicService()
	value := testBatchImageMappedProvider(303, "apikey", map[string]any{"source-image": "final-text"})
	svc.ProviderRepo = rebindBatchFixtureProviders(svc, &publicBatchImageProviderRepo{providers: []providercore.Record{value}})
	image, text := []string{"image"}, []string{"text"}
	svc.Attributes = nil
	svc.ModelOutputModalities = func(id string) *[]string {
		switch id {
		case "catalog-image", "source-image":
			return &image
		case "final-text":
			return &text
		default:
			return nil
		}
	}
	svc.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1}
	result, err := svc.ListModels(context.Background(), testBatchImageOwner())
	require.NoError(t, err)
	require.Empty(t, result.Data)
}

// TestBatchImagePricingSnapshotUsesOneGroupVersion changes group rates
// between successive reads while resolving an image snapshot. It checks one
// GroupView read and billable and hold prices derived from that first
// version.
func TestBatchImagePricingSnapshotUsesOneGroupVersion(t *testing.T) {
	oldGroup := &batchimage.GroupView{
		ID: 7, AllowBatchImageGeneration: true, RateMultiplier: 10, BatchImageDiscountMultiplier: 0.5, BatchImageHoldMultiplier: 0.6,
	}
	newGroup := *oldGroup
	newGroup.RateMultiplier = 1
	groups := &changingMediaPricingGroupRepo{oldGroup: oldGroup, newGroup: &newGroup}
	svc := newBatchPublicFixture(nil, nil, nil, groups, nil, nil, nil, &batchimage.Pricing{Resolver: billingtestkit.SharedPriceResolver(billingtestkit.Calculator(nil, nil), 7, pricing.DefaultBillingSettings(), []routing.ModelPricingEntry{{Models: []string{"gemini-3.1-flash-image"}, BillingMode: routing.BillingModeImage, PerRequestPrice: testPtrFloat64(1)}}), GroupRepo: batchGroupReader{groups}}, nil, nil, nil)
	snapshot, err := svc.ResolvePricingSnapshot(context.Background(), batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &oldGroup.ID, BillingMode: apikey.APIKeyBillingModeBalance}, batchimage.BatchImageSubmitRequest{Model: "gemini-3.1-flash-image", ImageSize: "1K"}, "gemini_api", nil)
	require.NoError(t, err)
	require.Equal(t, 1, groups.calls)
	require.Equal(t, 1.0, snapshot.BaseUnitPrice)
	require.Equal(t, 10.0, snapshot.GroupRateMultiplier)
	require.InDelta(t, 5.0, snapshot.BillableUnitPrice, 1e-12)
	require.InDelta(t, 6.0, snapshot.HoldUnitPrice, 1e-12)
}

// TestBatchImagePublicService_Submit covers normalization, selected
// providers, model identities, pricing and funding snapshots, plus failures
// across submission phases. It checks recorded upstream and queue calls,
// compensation, idempotent replay and public response filtering.
func TestBatchImagePublicService_Submit(t *testing.T) {
	ctx := context.Background()

	t.Run("rejects when storage is unavailable", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		svc.Repo = nil
		_, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageDisabled)
	})

	t.Run("accepts valid request stores refs and enqueues once", func(t *testing.T) {
		svc, repo, queue, gemini, _, authCache := newTestBatchImagePublicService()
		req := validBatchImageSubmitRequest()
		req.SessionID = batchimage.BatchImageStringPtr("batch-session-123")

		got, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.NoError(t, err)
		require.Equal(t, "image.batch", got.Object)
		require.Equal(t, "queued", got.Status)
		require.Equal(t, batchimage.BatchImageProviderGeminiAPI, got.Platform)
		require.Equal(t, 2, got.ItemCount)
		require.Equal(t, 0.25, got.EstimatedCost)
		require.Len(t, repo.jobs, 1)
		require.Len(t, gemini.submits, 1)
		require.Equal(t, []string{got.ID}, queue.enqueued)
		billing := testassert.MustType[*fakeBatchImageBillingRepo](taskFixtureBilling(svc))
		require.Len(t, billing.reserves, 1)
		require.Equal(t, batchimage.BatchImageHoldRequestID(got.ID), billing.reserves[0].RequestID)
		require.InDelta(t, 0.3, billing.reserves[0].HoldAmount, 1e-12)
		require.Empty(t, billing.releases)
		require.Equal(t, []int64{11}, authCache.userIDs)

		job := repo.jobs[got.ID]
		require.Equal(t, batchimage.BatchImageJobStatusSubmitted, job.Status)
		require.Equal(t, "providers/gemini_api/job", batchimage.BatchImageDerefString(job.ProviderJobName))
		require.Equal(t, "files/gemini_api/input", batchimage.BatchImageDerefString(job.ProviderInputRef))
		require.Equal(t, "files/gemini_api/output", batchimage.BatchImageDerefString(job.ProviderOutputRef))
		require.NotNil(t, job.ProviderID)
		require.Equal(t, int64(101), *job.ProviderID)
		require.Equal(t, 3, job.PricingSnapshotVersion)
		require.InDelta(t, 0.25, job.BaseUnitPrice, 1e-12)
		require.InDelta(t, 1.0, job.GroupRateMultiplier, 1e-12)
		require.InDelta(t, 1.0, job.ProviderRateMultiplier, 1e-12)
		require.InDelta(t, 0.5, job.BatchDiscountMultiplier, 1e-12)
		require.InDelta(t, 0.6, job.HoldMultiplier, 1e-12)
		require.InDelta(t, 0.125, job.BillableUnitPrice, 1e-12)
		require.InDelta(t, 0.15, job.HoldUnitPrice, 1e-12)
		require.Equal(t, "batch-session-123", batchimage.BatchImageDerefString(job.SessionID))
	})

	t.Run("persists composite route identity and separates idempotency", func(t *testing.T) {
		svc, repo, _, _, _, _ := newTestBatchImagePublicService()
		groupID := int64(7)
		otherGroupID := int64(8)
		svc.GroupRepo = batchGroupReader{&publicBatchImageGroupRepo{groups: map[int64]*batchimage.GroupView{
			groupID: {
				ID:                        groupID,
				RateMultiplier:            1,
				AllowBatchImageGeneration: true,
			},
			otherGroupID: {
				ID:                        otherGroupID,
				RateMultiplier:            1,
				AllowBatchImageGeneration: true,
			},
		}}}
		owner := batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID}
		compositeCtx := context.WithValue(ctx, telemetry.ClientModel, "Gemini/gemini-2.5-flash-image")

		got, err := svc.Submit(compositeCtx, owner, validBatchImageSubmitRequest(), "composite-key")
		require.NoError(t, err)
		require.Equal(t, "Gemini/gemini-2.5-flash-image", got.Model)
		job := repo.jobs[got.ID]
		require.Equal(t, "gemini-2.5-flash-image", job.Model)
		require.Equal(t, "Gemini/gemini-2.5-flash-image", job.RequestedModel)
		require.Equal(t, "gemini-2.5-flash-image", job.InternalModel)
		require.NotNil(t, job.GroupID)
		require.Equal(t, groupID, *job.GroupID)

		caseInsensitiveCtx := context.WithValue(ctx, telemetry.ClientModel, "gEmInI/gemini-2.5-flash-image")
		retried, err := svc.Submit(caseInsensitiveCtx, owner, validBatchImageSubmitRequest(), "composite-key")
		require.NoError(t, err)
		require.Equal(t, got.ID, retried.ID)

		otherOwner := owner
		otherOwner.GroupID = &otherGroupID
		otherGroupCtx := context.WithValue(ctx, telemetry.ClientModel, "Backup/gemini-2.5-flash-image")
		second, err := svc.Submit(otherGroupCtx, otherOwner, validBatchImageSubmitRequest(), "composite-key")
		require.Nil(t, second)
		require.ErrorIs(t, err, batchimage.ErrBatchImageIdempotencyConflict)
	})

	t.Run("applies channel and provider model mappings before platform submit", func(t *testing.T) {
		svc, repo, _, gemini, _, _ := newTestBatchImagePublicService()
		groupID := int64(7)
		svc.GroupRepo = batchGroupReader{&publicBatchImageGroupRepo{groups: map[int64]*batchimage.GroupView{
			groupID: {
				ID:                           groupID,
				RateMultiplier:               1,
				AllowBatchImageGeneration:    true,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			},
		}}}
		svc.PricingConfigService = newPublicPricingConfigFixture(makePublicPricingConfigFixture(routing.PricingConfig{
			ID:       31,
			Status:   billingcore.StatusActive,
			GroupIDs: []int64{groupID},
		}, map[int64]string{groupID: capability.PlatformGemini}, routing.GroupRoutingPolicy{
			Enabled:      true,
			ModelMapping: map[string]string{"gemini-2.5-flash-image": "group-image-model"},
		}))
		svc.ProviderRepo = rebindBatchFixtureProviders(svc, &publicBatchImageProviderRepo{providers: []providercore.Record{
			testBatchImageMappedProvider(301, capability.ProviderTypeAPIKey, map[string]any{
				"group-image-model": "upstream-image-model",
			}),
		}})
		svc.Attributes = &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{Lookup: func(model string) modelcatalog.Attributes {
			image := []string{"image"}
			if model == "upstream-image-model" {
				return modelcatalog.Attributes{OutputModalities: &image}
			}
			return modelcatalog.Attributes{}
		}}}
		trace := modeltrace.NewAPIKeyModelRedirectTrace("image-alias", "image-alias", "gemini-2.5-flash-image")
		requestCtx := modeltrace.WithContext(ctx, trace)
		requestCtx = context.WithValue(requestCtx, telemetry.ClientModel, "image-alias")

		got, err := svc.Submit(requestCtx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID}, validBatchImageSubmitRequest(), "")

		require.NoError(t, err)
		require.Equal(t, "image-alias", got.Model)
		require.Len(t, gemini.submits, 1)
		require.Equal(t, "upstream-image-model", gemini.submits[0].Model)
		job := repo.jobs[got.ID]
		require.Equal(t, "upstream-image-model", job.Model)
		require.Equal(t, "image-alias", job.RequestedModel)
		require.Equal(t, "gemini-2.5-flash-image", job.InternalModel)
		require.ElementsMatch(t, []string{"gemini-2.5-flash-image", "group-image-model", "upstream-image-model"}, trace.ResponseModels())
		pricing := testassert.MustType[*fakeBatchImagePricingResolver](svc.Pricing)
		require.Equal(t, []string{"group-image-model"}, pricing.models)
	})

	t.Run("selects batch pricing model from channel billing source", func(t *testing.T) {
		tests := []struct {
			source string // selected billing stage
			want   string // expected observable result
		}{
			{source: routing.BillingModelSourceRequested, want: "key-target"},
			{source: routing.BillingModelSourceGroupMapped, want: "channel-target"},
			{source: routing.BillingModelSourceUpstream, want: "upstream-target"},
			{source: "", want: "channel-target"},
		}
		for _, test := range tests {
			require.Equal(t, test.want, batchimage.BatchImagePricingModel(
				routing.GroupMappingResult{BillingModelSource: test.source},
				"key-target",
				"channel-target",
				"upstream-target",
			))
		}
	})

	t.Run("combines user group image rate provider rate discount and hold margin", func(t *testing.T) {
		svc, repo, _, _, _, _ := newTestBatchImagePublicService()
		groupID := int64(7)
		providerMultiplier := 1.25
		providerRepo := testassert.MustType[*publicBatchImageProviderRepo](testassert.MustType[*batchProviderFixture](svc.ProviderRepo).source)
		providerRepo.providers[0].RateMultiplier = &providerMultiplier
		svc.GroupRepo = batchGroupReader{&publicBatchImageGroupRepo{groups: map[int64]*batchimage.GroupView{
			groupID: {
				ID:                           groupID,
				RateMultiplier:               2.0,
				AllowBatchImageGeneration:    true,
				BatchImageDiscountMultiplier: 0.8,
				BatchImageHoldMultiplier:     0.6,
			},
		}}}
		userRate := 0.5
		svc.UserGroupRateRepo = &publicBatchImageUserGroupRateRepo{rates: map[int64]*float64{groupID: &userRate}}

		got, err := svc.Submit(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID}, validBatchImageSubmitRequest(), "")
		require.NoError(t, err)
		require.InDelta(t, 0.25, got.EstimatedCost, 1e-12)
		billing := testassert.MustType[*fakeBatchImageBillingRepo](taskFixtureBilling(svc))
		require.Len(t, billing.reserves, 1)
		require.NotNil(t, billing.reserves[0].GroupID)
		require.Equal(t, groupID, *billing.reserves[0].GroupID)

		job := repo.jobs[got.ID]
		require.InDelta(t, 0.25, job.BaseUnitPrice, 1e-12)
		require.InDelta(t, 0.5, job.GroupRateMultiplier, 1e-12)
		require.InDelta(t, 1.25, job.ProviderRateMultiplier, 1e-12)
		require.InDelta(t, 0.8, job.BatchDiscountMultiplier, 1e-12)
		// A hold multiplier of 0.6 below the 0.8 discount cannot cover full success.
		// Snapshot creation clamps it to the discount so the hold covers the maximum cost.
		require.InDelta(t, 0.8, job.HoldMultiplier, 1e-12)
		require.InDelta(t, 0.125, job.BillableUnitPrice, 1e-12)
		require.InDelta(t, 0.125, job.HoldUnitPrice, 1e-12)
		require.InDelta(t, 0.25, *job.HoldAmount, 1e-12)
	})

	t.Run("uses subscription plan group rate before user balance rate", func(t *testing.T) {
		svc, repo, _, _, _, _ := newTestBatchImagePublicService()
		groupID := int64(7)
		svc.GroupRepo = batchGroupReader{&publicBatchImageGroupRepo{groups: map[int64]*batchimage.GroupView{
			groupID: {
				ID:                           groupID,
				RateMultiplier:               2,
				AllowBatchImageGeneration:    true,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			},
		}}}
		userRate := 0.25
		svc.UserGroupRateRepo = &publicBatchImageUserGroupRateRepo{rates: map[int64]*float64{groupID: &userRate}}
		billing := testassert.MustType[*fakeBatchImageBillingRepo](taskFixtureBilling(svc))
		billing.usableSubscription = &billingcore.UserSubscription{
			ID:     101,
			UserID: 11,
			Plan: &billingcore.SubscriptionPlan{
				ID:                   202,
				GroupIDs:             []int64{groupID},
				GroupRateMultipliers: map[int64]float64{groupID: 0.8},
			},
		}

		got, err := svc.Submit(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID}, validBatchImageSubmitRequest(), "")

		require.NoError(t, err)
		require.InDelta(t, 0.2, got.EstimatedCost, 1e-12)
		job := repo.jobs[got.ID]
		require.InDelta(t, 0.8, job.GroupRateMultiplier, 1e-12)
		require.InDelta(t, 0.1, job.BillableUnitPrice, 1e-12)
		require.InDelta(t, 0.12, job.HoldUnitPrice, 1e-12)
		require.InDelta(t, 0.24, *job.HoldAmount, 1e-12)
		require.Len(t, billing.reserves, 1)
		require.InDelta(t, 0.5, billing.reserves[0].BaseAmountUSD, 1e-12)
		require.InDelta(t, 1.2, billing.reserves[0].SubscriptionRateMultiplier, 1e-12)
		require.InDelta(t, 0.6, billing.reserves[0].SubscriptionRateMultiplierScale, 1e-12)
		require.InDelta(t, 0.15, billing.reserves[0].BalanceRateMultiplier, 1e-12)
		require.False(t, billing.reserves[0].DisablePlanGroupRateMultiplier)
	})

	t.Run("uses configured group 1k image price for batch image base price", func(t *testing.T) {
		svc, repo, _, _, _, _ := newTestBatchImagePublicService()
		groupID := int64(7)
		imagePrice := 0.134
		svc.GroupRepo = batchGroupReader{&publicBatchImageGroupRepo{groups: map[int64]*batchimage.GroupView{
			groupID: {
				ID:                           groupID,
				RateMultiplier:               1.0,
				AllowBatchImageGeneration:    true,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			},
		}}}

		svc.Pricing = &batchimage.Pricing{Resolver: billingtestkit.SharedPriceResolver(billingtestkit.Calculator(nil, nil), groupID, pricing.DefaultBillingSettings(), testImageModelPricing(map[string]*float64{"1K": &imagePrice})), GroupRepo: svc.GroupRepo}

		got, err := svc.Submit(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID}, validBatchImageSubmitRequest(), "")
		require.NoError(t, err)
		require.InDelta(t, 0.134, got.EstimatedCost, 1e-12)

		job := repo.jobs[got.ID]
		require.InDelta(t, 0.134, job.BaseUnitPrice, 1e-12)
		require.InDelta(t, 0.067, job.BillableUnitPrice, 1e-12)
		require.InDelta(t, 0.0804, job.HoldUnitPrice, 1e-12)
		require.InDelta(t, 0.1608, *job.HoldAmount, 1e-12)
	})

	t.Run("pricing missing rejects before platform submit", func(t *testing.T) {
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService()
		svc.Pricing = &fakeBatchImagePricingResolver{err: batchimage.ErrBatchImageSettlementPricingMissing}

		_, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageSettlementPricingMissing)
		require.Empty(t, repo.jobs)
		require.Empty(t, queue.enqueued)
		require.Empty(t, gemini.submits)
	})

	t.Run("group batch image disabled rejects before platform submit", func(t *testing.T) {
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService()
		groupID := int64(7)
		svc.GroupRepo = batchGroupReader{&publicBatchImageGroupRepo{groups: map[int64]*batchimage.GroupView{
			groupID: {
				ID:                           groupID,
				RateMultiplier:               1,
				AllowBatchImageGeneration:    false,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			},
		}}}

		_, err := svc.Submit(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID}, validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageGroupDisabled)
		require.Empty(t, repo.jobs)
		require.Empty(t, queue.enqueued)
		require.Empty(t, gemini.submits)
	})

	t.Run("group pricing load failure rejects before platform submit", func(t *testing.T) {
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService()
		groupID := int64(404)

		_, err := svc.Submit(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID}, validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageSettlementPricingMissing)
		require.Empty(t, repo.jobs)
		require.Empty(t, queue.enqueued)
		require.Empty(t, gemini.submits)
	})

	t.Run("generates custom ids deterministically", func(t *testing.T) {
		svc, _, _, gemini, _, _ := newTestBatchImagePublicService()
		req := validBatchImageSubmitRequest()
		req.Items[0].CustomID = ""
		req.Items[1].CustomID = ""

		_, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.NoError(t, err)
		require.Len(t, gemini.submits, 1)
		require.Equal(t, "item_000001", gemini.submits[0].Items[0].CustomID)
		require.Equal(t, "item_000002", gemini.submits[0].Items[1].CustomID)
	})

	t.Run("expands output count into separate billable items", func(t *testing.T) {
		svc, repo, _, gemini, _, _ := newTestBatchImagePublicService()
		req := validBatchImageSubmitRequest()
		req.Items = []batchimage.BatchImageSubmitItem{
			{CustomID: "cover", Prompt: "hero", OutputCount: 3, ReferenceImages: []batchimage.BatchImageReferenceInput{{MimeType: "image/png", Data: []byte("ref")}}},
		}

		got, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.NoError(t, err)
		require.Equal(t, 3, got.ItemCount)
		require.InDelta(t, 0.375, got.EstimatedCost, 1e-12)
		require.Len(t, gemini.submits, 1)
		require.Len(t, gemini.submits[0].Items, 3)
		require.Equal(t, []string{"cover_01", "cover_02", "cover_03"}, []string{
			gemini.submits[0].Items[0].CustomID,
			gemini.submits[0].Items[1].CustomID,
			gemini.submits[0].Items[2].CustomID,
		})
		require.Len(t, gemini.submits[0].Items[0].ReferenceImages, 1)
		require.Len(t, repo.items[got.ID], 3)
	})

	t.Run("validates request fields", func(t *testing.T) {
		tests := []struct {
			name   string                                    // scenario display name
			mutate func(*batchimage.BatchImageSubmitRequest) // scenario setup mutation
			want   error                                     // expected observable result
		}{
			{name: "missing_model", mutate: func(r *batchimage.BatchImageSubmitRequest) { r.Model = "" }, want: batchimage.ErrBatchImageInvalidModel},
			{name: "empty_items", mutate: func(r *batchimage.BatchImageSubmitRequest) { r.Items = nil }, want: batchimage.ErrBatchImageInvalidItems},
			{name: "duplicate_custom_ids", mutate: func(r *batchimage.BatchImageSubmitRequest) { r.Items[1].CustomID = r.Items[0].CustomID }, want: batchimage.ErrBatchImageDuplicateCustomIDInRequest},
			{name: "empty_prompt", mutate: func(r *batchimage.BatchImageSubmitRequest) { r.Items[0].Prompt = " " }, want: batchimage.ErrBatchImageInvalidItems},
			{name: "prompt_too_long", mutate: func(r *batchimage.BatchImageSubmitRequest) { r.Items[0].Prompt = strings.Repeat("x", 9) }, want: batchimage.ErrBatchImagePromptTooLong},
			{name: "unsupported_provider", mutate: func(r *batchimage.BatchImageSubmitRequest) { r.Platform = "other" }, want: batchimage.ErrBatchImageUnsupportedProvider},
			{name: "vertex_rejects_2k", mutate: func(r *batchimage.BatchImageSubmitRequest) {
				r.Platform = batchimage.BatchImageProviderVertex
				r.ImageSize = "2K"
			}, want: batchimage.ErrBatchImageInvalidItems},
			{name: "too_many_outputs_per_item", mutate: func(r *batchimage.BatchImageSubmitRequest) {
				r.Items[0].OutputCount = 5
			}, want: batchimage.ErrBatchImageInvalidItems},
			{name: "too_many_reference_images_for_flash", mutate: func(r *batchimage.BatchImageSubmitRequest) {
				r.Model = "gemini-2.5-flash-image"
				r.Items[0].ReferenceImages = []batchimage.BatchImageReferenceInput{
					{MimeType: "image/png", Data: []byte("1")},
					{MimeType: "image/png", Data: []byte("2")},
					{MimeType: "image/png", Data: []byte("3")},
					{MimeType: "image/png", Data: []byte("4")},
				}
			}, want: batchimage.ErrBatchImageTooManyReferenceImages},
			{name: "bad_reference_mime", mutate: func(r *batchimage.BatchImageSubmitRequest) {
				r.Items[0].ReferenceImages = []batchimage.BatchImageReferenceInput{{MimeType: "application/octet-stream", Data: []byte("x")}}
			}, want: batchimage.ErrBatchImageInvalidReferenceImage},
			{name: "reference_requires_data_or_file_uri", mutate: func(r *batchimage.BatchImageSubmitRequest) {
				r.Items[0].ReferenceImages = []batchimage.BatchImageReferenceInput{{MimeType: "image/png"}}
			}, want: batchimage.ErrBatchImageInvalidReferenceImage},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				svc, _, _, _, _, _ := newTestBatchImagePublicService()
				req := validBatchImageSubmitRequest()
				tt.mutate(&req)

				_, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
				require.ErrorIs(t, err, tt.want)
			})
		}
	})

	t.Run("rejects too many items", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		req := validBatchImageSubmitRequest()
		req.Items = append(req.Items, batchimage.BatchImageSubmitItem{CustomID: "too_many", Prompt: "x"})

		_, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageInvalidItems)
	})

	t.Run("rejects too many output images", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		svc.Options.MaxOutputImagesPerJob = 3
		req := validBatchImageSubmitRequest()
		req.Items[0].OutputCount = 2
		req.Items[1].OutputCount = 2

		_, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageTooManyOutputImages)
	})

	t.Run("rejects too many reference images across request", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		svc.Options.MaxReferenceImagesPerJob = 3
		req := validBatchImageSubmitRequest()
		req.Model = "gemini-2.5-flash-image"
		req.Items[0].ReferenceImages = []batchimage.BatchImageReferenceInput{
			{MimeType: "image/png", Data: []byte("1")},
			{MimeType: "image/png", Data: []byte("2")},
		}
		req.Items[1].ReferenceImages = []batchimage.BatchImageReferenceInput{
			{MimeType: "image/png", Data: []byte("3")},
			{MimeType: "image/png", Data: []byte("4")},
		}

		_, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageTooManyReferenceImages)
	})

	t.Run("rejects too much inline reference image data across request", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		svc.Options.MaxReferenceImagesPerJob = 10
		svc.Options.MaxReferenceInlineBytesPerJob = 4
		req := validBatchImageSubmitRequest()
		req.Model = "gemini-2.5-flash-image"
		req.Items[0].ReferenceImages = []batchimage.BatchImageReferenceInput{{MimeType: "image/png", Data: []byte("123")}}
		req.Items[1].ReferenceImages = []batchimage.BatchImageReferenceInput{{MimeType: "image/png", Data: []byte("456")}}

		_, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageReferenceImagesTooLarge)
	})

	t.Run("selects requested platform", func(t *testing.T) {
		svc, _, _, gemini, vertex, _ := newTestBatchImagePublicService()
		req := validBatchImageSubmitRequest()
		req.Platform = batchimage.BatchImageProviderVertex

		got, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.NoError(t, err)
		require.Equal(t, batchimage.BatchImageProviderVertex, got.Platform)
		require.Empty(t, gemini.submits)
		require.Len(t, vertex.submits, 1)
	})

	t.Run("insufficient balance rejects before platform submit", func(t *testing.T) {
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService()
		billing := &fakeBatchImageBillingRepo{err: batchimage.ErrBatchImageInsufficientBalance}
		svc.Funding = nativeTaskFundingFixture(billing)

		_, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageInsufficientBalance)
		require.Empty(t, queue.enqueued)
		require.Empty(t, gemini.submits)
		require.Len(t, billing.reserves, 1)
		require.Empty(t, billing.releases)
		require.Len(t, repo.jobs, 1)
		for _, job := range repo.jobs {
			require.Equal(t, batchimage.BatchImageJobStatusFailed, job.Status)
			require.Equal(t, "INSUFFICIENT_BALANCE", batchimage.BatchImageDerefString(job.LastErrorCode))
			require.NotNil(t, job.UserDeletedAt)
		}
	})

	t.Run("preferred subscription failure rejects before platform submit", func(t *testing.T) {
		tests := []struct {
			name     string // scenario display name
			err      error  // injected operation error
			wantCode string // expected failure category
		}{
			{name: "unavailable", err: apikey.ErrPreferredSubscriptionInvalid, wantCode: "PREFERRED_SUBSCRIPTION_INVALID"},
			{name: "group not allowed", err: apikey.ErrPreferredSubscriptionGroup, wantCode: "PREFERRED_SUBSCRIPTION_GROUP_NOT_ALLOWED"},
			{name: "quota exhausted", err: apikey.ErrPreferredSubscriptionInsufficient, wantCode: "PREFERRED_SUBSCRIPTION_EXHAUSTED"},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService()
				billing := &fakeBatchImageBillingRepo{err: test.err}
				svc.Funding = nativeTaskFundingFixture(billing)
				preferredSubscriptionID := int64(301)
				svc.PreferredSubscription = func(context.Context, int64, int64, *int64) *billingcore.UserSubscription {
					return &billingcore.UserSubscription{ID: preferredSubscriptionID}
				}
				owner := testBatchImageOwner()
				owner.BillingMode = apikey.APIKeyBillingModeSubscription
				owner.PreferredSubscriptionID = &preferredSubscriptionID

				_, err := svc.Submit(ctx, owner, validBatchImageSubmitRequest(), "")

				require.ErrorIs(t, err, test.err)
				require.Empty(t, queue.enqueued)
				require.Empty(t, gemini.submits)
				require.Len(t, billing.reserves, 1)
				require.Len(t, repo.jobs, 1)
				for _, job := range repo.jobs {
					require.Equal(t, batchimage.BatchImageJobStatusFailed, job.Status)
					require.Equal(t, test.wantCode, batchimage.BatchImageDerefString(job.LastErrorCode))
					require.NotNil(t, job.UserDeletedAt)
				}
			})
		}
	})

	t.Run("platform failure marks failed and does not enqueue", func(t *testing.T) {
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService()
		gemini.submitErr = errors.New("projects/secret-platform-job failed")
		billing := testassert.MustType[*fakeBatchImageBillingRepo](taskFixtureBilling(svc))

		_, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageProviderSubmitFailed)
		require.Empty(t, queue.enqueued)
		require.Len(t, billing.reserves, 1)
		require.Len(t, billing.releases, 1)
		require.Equal(t, batchimage.BatchImageReleaseRequestID(billing.reserves[0].Task.ID), billing.releases[0].RequestID)
		require.Len(t, repo.jobs, 1)
		for _, job := range repo.jobs {
			require.Equal(t, batchimage.BatchImageJobStatusFailed, job.Status)
			require.Equal(t, "PROVIDER_SUBMIT_FAILED", batchimage.BatchImageDerefString(job.LastErrorCode))
			require.Equal(t, "upstream platform operation failed", batchimage.BatchImageDerefString(job.LastErrorMessage))
			require.NotNil(t, job.UserDeletedAt)
		}
	})

	t.Run("platform failure with release failure enqueues billing retry", func(t *testing.T) {
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService()
		gemini.submitErr = errors.New("projects/secret-platform-job failed")
		billing := testassert.MustType[*fakeBatchImageBillingRepo](taskFixtureBilling(svc))
		billing.releaseErr = errors.New("billing database timeout")

		_, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageBillingHoldFailed)
		require.Len(t, billing.reserves, 1)
		require.Len(t, billing.releases, 1)
		require.Len(t, repo.jobs, 1)
		for _, job := range repo.jobs {
			require.Equal(t, batchimage.BatchImageJobStatusFailed, job.Status)
			require.Equal(t, "BILLING_RELEASE_FAILED", batchimage.BatchImageDerefString(job.LastErrorCode))
			require.Equal(t, []string{job.BatchID}, queue.enqueued)
		}
	})

	t.Run("queue failure is recorded after platform submit", func(t *testing.T) {
		svc, repo, queue, _, _, _ := newTestBatchImagePublicService()
		queue.err = errors.New("redis unavailable")
		billing := testassert.MustType[*fakeBatchImageBillingRepo](taskFixtureBilling(svc))

		_, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageQueueFailed)
		require.Len(t, billing.reserves, 1)
		require.Empty(t, billing.releases)
		require.Len(t, repo.jobs, 1)
		for _, job := range repo.jobs {
			require.Equal(t, batchimage.BatchImageJobStatusSubmitted, job.Status)
			require.Equal(t, "QUEUE_FAILED", batchimage.BatchImageDerefString(job.LastErrorCode))
			require.Contains(t, repo.events[job.BatchID], "queue_failed")
		}
	})

	t.Run("idempotency returns same batch without platform resubmit", func(t *testing.T) {
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService()
		req := validBatchImageSubmitRequest()
		req.SessionID = batchimage.BatchImageStringPtr("original-session")

		first, err := svc.Submit(ctx, testBatchImageOwner(), req, "client-key")
		require.NoError(t, err)
		req.SessionID = batchimage.BatchImageStringPtr("retry-session")
		second, err := svc.Submit(ctx, testBatchImageOwner(), req, "client-key")
		require.NoError(t, err)

		require.Equal(t, first.ID, second.ID)
		require.Equal(t, "original-session", batchimage.BatchImageDerefString(repo.jobs[first.ID].SessionID))
		require.Len(t, gemini.submits, 1)
		require.Equal(t, []string{first.ID}, queue.enqueued)
	})

	t.Run("idempotency conflict rejects changed request", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		req := validBatchImageSubmitRequest()
		first, err := svc.Submit(ctx, testBatchImageOwner(), req, "client-key")
		require.NoError(t, err)

		req.Items[0].Prompt = "diff"
		second, err := svc.Submit(ctx, testBatchImageOwner(), req, "client-key")
		require.Nil(t, second)
		require.ErrorIs(t, err, batchimage.ErrBatchImageIdempotencyConflict)
		require.NotEmpty(t, first.ID)
	})

	t.Run("public response does not expose internals", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		got, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "")
		require.NoError(t, err)

		body, err := json.Marshal(got)
		require.NoError(t, err)
		requireBatchImagePublicJSONHasNoInternals(t, string(body))
	})
}

// TestBatchImagePublicService_List stores jobs owned by different API keys
// under the same user. The listing exposes only the requested key's batch and
// reports a short page with no more-data indicator.
func TestBatchImagePublicService_List(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, _, _, _ := newTestBatchImagePublicService()
	visibleKeyID := int64(22)
	otherKeyID := int64(23)

	repo.jobs["visible-1"] = &batchimage.BatchImageJob{
		BatchID:   "visible-1",
		UserID:    11,
		APIKeyID:  &visibleKeyID,
		Status:    batchimage.BatchImageJobStatusCompleted,
		Platform:  batchimage.BatchImageProviderVertex,
		Model:     "gemini-3.1-flash-lite-image",
		ItemCount: 1,
		CreatedAt: time.Now(),
	}
	repo.jobs["hidden-other-key"] = &batchimage.BatchImageJob{
		BatchID:   "hidden-other-key",
		UserID:    11,
		APIKeyID:  &otherKeyID,
		Status:    batchimage.BatchImageJobStatusCompleted,
		Platform:  batchimage.BatchImageProviderVertex,
		Model:     "gemini-3.1-flash-lite-image",
		ItemCount: 1,
		CreatedAt: time.Now(),
	}

	got, err := svc.List(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: visibleKeyID}, batchimage.BatchImageJobsQuery{Limit: 20})
	require.NoError(t, err)
	require.Equal(t, "list", got.Object)
	require.Len(t, got.Data, 1)
	require.Equal(t, "visible-1", got.Data[0].ID)
	require.False(t, got.HasMore)
}

// TestBatchImagePublicService_ListModels uses concrete and wildcard provider
// mappings with group and price controls. It checks backend-specific
// catalogue entries, missing-price filtering and group permission rejection
// before exposing available models.
func TestBatchImagePublicService_ListModels(t *testing.T) {
	ctx := context.Background()

	t.Run("requires explicit provider model mapping", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()

		got, err := svc.ListModels(ctx, testBatchImageOwner())
		require.NoError(t, err)
		require.Equal(t, "list", got.Object)
		require.Empty(t, got.Data)
	})

	t.Run("returns priced models from selected provider group", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		groupID := int64(7)
		svc.GroupRepo = batchGroupReader{&publicBatchImageGroupRepo{groups: map[int64]*batchimage.GroupView{
			groupID: {
				ID:                           groupID,
				RateMultiplier:               1,
				AllowBatchImageGeneration:    true,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			},
		}}}
		providerRepo := testassert.MustType[*publicBatchImageProviderRepo](testassert.MustType[*batchProviderFixture](svc.ProviderRepo).source)
		providerRepo.providers = []providercore.Record{testBatchImageMappedProvider(303, capability.ProviderTypeAPIKey, map[string]any{
			"gemini-2.5-flash-image": "gemini-2.5-flash-image",
		})}

		got, err := svc.ListModels(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID})
		require.NoError(t, err)
		require.Equal(t, []batchimage.BatchImagePublicModel{{
			ID:       "gemini-2.5-flash-image",
			Object:   "image.batch.model",
			Platform: batchimage.BatchImageProviderGeminiAPI,
		}}, got.Data)
	})

	t.Run("wildcard mappings enumerate configured targets", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		providerRepo := testassert.MustType[*publicBatchImageProviderRepo](testassert.MustType[*batchProviderFixture](svc.ProviderRepo).source)
		providerRepo.providers = []providercore.Record{testBatchImageMappedProvider(303, capability.ProviderTypeAPIKey, map[string]any{
			"gemini-3.1-*": "gemini-3.1-flash-lite-image",
		})}

		got, err := svc.ListModels(ctx, testBatchImageOwner())
		require.NoError(t, err)
		require.NotEmpty(t, got.Data)
		ids := make([]string, 0, len(got.Data))
		for _, model := range got.Data {
			ids = append(ids, model.ID)
		}
		require.NotContains(t, ids, "gemini-3.1-flash-image")
		require.Contains(t, ids, "gemini-3.1-flash-lite-image")
		require.NotContains(t, ids, "gemini-2.5-flash-image")
	})

	t.Run("filters models without batch image pricing", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		svc.Pricing = &fakeBatchImagePricingResolver{
			unitPrice:     0.25,
			missingModels: map[string]bool{"gemini-3.1-flash-lite-image": true},
		}
		providerRepo := testassert.MustType[*publicBatchImageProviderRepo](testassert.MustType[*batchProviderFixture](svc.ProviderRepo).source)
		providerRepo.providers = []providercore.Record{testBatchImageMappedProvider(303, capability.ProviderTypeAPIKey, map[string]any{
			"gemini-2.5-flash-image":      "gemini-2.5-flash-image",
			"gemini-3.1-flash-lite-image": "gemini-3.1-flash-lite-image",
		})}

		got, err := svc.ListModels(ctx, testBatchImageOwner())
		require.NoError(t, err)
		ids := make([]string, 0, len(got.Data))
		for _, model := range got.Data {
			ids = append(ids, model.ID)
		}
		require.Contains(t, ids, "gemini-2.5-flash-image")
		require.NotContains(t, ids, "gemini-3.1-flash-lite-image")
	})

	t.Run("rejects when group disables batch image", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		groupID := int64(7)
		svc.GroupRepo = batchGroupReader{&publicBatchImageGroupRepo{groups: map[int64]*batchimage.GroupView{
			groupID: {ID: groupID, AllowBatchImageGeneration: false},
		}}}

		_, err := svc.ListModels(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID})
		require.ErrorIs(t, err, batchimage.ErrBatchImageGroupDisabled)
	})
}

// TestBatchImagePublicService_StatusItemsAndCancel queries owner-scoped
// status and filtered result pages before exercising cancellation. It checks
// scrubbed public diagnostics, pending upstream cancellation with held funds,
// terminal replay and public cancellation errors.
func TestBatchImagePublicService_StatusItemsAndCancel(t *testing.T) {
	ctx := context.Background()

	t.Run("status is owner scoped and maps public status", func(t *testing.T) {
		svc, repo, _, _, _, _ := newTestBatchImagePublicService()
		apiKeyID := int64(22)
		providerID := int64(101)
		repo.jobs["imgbatch_status"] = &batchimage.BatchImageJob{
			BatchID:         "imgbatch_status",
			UserID:          11,
			APIKeyID:        &apiKeyID,
			ProviderID:      &providerID,
			Platform:        batchimage.BatchImageProviderGeminiAPI,
			Model:           "gemini-2.5-flash-image",
			Status:          batchimage.BatchImageJobStatusIndexing,
			ProviderJobName: batchimage.BatchImageStringPtr("providers/internal/job"),
			CreatedAt:       time.Now(),
		}

		got, err := svc.Get(ctx, testBatchImageOwner(), "imgbatch_status")
		require.NoError(t, err)
		require.Equal(t, "processing_results", got.Status)
		body, err := json.Marshal(got)
		require.NoError(t, err)
		requireBatchImagePublicJSONHasNoInternals(t, string(body))

		_, err = svc.Get(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 999}, "imgbatch_status")
		require.ErrorIs(t, err, batchimage.ErrBatchImageJobNotFound)
	})

	t.Run("items are filtered paginated and sanitized", func(t *testing.T) {
		svc, repo, _, _, _, _ := newTestBatchImagePublicService()
		apiKeyID := int64(22)
		repo.jobs["imgbatch_items"] = &batchimage.BatchImageJob{
			BatchID:   "imgbatch_items",
			UserID:    11,
			APIKeyID:  &apiKeyID,
			Platform:  batchimage.BatchImageProviderGeminiAPI,
			Model:     "gemini-2.5-flash-image",
			Status:    batchimage.BatchImageJobStatusCompleted,
			CreatedAt: time.Now(),
		}
		sourceObject := "gs://bucket/internal/output.jsonl"
		mime := "image/png"
		ext := "png"
		code := "SAFETY_BLOCKED"
		msg := "blocked in gs://bucket/internal/output.jsonl"
		repo.items["imgbatch_items"] = []batchimage.CreateBatchImageItemParams{
			{JobID: "imgbatch_items", CustomID: "ok_1", Status: batchimage.BatchImageItemStatusSuccess, ProviderSourceObject: &sourceObject, MimeType: &mime, FileExtension: &ext, ImageCount: 1},
			{JobID: "imgbatch_items", CustomID: "bad_1", Status: batchimage.BatchImageItemStatusFailed, ProviderSourceObject: &sourceObject, ErrorCode: &code, ErrorMessage: &msg},
			{JobID: "imgbatch_items", CustomID: "ok_2", Status: batchimage.BatchImageItemStatusSuccess, MimeType: &mime, FileExtension: &ext, ImageCount: 1},
		}

		page, err := svc.ListItems(ctx, testBatchImageOwner(), "imgbatch_items", batchimage.BatchImageItemsQuery{Limit: 1})
		require.NoError(t, err)
		require.True(t, page.HasMore)
		require.Len(t, page.Data, 1)
		require.Equal(t, "ok_1", page.Data[0].CustomID)

		filtered, err := svc.ListItems(ctx, testBatchImageOwner(), "imgbatch_items", batchimage.BatchImageItemsQuery{Status: "failed", Limit: 100})
		require.NoError(t, err)
		require.False(t, filtered.HasMore)
		require.Len(t, filtered.Data, 1)
		require.Equal(t, "failed", filtered.Data[0].Status)
		require.NotNil(t, filtered.Data[0].Error)
		require.Equal(t, "upstream platform operation failed", filtered.Data[0].Error.Message)

		body, err := json.Marshal(filtered)
		require.NoError(t, err)
		requireBatchImagePublicJSONHasNoInternals(t, string(body))
		require.NotContains(t, string(body), "download_url")

		_, err = svc.ListItems(ctx, batchimage.BatchImageOwner{UserID: 12, APIKeyID: 22}, "imgbatch_items", batchimage.BatchImageItemsQuery{})
		require.ErrorIs(t, err, batchimage.ErrBatchImageJobNotFound)
	})

	t.Run("cancel active job calls platform and waits for confirmed terminal state", func(t *testing.T) {
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService()
		apiKeyID := int64(22)
		providerID := int64(101)
		holdAmount := 0.5
		holdID := batchimage.BatchImageHoldRequestID("imgbatch_cancel")
		repo.jobs["imgbatch_cancel"] = &batchimage.BatchImageJob{
			BatchID:         "imgbatch_cancel",
			UserID:          11,
			APIKeyID:        &apiKeyID,
			ProviderID:      &providerID,
			Platform:        batchimage.BatchImageProviderGeminiAPI,
			Model:           "gemini-2.5-flash-image",
			Status:          batchimage.BatchImageJobStatusSubmitted,
			ProviderJobName: batchimage.BatchImageStringPtr("providers/internal/job"),
			EstimatedCost:   holdAmount,
			HoldAmount:      &holdAmount,
			HoldID:          &holdID,
			CreatedAt:       time.Now(),
		}

		got, err := svc.Cancel(ctx, testBatchImageOwner(), "imgbatch_cancel")
		require.NoError(t, err)
		require.Equal(t, "queued", got.Status)
		require.Equal(t, 1, gemini.cancelCount)
		billing := testassert.MustType[*fakeBatchImageBillingRepo](taskFixtureBilling(svc))
		require.Empty(t, billing.releases)
		require.Equal(t, []string{"imgbatch_cancel"}, queue.enqueued)
		require.Equal(t, batchimage.BatchImageJobStatusSubmitted, repo.jobs["imgbatch_cancel"].Status)
		require.Contains(t, repo.events["imgbatch_cancel"], "job_cancel_requested")
	})

	t.Run("cancel terminal job is idempotent", func(t *testing.T) {
		svc, repo, _, gemini, _, _ := newTestBatchImagePublicService()
		apiKeyID := int64(22)
		repo.jobs["imgbatch_done"] = &batchimage.BatchImageJob{
			BatchID:   "imgbatch_done",
			UserID:    11,
			APIKeyID:  &apiKeyID,
			Platform:  batchimage.BatchImageProviderGeminiAPI,
			Model:     "gemini-2.5-flash-image",
			Status:    batchimage.BatchImageJobStatusCompleted,
			CreatedAt: time.Now(),
		}

		got, err := svc.Cancel(ctx, testBatchImageOwner(), "imgbatch_done")
		require.NoError(t, err)
		require.Equal(t, "completed", got.Status)
		require.Zero(t, gemini.cancelCount)
	})

	t.Run("cancel hides platform raw errors behind public error", func(t *testing.T) {
		svc, repo, _, gemini, _, _ := newTestBatchImagePublicService()
		gemini.cancelErr = errors.New("projects/secret-platform-job not found")
		apiKeyID := int64(22)
		providerID := int64(101)
		repo.jobs["imgbatch_cancel_error"] = &batchimage.BatchImageJob{
			BatchID:         "imgbatch_cancel_error",
			UserID:          11,
			APIKeyID:        &apiKeyID,
			ProviderID:      &providerID,
			Platform:        batchimage.BatchImageProviderGeminiAPI,
			Model:           "gemini-2.5-flash-image",
			Status:          batchimage.BatchImageJobStatusSubmitted,
			ProviderJobName: batchimage.BatchImageStringPtr("providers/internal/job"),
			CreatedAt:       time.Now(),
		}

		_, err := svc.Cancel(ctx, testBatchImageOwner(), "imgbatch_cancel_error")
		require.ErrorIs(t, err, batchimage.ErrBatchImageCancelFailed)
		require.Equal(t, "BATCH_IMAGE_CANCEL_FAILED", apperror.Reason(err))
		require.NotContains(t, apperror.Message(err), "projects/")
	})
}

// TestBatchImageServiceAccountErrorCode maps the missing Google
// service-account credential error through public submission translation. It
// checks the exact protocol reason retained for clients needing that
// credential configuration.
func TestBatchImageServiceAccountErrorCode(t *testing.T) {
	err := batchimage.BatchImageProviderSubmitPublicError(batchimage.ErrBatchImageProviderMissingServiceAccount)
	require.Equal(t, "BATCH_IMAGE_PROVIDER_MISSING_SERVICE_ACCOUNT", apperror.Reason(err))
}

// TestBatchImageConfiguredAlias queries a custom image alias and its
// configured target with differing provider allowlists. Both names appear
// when allowed and are filtered when the provider permits another model.
func TestBatchImageConfiguredAlias(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		t.Run(fmt.Sprintf("allowed=%t", allowed), func(t *testing.T) {
			svc, _, _, _, _, _ := newTestBatchImagePublicService()
			repo := testassert.MustType[*publicBatchImageProviderRepo](testassert.MustType[*batchProviderFixture](svc.ProviderRepo).source)
			value := testBatchImageMappedProvider(303, capability.ProviderTypeAPIKey, map[string]any{"public-image": "nano-banana-pro"})
			if !allowed {
				value.Credentials["model_whitelist"] = []string{"other-model"}
			}
			repo.providers = []providercore.Record{value}
			svc.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1}
			svc.Attributes = &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{Lookup: func(model string) modelcatalog.Attributes {
				image := []string{"image"}
				if model == "nano-banana-pro" {
					return modelcatalog.Attributes{OutputModalities: &image}
				}
				return modelcatalog.Attributes{}
			}}}
			result, err := svc.ListModels(context.Background(), testBatchImageOwner())
			require.NoError(t, err)
			ids := []string{}
			for _, model := range result.Data {
				ids = append(ids, model.ID)
			}
			if allowed {
				require.Contains(t, ids, "public-image")
				require.Contains(t, ids, "nano-banana-pro")
			} else {
				require.NotContains(t, ids, "public-image")
				require.NotContains(t, ids, "nano-banana-pro")
			}
		})
	}
}

// GetGroupPolicy counts the group read and delegates to the real pricing
// service. A returned policy receives the fixture's ModelsList, allowing
// catalogue tests to inject concrete names alongside production restriction
// handling.
func (p *batchCataloguePolicyReads) GetGroupPolicy(ctx context.Context, id int64) (*routing.GroupPolicyView, error) {
	p.policyReads++
	policy, err := p.PricingConfigService.GetGroupPolicy(ctx, id)
	if policy != nil {
		policy.ModelsList = p.modelsList
	}
	return policy, err
}

// GetPricingConfigForGroup counts the pricing-source lookup before delegating
// to the configured service.
func (p *batchCataloguePolicyReads) GetPricingConfigForGroup(ctx context.Context, id int64) (*routing.PricingConfig, error) {
	p.pricingReads++
	return p.PricingConfigService.GetPricingConfigForGroup(ctx, id)
}

// GetByIDLite counts reads and returns oldGroup for the first lookup, then
// newGroup for later lookups. The pricing test detects accidental rereads by
// comparing totals and the number of calls.
func (r *changingMediaPricingGroupRepo) GetByIDLite(context.Context, int64) (*batchimage.GroupView, error) {
	r.calls++
	if r.calls == 1 {
		return r.oldGroup, nil
	}
	return r.newGroup, nil
}

// testPtrFloat64 returns fresh pointer storage for an optional fixture price.
func testPtrFloat64(value float64) *float64 { return &value }

// rebindBatchFixtureProviders replaces the record source while retaining the
// public fixture's existing backend registry. It requires the current
// ProviderRepo to be a batchProviderFixture and returns production candidate
// conversion over the new source.
func rebindBatchFixtureProviders(core *batchimage.Public, source batchProvidersFixtureSource) batchimage.ProviderReader {
	return &batchProviderFixture{source: source, registry: testassert.MustType[*batchProviderFixture](core.ProviderRepo).registry}
}

// ListAll returns one outer copy of the configured pricing record in a fresh
// slice. Nested configuration fields remain shared with this fixture, so tests
// can change the retained configuration before later service reads.
func (r *publicPricingConfigFixture) ListAll(context.Context) ([]routing.PricingConfig, error) {
	return []routing.PricingConfig{r.pricingConfig}, nil
}

// GetGroupPlatforms returns the retained platform map and ignores the requested
// group IDs. Callers changing that map also change later fixture reads; absent
// configuration returns the retained nil map successfully.
func (r *publicPricingConfigFixture) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return r.platforms, nil
}

// makePublicPricingConfigFixture stores the price configuration, platform map
// and first supplied policy. With no policy it retains a zero policy, and
// later optional policies are ignored by this fixture.
func makePublicPricingConfigFixture(pricingConfig routing.PricingConfig, platforms map[int64]string, policies ...routing.GroupRoutingPolicy) *publicPricingConfigFixture {
	policy := routing.GroupRoutingPolicy{}
	if len(policies) > 0 {
		policy = policies[0]
	}
	return &publicPricingConfigFixture{pricingConfig: pricingConfig, platforms: platforms, policy: policy}
}

// newPublicPricingConfigFixture constructs the shared routing service over
// this fixture and a group-read callback. Each group read clones the
// configured policy, while warning, clock and pricing-location callbacks use
// production helpers.
func newPublicPricingConfigFixture(repo *publicPricingConfigFixture) *routing.PricingConfigService {
	return routing.NewPricingConfigService(repo, nil, routing.PricingConfigOptions{Warn: slog.Warn, Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation, ReadGroup: func(_ context.Context, id int64) (*routing.Group, error) {
		return &routing.Group{ID: id, RoutingPolicy: repo.policy.Clone()}, nil
	}})
}

// testImageModelPricing creates a wildcard image price card from the supplied
// size tiers. The generated interval slice is new, while each optional price
// pointer remains shared with the map values supplied by the test.
func testImageModelPricing(prices map[string]*float64) []routing.ModelPricingEntry {
	card := routing.ModelPricingEntry{Models: []string{"*"}, BillingMode: routing.BillingModeImage}
	for tier, price := range prices {
		card.Intervals = append(card.Intervals, routing.PricingInterval{TierLabel: tier, PerRequestPrice: price})
	}
	return []routing.ModelPricingEntry{card}
}

// newTestBatchImagePublicService builds shared recording dependencies and a
// public service with configured request limits. Tight item and prompt caps
// keep validation cases small, while two registered backends expose provider
// selection.
func newTestBatchImagePublicService() (*batchimage.Public, *fakeBatchImageRepository, *publicBatchImageQueue, *publicBatchImageProvider, *publicBatchImageProvider, *fakeBatchImageAuthCacheInvalidator) {
	repo := newFakeBatchImageRepository()
	queue := &publicBatchImageQueue{}
	gemini := &publicBatchImageProvider{name: batchimage.BatchImageProviderGeminiAPI}
	vertex := &publicBatchImageProvider{name: batchimage.BatchImageProviderVertex}
	authCache := &fakeBatchImageAuthCacheInvalidator{}
	svc := newBatchPublicFixture(repo,
		&publicBatchImageProviderRepo{providers: []providercore.Record{testBatchImageProvider(101, capability.ProviderTypeAPIKey), testBatchImageProvider(202, capability.ProviderTypeServiceAccount)}}, nil, nil, nil, queue,
		batchimage.NewRegistry[batchimageprovider.BatchImageProvider](
			gemini,
			vertex,
		),
		&fakeBatchImagePricingResolver{unitPrice: 0.25},
		&fakeBatchImageBillingRepo{},
		authCache,
		&config.Config{BatchImage: config.BatchImageConfig{
			MaxItemsPerJob:          2,
			MaxPromptBytesPerItem:   8,
			DefaultResponseMimeType: "image/png",
			DefaultImageSize:        "1K",
		}})

	return svc, repo, queue, gemini, vertex, authCache
}

// InvalidateAuthCacheByKey records the API key spelling requested for fixture
// invalidation.
func (f *fakeBatchImageAuthCacheInvalidator) InvalidateAuthCacheByKey(_ context.Context, key string) {
	f.keys = append(f.keys, key)
}

// InvalidateAuthCacheByUserID records the payer identity requested for
// fixture invalidation.
func (f *fakeBatchImageAuthCacheInvalidator) InvalidateAuthCacheByUserID(_ context.Context, userID int64) {
	f.userIDs = append(f.userIDs, userID)
}

// InvalidateAuthCacheByGroupID records the group identity requested for
// fixture invalidation.
func (f *fakeBatchImageAuthCacheInvalidator) InvalidateAuthCacheByGroupID(_ context.Context, groupID int64) {
	f.groupIDs = append(f.groupIDs, groupID)
}

// testBatchImageMappedProvider adds the supplied model mapping to a schedulable
// provider fixture. Its credential map retains that mapping, including nil, so
// later test edits to the supplied entries affect candidate preparation.
func testBatchImageMappedProvider(id int64, providerType string, mapping map[string]any) providercore.Record {
	provider := testBatchImageProvider(id, providerType)
	provider.Credentials["model_mapping"] = mapping
	return provider
}

// BenchmarkBatchImageConfiguredCatalogue queries sixty-five providers with
// 186 configured names each. It measures allocations and repeated catalogue
// work while asserting that only the fixture's image-capable request name
// remains visible.
func BenchmarkBatchImageConfiguredCatalogue(b *testing.B) {
	service, _, _, _, _, _ := newTestBatchImagePublicService()
	rows := &publicBatchImageProviderRepo{}
	whitelist := make([]any, 186)
	mapping := map[string]any{}
	for i := range whitelist {
		model := fmt.Sprintf("probe-model-%03d", i)
		if i == 0 {
			model = "gemini-2.5-flash-image"
		}
		whitelist[i] = model
		mapping[model] = model
	}
	for i := range 65 {
		value := testBatchImageMappedProvider(int64(i+1), "apikey", mapping)
		value.Credentials["model_whitelist"] = whitelist
		rows.providers = append(rows.providers, value)
	}
	service.ProviderRepo = rebindBatchFixtureProviders(service, rows)
	service.ProviderExists = func(name string) bool { return name == batchimage.BatchImageProviderGeminiAPI }
	image, text := []string{"image"}, []string{"text"}
	service.ModelOutputModalities = func(model string) *[]string {
		if model == "gemini-2.5-flash-image" {
			return &image
		}
		return &text
	}
	service.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result, err := service.ListModels(context.Background(), testBatchImageOwner())
		if err != nil {
			b.Fatal(err)
		}
		if len(result.Data) != 1 {
			b.Fatalf("models=%d", len(result.Data))
		}
	}
}

// TestBatchImageCustomListDeclaresUnknownModality adds one concrete
// group-listed model whose output modality is unknown but priced. The
// catalogue returns entries for that declared name, protecting the
// configured-name admission path.
func TestBatchImageCustomListDeclaresUnknownModality(t *testing.T) {
	service, _, _, _, _, _ := newTestBatchImagePublicService()
	owner := testBatchImageOwner()
	value := testBatchImageMappedProvider(303, "apikey", nil)
	service.ProviderRepo = rebindBatchFixtureProviders(service, &publicBatchImageProviderRepo{providers: []providercore.Record{value}})
	service.PricingConfigService = &batchCataloguePolicyReads{PricingConfigService: newPublicPricingConfigFixture(makePublicPricingConfigFixture(routing.PricingConfig{ID: 1, Status: "active", GroupIDs: []int64{*owner.GroupID}}, nil, routing.GroupRoutingPolicy{Enabled: true})), modelsList: []string{"custom-image"}}
	service.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1}
	result, err := service.ListModels(context.Background(), owner)
	require.NoError(t, err)
	require.Empty(t, result.Data)
}
