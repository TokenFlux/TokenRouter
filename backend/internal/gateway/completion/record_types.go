package completion

import (
	"context"
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// record_types.go defines captured completion inputs and recorder dependencies
// and dispatches records by usage representation. Completion captures observed
// work separately from the funds transaction that charges it. Result contains
// protocol and media evidence, while Input supplies the payer, actor, provider,
// routing choices, and distinct diagnostic and billing identities needed after
// the request ends. Snapshot separates mutable inputs before asynchronous
// processing; entrypoints decide which outcomes qualify for submission,
// including partial failures and dedicated Cyber records.
//
// Recorder dependencies bind price calculation, transactional settlement, analysis
// writes, and post-commit effects. Record selects native bucket handling when a
// converted OpenAI result already separates cache usage. A usage row can survive
// settlement failure with zero ActualCost, and log errors can follow a committed
// charge, so consumers verify financial success from the settlement result.

// These aliases identify the pricing mode, funding category, request classification,
// and model-source choices shared with billing, usage, and routing consumers.
const (
	BillingModeToken              = pricing.BillingModeToken              // token-count price mode
	BillingModeImage              = pricing.BillingModeImage              // image-count price mode
	BillingModeVideo              = pricing.BillingModeVideo              // video-duration price mode
	BillingModePerRequest         = pricing.BillingModePerRequest         // request-unit price mode
	BillingTypeBalance            = usage.BillingTypeBalance              // balance funding category
	BillingTypeSubscription       = usage.BillingTypeSubscription         // subscription funding category
	RequestTypeCyberBlocked       = usage.RequestTypeCyberBlocked         // Cyber failure classification
	BillingModelSourceRequested   = routing.BillingModelSourceRequested   // client model price source
	BillingModelSourceUpstream    = routing.BillingModelSourceUpstream    // outbound model price source
	BillingModelSourceGroupMapped = routing.BillingModelSourceGroupMapped // mapped model price source
)

// ErrModelPricingUnavailable shares billing's missing-price sentinel so completion
// can preserve measured work with a zero-cost diagnostic record.
var ErrModelPricingUnavailable = pricing.ErrModelPricingUnavailable

// Result contains observed usage and metadata needed for completion processing.
type Result struct {
	// Timings owns the frozen observations of the exchange producing this usage.
	// Completion clones it before asynchronous processing and never mutates its values.
	Timings *timing.Performance

	// UpstreamResponseModel retains the pre-conversion declaration; an empty
	// value yields unknown mismatch evidence rather than a requested-model substitute.
	UpstreamResponseModel string
	// NativeUsage sends input and cache counts through Anthropic-style separate
	// buckets even when the caller submitted an OpenAI completion result.
	NativeUsage bool
	// RequestID: result operation identity. ResponseID: upstream response identity.
	// Model: internal request model. BillingModel: selected billing model candidate.
	// UpstreamModel: actual outbound model. SettlementKey applies operation IDs;
	// price-source settings can override the billing candidate while logs retain models.
	RequestID, ResponseID, Model, BillingModel, UpstreamModel string
	UpstreamRequestID                                         *string    // captured upstream correlation identifier
	Usage                                                     TokenUsage // measured token usage
	// ServiceTier, ReasoningEffort, and RequestedReasoningEffort contain distinct
	// outbound tier, final effort, and client effort evidence. SnapshotResult copies
	// their pointed-to values before normalization can replace selected metadata.
	// ServiceTier: outbound billing tier. ReasoningEffort: final forwarded effort.
	// RequestedReasoningEffort: original client effort.
	ServiceTier, ReasoningEffort, RequestedReasoningEffort *string
	UpstreamResponseServiceTier                            string // raw response tier
	// Stream: client streaming mode. OpenAIWSMode: Responses WebSocket execution.
	// OpenAIWSMode selects the result RequestID as the financial turn key when
	// nonblank; Stream records the client protocol mode in the usage row.
	Stream, OpenAIWSMode bool
	Duration             time.Duration // upstream execution duration

	// ImageCount: producer image quantity. VideoCount: completed video quantity.
	// VideoDurationSeconds: producer duration seconds. WebSearchCalls: Alpha search calls.
	// SearchCount: additional search operations. ImageCount may be a model-derived
	// estimate; video duration and dimensions are normalized before cost calculation.
	ImageFrameCount                                                           int // encoded output frame billing units
	ImageCount, VideoCount, VideoDurationSeconds, WebSearchCalls, SearchCount int
	// ImageSize: current billing tier. ImageInputSize: caller dimension evidence.
	// ImageOutputSize: selected output dimensions. ImageSizeSource: chosen dimension source.
	// VideoResolution: producer video resolution. Normalization can replace the image
	// values using decoded outputs and normalizes video resolution at billing time.
	ImageSize, ImageInputSize, ImageOutputSize, ImageSizeSource, VideoResolution string
	// ImageOutputSizes and ImageSizeBreakdown retain mutable size observations;
	// SnapshotResult separates their storage before worker-side normalization updates
	// the captured result or a usage row reuses those collections.
	ImageOutputSizes   []string       // observed output dimensions
	ImageSizeBreakdown map[string]int // counts by billing size
	AudioUsage         *AudioUsage    // observed continuous audio units
}

// TokenUsage carries protocol usage buckets. Entrypoints determine whether
// input is total or uncached.
type TokenUsage struct {
	// InputTokens, CacheCreationInputTokens, and CacheReadInputTokens overlap for
	// ordinary OpenAI total-input results; RecordOpenAI subtracts both cache buckets.
	// NativeUsage and Anthropic results already report separate billable input.
	// InputTokens: producer input count. OutputTokens: producer output count.
	// CacheCreationInputTokens: aggregate cache writes. CacheReadInputTokens: cache reads.
	InputTokens, OutputTokens, CacheCreationInputTokens, CacheReadInputTokens int
	// CacheCreation5mTokens: five-minute cache writes. CacheCreation1hTokens: one-hour cache writes.
	// ImageInputTokens: image input subset. ImageOutputTokens: image output subset.
	// TTL detail sits beneath aggregate writes; image subsets let pricing select
	// modality-specific rates without adding the same reported tokens twice.
	ImageCacheReadTokens                                                              int // explicit cached image token subset
	CacheCreation5mTokens, CacheCreation1hTokens, ImageInputTokens, ImageOutputTokens int
	Speed                                                                             string // Anthropic processing speed
}

// AudioUsage carries the continuous measurement used by audio operation pricing.
type AudioUsage struct {
	// Mode selects realtime, TTS, or STT measurement rules in CalculateAudioCost;
	// DurationOrUnits therefore means minutes, millions of characters, or hours respectively.
	Mode            string  // audio operation mode
	DurationOrUnits float64 // mode-specific measured units
}

// PayerSnapshot captures the funding user and balance-notification data.
type PayerSnapshot struct {
	ID int64 // funding user identifier
	// Balance supplies pre-settlement notification evidence when the transaction
	// result omits NewBalance; an available committed balance takes precedence.
	Balance      float64
	Notification *billing.UserSummary // captured balance notification settings
}

// ProviderSnapshot captures provider classification, billing policy, and quota-notification data.
type ProviderSnapshot struct {
	// CacheTTLOverrideEnabled and CacheTTLOverrideTarget describe provider cache
	// category policy read after Input.CacheOverrideTarget. An enabled provider
	// policy wins even with a blank target, suppressing global injection lookup.
	CacheTTLOverrideEnabled    bool    // provider TTL override enabled
	CacheTTLOverrideTarget     string  // selected cache TTL bucket
	AnthropicOAuthOrSetupToken bool    // Anthropic injection eligibility
	Platform                   string  // executing provider platform
	ID                         int64   // executing provider identifier
	Type                       string  // captured provider category
	RateMultiplier             float64 // provider cost multiplier
	// OpenAI, OAuthLike, QuotaEligible, and HasQuotaLimit retain independent
	// classification and quota inputs for tier exceptions and provider usage updates.
	// OpenAI identifies the platform; OAuthLike identifies its credential lifecycle.
	// QuotaEligible identifies API-key/Bedrock usage; HasQuotaLimit records configured limits.
	OpenAI, OAuthLike, QuotaEligible, HasQuotaLimit bool
	// RequiresExplicitClaudePricing prevents compatible aliases from inheriting
	// ordinary Claude catalogue prices unless the price configuration permits them.
	RequiresExplicitClaudePricing bool
	// ResetForbiddenCounterOnSuccess clears accumulated forbidden evidence after
	// a result is accepted, independently of the model's pricing policy.
	ResetForbiddenCounterOnSuccess bool
	// CredentialProviderID names a shadow's credential parent. OpenAI tier and
	// free-Fast eligibility use that parent; usage attribution keeps this provider ID.
	CredentialProviderID *int64
	Notification         *billing.QuotaNotifyProvider // captured quota notification settings
}

// KeySnapshot captures funding bindings and actor identity separately from the payer.
type KeySnapshot struct {
	// ActorUserPresent permits ActorUserID to be used even when zero; absent
	// actor evidence otherwise makes settlement and usage attribute work to the payer.
	ActorUserPresent bool
	ID               int64 // captured API key identifier
	// Key retains authentication credential text for exhausted-key invalidation.
	// SnapshotKey copies its string value; apikey.APIKeyService derives the cache key
	// when its invalidation method receives this credential.
	Key         string // auth invalidation lookup value
	ActorUserID int64  // request actor identifier
	// GroupID: selected routing group. TeamID: captured team identifier.
	// PreferredSubscriptionID: locked subscription identifier. Their pointer values
	// are copied during SnapshotKey so later key changes cannot alter funds ownership.
	GroupID, TeamID, PreferredSubscriptionID *int64
	BillingMode                              string         // captured funding source mode
	Quota                                    float64        // USD quota limit
	HasRateLimits                            bool           // positive rolling limits present
	Group                                    *GroupSnapshot // selected group billing data
}

// GroupSnapshot contains group fields used by completion billing.
type GroupSnapshot struct {
	ID             int64   // selected group identifier
	RateMultiplier float64 // group base charge multiplier
	// PeakRateEnabled, PeakStart, PeakEnd, and PeakRateMultiplier describe one
	// configured window. keyWithBillingSettings replaces them from current shared
	// settings before PeakMultiplierAt evaluates the chosen billing time.
	PeakRateEnabled bool // configured peak window enabled
	// PeakStart: window start text. PeakEnd: window end text.
	PeakStart, PeakEnd string
	PeakRateMultiplier float64 // peak token charge factor
	// Location controls peak-window interpretation. SnapshotKey retains this
	// pointer, so callers treat the supplied timezone object as immutable data.
	Location *time.Location
	// FreeOpenAIFast: standard-priced Fast enabled. SupportsOpenAIFast: group capability flag.
	// Both flags participate with the credential provider's OpenAI classification
	// and the billed premium tier before user allocation can use standard prices.
	FreeOpenAIFast, SupportsOpenAIFast bool
	// WebSearchPricePerCall: Alpha search dollars per call.
	// SearchPricePer1k: search dollars per thousand operations.
	// These optional prices distinguish inherited defaults from an explicitly free
	// zero amount; SnapshotKey copies them and settlement reloads shared settings.
	WebSearchPricePerCall, SearchPricePer1k *float64
	// AudioPrice holds optional rates cloned by SnapshotKey. Recorder entrypoints
	// replace it from current settings, while direct cost helpers read this captured value.
	AudioPrice *pricing.AudioPriceConfig // shared audio operation prices
}

// Input contains asynchronous completion data. Call Snapshot before enqueueing.
type Input struct {
	// BillingKey deduplicates financial operations. RequestID remains the local
	// diagnostic identity used by the usage row and background request-record updates.
	BillingKey string
	Result     *Result           // captured measured work
	APIKey     *KeySnapshot      // actor and funding policy
	User       *PayerSnapshot    // captured funding payer
	Provider   *ProviderSnapshot // executing provider evidence
	// Subscription retains request-time ID and plan rates after Snapshot; the
	// funds transaction reloads live eligibility and computes final funding allocations.
	Subscription *billing.UserSubscription
	// InboundEndpoint: client request path. UpstreamEndpoint: upstream operation path.
	// UserAgent: caller agent text. IPAddress: caller IP text.
	// ClientSessionID: explicit client session identifier.
	InboundEndpoint, UpstreamEndpoint, UserAgent, IPAddress, ClientSessionID string
	// RequestID: diagnostic request identifier. RequestPayloadHash: settlement fingerprint evidence.
	// CacheOverrideTarget: captured cache category override.
	RequestID, RequestPayloadHash, CacheOverrideTarget string
	RequestedReasoningEffort                           *string // captured original request effort
	// ForceCacheBilling: sticky-switch cache adjustment. QuotaUpdates: caller quota capability.
	// CyberBlocked: Cyber usage classification. NativeCompactionV2: native compaction observation.
	// These four flags remain in the same captured Input so protocol recording and
	// financial application can use the entrypoint's decisions after the request ends.
	//
	// ForceCacheBilling makes RecordAnthropic move positive uncached input into cache
	// reads before cost calculation selects its billable buckets. QuotaUpdates lets
	// positive user costs enter configured key quota/window updates and enables auth
	// invalidation when a committed result reports exhaustion. billing.Finalize
	// separately checks positive cost and HasRateLimits before updating its window cache.
	//
	// RecordOpenAI maps CyberBlocked to RequestType and copies NativeCompactionV2 into
	// its row; RecordAnthropic leaves both classifications unset in its analysis row.
	ForceCacheBilling, QuotaUpdates, CyberBlocked, NativeCompactionV2 bool
	// PricingAt fixes OpenAI turn pricing when nonzero; RecordAnthropic chooses
	// recording time independently, so callers use the protocol-specific timing rule.
	PricingAt          time.Time
	PricingUsageFields // captured routing and billing choices
}

type (
	// PricingUsageFields aliases routing evidence used to select the billed model.
	PricingUsageFields = routing.PricingUsageFields
	// UsageLog aliases the analysis row written after attempted financial settlement.
	UsageLog = usage.UsageLog
	// UsageTokens aliases the measured token buckets consumed by billing calculations.
	UsageTokens = pricing.UsageTokens
	// CostBreakdown aliases calculated base costs and the provisional user charge.
	CostBreakdown = pricing.CostBreakdown
	// ResolvedPricing aliases an effective price card selected by the resolver.
	ResolvedPricing = pricing.ResolvedPricing
	// CostInput aliases the combined measurements and settings passed to billing.
	CostInput = billing.CostInput
	// PricingInput aliases the model and group lookup supplied to pricing.
	PricingInput = billing.PricingInput
)

// PricingOptions holds the recording time used by native-bucket pricing calculations.
type PricingOptions struct {
	PricingAt time.Time // chosen billing instant
}

// Store commits financial deduplication and allocation independently of analysis
// writes. Implementations use their own transaction even when ctx carries an
// outer transaction, and return Applied=false for an already settled command.
type Store interface {
	// Apply settles a normalized command using its financial request key and API
	// key, then returns the funds outcome for completion processing. An already
	// settled matching command can return Applied=false.
	//
	// Financial deduplication and funding allocation share the implementation's own
	// transaction, even when ctx carries an outer transaction. Newly applied results
	// supply committed allocations before completion updates its row or runs effects.
	// Errors leave the recording entrypoint to preserve measured work as an unsettled
	// analysis row. Its later log retries do not invoke this transaction again.
	Apply(context.Context, *billing.UsageBillingCommand) (*billing.UsageBillingApplyResult, error)
}

// SubscriptionReader reloads a usable subscription for automatic group funding.
type SubscriptionReader interface {
	// ResolveUsableSubscriptionForGroup reads funding eligibility for a user and
	// selected group. Completion uses it only after resolving key funding mode and
	// suppresses read errors to an absent preliminary subscription.
	ResolveUsableSubscriptionForGroup(context.Context, int64, int64) (*billing.UserSubscription, error)
}

// RateReader supplies the user override used for balance-funded group charges.
type RateReader interface {
	// Resolve returns a user/group balance multiplier using the supplied group
	// default when appropriate. Recorder calls this for preliminary balance pricing;
	// subscription-plan rates are selected separately before transaction allocation.
	Resolve(context.Context, int64, int64, float64) float64
}

// ProviderReader resolves the credential owner used by OpenAI billing policy.
type ProviderReader interface {
	// CredentialProvider returns the selected provider or its credential parent for
	// OpenAI tier eligibility. A nil successful result supplies no OAuth or free-Fast
	// eligibility; errors stop RecordOpenAI before cost calculation and settlement.
	CredentialProvider(context.Context, ProviderSnapshot) (*ProviderSnapshot, error)
}
type (
	// HealthObserver clears recorded OpenAI forbidden-response evidence on observed
	// usage from an eligible executing provider before later completion validation.
	HealthObserver interface {
		// ResetOpenAI403Counter clears the provider's accumulated forbidden
		// counter. RecordOpenAI calls it when a result exists and the captured
		// provider is OpenAI or Kimi, Zhipu, and DeepSeek-compatible, before it
		// resolves pricing and funds.
		ResetOpenAI403Counter(context.Context, int64)
	}
	// ModelCandidates chooses the selected billing identity or an available
	// alternate before the recorder applies platform-specific candidate filtering.
	ModelCandidates interface {
		// Candidates returns ordered identifiers for usage pricing from one selected
		// model and alternate observations. The production CompletionModels chooses
		// the first nonblank model, so unavailable selected prices do not borrow another.
		Candidates(string, ...string) []string
	}
)

// LogWriter persists analysis records separately from a financial settlement attempt.
type LogWriter interface {
	// Create writes or deduplicates the supplied usage row and reports creation.
	// Completion ignores the creation flag and logs errors; this operation must not
	// repeat settlement after a financial command has already committed.
	Create(context.Context, *usage.UsageLog) (bool, error)
}

// BestEffortLogWriter optionally accepts analysis writes through the usage queue.
type BestEffortLogWriter interface {
	// CreateBestEffort attempts analysis persistence using the repository's queue
	// policy. An error triggers Recorder.WriteUsage's synchronous Create retry; a nil
	// result reflects the delegate's acceptance behavior rather than financial settlement.
	CreateBestEffort(context.Context, *usage.UsageLog) error
}

// ProviderStats supplies a provider cost basis independent of user funding.
type ProviderStats interface {
	// ResolveProviderStats returns an optional provider base amount for measured
	// work. A nil result leaves TotalCost as the quota basis, while a present zero
	// records an explicitly zero provider cost.
	ResolveProviderStats(context.Context, billing.ProviderStatsCostInput) *float64
}

// Effects applies cache and notification work after the funds result is known.
type Effects interface {
	// ProviderUsed schedules activity for both a newly settled command and a
	// duplicate result. It receives the executing provider identity from the
	// completion snapshot, after the funds store has returned successfully.
	ProviderUsed(int64)
	// InvalidateAuth expires cached key admission after committed quota exhaustion.
	// Recorder calls it only when captured quota capability is enabled and the
	// key value is nonblank, before the remaining settled effects run.
	InvalidateAuth(context.Context, string)
	// Settled receives a newly applied transaction result and captured charge data.
	// Recorder has already updated the usage row from committed allocations;
	// implementations update caches and dispatch notifications from that result.
	Settled(SettlementInput, *billing.UsageBillingApplyResult)
}

// CacheInjectionPolicy reads the Anthropic one-hour cache TTL injection setting.
type CacheInjectionPolicy interface {
	// IsAnthropicCacheTTL1hInjectionEnabled reports whether the global injection
	// setting applies. Recorder reads it only after captured and provider-specific
	// overrides are absent and Anthropic OAuth/setup-token eligibility is present.
	IsAnthropicCacheTTL1hInjectionEnabled(context.Context) bool
}

// BillingEvent contains fields for the billing log adapter.
type BillingEvent struct {
	// Kind: billing diagnostic category. Component: emitting service component.
	// RequestID: local correlation identity. RequestedModel: client model evidence.
	// MappedModel: group model evidence. UpstreamModel: outbound model evidence.
	// Model: search billing model. Platform: executing provider platform.
	Kind, Component, RequestID, RequestedModel, MappedModel, UpstreamModel, Model, Platform string
	// RequestedTier: normalized outbound tier. ObservedTier: normalized response tier.
	// BilledTier: selected charge tier. The downgrade adapter logs all three
	// together so the charge decision remains traceable to its two observations.
	RequestedTier, ObservedTier, BilledTier string
	Models                                  []string // attempted pricing model candidates
	// KeyID: captured API key identifier. ProviderID: executing provider identifier.
	KeyID, ProviderID int64
	GroupID           *int64 // selected routing group
	SearchCount       int    // additional search operation count
	Err               error  // price resolution failure
}

// Dependencies binds externally owned readers, writers, and post-settlement effects.
// Recorder stores these references for reuse; callbacks and services must support
// concurrent completion calls when the worker pool shares this recorder.
type Dependencies struct {
	// RequestRecords supplements a matching request capture or publishes an unmatched
	// completion record; nil skips this summary work while usage persistence continues.
	RequestRecords func(telemetry.RequestRecord) // related request record observer
	// Emit receives structured missing-price and tier observations synchronously; nil
	// skips these diagnostic events while the recorder continues its completion work.
	Emit func(BillingEvent) // structured billing diagnostic observer
	// CacheInjection is consulted only after captured and provider overrides are
	// absent; nil leaves eligible Anthropic providers without this global TTL adjustment.
	CacheInjection CacheInjectionPolicy // global cache injection reader
	// Calculator supplies the required pricing implementation for recording calls;
	// NewRecorder stores it directly, so callers bind it before calculating usage costs.
	Calculator *billing.Calculator // shared billing cost calculator
	// Prices takes precedence over ProviderStats and supplies current shared settings.
	// When nil, default settings and calculator catalogue paths remain available.
	Prices *billing.PriceResolver // shared effective price resolver
	// ProviderStats supplies cost rules when Prices is nil; absent sources leave
	// the statistics amount unset so provider quota falls back to TotalCost.
	ProviderStats ProviderStats // provider cost basis resolver
	// Funds is required when Effects enables settlement; an absent binding returns
	// a repository error before any command is applied or committed effects run.
	Funds Store // transactional financial settlement store
	// Subscriptions reloads preliminary automatic funding only when capture lacks a
	// subscription; nil skips that lookup while the financial transaction retains final eligibility.
	Subscriptions SubscriptionReader // automatic subscription eligibility reader
	// Rates resolves a balance-funded user override; nil retains the group's base
	// rate, and captured subscription plans select their applicable rates separately.
	Rates RateReader // balance user-group multiplier reader
	// Providers resolves a credential parent for OpenAI tier eligibility; nil uses
	// the captured executing provider, while reader errors stop completion before settlement.
	Providers ProviderReader // OpenAI credential owner reader
	// Health resets forbidden evidence for OpenAI/Kimi, Zhipu, and DeepSeek
	// usage when configured; nil skips the reset, and validation or later
	// pricing errors can still follow it.
	Health HealthObserver // eligible provider success observer
	// Models supplies the candidate selection required by RecordOpenAI; bind it
	// before using that path, which filters candidates for compatible Kimi,
	// Zhipu, and DeepSeek providers afterward.
	Models ModelCandidates // OpenAI billing identity selector
	// Logs persists analysis after attempted settlement; nil skips WriteUsage, including
	// its request-summary supplement, while the earlier funds attempt can still succeed.
	Logs LogWriter // usage analysis persistence writer
	// Effects enables settlement and post-commit work. Nil makes Apply return an
	// unapplied nil-error result, so caller configuration determines whether funds are submitted.
	Effects Effects // committed funds effects adapter
	// Observe receives formatted write failures and other recorder messages; nil
	// suppresses these diagnostics, and configured callbacks execute in the completion caller.
	Observe func(string, string) // formatted recorder diagnostic observer
}

// RecorderOptions supplies default charge scaling and the native recording clock.
type RecorderOptions struct {
	// DefaultMultiplier is used when captured group funding data supplies no
	// applicable rate; NewRecorder retains zero as supplied rather than replacing it.
	DefaultMultiplier float64
	// Now provides the billing instant for native usage and uncaptured OpenAI
	// requests; nil selects time.Now and shared recorders require a concurrency-safe clock.
	Now func() time.Time
}

// Recorder normalizes captured work and orders settlement before analysis persistence.
// Its bound dependencies are shared across worker tasks; each invocation owns its
// input snapshot while the services implement their own concurrent state rules.
type Recorder struct {
	requestRecords    func(telemetry.RequestRecord) // related request record observer
	emit              func(BillingEvent)            // structured billing event observer
	cacheInjection    CacheInjectionPolicy          // global cache injection reader
	billingService    *billing.Calculator           // shared billing cost calculator
	resolver          *billing.PriceResolver        // shared effective price resolver
	stats             ProviderStats                 // alternate provider cost resolver
	store             Store                         // financial transaction store
	subscriptions     SubscriptionReader            // automatic subscription reader
	rates             RateReader                    // balance multiplier reader
	providers         ProviderReader                // OpenAI credential owner reader
	health            HealthObserver                // provider forbidden-counter reset
	models            ModelCandidates               // OpenAI billing identity selector
	logs              LogWriter                     // analysis row persistence writer
	effects           Effects                       // committed funds effects adapter
	observe           func(string, string)          // formatted diagnostic message observer
	defaultMultiplier float64                       // default user charge multiplier
	now               func() time.Time              // chosen billing instant provider
}

// PeakMultiplierAt evaluates a nonnil group's configured peak window at the
// supplied instant. A configured Location first converts the instant. Missing,
// invalid, disabled, or inactive peak windows produce a factor of one.
func (g *GroupSnapshot) PeakMultiplierAt(at time.Time) float64 {
	if g.Location != nil {
		at = at.In(g.Location)
	}
	return (&pricing.BillingSettings{PeakRateEnabled: g.PeakRateEnabled, PeakStart: g.PeakStart, PeakEnd: g.PeakEnd, PeakRateMultiplier: g.PeakRateMultiplier}).PeakMultiplierAt(at)
}

// NewRecorder binds the supplied services and callbacks without starting work.
// A nil clock selects time.Now, and DefaultMultiplier is retained as supplied.
// Callers provide dependencies required by their recording path and keep shared
// callbacks safe for concurrent completion tasks.
func NewRecorder(d Dependencies, o RecorderOptions) *Recorder {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Recorder{
		requestRecords:    d.RequestRecords,
		emit:              d.Emit,
		cacheInjection:    d.CacheInjection,
		billingService:    d.Calculator,
		resolver:          d.Prices,
		stats:             d.ProviderStats,
		store:             d.Funds,
		subscriptions:     d.Subscriptions,
		rates:             d.Rates,
		providers:         d.Providers,
		health:            d.Health,
		models:            d.Models,
		logs:              d.Logs,
		effects:           d.Effects,
		observe:           d.Observe,
		defaultMultiplier: o.DefaultMultiplier,
		now:               o.Now,
	}
}

// Record snapshots caller inputs and selects the protocol's bucket interpretation.
// OpenAI submissions with NativeUsage use the Anthropic path because their input
// already excludes cache buckets; ordinary OpenAI results use total-input splitting.
//
// Callers submit only outcomes eligible under their entrypoint's completion rules,
// including any retained partial result. Native recording requires complete input,
// result, key, payer, and provider data; RecordOpenAI reports missing inputs. The
// snapshot is normalized and settled before an analysis write is attempted, and
// a returned settlement error can accompany an unsettled zero-ActualCost usage row.
func (s *Recorder) Record(ctx context.Context, input *Input, openAI bool) (err error) {
	defer func() {
		if err == nil {
			return
		}
		p := timing.PerformanceFromContext(ctx)
		telemetry.UpdateRequest(ctx, func(record *telemetry.RequestRecord) {
			if record.UsageWriteState == "pending" {
				record.UsageWriteState = "skipped"
				record.UsageWriteError = logredact.RedactText(err.Error())
				record.UsageWriteUpdatedAt = time.Now().UTC()
				record.FailureTimings = p.FailureTimings()
			}
		})
	}()
	input = Snapshot(input)
	if openAI && (input == nil || input.Result == nil || !input.Result.NativeUsage) {
		return s.RecordOpenAI(ctx, input)
	}
	return s.RecordAnthropic(ctx, input, &PricingOptions{})
}

// printf formats recorder diagnostics for the optional observation callback.
// A missing callback discards the diagnostic; configured callbacks run synchronously
// in the current completion task and handle their own emission policy.
func (s *Recorder) printf(component, format string, args ...any) {
	if s.observe != nil {
		s.observe(component, fmt.Sprintf(format, args...))
	}
}
