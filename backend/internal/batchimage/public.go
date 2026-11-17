package batchimage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// public.go validates and submits batch-image jobs, lists owned resources, and
// requests cancellation.
// A submitted batch fixes its expanded custom IDs, routed model stages,
// execution provider and funding settings before contacting the upstream
// platform. The public API checks group permission, prices the normalized
// output workload and creates its persistent record before reserving funds.
// Provider results, later cancellation and settlement continue using those
// stored choices.
//
// Submission spans database, funds, upstream execution and Redis publication
// as separate operations. Failure before a usable upstream name releases the
// hold, while a failed local save after provider creation attempts
// cancellation and input cleanup. A submission heartbeat protects slow
// uploads from stale recovery. Idempotent replay compares the normalized
// request hash and can retry queue publication for a submitted job. Submitted
// and running cancellation requests retain funds until polling confirms their
// outcome; indexing visits continue reading saved results.

// Submission defaults define workload caps, omitted output preferences and
// funding multipliers before normalized items are saved or sent to a
// provider.
const (
	// PlatformGemini selects the provider routing platform shared by both
	// backend implementations during group-scoped candidate lookup.
	PlatformGemini = "gemini"

	// BillingModelSourceUpstream selects the final execution model for a group's
	// upstream-stage restriction check after provider mapping is resolved.
	BillingModelSourceUpstream = routing.BillingModelSourceUpstream

	DefaultBatchImageMaxItems           = 200
	DefaultBatchImageMaxOutputImages    = 200
	DefaultBatchImageMaxOutputCount     = 4
	DefaultBatchImageMaxPromptChars     = 8000
	DefaultBatchImageResponseMime       = "image/png"
	DefaultBatchImageImageSize          = "1K"
	DefaultBatchImageDiscountMultiplier = 0.5
	DefaultBatchImageHoldMultiplier     = 0.6

	// Inline reference limits bound each decoded attachment and the repeated
	// workload sent upstream, with job totals enforced after output expansion.
	MaxBatchImageReferenceImageBytes    = 10 * 1024 * 1024
	DefaultBatchImageMaxReferenceImages = 1000
	DefaultBatchImageMaxReferenceBytes  = 128 * 1024 * 1024
)

type (
	// GroupMappingResult retains the shared routing mapping and chosen
	// billing-model source.
	GroupMappingResult = routing.GroupMappingResult
	// GroupMappingReader supplies group mapping, restriction and pricing-source
	// information for batch selection.
	GroupMappingReader interface {
		// ResolveGroupMapping returns the selected group mapping and billing-model
		// stages for a requested model. Submit retains trace information around
		// this result and uses the mapped spelling for provider selection.
		ResolveGroupMapping(context.Context, int64, string) GroupMappingResult
		// IsModelRestricted reports whether the group rejects the supplied model
		// spelling. Its caller selects the restriction stage first, so requested,
		// group-mapped and upstream checks use their own resolved model identities.
		IsModelRestricted(context.Context, int64, string) bool
		// GetGroupPolicy reads the group model policy used during one catalogue
		// query. Callers retain that view across candidate evaluation so mapping
		// and restriction decisions use the same returned rules.
		GetGroupPolicy(context.Context, int64) (*routing.GroupPolicyView, error)
		// GetPricingConfigForGroup reads the billing-model source linked to this
		// group. ListModels retains that source for its query and keeps the
		// group-mapped default when this optional lookup fails or returns no
		// config.
		GetPricingConfigForGroup(context.Context, int64) (*routing.PricingConfig, error)
	}
)

// CandidateRules exposes scheduling, model mapping and billing-rate decisions
// for one provider snapshot.
type CandidateRules interface {
	// IsSchedulable reports whether this provider snapshot is available for
	// selection.
	IsSchedulable() bool
	// IsModelSupported checks the supplied routing-model spelling through
	// prepared provider rules. Selection invokes it before resolving the final
	// upstream model, keeping provider whitelist handling with the provider's
	// own model rules.
	IsModelSupported(string) bool
	// GetModelMapping returns the prepared provider model mapping used by
	// catalogue candidates.
	GetModelMapping() map[string]string
	// ResolveMappedModel returns one provider mapping target and whether a rule
	// matched the supplied spelling. Catalogue resolution keeps the input when
	// no usable mapping is selected, preserving its group-routing stage.
	ResolveMappedModel(string) (string, bool)
	// BillingRateMultiplier returns the provider price scaling used in submitted
	// batch estimates.
	BillingRateMultiplier() float64
}

// ExecutionProvider binds provider operations and a registry name to the
// selected execution snapshot.
type ExecutionProvider interface {
	BoundProvider // inherited provider operation set
	// Name returns the registered backend name saved as the submitted job
	// platform.
	Name() string
}

// Candidate combines a prepared provider snapshot with model and execution
// selection callbacks.
type Candidate struct {
	// AttributeTarget optionally replaces the default Gemini catalogue target.
	// modelAttributeTarget still assigns the image_batches client protocol after
	// that callback returns.
	AttributeTarget func(string) modelrules.Target // final model attribute target builder
	ID              int64                          // database record identity
	Priority        int                            // provider selection priority
	CandidateRules                                 // prepared provider selection rules
	// CanSubmit, ResolveUpstream and Bind are supplied together by candidate
	// preparation. Catalogue and submission share protocol, credential and storage
	// admission before resolving models or binding the selected backend.
	CanSubmit       func(string) bool                    // new job backend eligibility
	ResolveUpstream func(context.Context, string) string // final execution model resolver
	Bind            func(string) ExecutionProvider       // provider snapshot operation binder
}

// submissionCandidate pairs a provider snapshot with its eligible batch backend.
// The same provider may appear under multiple adapters when an adapter supports
// its credentials; each entry retains the backend used for binding operations.
type submissionCandidate struct {
	Candidate        // prepared provider selection snapshot
	backend   string // eligible batch backend name
}

// ProviderReader supplies batch candidates by bound provider identity or
// routing membership.
type ProviderReader interface {
	// GetByID reads the provider candidate bound to an existing job identity.
	// Existing-job cancellation uses that binding rather than choosing another
	// provider from the owner's current group membership.
	GetByID(context.Context, int64) (*Candidate, error)
	// ListSchedulableByPlatform returns provider candidates in the requested
	// routing platform.
	ListSchedulableByPlatform(context.Context, string) ([]Candidate, error)
	// ListSchedulableByGroupIDAndPlatform returns candidates associated with the
	// selected positive group and platform. Public selection applies model,
	// protocol and backend checks after this membership lookup before binding
	// one provider.
	ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]Candidate, error)
}

// GroupView supplies batch permission and submission multipliers for one
// routing group.
type GroupView struct {
	ID                        int64 // database record identity
	AllowBatchImageGeneration bool  // group batch submission permission
	// RateMultiplier, BatchImageDiscountMultiplier and BatchImageHoldMultiplier
	// are read from one view for submission estimates. ResolvePricingSnapshot
	// clamps negative base and discount rates to zero and uses its default when
	// the stored hold rate is negative.
	// RateMultiplier: base group price scale; BatchImageDiscountMultiplier:
	// group batch discount scale; BatchImageHoldMultiplier: group batch
	// reservation scale
	RateMultiplier, BatchImageDiscountMultiplier, BatchImageHoldMultiplier float64
}

// GroupReader reads the group view used for permission and pricing snapshots.
type GroupReader interface {
	// GetByIDLite reads permission and batch pricing multipliers for a selected
	// group. Missing or unavailable data prevents public submission and
	// catalogue access; callers retain the returned view through their rate
	// calculations.
	GetByIDLite(context.Context, int64) (*GroupView, error)
}

// BatchImagePriceInput identifies the billing model, selected group and
// requested output size.
type BatchImagePriceInput struct {
	Model   string // selected billing model
	GroupID *int64 // selected routing group
	// Group carries the submission view supplied by ResolvePricingSnapshot for
	// an injected price lookup. The configured Pricing wrapper currently
	// forwards Model and GroupID to billing and does not read this view.
	Group     *GroupView // optional selected group view
	ImageSize string     // requested output size
}

// ImagePricer supplies the per-image base price used by submission and
// catalogue filtering.
type ImagePricer interface {
	// BatchImageUnitPrice resolves the per-image base price for a billing model,
	// group and output size. Zero is a valid configured price, while missing
	// pricing rejects submission and filters catalogue entries.
	BatchImageUnitPrice(context.Context, BatchImagePriceInput) (float64, error)
}

// PublicOptions configures request admission, byte limits and omitted output
// preferences.
type PublicOptions struct {
	// StaleActiveAfterSeconds: silent submission window seconds;
	// MaxItemsPerJobDefault: submitted prompt entry cap; MaxOutputImagesPerJob:
	// expanded output request cap; MaxOutputImagesPerItem: per prompt repetition
	// cap; MaxPromptCharsPerItem: prompt UTF-8 byte cap;
	// MaxReferenceImagesPerJob: expanded reference attachment cap;
	// MaxReferenceInlineBytesPerJob: expanded inline reference byte cap
	StaleActiveAfterSeconds, MaxItemsPerJobDefault, MaxOutputImagesPerJob, MaxOutputImagesPerItem, MaxPromptCharsPerItem, MaxReferenceImagesPerJob, MaxReferenceInlineBytesPerJob int
	// DefaultResponseMimeType: omitted output MIME preference; DefaultImageSize:
	// omitted output size preference
	DefaultResponseMimeType, DefaultImageSize string
}

