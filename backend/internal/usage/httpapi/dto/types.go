package dto

import (
	"time"

	keydto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	identitydto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// types.go defines user and administrator usage-response fields and
// cleanup-task DTOs.
// User usage responses display client model names and charged amounts
// alongside measured token and image details. AdminUsageLog embeds that
// response and adds outbound model observations, provider cost inputs and
// performance observations read from the same persisted usage row.

type (
	// User names the account response used for loaded usage display relations.
	User = identitydto.User[APIKey]
	// APIKey names the key response containing routing-group display data for
	// usage.
	APIKey = keydto.APIKey[Group]
	// Group names the routing-group response used in nested usage detail
	// display.
	Group = routingdto.Group
	// UserSubscription names the billing grant response used in nested usage
	// details.
	UserSubscription = billinghttp.UserSubscription
)

// UsageLog carries regular-user request details and charged usage values for
// HTTP encoding.
type UsageLog struct {
	ID         int64  `json:"id"`                // usage row identifier
	UserID     *int64 `json:"user_id"`           // acting user identifier
	TeamID     *int64 `json:"team_id,omitempty"` // optional team identifier omitted when nil
	APIKeyID   *int64 `json:"api_key_id"`        // used key identifier
	ProviderID int64  `json:"provider_id"`       // execution provider identifier
	Platform   string `json:"platform"`          // stored execution platform
	RequestID  string `json:"request_id"`        // local diagnostic request identifier
	// Model is the client display name selected by FromUsage. Nonempty
	// RequestedModel wins; historical records with an empty display name use
	// their stored Model.
	Model                    string  `json:"model"`                                // client display model name
	ServiceTier              *string `json:"service_tier,omitempty"`               // optional forwarded billing tier
	ReasoningEffort          *string `json:"reasoning_effort,omitempty"`           // optional forwarded effort value
	RequestedReasoningEffort *string `json:"requested_reasoning_effort,omitempty"` // optional requested effort value
	InboundEndpoint          *string `json:"inbound_endpoint,omitempty"`           // optional client facing endpoint
	// UpstreamEndpoint is populated by FromUsageAdmin for administrator
	// responses. The regular-user mapper leaves it nil, so JSON omits the
	// outbound endpoint field there.
	UpstreamEndpoint *string `json:"upstream_endpoint,omitempty"` // optional outbound endpoint observation

	GroupID        *int64 `json:"group_id"`        // optional group identifier JSON null
	SubscriptionID *int64 `json:"subscription_id"` // optional subscription identifier JSON null

	InputTokens         int `json:"input_tokens"`          // input token count
	OutputTokens        int `json:"output_tokens"`         // output token count
	CacheCreationTokens int `json:"cache_creation_tokens"` // cache creation token count
	CacheReadTokens     int `json:"cache_read_tokens"`     // cache read token count

	CacheCreation5mTokens int `json:"cache_creation_5m_tokens"` // five minute cache creation tokens
	CacheCreation1hTokens int `json:"cache_creation_1h_tokens"` // one hour cache creation tokens

	InputCost             float64 `json:"input_cost"`              // standard input cost USD
	OutputCost            float64 `json:"output_cost"`             // standard output cost USD
	CacheCreationCost     float64 `json:"cache_creation_cost"`     // cache creation cost USD
	CacheReadCost         float64 `json:"cache_read_cost"`         // cache read cost USD
	TotalCost             float64 `json:"total_cost"`              // standard total cost USD
	ActualCost            float64 `json:"actual_cost"`             // user charged amount USD
	SubscriptionAmountUSD float64 `json:"subscription_amount_usd"` // subscription allocation amount USD
	BalanceAmountUSD      float64 `json:"balance_amount_usd"`      // balance allocation amount USD
	// BillingAllocations holds copied funding entries from
	// cloneBillingAllocationsDTO. Optional subscription and plan identifiers are
	// copied there; empty input becomes an omitted nil slice.
	BillingAllocations        []billing.BillingAllocation `json:"billing_allocations,omitempty"` // per source funding allocation entries
	RateMultiplier            float64                     `json:"rate_multiplier"`               // applied user price multiplier
	LongContextBillingApplied bool                        `json:"long_context_billing_applied"`  // long context increase flag

	BillingType int8 `json:"billing_type"` // balance or subscription category
	// RequestType, Stream and OpenAIWSMode are resolved together by the mapper
	// from the effective category. Special execution categories retain their
	// source flags to keep recorded transport information available.
	RequestType        string   `json:"request_type"`         // effective request category label
	Stream             bool     `json:"stream"`               // resolved stream transport flag
	OpenAIWSMode       bool     `json:"openai_ws_mode"`       // resolved WebSocket transport flag
	NativeCompactionV2 bool     `json:"native_compaction_v2"` // remote compaction v2 flag
	DurationMs         *int     `json:"duration_ms"`          // optional upstream duration milliseconds
	TTFTMs             *int     `json:"ttft_ms"`              // selected upstream delay milliseconds
	TPS                *float64 `json:"tps"`                  // estimated output tokens per second
	TTFTMode           string   `json:"ttft_mode,omitempty"`  // captured request timing mode

	ImageCount           int     `json:"image_count"`                       // recorded generated image count
	ImageFrameCount      int     `json:"image_frame_count,omitempty"`       // encoded output frame billing units
	ImageSize            *string `json:"image_size"`                        // optional image billing size tier
	ImageInputSize       *string `json:"image_input_size"`                  // optional input image size
	ImageOutputSize      *string `json:"image_output_size"`                 // optional output image size
	ImageInputTokens     int     `json:"image_input_tokens"`                // image input token count
	ImageCacheReadTokens int     `json:"image_cache_read_tokens,omitempty"` // explicit cached image token subset
	ImageInputCost       float64 `json:"image_input_cost"`                  // image input cost USD
	ImageOutputTokens    int     `json:"image_output_tokens"`               // image output token count
	ImageOutputCost      float64 `json:"image_output_cost"`                 // image output cost USD
	ImageSizeSource      *string `json:"image_size_source"`                 // optional size selection source
	// ImageSizeBreakdown carries image counts grouped by size tier. Usage
	// mappers retain the source map, so callers leave it unchanged during JSON
	// encoding.
	ImageSizeBreakdown map[string]int `json:"image_size_breakdown"` // size tier image count map
	MediaType          *string        `json:"media_type"`           // optional recorded media category

	UserAgent *string `json:"user_agent"`           // optional client user agent
	IPAddress *string `json:"ip_address,omitempty"` // optional client request address
	SessionID *string `json:"session_id,omitempty"` // optional client session correlation identifier

	CacheTTLOverridden bool `json:"cache_ttl_overridden"` // cache TTL price override flag

	BillingMode *string `json:"billing_mode,omitempty"` // optional recorded pricing mode

	CreatedAt time.Time `json:"created_at"` // usage row creation instant

	User         *User             `json:"user,omitempty"`         // loaded actor account response
	APIKey       *APIKey           `json:"api_key,omitempty"`      // loaded key display response
	Group        *Group            `json:"group,omitempty"`        // loaded group display response
	Subscription *UserSubscription `json:"subscription,omitempty"` // loaded funding grant response
}

// AdminUsageLog carries usage details with administrator model, provider-cost
// and timing observations.
type AdminUsageLog struct {
	RequestSource string             `json:"request_source"`         // recorded customer or system source
	SystemUsage   *usage.SystemUsage `json:"system_usage,omitempty"` // optional internal execution evidence
	UsageLog                         // embedded regular usage response

	// DetailedTiming is joined by the administrator handler from http.access
	// observations. Nil leaves the timing object omitted when lookup finds no
	// usable data.
	DetailedTiming *UsageLogTiming `json:"detailed_timing,omitempty"` // optional request stage observations

	// UpstreamModel retains observed outbound-model text from the source row.
	// Nil omits this field and does not establish whether a mapping occurred.
	UpstreamModel *string `json:"upstream_model,omitempty"` // optional outbound model observation
	// UpstreamResponseModel and UpstreamModelMismatch retain raw response
	// declaration and comparison observations. Missing historical or upstream
	// declarations leave these pointers nil and their JSON fields omitted.
	UpstreamResponseModel *string `json:"upstream_response_model,omitempty"` // optional upstream response model
	UpstreamModelMismatch *bool   `json:"upstream_model_mismatch,omitempty"` // optional response model difference flag
	PricingConfigID       *int64  `json:"pricing_config_id,omitempty"`       // optional selected pricing configuration
	ModelMappingChain     *string `json:"model_mapping_chain,omitempty"`     // optional recorded model mapping chain
	UpstreamRequestID     *string `json:"upstream_request_id,omitempty"`     // optional immediate upstream request identifier
	BillingTier           *string `json:"billing_tier,omitempty"`            // optional selected price tier

	// ProviderRateMultiplier carries the recorded provider multiplier and
	// remains JSON null when absent. Cost readers use one for absent historical
	// multipliers.
	ProviderRateMultiplier *float64 `json:"provider_rate_multiplier"` // optional provider price multiplier
	// ProviderStatsCost is the provider-cost base before the recorded
	// multiplier. Nil omits this field and lets cost readers use TotalCost; zero
	// remains a supplied base.
	ProviderStatsCost *float64 `json:"provider_stats_cost,omitempty"` // optional provider cost base USD

	IPAddress *string `json:"ip_address,omitempty"` // optional client request address

	Provider *ProviderSummary `json:"provider,omitempty"` // optional provider display identity
}

// UsageLogTiming exposes request metadata, admission durations, event offsets,
// transport counts and connection observations in the usage response.
type UsageLogTiming struct {
	UpstreamStream   bool `json:"upstream_stream"`   // actual upstream streaming applicability
	UpstreamComplete bool `json:"upstream_complete"` // successful upstream terminal observation
	UsageComplete    bool `json:"usage_complete"`    // final output usage observation

	UpstreamStartedMs     *int64 `json:"upstream_started_ms,omitempty"`      // milliseconds from request ingress
	UpstreamFirstOutputMs *int64 `json:"upstream_first_output_ms,omitempty"` // milliseconds from request ingress
	UpstreamLastOutputMs  *int64 `json:"upstream_last_output_ms,omitempty"`  // milliseconds from request ingress
	UpstreamCompletedMs   *int64 `json:"upstream_completed_ms,omitempty"`    // milliseconds from request ingress
	UpstreamFinishedMs    *int64 `json:"upstream_finished_ms,omitempty"`     // milliseconds from request ingress
	AttemptNumber         *int64 `json:"attempt_number,omitempty"`           // usage-producing exchange ordinal

	// RequestContentLength records the observed body length in bytes. Missing,
	// unsupported, or rejected numeric observations remain nil, while zero is retained.
	RequestContentLength *int64 `json:"request_content_length,omitempty"` // bytes
	// TimingStream and TimingAdmitted use zero or one for known streaming and
	// initial admission state. Their nil values preserve unknown historical metadata.
	TimingStream   *int64 `json:"timing_stream,omitempty"`   // client streaming flag
	TimingAdmitted *int64 `json:"timing_admitted,omitempty"` // initial admission outcome

	// RequestBodyReadMs, PreprocessMs, UserSlotWaitMs and ProviderSlotWaitMs
	// partition initial admission time. Their values freeze at provider admission
	// or request termination; nil means the interval was not recorded.
	RequestBodyReadMs  *int64 `json:"request_body_read_ms,omitempty"`  // milliseconds
	PreprocessMs       *int64 `json:"preprocess_ms,omitempty"`         // milliseconds
	UserSlotWaitMs     *int64 `json:"user_slot_wait_ms,omitempty"`     // milliseconds
	ProviderSlotWaitMs *int64 `json:"provider_slot_wait_ms,omitempty"` // milliseconds
	// RetryProviderSlotWaitMs counts later provider waits separately, leaving the
	// four initial admission durations unchanged when another attempt queues.
	RetryProviderSlotWaitMs *int64 `json:"retry_provider_slot_wait_ms,omitempty"` // milliseconds

	// ProviderSlotAcquiredMs, UpstreamGetConnMs, UpstreamGotConnMs,
	// UpstreamWroteRequestMs, UpstreamFirstResponseByteMs, UpstreamFirstEventMs,
	// are first-event offsets from ingress in milliseconds, not additive durations.
	// Nil leaves an unrecorded event absent; measured zero remains present.
	ProviderSlotAcquiredMs      *int64 `json:"provider_slot_acquired_ms,omitempty"`       // milliseconds
	UpstreamGetConnMs           *int64 `json:"upstream_get_conn_ms,omitempty"`            // milliseconds
	UpstreamGotConnMs           *int64 `json:"upstream_got_conn_ms,omitempty"`            // milliseconds
	UpstreamWroteRequestMs      *int64 `json:"upstream_wrote_request_ms,omitempty"`       // milliseconds
	UpstreamFirstResponseByteMs *int64 `json:"upstream_first_response_byte_ms,omitempty"` // milliseconds
	UpstreamFirstEventMs        *int64 `json:"upstream_first_event_ms,omitempty"`         // milliseconds
	FirstVisibleOutputMs        *int64 `json:"first_visible_output_ms,omitempty"`         // milliseconds
	FirstDownstreamFlushMs      *int64 `json:"first_downstream_flush_ms,omitempty"`       // milliseconds

	// UpstreamGetConnCount, UpstreamGotConnCount, UpstreamAttemptCount and
	// UpstreamFirstResponseByteCount count retained transport events across attempts.
	// Nil means no observation, while a nonnil zero preserves a measured count.
	UpstreamGetConnCount           *int64 `json:"upstream_get_conn_count,omitempty"`            // occurrences
	UpstreamGotConnCount           *int64 `json:"upstream_got_conn_count,omitempty"`            // occurrences
	UpstreamAttemptCount           *int64 `json:"upstream_attempt_count,omitempty"`             // occurrences
	UpstreamFirstResponseByteCount *int64 `json:"upstream_first_response_byte_count,omitempty"` // occurrences

	// UpstreamConnectionReused and UpstreamWroteRequestError retain observed
	// connection reuse and write failure. A missing observation remains absent,
	// while a measured false survives JSON encoding as an observed boolean.
	UpstreamConnectionReused  *bool `json:"upstream_connection_reused,omitempty"`   // observed connection reuse flag
	UpstreamWroteRequestError *bool `json:"upstream_wrote_request_error,omitempty"` // observed request write-error flag
}

// UsageCleanupFilters carries the filter fields exposed in cleanup-task HTTP
// responses.
type UsageCleanupFilters struct {
	RequestSource string `json:"request_source,omitempty"` // optional source filter
	// StartTime and EndTime represent the cleanup store's inclusive bounds. The
	// creation handler expands an end date by a fixed twenty-four hours minus
	// one nanosecond before persisting these instants.
	StartTime   time.Time `json:"start_time"`             // inclusive cleanup start instant
	EndTime     time.Time `json:"end_time"`               // inclusive cleanup end instant
	UserID      *int64    `json:"user_id,omitempty"`      // optional acting user selector
	APIKeyID    *int64    `json:"api_key_id,omitempty"`   // optional key equality selector
	ProviderID  *int64    `json:"provider_id,omitempty"`  // optional provider equality selector
	GroupID     *int64    `json:"group_id,omitempty"`     // optional group equality selector
	Model       *string   `json:"model,omitempty"`        // optional stored model selector
	RequestType *string   `json:"request_type,omitempty"` // optional normalized category label
	Stream      *bool     `json:"stream,omitempty"`       // optional legacy stream selector
	BillingType *int8     `json:"billing_type,omitempty"` // optional funding category selector
}

// UsageCleanupTask carries persisted cleanup status, reported progress and
// operator lifecycle metadata.
type UsageCleanupTask struct {
	ID        int64               `json:"id"`         // persisted cleanup task identifier
	Status    string              `json:"status"`     // persisted cleanup state label
	Filters   UsageCleanupFilters `json:"filters"`    // exposed cleanup selection fields
	CreatedBy int64               `json:"created_by"` // creating administrator user identifier
	// DeletedRows is the task's reported cumulative deletion count. Progress
	// saves occur after batch deletion, so this persisted value can trail
	// committed work when a save fails.
	DeletedRows  int64      `json:"deleted_rows"`            // reported cumulative removed row count
	ErrorMessage *string    `json:"error_message,omitempty"` // optional recorded failure diagnostic
	CanceledBy   *int64     `json:"canceled_by,omitempty"`   // optional canceling administrator identifier
	CanceledAt   *time.Time `json:"canceled_at,omitempty"`   // optional cancellation recording instant
	StartedAt    *time.Time `json:"started_at,omitempty"`    // optional task execution start
	FinishedAt   *time.Time `json:"finished_at,omitempty"`   // optional terminal recording instant
	CreatedAt    time.Time  `json:"created_at"`              // task creation recording instant
	UpdatedAt    time.Time  `json:"updated_at"`              // latest task update instant
}

// ProviderSummary carries the loaded provider identifier and name for
// administrator usage display.
type ProviderSummary struct {
	ID   int64  `json:"id"`   // loaded provider identifier
	Name string `json:"name"` // loaded provider display name
}
