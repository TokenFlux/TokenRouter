package apikey

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
)

// auth_cache.go defines serialized authentication snapshots containing
// credential configuration, payer observations, and group policy. Versioned
// snapshots carry identity, group policy and configured limits needed by
// request admission, while current spending-window usage is read separately.
// Conversion reconstructs request-owned collections so prefix selection and
// policy processing can change that view without mutating the cached entry.

// APIKeyAuthSnapshot stores versioned credential configuration and hydrated
// authentication identity views.
type APIKeyAuthSnapshot struct {
	// Version controls cache acceptance. KeyApplyAuthCacheEntry treats every
	// version other than the current schema as a miss requiring a reload.
	Version  int    `json:"version"`           // authentication snapshot schema version
	APIKeyID int64  `json:"api_key_id"`        // persisted credential record identifier
	UserID   int64  `json:"user_id"`           // persisted creator user identifier
	TeamID   *int64 `json:"team_id,omitempty"` // credential team scope identifier
	// TeamOwnerDisabled carries the owner's separate credential lock. The
	// reconstructed Key's active-state check rejects that flag even with active status.
	TeamOwnerDisabled bool `json:"team_owner_disabled"`
	// CreatedAt participates in the team join-time check. A later current member
	// join rejects reuse of this credential even when its team ID matches.
	CreatedAt time.Time `json:"created_at"` // credential creation timestamp
	// ordinary group binding identifier.
	GroupID     *int64 `json:"group_id,omitempty"`
	IsComposite bool   `json:"is_composite"` // prefix selection mode flag
	// ordered composite group snapshots.
	CompositeGroups []APIKeyAuthCompositeGroupSnapshot `json:"composite_groups,omitempty"`
	Name            string                             `json:"name"`             // saved credential display name
	Status          string                             `json:"status"`           // stored credential lifecycle state
	FastModePolicy  string                             `json:"fast_mode_policy"` // stored Key acceleration preference
	// BillingMode and PreferredSubscriptionID select automatic, wallet or
	// specified subscription funding. Admission checks the chosen source against
	// the payer and final group after authentication.
	BillingMode string `json:"billing_mode"` // stored funding source mode
	// selected payer subscription identifier.
	PreferredSubscriptionID *int64 `json:"preferred_subscription_id,omitempty"`
	// copied Key model redirects.
	ModelMapping map[string]string `json:"model_mapping,omitempty"`
	// copied allowed IP patterns.
	IPWhitelist []string `json:"ip_whitelist,omitempty"`
	// copied denied IP patterns.
	IPBlacklist []string `json:"ip_blacklist,omitempty"`
	// User, ActorUser, Team and TeamMembership keep payer and assigned member
	// views distinct for team authentication. UserID retains the credential
	// creator while User.ID identifies the hydrated payer.
	User APIKeyAuthUserSnapshot `json:"user"` // hydrated payer authentication snapshot
	// optional assigned member snapshot.
	ActorUser *APIKeyAuthActorSnapshot `json:"actor_user,omitempty"`
	// optional hydrated team snapshot.
	Team *APIKeyAuthTeamSnapshot `json:"team,omitempty"`
	// copied current membership data.
	TeamMembership *TeamMembership `json:"team_membership,omitempty"`
	// optional ordinary group snapshot.
	Group *APIKeyAuthGroupSnapshot `json:"group,omitempty"`
	// Quota and QuotaUsed describe the total allowance and observed spending.
	// Snapshot conversion copies them by value; later billing can advance stored
	// usage before cache invalidation reaches this view.
	Quota     float64    `json:"quota"`                // total USD allowance; zero unbounded
	QuotaUsed float64    `json:"quota_used"`           // observed total USD spending
	ExpiresAt *time.Time `json:"expires_at,omitempty"` // nil: no expiry
	// MaxTokenRateMultiplier caps the effective group Token multiplier. Zero
	// disables protection; positive values reject requests above the saved ceiling.
	MaxTokenRateMultiplier float64 `json:"max_token_rate_multiplier"` // maximum effective Token multiplier
	// ConcurrencyLimit and RPMLimit use zero to disable each request check
	// independently. RateLimit5h, RateLimit1d and RateLimit7d configure USD
	// spending allowances, with current window usage read separately during
	// admission.
	ConcurrencyLimit int     `json:"concurrency_limit"` // maximum admitted active requests
	RPMLimit         int     `json:"rpm_limit"`         // maximum admitted requests per minute
	RateLimit5h      float64 `json:"rate_limit_5h"`     // five-hour USD spending allowance
	RateLimit1d      float64 `json:"rate_limit_1d"`     // one-day USD spending allowance
	RateLimit7d      float64 `json:"rate_limit_7d"`     // seven-day USD spending allowance
	// FallbackWhenGroupUnavailable retains the saved unavailable-target
	// preference. Runtime selection requires the group's configured target and
	// current payer permission before replacing the request binding.
	FallbackWhenGroupUnavailable bool `json:"fallback_when_group_unavailable"`
}