// Public coordinates owner operations, request validation, provider selection
// and submission-time reservations.
type Public struct {
	// Attributes takes precedence over ModelOutputModalities for catalogue
	// lookups when configured. The callback evaluates final upstream models when
	// the attribute service is absent.
	Attributes *routing.ModelAttributeService // optional shared model attribute resolver
	// ModelOutputModalities supplies final-model output information when
	// Attributes is absent. Nil output information means unknown and excludes
	// the route from image listings until a declaration becomes available.
	ModelOutputModalities func(string) *[]string            // optional final model modality lookup
	Now                   func() time.Time                  // injected current time
	Repo                  BatchImageRepository              // persistent batch state store
	ProviderRepo          ProviderReader                    // prepared provider candidate reader
	PricingConfigService  GroupMappingReader                // group routing and pricing reader
	GroupRepo             GroupReader                       // group permission and multiplier reader
	UserGroupRateRepo     BatchImageUserGroupRateRepository // optional payer wallet multiplier reader
	Queue                 BatchImageQueue                   // optional batch visit publisher
	Pricing               ImagePricer                       // per image base price resolver
	Funding               Funding                           // shared task funding adapter
	Options               PublicOptions                     // public request admission settings
	// ProviderExists checks the registered backends during catalogue queries,
	// provider selection and existing-job cancellation. Configure this callback
	// before enabling operations because those call sites invoke it directly.
	ProviderExists func(string) bool            // registered backend presence check
	InvalidateAuth func(context.Context, int64) // optional payer cache invalidation
	Observe        func(string, ...any)         // optional public diagnostic callback
	// ClientModel, WithModelTrace and RegisterModel are construction-time
	// callbacks used by Submit and provider selection. Configure them before
	// serving requests because these call sites invoke them directly.
	ClientModel    func(context.Context) string                                         // original client model reader
	WithModelTrace func(context.Context, GroupMappingResult, string) GroupMappingResult // group model trace recorder
	RegisterModel  func(context.Context, string)                                        // selected execution model recorder
	// AutoSubscription, PreferredSubscription and SubscriptionMultiplier resolve
	// source-specific estimate rates. Construction supplies the callbacks before
	// funding modes requiring them can submit a pricing snapshot.
	// AutoSubscription supplies the subscription used for an automatic estimate.
	AutoSubscription func(context.Context, int64, *int64) *billing.UserSubscription
	// PreferredSubscription supplies the selected subscription used for an estimate.
	PreferredSubscription func(context.Context, int64, int64, *int64) *billing.UserSubscription
	// SubscriptionMultiplier selects the estimate rate for the resolved subscription.
	SubscriptionMultiplier func(context.Context, BatchImageOwner, *GroupView, float64, *billing.UserSubscription) float64
}

// warn sends a public-operation diagnostic through the configured observation
// callback.
func (s *Public) warn(event string, values ...any) {
	if s.Observe != nil {
		s.Observe(event, values...)
	}
}

// mappedCandidateModel returns the trimmed mapped target selected by this
// candidate. A nil candidate returns empty text, and an unmatched or blank
// mapping leaves the supplied routing model unchanged.
func mappedCandidateModel(c *Candidate, m string) string {
	if c == nil {
		return ""
	}
	value, matched := c.ResolveMappedModel(m)
	if !matched || strings.TrimSpace(value) == "" {
		// A missing or blank mapped target cannot supply a callable model; retain
		// the routing spelling for catalogue restrictions and pricing-stage
		// selection.
		return m
	}
	return strings.TrimSpace(value)
}

// Submit validates and persists a normalized batch, reserves its funding and
// submits it to the selected backend. Group permission, model attributes,
// provider selection and pricing are checked before job creation. Idempotent
// replay requires the same normalized hash and can retry enqueueing an
// already submitted job. Retries supply a stable TaskName because omitted
// titles use the current clock and participate in that fingerprint.
//
// The saved job precedes reserve and pending item creation. Uploading state
// and its heartbeat cover the provider call; the caller joins that heartbeat
// before saving returned resources. Pre-upstream failures release funds,
// while a failed local save after upstream creation attempts orphan
// cancellation and input cleanup. References are persisted before queue
// publication, so a queue error retains the submitted job and reservation for
// a later retry.
// @project-doc docs/domains/batch_image_jobs.md#job_lifecycle
func (s *Public) Submit(ctx context.Context, owner BatchImageOwner, req BatchImageSubmitRequest, idempotencyKey string) (*BatchImagePublicBatch, error) {
	if s == nil || s.Repo == nil || s.ProviderRepo == nil {
		return nil, ErrBatchImageDisabled
	}
	if routing.ModelAttributesFromContext(ctx) == nil {
		ctx = routing.WithModelAttributes(ctx, s.Attributes)
	}
	attributeSession := routing.ModelAttributesFromContext(ctx)
	normalized, err := s.ValidateSubmitRequest(req)
	if err != nil {
		return nil, err
	}
	// Apply the same AllowBatchImageGeneration check as ListModels so submission
	// enforces the group permission advertised by the model catalogue.
	if err := s.EnsureGroupAllowsBatchImage(ctx, owner.GroupID); err != nil {
		return nil, err
	}
	requestedModel := normalized.Model
	if clientModel := s.ClientModel(ctx); strings.TrimSpace(clientModel) != "" {
		requestedModel = strings.TrimSpace(clientModel)
	}
	requestHash := HashBatchImageSubmitRequest(normalized)
	// The client spelling can differ from the normalized model. Include the
	// selected group so equal normalized content cannot replay into another group.
	if requestedModel != normalized.Model {
		requestHash = HashCompositeBatchImageSubmitRequest(normalized, owner.GroupID)
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey != "" {
		existing, err := s.Repo.GetBatchImageJobByIdempotencyKey(ctx, owner.UserID, owner.APIKeyID, idempotencyKey)
		if err == nil {
			if BatchImageDerefString(existing.RequestHash) != requestHash {
				return nil, ErrBatchImageIdempotencyConflict
			}
			// An earlier submit can save its provider job before Redis publication
			// fails; replay the queue publication so the persisted task can resume
			// processing.
			if existing.Status == BatchImageJobStatusSubmitted && s.Queue != nil {
				if enqueueErr := s.Queue.Enqueue(ctx, existing.BatchID); enqueueErr != nil && !errors.Is(enqueueErr, ErrBatchImageAlreadyQueued) {
					_ = s.Repo.RecordBatchImageJobSubmitFailure(ctx, existing.BatchID, "QUEUE_FAILED", SanitizeBatchImagePublicMessage(enqueueErr.Error()), false)
					return nil, ErrBatchImageQueueFailed
				}
			}
			return BatchImageJobToPublic(existing), nil
		}
		if !errors.Is(err, ErrBatchImageJobNotFound) {
			return nil, err
		}
	}

	groupMapping, routingModel, err := s.ResolveBatchImageGroupModel(ctx, owner.GroupID, normalized.Model)
	if err != nil {
		return nil, err
	}
	platform, provider, upstreamModel, err := s.SelectProviderAndProvider(
		ctx,
		owner,
		normalized.Platform,
		routingModel,
		groupMapping,
	)
	if err != nil {
		return nil, err
	}
	if err := s.validateModelAttributes(ctx, attributeSession, owner.GroupID, provider, upstreamModel, normalized); err != nil {
		return nil, err
	}
	pricingRequest := normalized
	pricingRequest.Model = BatchImagePricingModel(groupMapping, normalized.Model, routingModel, upstreamModel)
	pricingSnapshot, err := s.ResolvePricingSnapshot(ctx, owner, pricingRequest, platform.Name(), provider)
	if err != nil {
		return nil, err
	}
	parentBatchID := BatchImageOptionalStringPtr(normalized.ParentBatchID)
	if parentBatchID != nil {
		parent, parentErr := s.Repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID, owner.APIKeyID, *parentBatchID)
		if parentErr != nil {
			return nil, parentErr
		}
		if parent.ParentBatchID != nil && strings.TrimSpace(*parent.ParentBatchID) != "" {
			parentBatchID = BatchImageOptionalStringPtr(*parent.ParentBatchID)
		}
	}
	batchID, err := NewBatchImageID()
	if err != nil {
		return nil, err
	}
	apiKeyID := owner.APIKeyID
	providerID := provider.ID
	holdID := BatchImageHoldRequestID(batchID)
	holdAmount := pricingSnapshot.HoldAmount
	// Save model, ownership and price snapshots before reserving funds; the
	// billing participant needs this job row when its reservation transaction
	// records allocations.
	job, err := s.Repo.CreateBatchImageJob(ctx, CreateBatchImageJobParams{
		BatchID:                    batchID,
		UserID:                     owner.UserID,
		BillingUserID:              owner.BillingUserID,
		TeamID:                     owner.TeamID,
		APIKeyID:                   &apiKeyID,
		ProviderID:                 &providerID,
		GroupID:                    owner.GroupID,
		BillingMode:                owner.BillingMode,
		PreferredSubscriptionID:    CloneInt64Ptr(owner.PreferredSubscriptionID),
		Platform:                   platform.Name(),
		Model:                      upstreamModel,
		RequestedModel:             requestedModel,
		InternalModel:              normalized.Model,
		TaskName:                   normalized.TaskName,
		ParentBatchID:              parentBatchID,
		Status:                     BatchImageJobStatusCreated,
		ItemCount:                  len(normalized.Items),
		EstimatedCost:              pricingSnapshot.EstimatedCost,
		HoldAmount:                 &holdAmount,
		BaseUnitPrice:              pricingSnapshot.BaseUnitPrice,
		GroupRateMultiplier:        pricingSnapshot.GroupRateMultiplier,
		SubscriptionRateMultiplier: pricingSnapshot.SubscriptionRateMultiplier,
		BalanceRateMultiplier:      pricingSnapshot.BalanceRateMultiplier,
		PlanGroupRateEnabled:       pricingSnapshot.PlanGroupRateEnabled,
		ProviderRateMultiplier:     pricingSnapshot.ProviderRateMultiplier,
		BatchDiscountMultiplier:    pricingSnapshot.BatchDiscountMultiplier,
		HoldMultiplier:             pricingSnapshot.HoldMultiplier,
		BillableUnitPrice:          pricingSnapshot.BillableUnitPrice,
		HoldUnitPrice:              pricingSnapshot.HoldUnitPrice,
		PricingSnapshotVersion:     3,
		Currency:                   "USD",
		HoldID:                     &holdID,
		IdempotencyKey:             BatchImageOptionalStringPtr(idempotencyKey),
		RequestHash:                BatchImageStringPtr(requestHash),
		SessionID:                  normalized.SessionID,
	})
	if err != nil {
		return nil, err
	}
	telemetry.RecordRelatedRequest(ctx, telemetry.RequestRecord{RequestID: telemetry.StableRequestID(BatchImageCaptureRequestID(job.BatchID)), State: "running", Model: job.Model, StartedAt: job.CreatedAt, Aliases: []telemetry.RequestAlias{{Kind: "task", Value: job.BatchID}, {Kind: "billing", Value: BatchImageCaptureRequestID(job.BatchID)}}})
	// The job now exists before funding begins; require reserve success before
	// creating provider inputs that can incur upstream execution costs.
	if err := s.Funding.Reserve(ctx, job, owner.GroupID, requestHash); err != nil {
		// Reserve rejection prevents upstream work. Diagnostic and hiding writes
		// are best effort while the funding category remains the submit result.
		code := BatchImageBillingHoldFailureCode(err)
		_ = s.Repo.RecordBatchImageJobSubmitFailure(ctx, job.BatchID, code, SanitizeBatchImagePublicMessage(err.Error()), true)
		s.HidePreUpstreamSubmitFailure(ctx, owner, job)
		return nil, err
	}
	s.InvalidateAuthCache(ctx, owner.EffectiveBillingUserID())
	// Item persistence is still before upstream creation. Refund the hold before
	// recording and hiding a failed submission that cannot provide result identities.
	if err := s.CreatePendingItems(ctx, job.BatchID, requestHash, normalized.Items); err != nil {
		return nil, s.compensateFailedSubmit(ctx, owner, job, requestHash, func() (string, string, error) {
			return "ITEM_CREATE_FAILED", SanitizeBatchImagePublicMessage(err.Error()), ErrBatchImageQueueFailed
		})
	}

	input := BatchImageInput{
		BatchID:          job.BatchID,
		Model:            upstreamModel,
		DisplayName:      job.BatchID,
		ResponseMimeType: normalized.ResponseMimeType,
		AspectRatio:      normalized.AspectRatio,
		ImageSize:        normalized.ImageSize,
		Metadata:         normalized.Metadata,
		Items:            make([]BatchImageInputItem, 0, len(normalized.Items)),
	}
	for _, item := range normalized.Items {
		refs := make([]BatchImageReference, 0, len(item.ReferenceImages))
		for _, ref := range item.ReferenceImages {
			refs = append(refs, BatchImageReference(ref))
		}
		input.Items = append(input.Items, BatchImageInputItem{
			CustomID:        item.CustomID,
			Prompt:          item.Prompt,
			ReferenceImages: refs,
		})
	}

	// Provider submission can outlive a stale recovery interval. Enter uploading
	// and refresh updated_at while the call is in progress so the conditional
	// recovery update can reject recently heartbeated jobs.
	if err := s.Repo.TransitionBatchImageJobStatus(ctx, job.BatchID, BatchImageJobStatusUploading, BatchImageTransitionOptions{
		EventType:    "upload_started",
		EventPayload: map[string]any{"batch_id": job.BatchID},
	}); err != nil {
		if releaseErr := s.ReleaseFailedSubmitHold(ctx, job, requestHash); releaseErr != nil {
			return nil, releaseErr
		}
		// Concurrent cancellation can reject uploading. Keep that current state so
		// failure recording cannot overwrite the outcome already selected by its caller.
		if !errors.Is(err, ErrBatchImageInvalidTransition) {
			_ = s.Repo.RecordBatchImageJobSubmitFailure(ctx, job.BatchID, "UPLOAD_TRANSITION_FAILED", SanitizeBatchImagePublicMessage(err.Error()), true)
			s.HidePreUpstreamSubmitFailure(ctx, owner, job)
		}
		return nil, err
	}
	job.Status = BatchImageJobStatusUploading

	// Slow provider creation needs submission activity while it runs; cancel and
	// join this heartbeat before saving the returned job or handling its
	// failure.
	hbCtx, hbCancel := context.WithCancel(ctx)
	hbDone := make(chan struct{})
	go s.RunSubmitHeartbeat(hbCtx, job.BatchID, hbDone)
	providerJob, err := platform.Submit(ctx, job, input)
	hbCancel()
	<-hbDone
	// An error or unusable creation result cannot be queued for normal polling.
	// Refund before recording and hiding failure; a failed refund schedules recovery.
	if err != nil {
		return nil, s.compensateFailedSubmit(ctx, owner, job, requestHash, func() (string, string, error) {
			publicErr := BatchImageProviderSubmitPublicError(err)
			reason := BatchImageProviderSubmitRecordCode(publicErr)
			return reason, SanitizeBatchImagePublicMessage(err.Error()), publicErr
		})
	}
	if providerJob == nil || strings.TrimSpace(providerJob.ProviderJobName) == "" {
		return nil, s.compensateFailedSubmit(ctx, owner, job, requestHash, func() (string, string, error) {
			return "PROVIDER_SUBMIT_FAILED", "platform job name missing", ErrBatchImageProviderSubmitFailed
		})
	}

	// Persist the newly created upstream identity and references before queue
	// publication so later worker visits can locate that same provider task.
	if err := s.Repo.UpdateBatchImageJobProviderSubmit(ctx, UpdateBatchImageJobProviderSubmitParams{
		BatchID:           job.BatchID,
		ProviderJobName:   providerJob.ProviderJobName,
		ProviderInputRef:  providerJob.ProviderInputRef,
		ProviderOutputRef: providerJob.ProviderOutputRef,
		GCSInputURI:       BatchImageGCSRef(platform.Name(), providerJob.ProviderInputRef),
		GCSOutputURI:      BatchImageGCSRef(platform.Name(), providerJob.ProviderOutputRef),
		EventPayload:      map[string]any{"platform": platform.Name()},
	}); err != nil {
		// Recovery may already have failed and refunded this job. Cancel the newly created
		// upstream job and clean up its inputs to limit orphaned provider costs.
		s.AbortOrphanProviderJob(ctx, platform, job, providerJob)
		return nil, err
	}

	// Upstream resources are saved before queue publication. Keep submitted
	// state and the hold when publication fails so an idempotent retry can wake it.
	if s.Queue != nil {
		if err := s.Queue.Enqueue(ctx, job.BatchID); err != nil && !errors.Is(err, ErrBatchImageAlreadyQueued) {
			_ = s.Repo.RecordBatchImageJobSubmitFailure(ctx, job.BatchID, "QUEUE_FAILED", SanitizeBatchImagePublicMessage(err.Error()), false)
			return nil, ErrBatchImageQueueFailed
		}
	}

	created, err := s.Repo.GetBatchImageJobByBatchID(ctx, job.BatchID)
	if err != nil {
		return nil, err
	}
	return BatchImageJobToPublic(created), nil
}

