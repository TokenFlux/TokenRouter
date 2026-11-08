package usage

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
)

// log.go defines completed-request usage records and reconciles their request
// categories with legacy transport flags.
// Completed requests carry both the member who acted and the user whose funds
// were charged. UsageLog also keeps the local diagnostic request ID beside a
// separate financial deduplication key, so retained analysis rows can be
// correlated with requests and historical billing operations.
//
// The client display model, internal model and outbound observations describe
// different stages of the request. Stored request categories coexist with
// older stream and WebSocket flags: explicit transport categories determine
// those flags, while special execution categories retain recorded transport
// information. Token totals combine the four general counters; image and
// cache-TTL detail counters remain separately available for display and
// pricing analysis.

// UsageLog is the completed-request analysis row with attribution, measured
// usage, cost and loaded display relations.
type UsageLog struct {
	RequestSource string       // customer or system request source
	SystemUsage   *SystemUsage // optional internal execution evidence
	// Timings owns the frozen observations of the exchange producing this usage.
	// Completion clones it before asynchronous processing and never mutates its values.
	Timings *timing.Performance

	ID int64 // usage row identifier
	// UserID records the acting member, BillingUserID the payer and TeamID
	// team-key attribution. User visibility selects acting records, while
	// PostgreSQL spending rankings group recorded charges by payer.
	UserID int64 // acting user or zero for system
	// BillingUserID supplies payer attribution. PostgreSQL insertion substitutes
	// UserID for customer rows when this value is nonpositive. System rows
	// persist a null payer; query reads use zero for that absence and coalesce absent
	// historical payer identity to the acting user.
	BillingUserID int64  // paying user identifier
	TeamID        *int64 // optional team identifier
	APIKeyID      int64  // used key or zero for system
	ProviderID    int64  // execution provider identifier
	// Platform retains the execution platform recorded at completion; reports
	// use this stored value after provider or group configuration changes.
	Platform string // stored execution platform
	// RequestID identifies the local diagnostic request. Request lookup can
	// associate it with external aliases, while BillingKey supplies
	// financial-operation identity.
	RequestID string // local request identifier
	// BillingKey identifies the financial deduplication action. When it is
	// absent, PostgreSQL uniqueness and duplicate lookup use RequestID for that
	// identity.
	BillingKey string // financial operation identifier
	Model      string // internal request model
	// RequestedModel retains the client's submitted display name. Usage HTTP
	// mappers substitute Model when this field is the empty string.
	RequestedModel string // client display model
	// UpstreamModel records nonempty outbound-model evidence from completion.
	// Nil means that observation is absent, so it cannot establish whether model
	// mapping occurred.
	UpstreamModel *string // outbound model observation
	// UpstreamResponseModel records the upstream's declared response model for
	// administrator diagnostics. Nil retains an absent declaration rather than
	// supplying the requested model.
	UpstreamResponseModel *string // upstream response declaration
	// UpstreamModelMismatch records completion's trimmed, case-folded response
	// comparison. The completion result's Model supplies the comparison target
	// when outbound text is blank; a missing response declaration leaves nil.
	UpstreamModelMismatch *bool   // response model difference
	PricingConfigID       *int64  // selected pricing configuration
	ModelMappingChain     *string // recorded model mapping chain
	BillingTier           *string // selected request price tier
	BillingMode           *string // recorded pricing mode
	ServiceTier           *string // forwarded billing service tier
	// ReasoningEffort records the forwarded value after request policy and
	// model-family handling. Nil keeps an absent or inapplicable effort
	// observation.
	ReasoningEffort *string // forwarded effort observation
	// RequestedReasoningEffort keeps the client value before policy rewriting.
	// Nil leaves historical or unobserved input absent for display consumers.
	RequestedReasoningEffort *string // requested effort observation
	InboundEndpoint          *string // client facing endpoint
	UpstreamEndpoint         *string // normalized outbound endpoint

	GroupID        *int64 // selected group identifier
	SubscriptionID *int64 // associated subscription identifier

	InputTokens         int // input token count
	OutputTokens        int // output token count
	CacheCreationTokens int // cache creation token count
	CacheReadTokens     int // cache read token count

	// CacheCreation5mTokens and CacheCreation1hTokens describe TTL-specific
	// creation buckets. TotalTokens uses CacheCreationTokens once, keeping these
	// detail counters outside its four-counter sum.
	CacheCreation5mTokens int `gorm:"column:cache_creation_5m_tokens"` // five minute cache creation tokens
	CacheCreation1hTokens int `gorm:"column:cache_creation_1h_tokens"` // one hour cache creation tokens

	ImageInputTokens     int     // image input token count
	ImageCacheReadTokens int     // explicit cached image token subset
	ImageInputCost       float64 // image input cost USD
	ImageOutputTokens    int     // image output token count
	ImageOutputCost      float64 // image output cost USD

	InputCost         float64 // standard input cost USD
	OutputCost        float64 // standard output cost USD
	CacheCreationCost float64 // cache creation cost USD
	CacheReadCost     float64 // cache read cost USD
	// TotalCost records the standard cost, ActualCost records user charges, and
	// the subscription and balance amounts describe funding allocation. Provider
	// cost reports separately use ProviderStatsCost and ProviderRateMultiplier.
	TotalCost             float64 // standard total cost USD
	ActualCost            float64 // user charged amount USD
	SubscriptionAmountUSD float64 // subscription allocation amount USD
	BalanceAmountUSD      float64 // balance allocation amount USD
	// BillingAllocations carries per-source funding entries for display. HTTP
	// mapping copies entries and their optional plan and subscription
	// identifiers before encoding them.
	BillingAllocations        []billing.BillingAllocation // source funding allocation entries
	RateMultiplier            float64                     // applied user price multiplier
	LongContextBillingApplied bool                        // long context increase flag
	// ProviderRateMultiplier retains the provider multiplier observed at
	// completion. PostgreSQL cost reports interpret nil as one when reading
	// historical rows.
	ProviderRateMultiplier *float64 // provider price multiplier snapshot
	// ProviderStatsCost supplies the provider-cost base before its multiplier.
	// Nil uses TotalCost; a recorded zero therefore represents a zero
	// provider-cost base.
	ProviderStatsCost *float64 // provider cost base USD

	BillingType int8 // balance or subscription category
	// RequestType, Stream and OpenAIWSMode coexist for category and legacy
	// transport consumers. EffectiveRequestType resolves unknown categories from
	// flags; SyncRequestTypeAndLegacyFields publishes the resulting compatible
	// representation.
	RequestType        RequestType // stored execution category
	Stream             bool        // recorded stream transport flag
	OpenAIWSMode       bool        // recorded WebSocket transport flag
	NativeCompactionV2 bool        // remote compaction v2 flag
	// DurationMs records the upstream elapsed duration in whole milliseconds.
	// Nil leaves the duration unobserved for latency statistics and HTTP
	// display.
	DurationMs *int    // upstream duration milliseconds
	UserAgent  *string // client user agent
	IPAddress  *string // client request address
	// SessionID retains a validated client session_id or X-Session-Id value.
	// Absent or invalid input leaves nil for the user-visible correlation field.
	SessionID *string // client session correlation identifier
	// UpstreamRequestID retains the configured immediate upstream
	// response-header identifier. Missing configured headers and WebSocket turns
	// leave that optional observation absent.
	UpstreamRequestID *string // upstream request header identifier

	CacheTTLOverridden bool // cache TTL price override flag

	ImageCount      int     // generated image count
	ImageFrameCount int     // encoded output frame billing units
	ImageSize       *string // image billing size tier
	ImageInputSize  *string // observed input image size
	ImageOutputSize *string // observed output image size
	ImageSizeSource *string // image size selection source
	// ImageSizeBreakdown carries recorded image counts by size tier. Usage DTO
	// mapping passes this map through, so response consumers treat its entries
	// as immutable.
	ImageSizeBreakdown map[string]int // size tier image counts
	MediaType          *string        // recorded media category

	// VideoCount, VideoResolution and VideoDurationSeconds describe produced
	// video. Completion retains these observations even when a token price card
	// supplies the request charge.
	VideoCount           int     // generated video count
	VideoResolution      *string // recorded video resolution
	VideoDurationSeconds *int    // video duration seconds

	CreatedAt time.Time // record creation instant

	// User, APIKey, Provider, Group and Subscription hold loaded display
	// relations. HTTP mappers format those current records; these relation
	// pointers can share the loader's data.
	User         *UserView                 // loaded actor account
	APIKey       *KeyView                  // loaded key display data
	Provider     *ProviderView             // loaded provider display identity
	Group        *GroupView                // loaded group display data
	Subscription *billing.UserSubscription // loaded subscription display data
}