// APIKeyAuthCompositeGroupSnapshot stores one ordered prefix binding with
// prefetched group access data.
type APIKeyAuthCompositeGroupSnapshot struct {
	ID      int64 `json:"id"`       // persisted mapping row identifier
	GroupID int64 `json:"group_id"` // configured target group identifier
	// Prefix and NormalizedPrefix retain display and lookup forms for the same
	// binding. SortOrder preserves submitted order when requests and model lists
	// iterate composite mappings.
	Prefix string `json:"prefix"` // displayed group selection prefix
	// lowercase prefix lookup text.
	NormalizedPrefix string `json:"normalized_prefix"`
	SortOrder        int    `json:"sort_order"` // configured mapping order index
	// UserGroupRPMOverride is the payer-specific RPM value prefetched for this
	// binding. Nil lets subsequent admission look it up, while zero selects an
	// unbounded override.
	// prefetched payer-group request allowance.
	UserGroupRPMOverride *int `json:"user_group_rpm_override,omitempty"`
	// hydrated selectable group snapshot.
	Group *APIKeyAuthGroupSnapshot `json:"group,omitempty"`
}

// APIKeyAuthActorSnapshot stores the assigned member's identity and account
// state.
type APIKeyAuthActorSnapshot struct {
	ID       int64  `json:"id"`       // assigned member user identifier
	Status   string `json:"status"`   // member account lifecycle state
	Email    string `json:"email"`    // member account email address
	Username string `json:"username"` // member account display username
}

// APIKeyAuthTeamSnapshot stores the team identity and state checked during
// authentication.
type APIKeyAuthTeamSnapshot struct {
	ID     int64  `json:"id"`     // credential team record identifier
	Name   string `json:"name"`   // saved team display name
	Status string `json:"status"` // team account lifecycle state
}

// APIKeyAuthUserSnapshot stores payer identity, permission and funding
// observation data.
type APIKeyAuthUserSnapshot struct {
	ID          int64   `json:"id"`          // hydrated payer user identifier
	Status      string  `json:"status"`      // payer account lifecycle state
	Role        string  `json:"role"`        // payer account authorization role
	Balance     float64 `json:"balance"`     // observed payer wallet USD
	Concurrency int     `json:"concurrency"` // payer active request allowance
	// AllowedGroups lists exclusive grants, and DisabledPublicGroups lists public
	// denials. KeySnapshotToAPIKey marks these restrictions loaded so bound
	// public-group checks can use the reconstructed user.
	AllowedGroups []int64 `json:"allowed_groups,omitempty"` // copied exclusive group grants
	Email         string  `json:"email"`                    // payer account email address
	Username      string  `json:"username"`                 // payer account display username
	// BalanceNotifyEnabled, BalanceNotifyThresholdType, BalanceNotifyThreshold and
	// BalanceNotifyExtraEmails carry the payer notice settings used after
	// deduction. TotalRecharged supplies the recharge amount for percentage
	// thresholds at that consumer.
	// balance notice enablement flag.
	BalanceNotifyEnabled bool `json:"balance_notify_enabled"`
	// configured balance threshold mode.
	BalanceNotifyThresholdType string `json:"balance_notify_threshold_type"`
	// optional configured balance threshold.
	BalanceNotifyThreshold *float64 `json:"balance_notify_threshold,omitempty"`
	// copied balance notice recipients.
	BalanceNotifyExtraEmails []NotifyEmailEntry `json:"balance_notify_extra_emails,omitempty"`
	// payer cumulative wallet recharge USD.
	TotalRecharged float64 `json:"total_recharged"`
	RPMLimit       int     `json:"rpm_limit"` // payer requests per minute
	// UserGroupRPMOverride selects the payer-group RPM allowance when present.
	// Scheduler admission uses this value for the group allowance; zero skips that
	// allowance, nil requests lookup, and the total user RPM still applies
	// separately.
	// prefetched payer-group RPM override.
	UserGroupRPMOverride *int `json:"user_group_rpm_override,omitempty"`
	// copied denied public group identifiers.
	DisabledPublicGroups []int64 `json:"disabled_public_groups,omitempty"`
}