// compensateFailedSubmit refunds a reserved job after item creation or upstream
// submission cannot continue. The caller supplies failureDetails to choose its
// diagnostic and public error after the refund succeeds; release failure skips
// that callback and leaves the retry record visible.
//
// After a successful refund, the diagnostic write precedes owner hiding. Both
// writes are best effort, so their errors cannot replace the caller-selected
// submission failure. The refund uses the original request hash, and persistent
// operations reuse the supplied submission context.
func (s *Public) compensateFailedSubmit(
	ctx context.Context,
	owner BatchImageOwner,
	job *BatchImageJob,
	requestHash string,
	failureDetails func() (code, message string, publicErr error),
) error {
	// Failed releases already attempt a billing diagnostic and retry publication.
	// Return before owner hiding so the pending refund remains visible to callers.
	if releaseErr := s.ReleaseFailedSubmitHold(ctx, job, requestHash); releaseErr != nil {
		return releaseErr
	}
	code, message, publicErr := failureDetails()
	// Once funds are returned, attempt both state marking and owner hiding even
	// when recording the diagnostic fails.
	_ = s.Repo.RecordBatchImageJobSubmitFailure(ctx, job.BatchID, code, message, true)
	s.HidePreUpstreamSubmitFailure(ctx, owner, job)
	return publicErr
}

// BatchImageBillingHoldFailureCode selects a stable job diagnostic for a
// funding reservation failure.
func BatchImageBillingHoldFailureCode(err error) string {
	switch {
	case errors.Is(err, ErrBatchImageInsufficientBalance):
		return "INSUFFICIENT_BALANCE"
	case errors.Is(err, billing.ErrPreferredSubscriptionInvalid):
		return "PREFERRED_SUBSCRIPTION_INVALID"
	case errors.Is(err, billing.ErrPreferredSubscriptionGroup):
		return "PREFERRED_SUBSCRIPTION_GROUP_NOT_ALLOWED"
	case errors.Is(err, billing.ErrPreferredSubscriptionInsufficient):
		return "PREFERRED_SUBSCRIPTION_EXHAUSTED"
	default:
		return "BILLING_HOLD_FAILED"
	}
}

// ReleaseFailedSubmitHold returns the reservation after submission cannot
// continue and invalidates the payer cache on success. The caller supplies
// the original request fingerprint, shared by cancellation and recovery
// releases. Funding errors are translated into the hold-failed result.
//
// A failed release first attempts to record a failed job before enqueueing a
// normal processor retry. That ordering gives ReleaseTerminalHold an eligible
// state when the worker reads it. Diagnostic persistence is best effort, and
// enqueue errors are observed through EnqueueBillingRetry while the original
// release failure still reaches the submit caller.
func (s *Public) ReleaseFailedSubmitHold(ctx context.Context, job *BatchImageJob, requestHash string) error {
	// A failed refund needs a terminal record before its retry can use
	// ReleaseTerminalHold. Attempt that write even though its diagnostic error
	// cannot replace the refund failure.
	if err := s.Funding.Release(ctx, job, requestHash); err != nil {
		_ = s.Repo.RecordBatchImageJobSubmitFailure(ctx, job.BatchID, "BILLING_RELEASE_FAILED", SanitizeBatchImagePublicMessage(err.Error()), true)
		s.EnqueueBillingRetry(ctx, job.BatchID)
		return ErrBatchImageBillingHoldFailed
	}
	s.InvalidateAuthCache(ctx, BillingUserID(job))
	return nil
}

// RunSubmitHeartbeat refreshes persisted activity at SubmitHeartbeatInterval
// until its context is cancelled, then closes done. Submit owns this
// goroutine and waits for its completion after the provider call; heartbeat
// failures are observed and polling continues.
func (s *Public) RunSubmitHeartbeat(ctx context.Context, batchID string, done chan<- struct{}) {
	defer close(done)
	interval := s.SubmitHeartbeatInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// A failed heartbeat does not confirm submission has stopped. Observe the
			// failure and keep trying while the provider call and context remain active.
			if err := s.Repo.TouchBatchImageJobSubmitting(ctx, batchID); err != nil && ctx.Err() == nil {
				s.warn("batch_image.submit_heartbeat_failed",
					"batch_id", batchID,
					"error", err,
				)
			}
		}
	}
}

