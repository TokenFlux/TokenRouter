package creative

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// public.go provisions personal image tasks, resolves creation pricing, and
// coordinates durable outbox recovery across metadata, funds, transient input
// storage, and Redis queue admission.
// Creation validates and moderates through public_validation.go before
// checking idempotency keys, so replay still depends on current admission. A
// new task obtains a price snapshot and hidden execution Key, then persists
// metadata before reservation, transient input writes, and enqueue progress.
//
// Cross-store provisioning steps keep separate recovery markers; creation
// failures request release recovery for the persisted task while preserving
// prompt and media privacy by storing only SHA-256 digests in PostgreSQL.

// Platform names select image routes and adapters; MIME and input-size limits
// govern task defaults, PNG masks, and encoded Gemini request admission.
const (
	PlatformOpenAI = "openai"
	PlatformGemini = "gemini"
	PlatformGrok   = "grok"

	DefaultCreativeResponseMime = "image/png"

	MaxCreativeMaskBytes         = 4 << 20
	MaxCreativeGeminiInlineBytes = 20 << 20
)

// ErrCreativeContentBlocked identifies a returned content denial that blocks
// submission before hidden Key creation and task funds reservation are attempted.
var ErrCreativeContentBlocked = infraerrors.New(infraerrors.CategoryForbidden, "CREATIVE_CONTENT_BLOCKED", "creative content failed moderation")

type (
	// UserAccess supplies current user group-binding permission for
	// creative model and task admission.
	UserAccess interface {
		// CanBindGroup checks whether the authenticated user can bind
		// the supplied
		// group. The exclusive flag comes from the current group record, and
		// admission checks this before any task is funded.
		CanBindGroup(int64, bool) bool
	}
	// UserReader supplies authenticated user access data for creative group
	// permission checks.
	UserReader interface {
		// GetByID returns binding access for the supplied user identifier. Public
		// converts a read failure or absent value to its configured UserNotFound
		// error before evaluating the selected creative group.
		GetByID(context.Context, int64) (UserAccess, error)
	}
)

// GroupView carries current creative admission flags, group display data, and
// image model/protocol rules.
type GroupView struct {
	ID   int64  // selected group row identifier
	Name string // current group display name
	// ClaudeCodeOnly excludes the group from Creative Studio; no fallback
	// group is selected.
	// client restricted group flag
	ClaudeCodeOnly bool
	// IsExclusive supplies the user binding check, while Active and
	// AllowImageGeneration gate creative admission. Discovery and submission
	// require these current group flags before a model can create funded work.
	// IsExclusive is exclusive group binding flag; AllowImageGeneration is group
	// image admission enabled flag; Active is current group active flag
	IsExclusive, AllowImageGeneration, Active bool
	RateMultiplier                            float64 // group default price multiplier
	// Operations and ProtocolFallbacks retain the supplied route maps;
	// RoutingPolicy is cloned by each query. Callers keep the supplied maps
	// stable while model and operation eligibility is resolved.
	// admitted operations by platform
	Operations map[string][]string
	// group protocol conversion route rules
	ProtocolFallbacks map[protocol.ProtocolID][]protocol.ProtocolID
	// current group model routing policy
	RoutingPolicy routing.GroupRoutingPolicy
	// ModelsList contains enabled custom request names included in finite
	// administrator candidates. Queries keep this supplied slice stable while
	// combining it with provider and policy names.
	ModelsList []string // enabled custom public request names
}

// GroupReader supplies current group metadata for creative submission and
// active model discovery.
type GroupReader interface {
	// GetByIDLite reads current creative flags and model policy for one exact
	// group. Submission rejects a missing or inactive group before managed Key
	// creation and funds reservation.
	GetByIDLite(context.Context, int64) (*GroupView, error)
	// ListActive reads active groups for user discovery and administrator
	// candidates. Each caller applies its own binding and image-route checks
	// before those groups can contribute returned model entries.
	ListActive(context.Context) ([]GroupView, error)
}