// TotalTokens returns input, output, cache-creation and cache-read tokens
// summed once. Call it on a nonnil record; cache-TTL detail counters and
// image-specific token fields stay outside this general token total.
func (u *UsageLog) TotalTokens() int {
	return u.InputTokens + u.OutputTokens + u.CacheCreationTokens + u.CacheReadTokens
}

// EffectiveRequestType returns the record's supported execution category, or
// unknown for a nil receiver. A valid non-unknown RequestType takes
// precedence over its legacy booleans, including special execution categories
// with separate transport observations.
//
// Unknown and invalid category codes are resolved through
// RequestTypeFromLegacy. WebSocket mode wins there even when Stream is false,
// allowing older rows to supply an effective transport category without
// treating their raw integer as a supported explicit category.
func (u *UsageLog) EffectiveRequestType() RequestType {
	if u == nil {
		return RequestTypeUnknown
	}
	if normalized := u.RequestType.Normalize(); normalized != RequestTypeUnknown {
		return normalized
	}
	return RequestTypeFromLegacy(u.Stream, u.OpenAIWSMode)
}

// SyncRequestTypeAndLegacyFields updates the stored category and its legacy
// transport flags together. Nil receivers return immediately; other records
// first resolve their effective category through EffectiveRequestType before
// applying the compatible booleans.
//
// Sync, stream and WebSocket categories determine both flags. Safety-refusal
// and Live categories retain their supplied flags because the category
// describes execution beyond transport shape. Keeping the effective category
// and compatible flags together supports readers that still inspect either
// representation.
func (u *UsageLog) SyncRequestTypeAndLegacyFields() {
	if u == nil {
		return
	}
	requestType := u.EffectiveRequestType()
	u.RequestType = requestType
	u.Stream, u.OpenAIWSMode = ApplyLegacyRequestFields(requestType, u.Stream, u.OpenAIWSMode)
}