// SubmitHeartbeatInterval chooses one third of the stale-submission window,
// using ten minutes when no positive setting is supplied. A fifteen-second
// floor limits write frequency even when the configured stale window is
// shorter.
func (s *Public) SubmitHeartbeatInterval() time.Duration {
	staleAfter := 10 * time.Minute
	if s != nil && s.Options.StaleActiveAfterSeconds > 0 {
		staleAfter = time.Duration(s.Options.StaleActiveAfterSeconds) * time.Second
	}
	// Three opportunities fit the stale window, subject to the fifteen-second
	// write-frequency floor that can exceed a very short configured window.
	interval := max(staleAfter/3, 15*time.Second)
	return interval
}

// AbortOrphanProviderJob attempts cancellation and input cleanup after a
// provider job was created but its local submission save failed. It copies
// the local job and attaches the resource references returned by that
// provider call. Missing dependencies or records return immediately.
//
// Cancellation runs before input cleanup, and either failure is observed
// while the other attempt still proceeds. The final audit append records the
// attempted abort using the same caller context. These best-effort operations
// limit orphan resources but can still fail after request cancellation or an
// unavailable provider; the failed save remains the caller's result.
func (s *Public) AbortOrphanProviderJob(ctx context.Context, platform ExecutionProvider, job *BatchImageJob, providerJob *BatchProviderJob) {
	if s == nil || platform == nil || job == nil || providerJob == nil {
		return
	}
	orphan := *job
	orphan.ProviderJobName = BatchImageOptionalStringPtr(providerJob.ProviderJobName)
	orphan.ProviderInputRef = BatchImageOptionalStringPtr(providerJob.ProviderInputRef)
	orphan.GCSInputURI = BatchImageOptionalStringPtr(BatchImageGCSRef(platform.Name(), providerJob.ProviderInputRef))
	// Attempt cancellation before removing input resources, but continue cleanup
	// even if cancellation fails so the newly returned references remain actionable.
	if err := platform.Cancel(ctx, &orphan); err != nil {
		s.warn("batch_image.orphan_provider_job_cancel_failed",
			"batch_id", job.BatchID,
			"platform", platform.Name(),
			"error", err,
		)
	}
	if err := platform.Cleanup(ctx, &orphan, CleanupTargetInput); err != nil {
		s.warn("batch_image.orphan_provider_job_cleanup_failed",
			"batch_id", job.BatchID,
			"platform", platform.Name(),
			"error", err,
		)
	}
	// The abort event describes attempted provider operations. An audit failure
	// is observed while the submission-save error remains the caller's result.
	if err := s.Repo.AppendBatchImageEvent(ctx, job.BatchID, "provider_job_aborted_after_submit", map[string]any{
		"batch_id": job.BatchID,
		"platform": platform.Name(),
	}); err != nil {
		s.warn("batch_image.orphan_provider_job_event_failed",
			"batch_id", job.BatchID,
			"error", err,
		)
	}
}

// CreatePendingItems stores the expanded custom IDs, shared request hash and
// byte-capped prompt previews before upstream submission. Missing service,
// repository or items returns immediately; the caller releases reserved
// funding if this write fails.
func (s *Public) CreatePendingItems(ctx context.Context, batchID, requestHash string, items []BatchImageSubmitItem) error {
	if s == nil || s.Repo == nil || len(items) == 0 {
		return nil
	}
	params := make([]CreateBatchImageItemParams, 0, len(items))
	for _, item := range items {
		preview := TruncateBatchImageMessage(item.Prompt, s.MaxPromptChars())
		params = append(params, CreateBatchImageItemParams{
			JobID:         batchID,
			CustomID:      item.CustomID,
			Status:        BatchImageItemStatusPending,
			RequestHash:   BatchImageStringPtr(requestHash),
			PromptPreview: BatchImageStringPtr(preview),
			ImageCount:    0,
		})
	}
	return s.Repo.BulkCreateBatchImageItems(ctx, params)
}

// EnqueueBillingRetry schedules a processor visit to return a failed
// submission's hold. Missing queues return immediately and duplicate
// membership is accepted; other failures are observed and recorded through a
// best-effort audit append.
func (s *Public) EnqueueBillingRetry(ctx context.Context, batchID string) {
	if s == nil || s.Queue == nil {
		return
	}
	// Existing membership already supplies a refund visit. Other queue failures
	// are observed because the caller still needs to return its original funding error.
	if err := s.Queue.Enqueue(ctx, batchID); err != nil && !errors.Is(err, ErrBatchImageAlreadyQueued) {
		s.warn("batch_image.billing_retry_enqueue_failed",
			"batch_id", batchID,
			"error", err,
		)
		// This audit is diagnostic after queue failure; it cannot publish a worker
		// visit or resolve the held funds, so its failure is reported separately.
		if eventErr := s.Repo.AppendBatchImageEvent(ctx, batchID, "billing_retry_enqueue_failed", map[string]any{
			"batch_id": batchID,
			"error":    SanitizeBatchImagePublicMessage(err.Error()),
		}); eventErr != nil {
			s.warn("batch_image.billing_retry_event_failed",
				"batch_id", batchID,
				"error", eventErr,
			)
		}
	}
}

// HidePreUpstreamSubmitFailure attempts to hide a failed job whose
// provider-name pointer is absent. Its metadata remains available to internal
// recovery; ownership or repository rejection is observed while the original
// submission failure is returned.
func (s *Public) HidePreUpstreamSubmitFailure(ctx context.Context, owner BatchImageOwner, job *BatchImageJob) {
	if s == nil || s.Repo == nil || job == nil || job.ProviderJobName != nil {
		return
	}
	// Keep failed metadata reachable to internal recovery while hiding it from
	// owner listings. A rejected hide is observed after the original submit failure.
	if err := s.Repo.MarkBatchImageJobUserDeleted(ctx, owner.UserID, owner.APIKeyID, job.BatchID, s.now()); err != nil {
		s.warn("batch_image.hide_pre_upstream_failure_failed",
			"batch_id", job.BatchID,
			"error", err,
		)
	}
}

// Get reads a batch after the repository checks both submitting user and API
// key. The returned value contains public status and model data, while
// missing or mismatched ownership uses the repository not-found result.
func (s *Public) Get(ctx context.Context, owner BatchImageOwner, batchID string) (*BatchImagePublicBatch, error) {
	job, err := s.Repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID, owner.APIKeyID, batchID)
	if err != nil {
		return nil, err
	}
	return BatchImageJobToPublic(job), nil
}

// List returns an owner-scoped page of public batches with normalized status,
// title, download and creation filters. Invalid downloaded values reject the
// query; invalid limits use twenty, and HasMore reports a page filled to its
// limit.
func (s *Public) List(ctx context.Context, owner BatchImageOwner, query BatchImageJobsQuery) (*BatchImagePublicListResponse, error) {
	filter := BatchImageJobFilter{Limit: query.Limit, Offset: ParseBatchImageCursor(query.Cursor), ExcludeDeleted: true}
	filter.TaskNameLike = strings.TrimSpace(query.TaskName)
	switch strings.TrimSpace(query.Status) {
	case "", "all":
	case "queued":
		filter.Status = BatchImageJobStatusSubmitted
	case "processing_results":
		filter.Status = BatchImageJobStatusIndexing
	case "completed":
		filter.Status = BatchImageJobStatusCompleted
	case "failed":
		filter.Status = BatchImageJobStatusFailed
	case "cancelled":
		filter.Status = BatchImageJobStatusCancelled
	case "output_deleted":
		filter.Status = BatchImageJobStatusOutputDeleted
	default:
		filter.Status = strings.TrimSpace(query.Status)
	}
	switch strings.TrimSpace(strings.ToLower(query.Downloaded)) {
	case "", "all":
	case "true", "1", "yes", "downloaded":
		downloaded := true
		filter.Downloaded = &downloaded
	case "false", "0", "no", "not_downloaded":
		downloaded := false
		filter.Downloaded = &downloaded
	default:
		return nil, ErrBatchImageInvalidItems
	}
	if from := ParseBatchImageListTime(query.From); from != nil {
		filter.CreatedAfter = from
	}
	if to := ParseBatchImageListTime(query.To); to != nil {
		filter.CreatedBefore = to
	}
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 20
	}
	jobs, err := s.Repo.ListBatchImageJobsForOwner(ctx, owner.UserID, owner.APIKeyID, filter)
	if err != nil {
		return nil, err
	}
	data := make([]*BatchImagePublicBatch, 0, len(jobs))
	for _, job := range jobs {
		data = append(data, BatchImageJobToPublic(job))
	}
	return &BatchImagePublicListResponse{
		Object:  "list",
		Data:    data,
		HasMore: len(data) == filter.Limit,
	}, nil
}

// MarkDownloaded authorizes the user and API key before recording the current
// time as a download acknowledgement. Repository rules retain the first saved
// acknowledgement; this method does not check whether generation is complete.
func (s *Public) MarkDownloaded(ctx context.Context, owner BatchImageOwner, batchID string) error {
	job, err := s.Repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID, owner.APIKeyID, batchID)
	if err != nil {
		return err
	}
	return s.Repo.MarkBatchImageDownloaded(ctx, job.BatchID, s.now())
}

// DeleteRecord authorizes the owner and attempts to hide a job once provider
// processing is done, including settling. The repository applies its own
// deletion state guard, so a settling record can still return
// ErrBatchImageRecordDeleteNotReady.
func (s *Public) DeleteRecord(ctx context.Context, owner BatchImageOwner, batchID string) error {
	job, err := s.Repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID, owner.APIKeyID, batchID)
	if err != nil {
		return err
	}
	if !IsBatchImageProcessorDoneStatus(job.Status) {
		return ErrBatchImageRecordDeleteNotReady
	}
	return s.Repo.MarkBatchImageJobUserDeleted(ctx, owner.UserID, owner.APIKeyID, job.BatchID, s.now())
}