// CatalogProvider supplies a provider scheduling state, platform, model
// mappings, and protocol capabilities.
type CatalogProvider interface {
	// SupportsImageGIF reports the provider's animation switch and endpoint mode.
	// Model format support is resolved separately; this method cannot grant a
	// model capability missing from its final-route attributes.
	SupportsImageGIF(protocol.ProtocolID) bool

	// PlatformID returns the provider platform used to select creative image
	// adapter rules.
	PlatformID() string
	// AllowsProtocol checks a creative operation protocol against provider
	// capabilities and group fallback routes. Model resolution applies this check
	// before adding that provider operation to the advertised route.
	AllowsProtocol(protocol.ProtocolID, map[protocol.ProtocolID][]protocol.ProtocolID) bool
	// IsSchedulable reports whether the provider current scheduling state permits
	// creative route discovery.
	IsSchedulable() bool
	// GetModelMapping returns configured provider mapping names used for finite
	// candidate generation. Actual admission still resolves each candidate and
	// checks provider support, protocols, group restrictions, and image output.
	GetModelMapping() map[string]string
	// GetConfiguredRequestModels returns concrete provider candidate names from
	// prepared execution rules. A candidate contributes discovery input, while
	// model resolution separately verifies scheduling, support, protocol routing,
	// and image capability.
	GetConfiguredRequestModels() []string
	// ResolveMappedModel applies one provider model mapping and reports whether
	// it matched. Creative route construction retains nonblank mapped spelling
	// and uses the requested name when that spelling is blank.
	ResolveMappedModel(string) (string, bool)
	// IsModelSupported reports provider admission for a group-mapped request
	// name. A rejected provider contributes no operation or parameter
	// observations to the resolved creative model route for that name.
	IsModelSupported(string) bool
}

// ProviderReader reads eligible group/platform candidates for creative model
// and image capability resolution.
type ProviderReader interface {
	// ListSchedulableByGroupIDAndPlatform returns candidates for one selected
	// group and platform. A creative query reads each enabled platform list once
	// and reuses it across concrete model resolution.
	ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]CatalogProvider, error)
}

// UserRateReader supplies optional user-specific balance rates used by
// creative task price snapshots.
type UserRateReader interface {
	// GetByUserAndGroup returns an optional balance multiplier for this user and
	// group. ResolveCreativePricing retains the group default when a read fails
	// or the returned override is absent.
	GetByUserAndGroup(context.Context, int64, int64) (*float64, error)
}

// SettingReader supplies current creative enablement and exact administrator
// model settings for public admission.
type SettingReader interface {
	// IsCreativeEnabled reports the runtime switch combined with process settings
	// by Public.Enabled. False blocks new creation and makes user model discovery
	// return an empty successful directory result.
	IsCreativeEnabled(context.Context) bool
	// GetCreativeModelSettings returns exact administrator group/model operations
	// for public admission. Empty settings close user discovery and submission,
	// while workers independently check their saved task against current group
	// model policy.
	GetCreativeModelSettings(context.Context) []CreativeModelSetting
}

// ModerationInput pairs request identity and scope with the temporary review
// request body.
type ModerationInput struct {
	RequestID string // local request ID value
	// UserID is authenticated request owner identifier; BillingUserID is request
	// billing owner identifier
	UserID, BillingUserID int64
	// GroupID points into the validated group view during review. Callers keep
	// that identifier stable while a Check implementation consumes the input.
	// target request group identifier
	GroupID *int64
	// GroupName is target group display name; Endpoint is creative request
	// endpoint label; Platform is selected image route platform; Model is public
	// request model name; Protocol is review request protocol label
	GroupName, Endpoint, Platform, Model, Protocol string
	// Body holds freshly encoded prompt and media data for Check. The review
	// consumer treats these bytes as immutable while evaluating or retaining them.
	// temporary JSON review request bytes
	Body []byte
	// review input retention exclusion flag
	// NoMediaRetention requests review logging without media snapshots or body
	// excerpts. ModerateCreativeRequest sets it true before delegating to the
	// configured moderation implementation.
	NoMediaRetention bool
}
type (
	// ModerationDecision carries the review permission returned for one
	// temporary creative request.
	ModerationDecision struct {
		Allowed bool // review permitted request flag
	}
	// Moderator evaluates a temporary creative prompt and media body before
	// task creation.
	Moderator interface {
		// Check reviews a temporary creative request and returns an optional
		// permission decision. Public requests NoMediaRetention, allows service
		// errors, and blocks a successful nonnil denial before creating a task.
		Check(context.Context, ModerationInput) (*ModerationDecision, error)
	}
)