// APIKeyAuthGroupSnapshot stores group policy fields reconstructed for a
// gateway request.
type APIKeyAuthGroupSnapshot struct {
	ID            int64              `json:"id"`             // selected group record identifier
	Name          string             `json:"name"`           // saved group display name
	SchedulerType GroupSchedulerType `json:"scheduler_type"` // configured group scheduler mode
	// copied advanced scheduler overrides.
	AdvancedSchedulerOverrides GroupAdvancedSchedulerOverrides `json:"advanced_scheduler_overrides"`
	IsExclusive                bool                            `json:"is_exclusive"` // exclusive group access flag
	Status                     string                          `json:"status"`       // group eligibility lifecycle state
	// base user charge multiplier.
	RateMultiplier float64 `json:"rate_multiplier"`
	// cross-user affinity isolation flag.
	SessionIsolationEnabled bool `json:"session_isolation_enabled"`
	// group image generation allowance.
	AllowImageGeneration bool `json:"allow_image_generation"`
	// group batch-image submission allowance.
	AllowBatchImageGeneration bool `json:"allow_batch_image_generation"`
	// copied model and feature policy.
	RoutingPolicy routing.GroupRoutingPolicy `json:"routing_policy"`
	// restricted client admission flag.
	ClaudeCodeOnly bool `json:"claude_code_only"`
	// restricted-client fallback group identifier.
	FallbackGroupID *int64 `json:"fallback_group_id,omitempty"`
	// FallbackGroupIDOnInvalidRequest identifies invalid-request fallback group
	// identifier.
	FallbackGroupIDOnInvalidRequest *int64 `json:"fallback_group_id_on_invalid_request,omitempty"`
	// UnavailableFallbackGroupID identifies unavailable-group fallback target
	// identifier.
	UnavailableFallbackGroupID *int64 `json:"unavailable_fallback_group_id,omitempty"`
	// ModelRouting names provider candidate IDs for configured model rules. An
	// empty map supplies no model-specific candidates; ModelRoutingEnabled
	// controls use of the configured rules at selection.
	// copied model provider candidates.
	ModelRouting map[string][]int64 `json:"model_routing,omitempty"`
	// model candidate selection flag.
	ModelRoutingEnabled bool `json:"model_routing_enabled"`
	MCPXMLInject        bool `json:"mcp_xml_inject"` // saved MCP XML setting
	// copied saved model scope labels.
	SupportedModelScopes []string `json:"supported_model_scopes,omitempty"`
	// AllowedProtocols is serialized even when empty because an empty configured
	// list rejects client protocols. Snapshot cloning retains nil versus empty
	// input through protocol admission reconstruction.
	// copied client protocol admission set.
	AllowedProtocols []protocol.ProtocolID `json:"allowed_protocols"`
	// copied protocol conversion targets.
	ProtocolFallbacks map[protocol.ProtocolID][]protocol.ProtocolID `json:"protocol_fallbacks"`
	// Responses image handling choice.
	ResponsesImagePolicy string `json:"responses_image_policy"`
	AllowLive            bool   `json:"allow_live"`        // group Live admission flag
	ForceOpenAIFast      bool   `json:"force_openai_fast"` // saved legacy acceleration flag
	// OpenAIFastPolicy keeps the saved group choice for gateway processing.
	// Ordinary Key snapshot construction applies the bound policy normalizer;
	// composite binding snapshots retain the supplied raw group value.
	OpenAIFastPolicy string `json:"openai_fast_policy"` // configured group acceleration choice
	// configured default mapped model.
	DefaultMappedModel string `json:"default_mapped_model,omitempty"`
	// copied model-list display configuration.
	ModelsListConfig GroupModelsListConfig `json:"models_list_config,omitempty"`
	RPMLimit         int                   `json:"rpm_limit"` // group requests per minute
	// MaxReasoningEffort, MaxReasoningEffortOverLimit and ReasoningEffortMappings
	// combine requested-effort rewrites with a cap and its action. The gateway
	// resolves model-specific mappings before comparing the effective cap.
	// configured reasoning effort cap.
	MaxReasoningEffort string `json:"max_reasoning_effort,omitempty"`
	// over-cap handling action choice.
	MaxReasoningEffortOverLimit string `json:"max_reasoning_effort_over_limit,omitempty"`
	// copied requested effort rewrites.
	ReasoningEffortMappings []ReasoningEffortMapping `json:"reasoning_effort_mappings"`
}

// APIKeyAuthCacheEntry represents a missing credential or a versioned positive
// authentication view.
type APIKeyAuthCacheEntry struct {
	// NotFound takes precedence over Snapshot in KeyApplyAuthCacheEntry. A true
	// value returns the missing-key error even when a snapshot is also supplied.
	NotFound bool `json:"not_found"` // negative credential lookup result
	// Snapshot is retained by local cache entries and reconstructed before normal
	// service callers receive a Key. Callers publishing entries treat its nested
	// data as immutable.
	Snapshot *APIKeyAuthSnapshot `json:"snapshot,omitempty"` // positive authentication data pointer
}

// GroupSchedulerType and the configuration aliases use routing's group types.
type (
	// GroupSchedulerType is the routing scheduler mode serialized in
	// authentication group data.
	GroupSchedulerType = accessview.GroupSchedulerType
	// GroupAdvancedSchedulerOverrides is routing's optional advanced scheduling
	// configuration carried by snapshots.
	GroupAdvancedSchedulerOverrides = accessview.GroupAdvancedSchedulerOverrides
	// GroupModelsListConfig is routing's configured model-list shape retained
	// during authentication conversion.
	GroupModelsListConfig = accessview.GroupModelsListConfig

	// ReasoningEffortMapping is routing's requested-to-effective effort rule
	// stored in group snapshots.
	ReasoningEffortMapping = routing.ReasoningEffortMapping
)