// ListModels returns priced concrete request names supported by the owner's
// group and available batch backends. Its configured reader fetches group policy
// and billing source at most once per catalogue call. Each candidate passes
// group mapping, provider mapping, stage-specific restrictions and per-image
// pricing.
//
// Final output modalities must explicitly include image. Unknown modalities
// exclude the route even when configuration and image prices are present;
// attribute-read errors stop listing. Results are deduplicated within
// each backend and sorted by request name. Catalogue and submission share
// protocol, credential, scheduling and storage eligibility before model checks.
func (s *Public) ListModels(ctx context.Context, owner BatchImageOwner) (*BatchImagePublicModelsResponse, error) {
	if s == nil || s.Repo == nil || s.ProviderRepo == nil {
		return nil, ErrBatchImageDisabled
	}
	if s.Pricing == nil {
		return nil, ErrBatchImageSettlementPricingMissing
	}
	if err := s.EnsureGroupAllowsBatchImage(ctx, owner.GroupID); err != nil {
		return nil, err
	}

	var candidates []string
	var policy *routing.GroupPolicyView
	billingMapping := GroupMappingResult{BillingModelSource: routing.BillingModelSourceGroupMapped}
	if s.PricingConfigService != nil && owner.GroupID != nil {
		var err error
		policy, err = s.PricingConfigService.GetGroupPolicy(ctx, *owner.GroupID)
		if err != nil {
			return nil, err
		}
		// Keep group-mapped pricing as the default when optional config lookup
		// fails; each catalogue entry still needs its resolved per-image price
		// before admission.
		if config, err := s.PricingConfigService.GetPricingConfigForGroup(ctx, *owner.GroupID); err == nil && config != nil {
			billingMapping.PricingConfigID = config.ID
			billingMapping.BillingModelSource = config.BillingModelSource
		}
	}
	if policy != nil {
		candidates = append(candidates, policy.AllowedModels...)
		candidates = append(candidates, policy.ModelsList...)
		for source, target := range policy.ModelMapping {
			candidates = append(candidates, source, target)
		}
	}
	// Several names can map to one upstream model; reuse its modalities within this query.
	outputs := make(map[string]*[]string)
	attributeSession := s.Attributes.NewSession()
	modelsByProvider := make(map[string]map[string]struct{})
	providers, err := s.submissionCandidates(ctx, owner.GroupID, "")
	if err != nil {
		return nil, err
	}
	for _, provider := range providers {
		providerName := provider.backend
		for _, model := range BatchImageModelsFromProviderMapping(&provider.Candidate, candidates...) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			routingModel := strings.TrimSpace(policy.ResolveModel(model))
			if routingModel == "" {
				routingModel = model
			}
			upstreamModel := mappedCandidateModel(&provider.Candidate, routingModel)
			restrictionModel := routing.ModelForRestriction(policy.RestrictionSource(), model, routingModel)
			if policy.RestrictionSource() == routing.BillingModelSourceUpstream {
				restrictionModel = upstreamModel
			}
			if policy.IsModelRestricted(restrictionModel) || !provider.IsModelSupported(routingModel) {
				continue
			}
			modalities, found := outputs[upstreamModel]
			if s.Attributes != nil {
				target := provider.modelAttributeTarget(upstreamModel)
				var groupID int64
				if owner.GroupID != nil {
					groupID = *owner.GroupID
				}
				resolved, err := attributeSession.Resolve(ctx, groupID, target)
				if err == nil {
					modalities = resolved.Attributes.OutputModalities
				} else {
					return nil, err
				}
			} else if !found && s.ModelOutputModalities != nil {
				modalities = s.ModelOutputModalities(upstreamModel)
				outputs[upstreamModel] = modalities
			}
			if modalities == nil || !slices.Contains(*modalities, "image") {
				continue
			}
			mapping := billingMapping
			mapping.MappedModel = routingModel
			pricingModel := BatchImagePricingModel(mapping, model, routingModel, upstreamModel)
			// An image-capable mapping can still lack an applicable image price; omit
			// that catalogue entry before clients choose an unpriceable batch model.
			if _, err := s.Pricing.BatchImageUnitPrice(ctx, BatchImagePriceInput{Model: pricingModel, GroupID: owner.GroupID, ImageSize: "1K"}); err != nil {
				continue
			}
			if modelsByProvider[providerName] == nil {
				modelsByProvider[providerName] = make(map[string]struct{})
			}
			modelsByProvider[providerName][model] = struct{}{}
		}
	}

	out := make([]BatchImagePublicModel, 0)
	for _, providerName := range BatchImageProviderSelectionOrder("") {
		models := make([]string, 0, len(modelsByProvider[providerName]))
		for model := range modelsByProvider[providerName] {
			models = append(models, model)
		}
		sort.Strings(models)
		for _, model := range models {
			out = append(out, BatchImagePublicModel{
				ID:       model,
				Object:   "image.batch.model",
				Platform: providerName,
			})
		}
	}
	return &BatchImagePublicModelsResponse{Object: "list", Data: out}, nil
}

// ListItems authorizes the batch owner and returns a normalized
// status-filtered item page. Succeeded and success select stored successful
// items, invalid filters reject the query, and invalid limits use one
// hundred; HasMore reports a full page.
func (s *Public) ListItems(ctx context.Context, owner BatchImageOwner, batchID string, query BatchImageItemsQuery) (*BatchImagePublicItemsResponse, error) {
	filter := BatchImageItemFilter{Limit: query.Limit, Offset: ParseBatchImageCursor(query.Cursor)}
	switch strings.TrimSpace(query.Status) {
	case "", "all":
	case "succeeded", "success":
		filter.Status = BatchImageItemStatusSuccess
	case "pending":
		filter.Status = BatchImageItemStatusPending
	case "failed":
		filter.Status = BatchImageItemStatusFailed
	default:
		return nil, ErrBatchImageInvalidItems
	}
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 100
	}
	items, err := s.Repo.ListBatchImageItemsForOwner(ctx, owner.UserID, owner.APIKeyID, batchID, filter)
	if err != nil {
		return nil, err
	}
	data := make([]BatchImagePublicItem, 0, len(items))
	for _, item := range items {
		data = append(data, BatchImageItemToPublic(item))
	}
	return &BatchImagePublicItemsResponse{
		Object:  "list",
		Data:    data,
		HasMore: len(data) == filter.Limit,
	}, nil
}

// Cancel authorizes the owner and requests cancellation according to the
// job's stored progress. Processor-done jobs return their current public state;
// failed and cancelled jobs also retry reservation release. Settling belongs to
// funding completion and returns its saved state.
//
// A saved provider name sends cancellation to the bound backend and requests a
// queue visit. Submitted and running visits poll for the outcome, but indexing
// visits reopen saved output directly and can still proceed to capture. A job
// with no saved provider name is marked cancelled before refund; failed release
// is enqueued so a later terminal visit can finish that cleanup.
func (s *Public) Cancel(ctx context.Context, owner BatchImageOwner, batchID string) (*BatchImagePublicBatch, error) {
	job, err := s.Repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID, owner.APIKeyID, batchID)
	if err != nil {
		return nil, err
	}
	if IsBatchImageProcessorDoneStatus(job.Status) {
		if job.Status == BatchImageJobStatusFailed || job.Status == BatchImageJobStatusCancelled {
			if err := s.Funding.Release(ctx, job, BatchImageDerefString(job.RequestHash)); err != nil {
				s.EnqueueBillingRetry(ctx, job.BatchID)
				return nil, ErrBatchImageCancelFailed
			}
			s.InvalidateAuthCache(ctx, owner.EffectiveBillingUserID())
		}
		return BatchImageJobToPublic(job), nil
	}
	if job.ProviderJobName != nil && strings.TrimSpace(*job.ProviderJobName) != "" {
		if !s.ProviderExists(job.Platform) {
			return nil, ErrBatchImageUnsupportedProvider
		}
		if job.ProviderID == nil {
			return nil, ErrBatchImageCancelFailed
		}
		provider, err := s.ProviderRepo.GetByID(ctx, *job.ProviderID)
		if err != nil {
			return nil, ErrBatchImageCancelFailed
		}
		if err := provider.Bind(job.Platform).Cancel(ctx, job); err != nil {
			return nil, ErrBatchImageCancelFailed
		}
		// Provider cancellation has already been requested. Observe a failed audit
		// independently so queue publication can still arrange the next owned visit.
		if eventErr := s.Repo.AppendBatchImageEvent(ctx, job.BatchID, "job_cancel_requested", map[string]any{"batch_id": job.BatchID}); eventErr != nil {
			s.warn("batch_image.cancel_event_failed",
				"batch_id", job.BatchID,
				"error", eventErr,
			)
		}
		if s.Queue != nil {
			if err := s.Queue.Enqueue(ctx, job.BatchID); err != nil && !errors.Is(err, ErrBatchImageAlreadyQueued) {
				return nil, ErrBatchImageCancelFailed
			}
		}
		updated, err := s.Repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID, owner.APIKeyID, batchID)
		if err != nil {
			return nil, err
		}
		return BatchImageJobToPublic(updated), nil
	}
	// With no upstream binding to poll, publish cancellation before refund. A
	// later terminal visit can finish release if this funding attempt fails.
	if err := s.Repo.TransitionBatchImageJobStatus(ctx, job.BatchID, BatchImageJobStatusCancelled, BatchImageTransitionOptions{
		EventType:    "job_cancelled",
		EventPayload: map[string]any{"batch_id": job.BatchID},
	}); err != nil {
		return nil, err
	}
	if err := s.Funding.Release(ctx, job, BatchImageDerefString(job.RequestHash)); err != nil {
		s.EnqueueBillingRetry(ctx, job.BatchID)
		return nil, ErrBatchImageCancelFailed
	}
	s.InvalidateAuthCache(ctx, owner.EffectiveBillingUserID())
	updated, err := s.Repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID, owner.APIKeyID, batchID)
	if err != nil {
		return nil, err
	}
	return BatchImageJobToPublic(updated), nil
}