// PublicOptions supplies process feature gating, upload and prompt limits, and
// the default image tier.
type PublicOptions struct {
	Enabled        bool // process creative enable flag
	MaxPromptChars int  // Unicode code points
	// MaxAssetBytes is bytes; MaxTotalInputBytes is bytes
	MaxAssetBytes, MaxTotalInputBytes int64
	DefaultImageSize                  string // default requested image size tier
}

// Public resolves creative admission and prices, then provisions persistent
// task metadata and transient inputs.
type Public struct {
	// Repo, UserRepo, GroupRepo, ProviderRepo, Queue, TransientStore, Results,
	// Settings, EnsureKey, and ImageUnitPrice are configured before serving. Keep
	// these shared dependencies and optional callbacks stable while concurrent
	// requests use them.
	// shared attribute query service
	Attributes *routing.ModelAttributeService
	// Now supplies public query time for expiry and receipt checks. A nil value
	// lets Queries use system time; Results owns its separate recovery clock.
	Now func() time.Time // optional public query clock
	// task metadata persistence repository
	Repo      CreativeRunRepository
	UserRepo  UserReader  // current user access reader
	GroupRepo GroupReader // current creative group rules reader
	// group platform candidate provider reader
	ProviderRepo ProviderReader
	// UserGroupRateRepo optionally overrides the balance multiplier for creation.
	// Missing readers and failed reads retain the current group default rate.
	UserGroupRateRepo UserRateReader
	Queue             CreativeRunQueue // creative task admission queue
	// temporary execution input storage
	TransientStore CreativeTransientStore
	Results        *Results // task funds and result service
	// runtime creative admission settings reader
	Settings SettingReader
	Options  PublicOptions // process feature and input settings
	// UserNotFound classifies every failed or absent UserRepo lookup in
	// ListModels and ValidateCreateParams, including read failures that did not
	// establish confirmed user absence.
	UserNotFound error // user absence error classification
	// EnsureKey supplies the hidden execution Key identifier before metadata
	// creation. Configure this callback before CreateRun; its failures stop
	// reservation and input provisioning.
	EnsureKey func(context.Context, int64, int64) (int64, error)
	// GroupMapping optionally supplies linked pricing model selection. A nil
	// callback leaves the resolved upstream name as the quote identity.
	GroupMapping func(context.Context, int64, string) routing.GroupMappingResult
	// ImageUnitPrice returns a unit amount and independent quote availability.
	// Configure it before discovery or creation so a valid zero quote remains
	// distinct from missing pricing.
	ImageUnitPrice func(context.Context, *GroupView, string, string) (float64, bool)
	// SubscriptionMultiplier optionally supplies the effective reservation rate.
	// A nil callback or false result keeps the selected balance rate as the
	// estimate multiplier while both source rates remain saved.
	SubscriptionMultiplier func(context.Context, int64, *GroupView, float64) (float64, bool)
	// Moderation optionally reviews validated temporary content before task creation.
	// A nil service permits creation; service errors are observed and allowed.
	Moderation Moderator
	// RequestID optionally supplies the current request identity for moderation.
	// A nil callback or blank result leaves the newly generated review ID in use.
	RequestID func(context.Context) string
	// Observe receives creative admission and provisioning diagnostics synchronously.
	// A nil callback skips those diagnostics; configured observers support
	// concurrent requests sharing this Public instance.
	Observe func(string, ...any)
}

// CreativePricingSnapshot records image base price, source multipliers, and
// estimated amount at task creation.
type CreativePricingSnapshot struct {
	// BaseUnitPrice, SubscriptionRateMultiplier, BalanceRateMultiplier, and
	// EstimatedCost retain the quote selected at creation. An effective
	// subscription quote can change the estimate while later funds commands
	// retain both separate source rates.
	BaseUnitPrice              float64 // USD per image
	SubscriptionRateMultiplier float64 // saved subscription price multiplier
	BalanceRateMultiplier      float64 // saved balance price multiplier
	PlanGroupRateEnabled       bool    // plan group multiplier enabled flag
	EstimatedCost              float64 // USD
}