// ValidateSubmitRequest normalizes a batch and expands OutputCount
// repetitions into independent custom IDs. It supplies a title, MIME type,
// size and missing IDs, requires 1K output, and rejects empty prompts or
// workloads above configured caps. Prompt and title limits measure bytes.
//
// The request value retains shared item storage, so trimming original items
// can modify the caller's slice before the returned request receives a new
// expanded slice. Reference byte buffers remain shared across repeats.
// Reference counts and inline byte totals are multiplied by each output
// count, and collisions are checked after ID suffix expansion. The resulting
// title, metadata and expanded items feed the submission fingerprint.
func (s *Public) ValidateSubmitRequest(req BatchImageSubmitRequest) (BatchImageSubmitRequest, error) {
	req.Model = strings.TrimSpace(req.Model)
	req.TaskName = strings.TrimSpace(req.TaskName)
	req.ParentBatchID = strings.TrimSpace(req.ParentBatchID)
	req.Platform = strings.TrimSpace(req.Platform)
	req.ResponseMimeType = strings.TrimSpace(req.ResponseMimeType)
	req.AspectRatio = strings.TrimSpace(req.AspectRatio)
	req.ImageSize = strings.TrimSpace(req.ImageSize)
	if req.Model == "" {
		return req, ErrBatchImageInvalidModel
	}
	if req.TaskName == "" {
		req.TaskName = DefaultBatchImageTaskName(s.now())
	}
	// Byte-cap long task titles before they enter persistent records and the
	// normalized request fingerprint.
	if len(req.TaskName) > 255 {
		req.TaskName = TruncateBatchImageMessage(req.TaskName, 255)
	}
	if req.Platform != "" && !IsSupportedBatchImageProvider(req.Platform) {
		return req, ErrBatchImageUnsupportedProvider
	}
	if len(req.Items) == 0 {
		return req, ErrBatchImageInvalidItems
	}
	MaxItems := s.MaxItems()
	if len(req.Items) > MaxItems {
		return req, ErrBatchImageInvalidItems
	}
	if req.ResponseMimeType == "" {
		req.ResponseMimeType = s.DefaultResponseMimeType()
	}
	if req.ImageSize == "" {
		req.ImageSize = s.DefaultImageSize()
	}
	// Case-insensitive 1K preferences share one normalized spelling, keeping
	// size-dependent pricing and request fingerprints consistent across valid retries.
	if !strings.EqualFold(req.ImageSize, DefaultBatchImageImageSize) {
		return req, ErrBatchImageInvalidItems
	}
	req.ImageSize = DefaultBatchImageImageSize
	req.Metadata = SanitizeBatchImageMetadata(req.Metadata)

	seen := make(map[string]struct{}, len(req.Items))
	totalReferenceImages := 0
	totalInlineReferenceBytes := 0
	totalOutputImages := 0
	expandedItems := make([]BatchImageSubmitItem, 0, len(req.Items))
	for i := range req.Items {
		req.Items[i].CustomID = strings.TrimSpace(req.Items[i].CustomID)
		if req.Items[i].CustomID == "" {
			req.Items[i].CustomID = fmt.Sprintf("item_%06d", i+1)
		}
		outputCount := req.Items[i].OutputCount
		if outputCount == 0 {
			outputCount = 1
		}
		if outputCount < 1 || outputCount > s.MaxOutputImagesPerItem() {
			return req, ErrBatchImageInvalidItems
		}
		totalOutputImages += outputCount
		if totalOutputImages > s.MaxOutputImagesPerJob() {
			return req, ErrBatchImageTooManyOutputImages
		}
		req.Items[i].Prompt = strings.TrimSpace(req.Items[i].Prompt)
		if req.Items[i].Prompt == "" {
			return req, ErrBatchImageInvalidItems
		}
		// The prompt budget measures storage bytes, so multibyte text consumes more
		// of the configured limit than the same number of ASCII characters.
		if len(req.Items[i].Prompt) > s.MaxPromptChars() {
			return req, ErrBatchImagePromptTooLong
		}
		referenceCount, inlineReferenceBytes, err := NormalizeBatchImageReferenceInputs(&req.Items[i])
		if err != nil {
			return req, err
		}
		// Each repeated output sends the same references again; charge expanded
		// attachment and byte totals against the whole-job caps before generating
		// those request IDs.
		totalReferenceImages += referenceCount * outputCount
		if totalReferenceImages > s.MaxReferenceImagesPerJob() {
			return req, ErrBatchImageTooManyReferenceImages
		}
		totalInlineReferenceBytes += inlineReferenceBytes * outputCount
		if totalInlineReferenceBytes > s.MaxReferenceInlineBytesPerJob() {
			return req, ErrBatchImageReferenceImagesTooLarge
		}
		// Expand before checking uniqueness: a repeated "cover" can collide with a
		// caller-supplied "cover_01", making provider results ambiguous after submission.
		for repeatIndex := 1; repeatIndex <= outputCount; repeatIndex++ {
			expanded := req.Items[i]
			expanded.OutputCount = 0
			if outputCount > 1 {
				expanded.CustomID = fmt.Sprintf("%s_%0*d", req.Items[i].CustomID, BatchImageRepeatSuffixWidth(outputCount), repeatIndex)
			}
			if _, ok := seen[expanded.CustomID]; ok {
				return req, ErrBatchImageDuplicateCustomIDInRequest
			}
			seen[expanded.CustomID] = struct{}{}
			expandedItems = append(expandedItems, expanded)
		}
	}
	req.Items = expandedItems
	return req, nil
}

// NormalizeBatchImageReferenceInputs validates and replaces an item's
// reference slice with normalized values, retaining shared byte buffers. Each
// reference needs an accepted image MIME and exactly one inline or gs://
// source; the result counts attachments and inline bytes.
func NormalizeBatchImageReferenceInputs(item *BatchImageSubmitItem) (int, int, error) {
	if item == nil || len(item.ReferenceImages) == 0 {
		return 0, 0, nil
	}
	out := make([]BatchImageReferenceInput, 0, len(item.ReferenceImages))
	inlineBytes := 0
	for _, ref := range item.ReferenceImages {
		// Bound reference labels before provider input and hashing so optional
		// identifying text cannot enlarge the normalized request indefinitely.
		ref.ID = TruncateBatchImageMessage(strings.TrimSpace(ref.ID), 80)
		ref.Type = TruncateBatchImageMessage(strings.TrimSpace(ref.Type), 40)
		ref.MimeType = NormalizeBatchImageReferenceMimeType(ref.MimeType)
		ref.FileURI = strings.TrimSpace(ref.FileURI)
		if ref.MimeType == "" {
			return 0, 0, ErrBatchImageInvalidReferenceImage
		}
		if len(ref.Data) == 0 && ref.FileURI == "" {
			return 0, 0, ErrBatchImageInvalidReferenceImage
		}
		if len(ref.Data) > 0 && ref.FileURI != "" {
			return 0, 0, ErrBatchImageInvalidReferenceImage
		}
		// Reject oversized decoded attachments before repeated-work byte totals
		// are calculated for the entire batch.
		if len(ref.Data) > MaxBatchImageReferenceImageBytes {
			return 0, 0, ErrBatchImageInvalidReferenceImage
		}
		if ref.FileURI != "" && !strings.HasPrefix(ref.FileURI, "gs://") {
			return 0, 0, ErrBatchImageInvalidReferenceImage
		}
		inlineBytes += len(ref.Data)
		out = append(out, ref)
	}
	item.ReferenceImages = out
	return len(out), inlineBytes, nil
}

// BatchImageRepeatSuffixWidth returns the decimal width used for expanded
// output IDs. Counts below ten use two digits, making repeated custom IDs
// readable and consistent with the public submission examples.
func BatchImageRepeatSuffixWidth(count int) int {
	if count < 10 {
		return 2
	}
	return len(strconv.Itoa(count))
}

// ResolveBatchImageGroupModel applies group mapping and the selected model
// restriction stage while retaining trace information. Callers supply a nonnil
// service and WithModelTrace callback. An absent group or mapping service keeps
// the requested model; an applicable restriction rejects provider selection.
func (s *Public) ResolveBatchImageGroupModel(ctx context.Context, groupID *int64, requestedModel string) (GroupMappingResult, string, error) {
	mapping := s.WithModelTrace(ctx, GroupMappingResult{MappedModel: requestedModel}, requestedModel)
	if s == nil || s.PricingConfigService == nil || groupID == nil || *groupID <= 0 {
		return mapping, requestedModel, nil
	}
	mapping = s.WithModelTrace(ctx, s.PricingConfigService.ResolveGroupMapping(ctx, *groupID, requestedModel), requestedModel)
	routingModel := strings.TrimSpace(mapping.MappedModel)
	if routingModel == "" {
		routingModel = requestedModel
	}
	billingModel := routing.ModelForRestriction(mapping.RestrictionModelSource, requestedModel, routingModel)
	if billingModel != "" && s.PricingConfigService.IsModelRestricted(ctx, *groupID, billingModel) {
		return mapping, routingModel, ErrBatchImageNoProviderAvailable
	}
	return mapping, routingModel, nil
}

// BatchImagePricingModel selects the billing model stage through the shared
// routing pricing rule.
func BatchImagePricingModel(mapping GroupMappingResult, requestedModel, groupMappedModel, upstreamModel string) string {
	return routing.BillingModelForPrice(mapping, requestedModel, groupMappedModel, upstreamModel)
}

// SelectProviderAndProvider returns a bound execution backend, candidate and
// final upstream model for the requested route. An explicit backend narrows
// selection; otherwise both backends participate in one ascending priority
// and provider-ID order.
//
// Catalogue and submission share candidate admission before checking each
// routed model. ResolveUpstream supplies the final execution model, which is
// checked against upstream-stage restrictions and image-output attributes.
// The selected model is registered for tracing before binding the provider
// snapshot. Rejected models allow the next candidate to participate; absence
// of any acceptable route returns the capability or provider error.
func (s *Public) SelectProviderAndProvider(
	ctx context.Context,
	owner BatchImageOwner,
	requestedProvider, routingModel string,
	mapping GroupMappingResult,
) (ExecutionProvider, *Candidate, string, error) {
	session := routing.ModelAttributesFromContext(ctx)
	if session == nil {
		session = s.Attributes.NewSession()
	}
	providers, err := s.submissionCandidates(ctx, owner.GroupID, requestedProvider)
	if err != nil {
		return nil, nil, "", err
	}
	var capabilityErr error
	for _, provider := range providers {
		if !provider.IsModelSupported(routingModel) {
			continue
		}
		upstreamModel := strings.TrimSpace(provider.ResolveUpstream(ctx, routingModel))
		if upstreamModel == "" {
			continue
		}
		if owner.GroupID != nil && *owner.GroupID > 0 && s.PricingConfigService != nil &&
			mapping.RestrictionModelSource == BillingModelSourceUpstream &&
			s.PricingConfigService.IsModelRestricted(ctx, *owner.GroupID, upstreamModel) {
			continue
		}
		var groupID int64
		if owner.GroupID != nil {
			groupID = *owner.GroupID
		}
		attributes, err := session.Resolve(ctx, groupID, provider.modelAttributeTarget(upstreamModel))
		if err != nil {
			return nil, nil, "", err
		}
		if err := routing.RequireImageOutput(attributes.Attributes); err != nil {
			capabilityErr = err
			continue
		}
		s.RegisterModel(ctx, upstreamModel)
		return provider.Bind(provider.backend), &provider.Candidate, upstreamModel, nil
	}
	if capabilityErr != nil {
		return nil, nil, "", capabilityErr
	}
	return nil, nil, "", ErrBatchImageNoProviderAvailable
}

// submissionCandidates lists group-bound providers that can create batch jobs.
// Both adapters share the Gemini provider pool; requestedBackend narrows adapter
// admission, and an empty value considers every registered batch backend.
//
// Protocol, credential, scheduling and storage checks precede model discovery
// and submission side effects. Eligible adapters remain attached to each provider
// while sorting by ascending numeric priority and provider ID, so authentication
// type cannot outrank an administrator's configured priority. Model checks remain
// with the caller because a catalogue evaluates several model names per provider.
func (s *Public) submissionCandidates(ctx context.Context, groupID *int64, requestedBackend string) ([]submissionCandidate, error) {
	providers, err := s.ListCandidateProviders(ctx, groupID, PlatformGemini)
	if err != nil {
		return nil, err
	}
	backends := BatchImageProviderSelectionOrder(requestedBackend)
	candidates := make([]submissionCandidate, 0, len(providers))
	for _, provider := range providers {
		if !provider.IsSchedulable() {
			continue
		}
		for _, backend := range backends {
			if s.ProviderExists(backend) && provider.CanSubmit(backend) {
				candidates = append(candidates, submissionCandidate{Candidate: provider, backend: backend})
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		return candidates[i].ID < candidates[j].ID
	})
	return candidates, nil
}

// ListCandidateProviders requests provider candidates for a positive bound
// group and platform. A missing repository or group returns the
// unavailable-provider result, so batch submission never widens selection to
// a global provider pool.
func (s *Public) ListCandidateProviders(ctx context.Context, groupID *int64, platform string) ([]Candidate, error) {
	if s.ProviderRepo == nil {
		return nil, ErrBatchImageNoProviderAvailable
	}
	if groupID != nil && *groupID > 0 {
		return s.ProviderRepo.ListSchedulableByGroupIDAndPlatform(ctx, *groupID, platform)
	}
	return nil, ErrBatchImageNoProviderAvailable
}

// EnsureGroupAllowsBatchImage requires a positive group identity and its
// batch generation permission. Missing group data returns the pricing
// category; a disabled group returns the permission category before
// submission or model catalogue work.
func (s *Public) EnsureGroupAllowsBatchImage(ctx context.Context, groupID *int64) error {
	if groupID == nil || *groupID <= 0 {
		return ErrBatchImageGroupDisabled
	}
	if s.GroupRepo == nil {
		return ErrBatchImageSettlementPricingMissing
	}
	group, err := s.GroupRepo.GetByIDLite(ctx, *groupID)
	if err != nil || group == nil {
		return ErrBatchImageSettlementPricingMissing
	}
	if !group.AllowBatchImageGeneration {
		return ErrBatchImageGroupDisabled
	}
	return nil
}

// ResolvePricingSnapshot computes submission-time image prices and separate
// source rates for the owner's selected funding mode. It validates the mode
// and requires a preferred subscription identity for subscription funding.
// One GroupView supplies its permission, base rate, discount and hold
// multiplier.
//
// The user-specific rate affects wallet funding; subscription selection can
// determine the displayed group estimate while newer funding commands retain
// distinct subscription and wallet rates. The hold multiplier is raised to
// the discount when necessary to cover a wholly successful batch. Per-image
// pricing is resolved through the injected pricer, provider scaling is
// applied, and the expanded item count produces the estimated charge and
// hold.
func (s *Public) ResolvePricingSnapshot(ctx context.Context, owner BatchImageOwner, req BatchImageSubmitRequest, platform string, provider *Candidate) (*BatchImagePricingSnapshot, error) {
	billingMode, ok := billing.NormalizeAPIKeyBillingMode(owner.BillingMode)
	if !ok {
		return nil, apikey.ErrInvalidAPIKeyBillingMode
	}
	if billingMode == billing.APIKeyBillingModeSubscription && (owner.PreferredSubscriptionID == nil || *owner.PreferredSubscriptionID <= 0) {
		return nil, apikey.ErrPreferredSubscriptionRequired
	}
	// Reuse one GroupView for permission and multiplier calculations. The
	// injected pricer resolves its image price separately, so passing this view
	// does not establish an atomic rate-and-price snapshot.
	var pricingGroup *GroupView
	unit := -1.0
	groupMultiplier := 1.0
	subscriptionRateMultiplier := 1.0
	balanceRateMultiplier := 1.0
	planGroupRateEnabled := true
	discountMultiplier := DefaultBatchImageDiscountMultiplier
	holdMultiplier := DefaultBatchImageHoldMultiplier
	if owner.GroupID != nil && *owner.GroupID > 0 {
		if s.GroupRepo == nil {
			return nil, ErrBatchImageSettlementPricingMissing
		}
		group, err := s.GroupRepo.GetByIDLite(ctx, *owner.GroupID)
		if err != nil || group == nil {
			return nil, ErrBatchImageSettlementPricingMissing
		}
		pricingGroup = group
		if !group.AllowBatchImageGeneration {
			return nil, ErrBatchImageGroupDisabled
		}
		groupDefaultMultiplier := group.RateMultiplier
		if groupDefaultMultiplier < 0 {
			groupDefaultMultiplier = 0
		}
		subscriptionRateMultiplier = groupDefaultMultiplier
		balanceRateMultiplier = groupDefaultMultiplier
		if s.UserGroupRateRepo != nil {
			userRate, rateErr := s.UserGroupRateRepo.GetByUserAndGroup(ctx, owner.EffectiveBillingUserID(), group.ID)
			if rateErr != nil {
				return nil, ErrBatchImageSettlementPricingMissing
			}
			if userRate != nil {
				balanceRateMultiplier = *userRate
			}
		}
		effectiveGroupMultiplier := balanceRateMultiplier
		// The funding mode chooses which subscription may supply the estimate rate.
		// A preferred subscription cannot be replaced by the auto resolver or wallet.
		var subscription *billing.UserSubscription
		switch billingMode {
		case billing.APIKeyBillingModeSubscription:
			subscription = s.PreferredSubscription(
				ctx,
				owner.EffectiveBillingUserID(),
				*owner.PreferredSubscriptionID,
				owner.GroupID,
			)
			if subscription == nil {
				return nil, billing.ErrPreferredSubscriptionInvalid
			}
		case billing.APIKeyBillingModeAuto:
			subscription = s.AutoSubscription(
				ctx,
				owner.EffectiveBillingUserID(),
				owner.GroupID,
			)
		}
		if subscription != nil {
			effectiveGroupMultiplier = s.SubscriptionMultiplier(ctx, owner, group, groupDefaultMultiplier, subscription)
		}
		groupMultiplier = effectiveGroupMultiplier
		if groupMultiplier < 0 {
			groupMultiplier = 0
		}
		discountMultiplier = group.BatchImageDiscountMultiplier
		if discountMultiplier < 0 {
			discountMultiplier = 0
		}
		if group.BatchImageHoldMultiplier >= 0 {
			holdMultiplier = group.BatchImageHoldMultiplier
		}
	}
	if unit < 0 {
		if s.Pricing == nil {
			return nil, ErrBatchImageSettlementPricingMissing
		}
		resolvedUnit, err := s.Pricing.BatchImageUnitPrice(ctx, BatchImagePriceInput{Model: req.Model, GroupID: owner.GroupID, Group: pricingGroup, ImageSize: req.ImageSize})
		if err != nil || resolvedUnit < 0 {
			return nil, ErrBatchImageSettlementPricingMissing
		}
		unit = resolvedUnit
	}
	// The hold multiplier must cover the discount multiplier; otherwise enough successful
	// images can make actualCost exceed holdAmount and prevent settlement.
	// Clamp invalid stored settings as well as validating new settings in the admin API.
	if holdMultiplier < discountMultiplier {
		s.warn("batch_image.hold_multiplier_below_discount_clamped",
			"hold_multiplier", holdMultiplier,
			"discount_multiplier", discountMultiplier,
		)
		holdMultiplier = discountMultiplier
	}
	providerMultiplier := 1.0
	if provider != nil {
		providerMultiplier = provider.BillingRateMultiplier()
	}
	if providerMultiplier < 0 {
		providerMultiplier = 0
	}
	standardUnitPrice := unit * groupMultiplier * providerMultiplier
	billableUnitPrice := standardUnitPrice * discountMultiplier
	holdUnitPrice := standardUnitPrice * holdMultiplier
	return &BatchImagePricingSnapshot{
		BaseUnitPrice:              unit,
		GroupRateMultiplier:        groupMultiplier,
		SubscriptionRateMultiplier: subscriptionRateMultiplier,
		BalanceRateMultiplier:      balanceRateMultiplier,
		PlanGroupRateEnabled:       planGroupRateEnabled,
		ProviderRateMultiplier:     providerMultiplier,
		BatchDiscountMultiplier:    discountMultiplier,
		HoldMultiplier:             holdMultiplier,
		BillableUnitPrice:          billableUnitPrice,
		HoldUnitPrice:              holdUnitPrice,
		EstimatedCost:              billableUnitPrice * float64(len(req.Items)),
		HoldAmount:                 holdUnitPrice * float64(len(req.Items)),
	}, nil
}

// InvalidateAuthCache invokes the optional payer invalidation callback for a
// positive user identity. Absent callbacks and nonpositive identities return
// immediately, allowing funding paths to share this cache refresh step.
func (s *Public) InvalidateAuthCache(ctx context.Context, userID int64) {
	if s != nil && s.InvalidateAuth != nil && userID > 0 {
		s.InvalidateAuth(ctx, userID)
	}
}

// MaxItems returns the positive configured cap on submitted prompt entries. A
// nil service or nonpositive setting uses two hundred; repetition expansion
// is governed separately by the output image cap.
func (s *Public) MaxItems() int {
	if s != nil && s.Options.MaxItemsPerJobDefault > 0 {
		return s.Options.MaxItemsPerJobDefault
	}
	return DefaultBatchImageMaxItems
}

// MaxOutputImagesPerJob returns the positive configured cap on expanded
// outputs across one submission. Missing or nonpositive settings use two
// hundred, and validation counts every OutputCount repetition against this
// total.
func (s *Public) MaxOutputImagesPerJob() int {
	if s != nil && s.Options.MaxOutputImagesPerJob > 0 {
		return s.Options.MaxOutputImagesPerJob
	}
	return DefaultBatchImageMaxOutputImages
}

// MaxOutputImagesPerItem returns the positive configured repetition cap for
// each submitted prompt. Missing or nonpositive settings use four, while a
// zero OutputCount is normalized to one before checking this cap.
func (s *Public) MaxOutputImagesPerItem() int {
	if s != nil && s.Options.MaxOutputImagesPerItem > 0 {
		return s.Options.MaxOutputImagesPerItem
	}
	return DefaultBatchImageMaxOutputCount
}

// MaxPromptChars returns the configured positive byte cap used for prompts
// and stored previews. Missing or nonpositive settings use eight thousand,
// and validation applies len(string) rather than counting Unicode characters.
func (s *Public) MaxPromptChars() int {
	if s != nil && s.Options.MaxPromptCharsPerItem > 0 {
		return s.Options.MaxPromptCharsPerItem
	}
	return DefaultBatchImageMaxPromptChars
}

// MaxReferenceImagesPerJob returns the configured positive attachment cap for
// the expanded batch workload. Missing or nonpositive settings use one
// thousand; references repeated by OutputCount contribute once per expanded
// output.
func (s *Public) MaxReferenceImagesPerJob() int {
	if s != nil && s.Options.MaxReferenceImagesPerJob > 0 {
		return s.Options.MaxReferenceImagesPerJob
	}
	return DefaultBatchImageMaxReferenceImages
}

// MaxReferenceInlineBytesPerJob returns the configured positive decoded
// inline-byte cap for the expanded workload. Missing or nonpositive settings
// use 128 MiB, and repeated attachments contribute their byte length for
// every output.
func (s *Public) MaxReferenceInlineBytesPerJob() int {
	if s != nil && s.Options.MaxReferenceInlineBytesPerJob > 0 {
		return s.Options.MaxReferenceInlineBytesPerJob
	}
	return DefaultBatchImageMaxReferenceBytes
}

// DefaultResponseMimeType returns a trimmed configured output MIME value when
// it is present. A nil service or blank setting uses image/png, which request
// normalization applies when the caller omitted its preference.
func (s *Public) DefaultResponseMimeType() string {
	if s != nil && strings.TrimSpace(s.Options.DefaultResponseMimeType) != "" {
		return strings.TrimSpace(s.Options.DefaultResponseMimeType)
	}
	return DefaultBatchImageResponseMime
}

// DefaultImageSize returns a trimmed configured size when it is present.
// Missing or blank settings use 1K; ValidateSubmitRequest then checks that
// the selected default satisfies the supported batch size.
func (s *Public) DefaultImageSize() string {
	if s != nil && strings.TrimSpace(s.Options.DefaultImageSize) != "" {
		return strings.TrimSpace(s.Options.DefaultImageSize)
	}
	return DefaultBatchImageImageSize
}

// HashBatchImageSubmitRequest returns hexadecimal SHA-256 of the request JSON
// after metadata sanitation. Callers supply the normalized request; SessionID
// is JSON-excluded, while the normalized title, item IDs and input data
// affect the fingerprint.
func HashBatchImageSubmitRequest(req BatchImageSubmitRequest) string {
	req.Metadata = SanitizeBatchImageMetadata(req.Metadata)
	// These request fields are JSON-encodable strings, byte slices and containers.
	// Discard the encoding error before hashing the resulting request representation.
	b, _ := json.Marshal(req)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// HashCompositeBatchImageSubmitRequest fingerprints the normalized request
// and selected group identity. Submit retains the original client spelling
// separately, so aliases selecting the same internal model and group can share
// this content fingerprint. Another group changes the encoded payload even
// when prompts and the internal model match.
//
// The group pointer is copied before JSON encoding, and metadata uses the same
// sanitation as ordinary submission hashing. SessionID remains excluded by its
// request tag. The hexadecimal digest lets an owner-scoped idempotency key
// reject a retry whose normalized content has moved into another group.
func HashCompositeBatchImageSubmitRequest(req BatchImageSubmitRequest, groupID *int64) string {
	payload := struct {
		Request BatchImageSubmitRequest `json:"request"`  // normalized submission request snapshot
		GroupID *int64                  `json:"group_id"` // selected routing group
	}{
		Request: req,
		GroupID: CloneInt64Ptr(groupID),
	}
	payload.Request.Metadata = SanitizeBatchImageMetadata(payload.Request.Metadata)
	// The wrapper contains the JSON-encodable request and one optional integer.
	// Encoding retains group identity alongside the normalized request fields.
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// BatchImageProviderPlatform returns the Gemini routing platform for batch
// backend lookup and usage reporting. Both supported backends and
// unrecognized labels select that same value; backend admission is checked
// separately by callers.
func BatchImageProviderPlatform(platform string) string {
	switch platform {
	case BatchImageProviderGeminiAPI, BatchImageProviderVertex:
		return PlatformGemini
	default:
		return PlatformGemini
	}
}

// BatchImageProviderSelectionOrder returns a trimmed explicit backend as the
// only backend when supplied. Blank input enumerates Gemini API and Vertex
// for candidate construction and stable public catalogue ordering; provider
// priority determines submission selection across those backends.
func BatchImageProviderSelectionOrder(requestedProvider string) []string {
	if strings.TrimSpace(requestedProvider) != "" {
		return []string{strings.TrimSpace(requestedProvider)}
	}
	return []string{BatchImageProviderGeminiAPI, BatchImageProviderVertex}
}

// BatchImageModelsFromProviderMapping merges supplied candidates with concrete
// provider names and mapping sources, then sorts distinct spellings. Nil
// providers return nil. Supplied candidates are filtered as written, while
// mapping source names are trimmed before wildcard screening; prepared target
// names arrive through the optional candidate interface.
func BatchImageModelsFromProviderMapping(provider *Candidate, candidates ...string) []string {
	if provider == nil {
		return nil
	}
	mapping := provider.GetModelMapping()
	models := make(map[string]struct{})
	if configured, ok := provider.CandidateRules.(interface {
		// GetConfiguredRequestModels supplies concrete request names prepared from
		// provider configuration.
		GetConfiguredRequestModels() []string
	}); ok {
		candidates = append(candidates, configured.GetConfiguredRequestModels()...)
	}
	for _, id := range candidates {
		if id != "" && !strings.ContainsAny(id, "*?") {
			models[id] = struct{}{}
		}
	}
	for model := range mapping {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if strings.ContainsAny(model, "*?") {
			continue
		}
		models[model] = struct{}{}
	}
	out := make([]string, 0, len(models))
	for model := range models {
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}

// BatchImageProviderSubmitPublicError maps known credential and
// managed-bucket failures to public application errors. Other reasons become
// the generic submit failure, limiting exposure of provider resource
// diagnostics returned by the failed operation.
func BatchImageProviderSubmitPublicError(err error) error {
	reason := strings.TrimSpace(infraerrors.Reason(err))
	switch reason {
	case "VERTEX_MANAGED_GCS_BUCKET_MISSING":
		return ErrBatchImageVertexGCSBucketMissing
	case "BATCH_IMAGE_PROVIDER_MISSING_API_KEY":
		return ErrBatchImageProviderMissingAPIKey
	case "BATCH_IMAGE_PROVIDER_MISSING_SERVICE_ACCOUNT":
		return ErrBatchImageProviderMissingServiceAccount
	case "BATCH_IMAGE_PROVIDER_UNSUPPORTED_PROVIDER":
		return ErrBatchImageProviderUnsupportedProvider
	default:
		return ErrBatchImageProviderSubmitFailed
	}
}

// BatchImageProviderSubmitRecordCode returns the application reason used for
// a saved submission diagnostic. Blank or generic submit reasons become
// PROVIDER_SUBMIT_FAILED, while specific mapped credential and bucket reasons
// remain available.
func BatchImageProviderSubmitRecordCode(err error) string {
	reason := strings.TrimSpace(infraerrors.Reason(err))
	if reason == "" || reason == "BATCH_IMAGE_PROVIDER_SUBMIT_FAILED" {
		return "PROVIDER_SUBMIT_FAILED"
	}
	return reason
}

// ParseBatchImageListTime accepts positive Unix seconds, RFC3339 timestamps
// or date-only text after trimming. Date-only values use UTC midnight; empty
// or invalid inputs return nil so List omits that creation filter.
func ParseBatchImageListTime(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if unix, err := strconv.ParseInt(raw, 10, 64); err == nil && unix > 0 {
		t := time.Unix(unix, 0)
		return &t
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return &t
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return &t
	}
	// An unrecognized time filter becomes absent, letting the list query proceed
	// with its remaining validated owner and pagination restrictions.
	return nil
}

// SanitizeBatchImageMetadata builds a fresh map of trimmed labels by visiting
// input keys in sorted order. Nil or empty input returns nil. Blank keys and
// keys above 64 bytes are skipped, values are capped at 256 bytes, and at most
// twenty normalized keys survive.
//
// Trimming can merge distinct input keys: " title" and "title" address the same
// output entry. The later sorted input replaces that value if the cap has not
// already stopped traversal. Sorting therefore fixes both collision precedence
// and the selected subset used by submission hashing.
func SanitizeBatchImageMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		key := strings.TrimSpace(k)
		if key == "" || len(key) > 64 {
			continue
		}
		value := strings.TrimSpace(in[k])
		if len(value) > 256 {
			// Metadata values retain a bounded byte prefix before hashing and provider
			// submission, even for long caller-supplied labels.
			value = value[:256]
		}
		out[key] = value
		// Sorted input keys determine which normalized labels survive the
		// twenty-entry request metadata cap.
		if len(out) >= 20 {
			break
		}
	}
	return out
}

// ParseBatchImageCursor parses a trimmed decimal page offset. Invalid or
// negative text becomes zero, so owner and item listings restart at their
// first page rather than rejecting an unusable cursor.
func ParseBatchImageCursor(cursor string) int {
	offset, err := strconv.Atoi(strings.TrimSpace(cursor))
	// Invalid cursors restart pagination at zero, leaving owner and status
	// restrictions in effect for the first returned page.
	if err != nil || offset < 0 {
		return 0
	}
	return offset
}

// now reads the public service clock when a callback was injected at
// construction. Otherwise it uses time.Now, supplying request titles and
// owner acknowledgement timestamps from the current system time.
func (s *Public) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