// warn sends a creative request event and attributes to Observe synchronously.
// A nil callback drops the diagnostic; supplied values pass through unchanged
// for admission or provisioning observations.
func (s *Public) warn(event string, values ...any) {
	if s.Observe != nil {
		s.Observe(event, values...)
	}
}

// Enabled combines process and runtime switches with task and group repository
// availability. Nil Public or absent Settings returns false; this gate does
// not validate every dependency later used by creation.
func (s *Public) Enabled(ctx context.Context) bool {
	return s != nil && s.Repo != nil && s.GroupRepo != nil && s.Options.Enabled && s.Settings != nil && s.Settings.IsCreativeEnabled(ctx)
}

// MaxPromptChars returns a positive configured Unicode-code-point limit or
// eight thousand. Nil Public and nonpositive configuration use the same
// default for capability output and normalized prompt validation.
func (s *Public) MaxPromptChars() int {
	if s != nil && s.Options.MaxPromptChars > 0 {
		return s.Options.MaxPromptChars
	}
	return 8000
}

// MaxAssetBytes returns a positive configured per-file byte limit or
// thirty-two MiB. Nil Public and nonpositive configuration use that default
// for HTTP upload parsing and domain image validation.
func (s *Public) MaxAssetBytes() int64 {
	if s != nil && s.Options.MaxAssetBytes > 0 {
		return s.Options.MaxAssetBytes
	}
	return 33554432
}

// MaxTotalInputBytes returns a positive configured combined media byte limit
// or sixty-four MiB. Nil Public and nonpositive configuration use that default
// for HTTP parsing and creation input validation.
func (s *Public) MaxTotalInputBytes() int64 {
	if s != nil && s.Options.MaxTotalInputBytes > 0 {
		return s.Options.MaxTotalInputBytes
	}
	return 67108864
}

// DefaultImageSize returns trimmed nonblank configured size or the one-K
// default. The chosen route still validates this default before submission can
// create or fund a task.
func (s *Public) DefaultImageSize() string {
	if s != nil && strings.TrimSpace(s.Options.DefaultImageSize) != "" {
		return strings.TrimSpace(s.Options.DefaultImageSize)
	}
	return "1K"
}

// CreateRun validates and moderates current scope, model permissions, and
// input before checking the trimmed idempotency key. Matching replay therefore
// still requires current admission to succeed.
//
// New tasks resolve price and hidden Key before metadata creation publishes
// provisioning recovery. Funds reservation, allowance marking, transient
// saves, and queue admission then advance separate saved phases. Later
// failures request release recovery for the persisted task rather than
// synchronously reversing every earlier cross-store action. Persistent hashes
// identify content while plaintext and media bytes enter temporary storage
// before task enqueue.
// @project-doc docs/domains/creative_studio.md#creative_task_lifecycle
func (s *Public) CreateRun(ctx context.Context, scope CreativeRunScope, params CreateCreativeRunParamsPublic, idempotencyKey string) (*CreativeRunPublic, error) {
	normalizedScope, err := NormalizeCreativeRunScope(scope)
	if err != nil {
		return nil, err
	}
	scope = normalizedScope
	userID := scope.UserID
	if !s.Enabled(ctx) {
		return nil, ErrCreativeDisabled
	}
	validated, err := s.ValidateCreateParams(ctx, userID, &params)
	if err != nil {
		return nil, err
	}
	if err := s.ModerateCreativeRequest(ctx, userID, validated); err != nil {
		return nil, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey != "" {
		existing, err := s.Repo.GetCreativeRunByIdempotencyKey(ctx, scope, idempotencyKey)
		if err == nil {
			if existing.RequestFingerprint != validated.Fingerprint {
				return nil, ErrCreativeRunIdempotencyConflict
			}
			out, err := s.Results.GetRunPublic(ctx, existing.RunID)
			if err != nil {
				return nil, err
			}
			out.IdempotentReplay = true
			return out, nil
		}
		if !errors.Is(err, ErrCreativeRunNotFound) {
			return nil, err
		}
	}
	pricing, err := s.ResolveCreativePricing(ctx, userID, validated)
	if err != nil {
		return nil, err
	}
	managedKey, err := s.EnsureKey(ctx, userID, validated.Group.ID)
	if err != nil {
		return nil, err
	}
	runID, err := NewCreativeRunID()
	if err != nil {
		return nil, err
	}
	holdAmount := pricing.EstimatedCost
	run, err := s.Repo.CreateCreativeRun(ctx, CreateCreativeRunParams{
		FrameCount: validated.FrameCount, FrameDurationMS: validated.FrameDurationMS,
		RunID:                      runID,
		UserID:                     userID,
		WorkspaceID:                scope.WorkspaceID,
		GroupID:                    validated.Group.ID,
		APIKeyID:                   managedKey,
		Model:                      validated.Model,
		RequestedModel:             params.Model,
		Operation:                  validated.Operation,
		Platform:                   validated.Platform,
		RequestedOutputCount:       validated.OutputCount,
		ImageSize:                  validated.ImageSize,
		AspectRatio:                validated.AspectRatio,
		ResponseMIMEType:           creativeRequestedMIME(validated.OutputFormat),
		PromptHash:                 validated.PromptHash,
		RequestFingerprint:         validated.Fingerprint,
		IdempotencyKey:             CreativeStringPtr(idempotencyKey),
		EstimatedCost:              pricing.EstimatedCost,
		HoldAmount:                 holdAmount,
		BaseUnitPrice:              pricing.BaseUnitPrice,
		SubscriptionRateMultiplier: pricing.SubscriptionRateMultiplier,
		BalanceRateMultiplier:      pricing.BalanceRateMultiplier,
		PlanGroupRateEnabled:       pricing.PlanGroupRateEnabled,
	})
	if err != nil {
		return nil, err
	}
	telemetry.RecordRelatedRequest(ctx, telemetry.RequestRecord{RequestID: telemetry.StableRequestID(CreativeSettlementRequestID(run.RunID)), State: "running", Model: run.Model, StartedAt: run.CreatedAt, Aliases: []telemetry.RequestAlias{{Kind: "task", Value: run.RunID}, {Kind: "billing", Value: CreativeSettlementRequestID(run.RunID)}}})
	// Task creation already published provisioning recovery in its transaction.
	// Ensure the action before reserving funds so later failures retain a durable
	// task identifier and recovery path across these separate store operations.
	if err := s.Results.EnsureCreativeOutbox(ctx, run.RunID, CreativeRunOutboxProvision); err != nil {
		return nil, err
	}
	// The task and provisioning action already exist before reservation. A later
	// creation failure therefore requests a persisted release target and recovery
	// action, because these funds, transient, and queue steps commit separately.
	if err := s.Results.Funding.Reserve(ctx, run); err != nil {
		s.FailRunAfterCreateError(ctx, run, "BILLING_HOLD_FAILED", err)
		return nil, err
	}
	if marker, ok := s.Repo.(CreativeRunAllowanceMarker); ok {
		if err := marker.SetCreativeRunAllowanceReserved(ctx, run.RunID, run.AllowanceReserved); err != nil {
			// Reserve is idempotent; retain the provisioning outbox
			// so recovery can persist the
			// reservation marker.
			return nil, err
		}
	}
	if err := s.Repo.SetCreativeRunProvisioningPhase(ctx, run.RunID, CreativeProvisioningPhaseHoldReserved); err != nil {
		s.FailRunAfterCreateError(ctx, run, "PROVISIONING_PHASE_FAILED", err)
		return nil, err
	}
	s.Results.InvalidateCreativeAuthCache(ctx, userID)
	if err := s.SaveRunTransient(ctx, run, validated); err != nil {
		s.FailRunAfterCreateError(ctx, run, "TRANSIENT_SAVE_FAILED", err)
		return nil, ErrCreativeTransientFailed
	}
	if err := s.Repo.SetCreativeRunProvisioningPhase(ctx, run.RunID, CreativeProvisioningPhaseTransientSaved); err != nil {
		s.FailRunAfterCreateError(ctx, run, "PROVISIONING_PHASE_FAILED", err)
		return nil, err
	}
	if s.Queue == nil {
		err := errors.New("creative queue is not configured")
		s.FailRunAfterCreateError(ctx, run, "QUEUE_FAILED", err)
		return nil, err
	}
	// Queue admission can already represent this persisted task through inflight
	// bookkeeping. Accept that conflict before marking enqueued so provisioning
	// recovery does not turn an existing queue attempt into a creation failure.
	if err := s.Queue.Enqueue(ctx, run.RunID); err != nil && !errors.Is(err, ErrCreativeAlreadyQueued) {
		s.FailRunAfterCreateError(ctx, run, "QUEUE_FAILED", err)
		return nil, err
	}
	if err := s.Repo.SetCreativeRunProvisioningPhase(ctx, run.RunID, CreativeProvisioningPhaseEnqueued); err != nil {
		s.FailRunAfterCreateError(ctx, run, "PROVISIONING_PHASE_FAILED", err)
		return nil, err
	}
	out, err := s.Results.GetRunPublic(ctx, run.RunID)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// FailRunAfterCreateError requests failed release recovery for a run and
// nonnil cause. A nil run returns directly; transition failures are logged
// while the caller retains the original creation error.
//
// The task creation transaction already recorded provisioning recovery before
// these separate funding, transient, or queue failures occurred. This helper
// first attempts a release_pending target, then ensures a release action on a
// best-effort basis. Its ignored ensure error cannot undo earlier store work,
// so a later reconciler still needs to inspect the persisted task and action
// state.
func (s *Public) FailRunAfterCreateError(ctx context.Context, run *CreativeRun, code string, cause error) {
	if run == nil {
		return
	}
	message := SanitizeCreativeMessage(cause.Error())
	if err := s.Repo.TransitionCreativeRunStatus(ctx, run.RunID, CreativeRunStatusReleasePending, CreativeRunTransitionOptions{
		ErrorCode:           &code,
		ErrorMessage:        &message,
		ReleaseTargetStatus: CreativeRunStatusFailed,
	}); err != nil {
		s.warn("creative.create_failure_mark_failed",
			"run_id", run.RunID,
			"error", err,
		)
	}
	// Recovery ensure is best effort after attempting the release target write.
	// Its failure is discarded here; the original task creation provision action
	// remains the durable evidence another reconciler can inspect.
	_ = s.Results.EnsureCreativeOutbox(ctx, run.RunID, CreativeRunOutboxRelease)
}

// SaveRunTransient writes execution JSON, ordered source bytes, and an
// optional mask through separate temporary storage calls. Callers supply
// nonnil run and validated inputs, keeping their retained bytes stable until
// those synchronous writes finish.
//
// Payload metadata is saved before the media indexes it describes. A later
// source or mask failure can leave earlier keys present, and the error stops
// the sequence without rollback. Creation publishes transient_saved only after
// every required write succeeds, so recovery uses that phase to distinguish
// complete input provisioning from partial storage work.
func (s *Public) SaveRunTransient(ctx context.Context, run *CreativeRun, validated *ValidatedCreativeParams) error {
	if s.TransientStore == nil {
		return errors.New("creative transient store is not configured")
	}
	payload := &CreativeRunPayload{
		FrameCount: run.FrameCount, FrameDurationMS: run.FrameDurationMS,
		RunID:              run.RunID,
		UserID:             run.UserID,
		GroupID:            run.GroupID,
		APIKeyID:           run.APIKeyID,
		Model:              run.Model,
		Operation:          run.Operation,
		Prompt:             validated.Prompt,
		ImageSize:          run.ImageSize,
		AspectRatio:        run.AspectRatio,
		Background:         validated.Background,
		ThinkingLevel:      validated.ThinkingLevel,
		Quality:            validated.Quality,
		SourceCount:        len(validated.Sources),
		HasMask:            validated.Mask != nil,
		RequestFingerprint: run.RequestFingerprint,
	}
	// Save JSON before its ordered source and mask keys. These are separate
	// writes, so a partial failure leaves earlier values for creation recovery
	// while the transient_saved phase is published only after this complete
	// sequence.
	if err := s.TransientStore.SavePayload(ctx, run.RunID, payload); err != nil {
		return err
	}
	for i := range validated.Sources {
		if err := s.TransientStore.SaveInput(ctx, run.RunID, i, validated.Sources[i].Bytes); err != nil {
			return err
		}
	}
	if validated.Mask != nil {
		if err := s.TransientStore.SaveMask(ctx, run.RunID, validated.Mask.Bytes); err != nil {
			return err
		}
	}
	return nil
}

// ResolveCreativePricing returns image base price, separate funding-source
// rates, and estimated task amount. Missing unit quotes fail before
// reservation, while an explicit zero quote remains valid.
//
// The nonnegative group rate supplies subscription and default balance rates.
// A successful user override replaces balance rate, while read failure keeps
// the group default. A subscription quote can change the effective estimate
// multiplier while both source rates stay saved for later allocation.
// BillingModel selects quote identity separately from admission, and count
// scales the unit quote for the selected image size.
func (s *Public) ResolveCreativePricing(ctx context.Context, userID int64, validated *ValidatedCreativeParams) (*CreativePricingSnapshot, error) {
	group := validated.Group
	groupDefault := group.RateMultiplier
	if groupDefault < 0 {
		groupDefault = 0
	}
	subscriptionRate := groupDefault
	balanceRate := groupDefault
	if s.UserGroupRateRepo != nil {
		// An override read failure keeps the group balance rate for this quote.
		// The source-specific snapshot below still records subscription
		// and balance
		// rates separately from the effective amount estimated for reservation.
		if userRate, err := s.UserGroupRateRepo.GetByUserAndGroup(ctx, userID, group.ID); err == nil && userRate != nil {
			balanceRate = *userRate
		}
	}
	effective := balanceRate
	planGroupRateEnabled := true
	if s.SubscriptionMultiplier != nil {
		if value, ok := s.SubscriptionMultiplier(ctx, userID, group, groupDefault); ok {
			effective = value
		}
	}
	baseUnitPrice := 0.0
	estimatedCost := 0.0
	pricingModel := s.BillingModel(ctx, group, validated.Model, validated.FinalModel)
	if resolvedUnitPrice, ok := s.ImageUnitPrice(ctx, group, pricingModel, validated.ImageSize); ok {
		baseUnitPrice = resolvedUnitPrice
		estimatedCost = resolvedUnitPrice * float64(validated.OutputCount) * float64(max(1, validated.FrameCount)) * effective
	} else {
		return nil, billing.ErrImageTaskPricingMissing
	}
	return &CreativePricingSnapshot{
		BaseUnitPrice:              baseUnitPrice,
		SubscriptionRateMultiplier: subscriptionRate,
		BalanceRateMultiplier:      balanceRate,
		PlanGroupRateEnabled:       planGroupRateEnabled,
		EstimatedCost:              estimatedCost,
	}, nil
}

// BillingModel selects a linked pricing model or the final upstream identity
// used for image quotes. Empty upstream uses requested input first.
//
// A linked configuration keeps requested, mapped, and upstream names separate
// while BillingModelForPrice chooses its configured source. Missing mapping
// callbacks, nil groups, and unlinked configurations return upstream directly.
// This quote identity does not replace group/provider admission mapping, whose
// route is resolved independently before the task saves funding values.
func (s *Public) BillingModel(ctx context.Context, group *GroupView, requested, upstream string) string {
	if upstream == "" {
		upstream = requested
	}
	if s.GroupMapping == nil || group == nil {
		return upstream
	}
	mapping := s.GroupMapping(ctx, group.ID, requested)
	if mapping.PricingConfigID == 0 {
		return upstream
	}
	mapped := mapping.MappedModel
	if mapped == "" {
		mapped = requested
	}
	return routing.BillingModelForPrice(mapping, requested, mapped, upstream)
}
