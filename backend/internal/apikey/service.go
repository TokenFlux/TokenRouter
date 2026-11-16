package apikey

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dgraph-io/ristretto"
	"golang.org/x/sync/singleflight"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ipmatch"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// service.go validates API key configuration, reconstructs authentication
// views, and records key usage observations.
// A Key configuration write can overlap billing increments and cached
// authentication reads, so the service selects stored configuration columns and
// includes usage fields only for an explicit reset. Credential creator, team
// payer and acting member remain distinct: creation saves the creator, while
// team hydration and payer-scoped subscription choices use the current owner.
// Group eligibility and specified funding are checked before configuration is
// persisted.
//
// Lookup reconstructs request data from accepted snapshots or current storage,
// resolves a configured unavailable-group target and keeps composite selection
// within saved bindings. Optional caches and live-count observations can fail
// while the underlying operation remains usable. TouchLastUsed coalesces
// same-Key writes and records retry deadlines, and the operation gate
// coordinates authentication, invalidation, touches and request leases with
// shutdown; ordinary CRUD methods retain their own caller contexts and
// dependency ownership.

// Credential bounds, storage lookup capacity and touch deadlines limit
// authentication work; numeric allowances follow PostgreSQL decimal capacity
// and custom-secret failures use the cache adapter lifetime.
const (
	MaxAPIKeyCredentialBytes        = 128
	KeyDefaultAuthLookupConcurrency = 64
	KeyDefaultNegativeAuthCacheSize = 16384
	// KeyApiKeyMaxErrorsPerHour bounds duplicate custom-secret failures when the
	// optional cache supplies a usable count.
	KeyApiKeyMaxErrorsPerHour = 20
	KeyApiKeyLastUsedMinTouch = 30 * time.Second
	// DECIMAL(20,8) permits 12 integer digits; values must be below this exclusive bound.
	KeyApiKeyLimitUpperBound = 1_000_000_000_000
	// KeyApiKeyLastUsedFailBackoff delays retries after database errors to bound synchronous writes.
	KeyApiKeyLastUsedFailBackoff = 5 * time.Second
)

// Configuration and credential errors carry stable response identities for
// service callers to map validation, authorization, conflict and temporary
// authentication failure.
var (
	ErrGroupNotAllowed                   = infraerrors.Forbidden("GROUP_NOT_ALLOWED", "user is not allowed to bind this group")
	ErrGroupDisabledForUser              = infraerrors.Forbidden("GROUP_DISABLED_FOR_USER", "user is not allowed to use this public group")
	ErrAPIKeyExists                      = infraerrors.Conflict("API_KEY_EXISTS", "api key already exists")
	ErrAPIKeyRotationConflict            = infraerrors.Conflict("API_KEY_ROTATION_CONFLICT", "API key has changed; refresh and try again")
	ErrAPIKeyLimitReached                = infraerrors.Conflict("API_KEY_LIMIT_REACHED", "api key limit reached")
	ErrAPIKeyTooShort                    = infraerrors.BadRequest("API_KEY_TOO_SHORT", "api key must be at least 16 characters")
	ErrAPIKeyInvalidChars                = infraerrors.BadRequest("API_KEY_INVALID_CHARS", "api key can only contain letters, numbers, underscores, and hyphens")
	ErrAPIKeyLimitInvalid                = infraerrors.BadRequest("API_KEY_LIMIT_INVALID", "api key limits must be finite, non-negative, and less than 1000000000000")
	ErrAPIKeyExpiryInvalid               = infraerrors.BadRequest("API_KEY_EXPIRY_INVALID", "expires_in_days must be greater than zero")
	ErrAPIKeyRateLimited                 = infraerrors.TooManyRequests("API_KEY_RATE_LIMITED", "too many failed attempts, please try again later")
	ErrAPIKeyAuthOverloaded              = infraerrors.ServiceUnavailable("API_KEY_AUTH_OVERLOADED", "api key authentication is temporarily overloaded")
	ErrInvalidIPPattern                  = infraerrors.BadRequest("INVALID_IP_PATTERN", "invalid IP or CIDR pattern")
	ErrInvalidAPIKeyFastModePolicy       = infraerrors.BadRequest("INVALID_API_KEY_FAST_MODE_POLICY", "invalid API key fast mode policy")
	ErrInvalidAPIKeyBillingMode          = infraerrors.BadRequest("INVALID_API_KEY_BILLING_MODE", "invalid API key billing mode")
	ErrPreferredSubscriptionRequired     = infraerrors.BadRequest("PREFERRED_SUBSCRIPTION_REQUIRED", "subscription billing mode requires a subscription")
	ErrPreferredSubscriptionInvalid      = billing.ErrPreferredSubscriptionInvalid
	ErrPreferredSubscriptionGroup        = billing.ErrPreferredSubscriptionGroup
	ErrPreferredSubscriptionInsufficient = billing.ErrPreferredSubscriptionInsufficient
	ErrCompositeKeyGroupsRequired        = infraerrors.BadRequest("COMPOSITE_KEY_GROUPS_REQUIRED", "composite api key requires at least one group")
	ErrCompositeKeyTooManyGroups         = infraerrors.BadRequest("COMPOSITE_KEY_TOO_MANY_GROUPS", "composite api key supports at most 20 groups")
	ErrCompositeKeyPrefixInvalid         = infraerrors.BadRequest("COMPOSITE_KEY_PREFIX_INVALID", "composite api key prefix is invalid")
	ErrCompositeKeyPrefixDuplicate       = infraerrors.BadRequest("COMPOSITE_KEY_PREFIX_DUPLICATE", "composite api key prefixes must be unique")
	ErrCompositeKeyGroupDuplicate        = infraerrors.BadRequest("COMPOSITE_KEY_GROUP_DUPLICATE", "composite api key groups must be unique")
	ErrCompositeKeyGroupConflict         = infraerrors.BadRequest("COMPOSITE_KEY_GROUP_CONFLICT", "composite api key cannot use group_id")
	ErrCompositeKeyTargetRequired        = infraerrors.BadRequest("COMPOSITE_KEY_TARGET_GROUP_REQUIRED", "converting a composite api key requires a target group")
	ErrCompositeKeyPrefixRequired        = infraerrors.BadRequest("COMPOSITE_KEY_MODEL_PREFIX_REQUIRED", "composite api key model must use prefix/model_id")
	ErrCompositeKeyPrefixNotFound        = infraerrors.BadRequest("COMPOSITE_KEY_PREFIX_NOT_FOUND", "composite api key model prefix was not found")
	ErrCompositeKeyUnsupported           = infraerrors.BadRequest("COMPOSITE_KEY_ENDPOINT_UNSUPPORTED", "composite api key is not supported for this endpoint")
	// ErrAPIKeyExpired reports current timestamp expiration to spending admission
	// callers before total-quota evaluation selects its separate rejection
	// identity.
	ErrAPIKeyExpired = infraerrors.Forbidden("API_KEY_EXPIRED", "The API key has expired.")
	// ErrAPIKeyQuotaExhausted reports that saved total USD usage has reached the
	// positive allowance checked during spending admission.
	ErrAPIKeyQuotaExhausted = infraerrors.TooManyRequests("API_KEY_QUOTA_EXHAUSTED", "The API key quota has been exhausted.")

	// Spending-window errors reuse billing identities so authentication and later
	// funding admission can map the same exhausted-window response.
	ErrAPIKeyRateLimit5hExceeded = billing.ErrAPIKeyRateLimit5hExceeded
	ErrAPIKeyRateLimit1dExceeded = billing.ErrAPIKeyRateLimit1dExceeded
	ErrAPIKeyRateLimit7dExceeded = billing.ErrAPIKeyRateLimit7dExceeded
	// Team lifecycle errors distinguish the assigned member from the paying owner
	// so authentication identifies which active-user requirement failed.
	ErrTeamActorInactive        = infraerrors.Forbidden("TEAM_ACTOR_INACTIVE", "The member assigned to this team API key is inactive")
	ErrTeamBillingOwnerInactive = infraerrors.Forbidden("TEAM_BILLING_OWNER_INACTIVE", "The team billing owner is inactive")

	// ErrAPIKeyNotFound reuses billing's missing-Key identity for credential
	// lookup and hidden managed-record responses exposed by management callers.
	ErrAPIKeyNotFound = billing.ErrAPIKeyNotFound
)

// APIKeyUpdateFields selects configuration and explicit usage-reset columns for
// persistence. Unselected counters retain current database values while billing
// increments them; callers choose reset fields when replacing saved usage state
// is intended.
type APIKeyUpdateFields struct {
	Name      bool // select saved display name
	Status    bool // select saved lifecycle state
	Quota     bool // select total USD allowance
	GroupID   bool // select ordinary group binding
	ExpiresAt bool // select stored expiry pointer
	// CompositeConfiguration updates composite mode and saved mapping rows
	// together. The PostgreSQL writer requires the same transaction for the owning
	// Key update and complete binding replacement.
	CompositeConfiguration bool // select composite mode and bindings
	FastModePolicy         bool // select Key acceleration choice
	// BillingConfiguration writes BillingMode and PreferredSubscriptionID together
	// so their selected funding choice remains one persisted update. Service
	// validation clears the selected ID outside subscription mode.
	BillingConfiguration         bool // select funding mode and subscription
	ModelMapping                 bool // select complete model redirect map
	FallbackWhenGroupUnavailable bool // select unavailable-group preference
	// QuotaUsed selects an explicit total-usage replacement. Ordinary
	// configuration edits leave this false so the loaded view cannot overwrite
	// concurrent billing increments.
	QuotaUsed              bool // select explicit total-usage replacement
	RateLimits             bool // select all USD window allowances
	MaxTokenRateMultiplier bool // select saved Token multiplier ceiling
	// ConcurrencyLimit and RPMLimit select their columns independently so changing
	// or clearing one request allowance retains the other saved value until it is
	// also selected.
	ConcurrencyLimit bool // select active request allowance
	RPMLimit         bool // select per-minute request allowance
	// RateLimitUsage selects all spending counters and starts together for a full
	// manual reset; configuration edits otherwise retain the stored windows.
	RateLimitUsage bool // select all counters and starts
	IPRules        bool // select both IP rule lists
}

// APIKeyRepository persists credentials, group bindings and selected usage or
// presentation observations.
type APIKeyRepository interface {
	// Create persists configuration and complete composite bindings, filling
	// stored identity and timestamps on the supplied record. Implementations
	// coordinate creator Key limits with insertion; caller-owned transaction
	// completion remains with its owner.
	Create(ctx context.Context, key *APIKey) error
	// GetByID reads a nondeleted record with user and binding relationships needed
	// by management. Missing records return ErrAPIKeyNotFound; user-facing callers
	// apply managed visibility and creator ownership before exposing its secret.
	GetByID(ctx context.Context, id int64) (*APIKey, error)
	// GetKeyAndOwnerID returns the stored secret and creator ID needed by
	// credential lifecycle callers. Missing nondeleted records return
	// ErrAPIKeyNotFound; related identity or group data is unnecessary for this
	// read.
	GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error)
	// GetByKey reads a nondeleted credential and its user group restrictions and
	// binding views. Caller authentication checks state, IP and payer eligibility
	// before an accepted lookup becomes request admission.
	GetByKey(ctx context.Context, key string) (*APIKey, error)
	// GetByKeyForAuth loads the selected fields needed to build an authentication
	// snapshot. Team creator identity is hydrated into its current actor and payer
	// afterward; missing credentials return ErrAPIKeyNotFound.
	GetByKeyForAuth(ctx context.Context, key string) (*APIKey, error)
	// Update writes the fields selected by the supplied mask and retains
	// unselected saved values. Callers select usage columns for explicit resets,
	// and implementations keep composite replacement consistent with the owning
	// Key row.
	Update(ctx context.Context, key *APIKey, fields APIKeyUpdateFields) error
	// RotateCredential replaces the supplied secret using creator, old-secret and
	// nondeleted unmanaged eligibility. A lost compare returns rotation conflict;
	// callers invalidate both secrets after successful persistence without
	// replacing configuration or usage columns.
	RotateCredential(ctx context.Context, key *APIKey, oldKey string) error
	// Delete tombstones and soft-deletes the supplied record while retaining its
	// ID. Existing tombstones can return success, while absent IDs report missing;
	// caller authorization and successful-write invalidation occur separately.
	Delete(ctx context.Context, id int64) error
	// DeleteWithAudit retains the historical lifecycle interface name for
	// tombstone deletion and soft deletion. Implementations coordinate their
	// transaction ownership; callers perform authorization beforehand and cache
	// invalidation after a successful operation.
	DeleteWithAudit(ctx context.Context, id int64) error
	// ListByUserID returns a filtered creator-scoped page and pagination metadata
	// for user presentation. The production store excludes managed Keys and adds
	// latest usage IP observations; callers retain repository errors for the
	// request response.
	ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error)
	// VerifyOwnership returns requested nondeleted IDs belonging to the supplied
	// creator. It establishes identifier ownership, while callers needing
	// user-facing credential visibility check that separate rule before returning
	// secret data.
	VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error)
	// CountByUserID returns the creator's nondeleted Key count, including managed
	// credentials. Insertion eligibility is decided by the production writer's own
	// count under the creator row lock; callers treat this separate read as an
	// observation.
	CountByUserID(ctx context.Context, userID int64) (int64, error)
	// ExistsByKey reports whether a nondeleted record already uses the supplied
	// secret. The custom creation path checks it before insertion; storage
	// uniqueness still decides racing writes after this preliminary read.
	ExistsByKey(ctx context.Context, key string) (bool, error)
	// ListByGroupID returns nondeleted credentials bound directly or through a
	// composite mapping to groupID. Administrative consumers may include managed
	// records; query pagination and stored-column ordering belong to the
	// implementation.
	ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]APIKey, *pagination.PaginationResult, error)
	// SearchAPIKeys searches nondeleted names with optional creator scope and a
	// caller-selected limit. It is an administrative lookup that can include
	// managed records; user credential presentation applies its own visibility
	// policy.
	SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]APIKey, error)
	// ClearGroupIDByGroupID clears ordinary bindings and deletes mappings
	// targeting the group, returning affected binding units. Callers needing
	// rollback across both writes supply an owning transaction and invalidate
	// affected authentication after success.
	ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error)
	// UpdateGroupIDByUserAndGroup moves the creator's old ordinary and composite
	// bindings to the target. Existing target mappings retain their prefix;
	// callers own any transaction spanning the complete move and later
	// invalidation.
	UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error)
	// CountByGroupID counts nondeleted credential records bound ordinarily or
	// compositely to the group. A Key with the requested relationship contributes
	// one record regardless of which binding path matched it.
	CountByGroupID(ctx context.Context, groupID int64) (int64, error)
	// ListKeysByUserID enumerates the creator's nondeleted stored secrets for
	// invalidation. The caller hashes them for cache identity and treats read
	// failure as an unsuccessful direct notification attempt.
	ListKeysByUserID(ctx context.Context, userID int64) ([]string, error)
	// ListKeysByGroupID enumerates secrets with an ordinary or composite target
	// binding for invalidation. The caller hashes returned credentials, and
	// underlying lookup failures leave its direct cache invalidation attempt
	// incomplete.
	ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error)
	// IncrementQuotaUsed adds a supplied quota delta for an eligible stored record
	// and returns resulting usage. Service callers skip nonpositive spending
	// costs; admitted settlement follows its separate billing transaction
	// eligibility rules.
	IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error)
	// UpdateLastUsed writes the caller's selected last-use timestamp on a
	// nondeleted credential. The service owns throttling and retry deadlines;
	// repository errors let it choose normal debounce versus failure backoff.
	UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error
	// IncrementRateLimitUsage adds a supplied USD cost to stored spending windows,
	// resetting or initializing them according to billing storage rules. Service
	// callers skip nonpositive costs and keep admitted settlement ownership
	// separate.
	IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error
	// ResetRateLimitWindows resets elapsed saved windows using the store's current
	// transaction time. It leaves current windows and missing saved starts under
	// the implementation's reset rules; explicit full resets use Update masks.
	ResetRateLimitWindows(ctx context.Context, id int64) error
	// GetRateLimitData returns stored window counters and start timestamps for
	// admission or presentation. Missing eligible records return the Key-not-found
	// identity, and callers decide whether their endpoint can proceed after lookup
	// failure.
	GetRateLimitData(ctx context.Context, id int64) (*APIKeyRateLimitData, error)
}

// APIKeyRateLimitData is billing's stored spending counters and associated
// window-start view.
type APIKeyRateLimitData = billing.APIKeyRateLimitData

// APIKeyQuotaUsageState is billing's compact current quota result after atomic
// update.
type APIKeyQuotaUsageState = billing.APIKeyQuotaUsageState

// APIKeyCache stores optional creation counters, usage observations and shared
// authentication entries.
type APIKeyCache interface {
	// GetCreateAttemptCount reads the creator's custom-secret failure count.
	// Missing counters return zero; the service permits creation when this
	// optional cache read fails and enforces its threshold on a usable result.
	GetCreateAttemptCount(ctx context.Context, userID int64) (int, error)
	// IncrementCreateAttemptCount records a failed custom-secret attempt according
	// to the adapter's counter lifetime. The production Redis adapter refreshes a
	// 24-hour expiry, and the service suppresses optional cache write failures.
	IncrementCreateAttemptCount(ctx context.Context, userID int64) error
	// DeleteCreateAttemptCount removes a creator's custom-secret failure counter.
	// Status updates invoke it before Key persistence; deletion invokes it after
	// successful storage. Service callers suppress cleanup errors, and an update
	// can clear the count even when its later write fails.
	DeleteCreateAttemptCount(ctx context.Context, userID int64) error
	// IncrementDailyUsage increments the exact date-named counter supplied by the
	// service. The configured calendar chooses its name, and an increment error is
	// returned before the service attempts expiry refresh.
	IncrementDailyUsage(ctx context.Context, apiKey string) error
	// SetDailyUsageExpiry applies the supplied lifetime to a service-named daily
	// counter. The service refreshes it after successful increment and suppresses
	// expiry failure, retaining the completed daily usage observation.
	SetDailyUsageExpiry(ctx context.Context, apiKey string, ttl time.Duration) error
	// GetAuthCache returns the entry stored under a hashed credential identity.
	// The service treats read failures as misses and validates snapshot version
	// before reconstructing a request-local Key from a positive result.
	GetAuthCache(ctx context.Context, key string) (*APIKeyAuthCacheEntry, error)
	// SetAuthCache publishes the supplied entry under a hashed credential identity
	// and lifetime. The service keeps published data immutable and can suppress an
	// optional shared-write error while returning its loaded request view.
	SetAuthCache(ctx context.Context, key string, entry *APIKeyAuthCacheEntry, ttl time.Duration) error
	// DeleteAuthCache removes a shared entry by hashed credential identity.
	// Service callers coordinate direct cache lifetime and can attempt publication
	// despite deletion failure; durable workers retry failures through their
	// outbox records.
	DeleteAuthCache(ctx context.Context, key string) error
	// PublishAuthCacheInvalidation sends a hashed identity for subscribers to
	// clear local entries. Successful transport acceptance establishes an
	// attempted notification, while missed delivery relies on the durable
	// invalidation protocol or configured expiry.
	PublishAuthCacheInvalidation(ctx context.Context, cacheKey string) error
	// SubscribeAuthCacheInvalidation acknowledges readiness through the stored
	// context callback and supplies cache identities to handler until closure.
	// Callers own cancellation and joining before local cache shutdown; the
	// service retries returned uncanceled failures.
	SubscribeAuthCacheInvalidation(ctx context.Context, handler func(cacheKey string)) error
}

// KeyAuthCacheSubscriptionReadyKey identifies the subscriber acknowledgement
// callback in standard contexts.
type KeyAuthCacheSubscriptionReadyKey struct{}

// APIKeyAuthCacheInvalidator attempts cached credential invalidation by secret,
// creator or group.
type APIKeyAuthCacheInvalidator interface {
	// InvalidateAuthCacheByKey attempts local and shared invalidation of the
	// supplied credential. Service implementations join authentication operation
	// admission and suppress optional cache errors; callers use successful
	// persistence as the notification trigger.
	InvalidateAuthCacheByKey(ctx context.Context, key string)
	// InvalidateAuthCacheByUserID enumerates current nondeleted creator secrets
	// and attempts their cache invalidation. Service implementations skip invalid
	// IDs and suppress enumeration failure, leaving configured durable delivery or
	// expiry to recover later.
	InvalidateAuthCacheByUserID(ctx context.Context, userID int64)
	// InvalidateAuthCacheByGroupID enumerates nondeleted ordinary and composite
	// group secrets and invalidates their cached identities. Service
	// implementations join operation admission and suppress enumeration failures;
	// callers coordinate the underlying group persistence result separately.
	InvalidateAuthCacheByGroupID(ctx context.Context, groupID int64)
}

// CreateAPIKeyRequest carries submitted configuration choices for a personal or
// team credential.
type CreateAPIKeyRequest struct {
	Name string `json:"name"` // submitted credential display name
	// Scope is trimmed and lowercased before validation. Empty selects personal;
	// team creation saves the supplied creator ID while current owner identity
	// supplies permissions and funding.
	Scope string `json:"scope"` // personal or team creation scope
	// GroupID selects a positive ordinary target when IsComposite is false.
	// Composite input requires it absent and supplies complete CompositeGroups;
	// service validates all target permissions before persistence. The returned
	// ordinary Key retains the supplied GroupID pointer.
	GroupID         *int64                      `json:"group_id"`         // requested ordinary group identifier
	IsComposite     bool                        `json:"is_composite"`     // requested composite selection mode
	CompositeGroups []APIKeyCompositeGroupInput `json:"composite_groups"` // complete submitted prefix group bindings
	// CustomKey is used when present and nonempty; otherwise generation supplies
	// the secret. Create checks custom syntax and duplicates, and only the
	// duplicate path records the failure counter.
	CustomKey *string `json:"custom_key"` // optional supplied authentication secret
	// IPWhitelist and IPBlacklist remain attached to the returned Key after
	// creation. Callers own the supplied slices while persistence and IP
	// compilation read their patterns.
	IPWhitelist    []string `json:"ip_whitelist"`     // supplied allowed address patterns
	IPBlacklist    []string `json:"ip_blacklist"`     // supplied denied address patterns
	FastModePolicy string   `json:"fast_mode_policy"` // submitted Key acceleration choice
	// BillingMode and PreferredSubscriptionID are validated against the current
	// payer before bindings are persisted. Automatic and balance modes clear the
	// preferred ID; specified funding requires an effective owned subscription.
	BillingMode             string            `json:"billing_mode"`              // submitted funding source choice
	PreferredSubscriptionID *int64            `json:"preferred_subscription_id"` // selected payer subscription identifier
	ModelMapping            map[string]string `json:"model_mapping"`             // complete supplied model redirect rules
	Quota                   float64           `json:"quota"`                     // total USD allowance; zero unbounded
	// ExpiresInDays omission keeps expiration absent. Positive input adds calendar
	// days to the configured service time; zero and negative values reject before
	// repository access.
	ExpiresInDays *int `json:"expires_in_days"` // optional validity in calendar days
	// MaxTokenRateMultiplier caps the effective group Token multiplier. Zero
	// disables protection; positive values reject requests above the saved ceiling.
	MaxTokenRateMultiplier float64 `json:"max_token_rate_multiplier"` // maximum effective Token multiplier
	ConcurrencyLimit       int     `json:"concurrency_limit"`         // maximum admitted active requests
	RPMLimit               int     `json:"rpm_limit"`                 // maximum admitted requests per minute
	RateLimit5h            float64 `json:"rate_limit_5h"`             // five-hour USD spending allowance
	RateLimit1d            float64 `json:"rate_limit_1d"`             // one-day USD spending allowance
	RateLimit7d            float64 `json:"rate_limit_7d"`             // seven-day USD spending allowance
	// FallbackWhenGroupUnavailable defaults true when absent. A supplied value is
	// saved for later request target selection, whose eligibility and payer checks
	// remain independent of this preference.
	FallbackWhenGroupUnavailable *bool `json:"fallback_when_group_unavailable"` // optional unavailable-target selection preference
}

// APIKeyBillingSubscriptionOption presents an effective payer subscription and
// its configured group range.
type APIKeyBillingSubscriptionOption struct {
	ID               int64     // offered subscription record identifier
	PlanID           int64     // offered subscription plan identifier
	PlanName         string    // request-localized subscription plan name
	ExpiresAt        time.Time // offered subscription expiry timestamp
	GroupsRestricted bool      // plan group restriction presence
	ApplicableGroups []int64   // copied applicable group identifiers
}

// UpdateAPIKeyRequest retains omitted configuration separately from
// replacements and explicit usage resets.
type UpdateAPIKeyRequest struct {
	Name    *string `json:"name"`     // optional submitted display name
	GroupID *int64  `json:"group_id"` // optional ordinary target identifier
	// IsComposite selects the final Key mode when present. Converting to composite
	// requires complete CompositeGroups; converting back requires a positive
	// GroupID, and composite input rejects a simultaneous ordinary target.
	IsComposite *bool `json:"is_composite"` // optional target composite mode
	// CompositeGroups leaves bindings unchanged when nil. A supplied list replaces
	// every saved binding, and an empty list fails composite validation before
	// persistence.
	CompositeGroups *[]APIKeyCompositeGroupInput `json:"composite_groups"` // optional complete binding replacement
	// Status replaces the saved lifecycle value when present. Requesting active on
	// an owner-disabled team Key rejects, and supplied status also attempts
	// creator-counter cleanup before persistence.
	Status *string `json:"status"` // optional credential lifecycle state
	// IPWhitelist and IPBlacklist leave their saved lists unchanged when nil.
	// Supplied empty slices clear the selected list, and accepted replacements
	// retain the supplied slice storage.
	IPWhitelist    *[]string `json:"ip_whitelist"`     // optional allowed-pattern replacement
	IPBlacklist    *[]string `json:"ip_blacklist"`     // optional denied-pattern replacement
	FastModePolicy *string   `json:"fast_mode_policy"` // optional Key acceleration replacement
	// BillingMode and PreferredSubscriptionID combine with saved funding values
	// when omitted. A supplied non-subscription mode clears the preferred ID;
	// keeping subscription mode requires an effective owned selection.
	BillingMode             *string `json:"billing_mode"`              // optional funding mode replacement
	PreferredSubscriptionID *int64  `json:"preferred_subscription_id"` // optional selected subscription replacement
	// ModelMapping leaves saved redirects unchanged when nil. A supplied map is
	// normalized into replacement rules, with an empty map clearing the
	// configuration.
	ModelMapping *map[string]string `json:"model_mapping"` // optional complete model-rule replacement
	Quota        *float64           `json:"quota"`         // optional total USD allowance
	// ExpiresAt replaces the stored timestamp when present, retaining the supplied
	// pointer. ClearExpiration takes precedence and removes expiry; both
	// operations can reactivate an expired status under the update rules.
	ExpiresAt       *time.Time `json:"expires_at"` // optional expiry timestamp replacement
	ClearExpiration bool       `json:"-"`          // selected stored expiry clearing
	// ResetQuota requests total-usage replacement when present and true. Its
	// dedicated write mask distinguishes an intentional reset from ordinary
	// quota-allowance changes.
	ResetQuota *bool `json:"reset_quota"` // optional total-usage reset request
	// MaxTokenRateMultiplier retains the saved ceiling when nil. Supplied zero
	// disables protection; positive values reject higher effective group Token rates.
	MaxTokenRateMultiplier *float64 `json:"max_token_rate_multiplier"` // optional Token multiplier ceiling
	// ConcurrencyLimit and RPMLimit retain the saved request settings when nil.
	// Supplied zero clears the selected allowance, while positive replacements are
	// validated before any repository lookup.
	ConcurrencyLimit *int     `json:"concurrency_limit"` // optional active request allowance
	RPMLimit         *int     `json:"rpm_limit"`         // optional per-minute request allowance
	RateLimit5h      *float64 `json:"rate_limit_5h"`     // optional five-hour USD allowance
	RateLimit1d      *float64 `json:"rate_limit_1d"`     // optional one-day USD allowance
	RateLimit7d      *float64 `json:"rate_limit_7d"`     // optional seven-day USD allowance
	// ResetRateLimitUsage requests complete counter and window-start replacement
	// when present and true. The service attempts spending-cache invalidation
	// after successful persistence.
	ResetRateLimitUsage          *bool `json:"reset_rate_limit_usage"`          // optional spending-window reset request
	FallbackWhenGroupUnavailable *bool `json:"fallback_when_group_unavailable"` // optional unavailable-target preference replacement
}

// RateLimitCacheInvalidator clears cached spending-window observations after
// persisted manual resets.
type RateLimitCacheInvalidator interface {
	// InvalidateAPIKeyRateLimit clears cached spending-window usage after a
	// persisted full reset. Callers pass the Key ID after successful storage and
	// treat returned failures as optional notification errors beside the accepted
	// reset.
	InvalidateAPIKeyRateLimit(ctx context.Context, keyID int64) error
}

// APIKeyService validates Key configuration and coordinates authentication data
// and runtime observations.
type APIKeyService struct {
	calendar timezone.Calendar // captured daily-counter calendar choice
	// groupFastPolicy is installed during assembly and read during Key policy
	// resolution. Callers finish callback configuration before concurrent requests
	// use the service.
	groupFastPolicy func(string, bool) string // optional group acceleration callback
	// runtimeStart, runtimeStop, runtimeMu, operations and stopped coordinate
	// authentication resource lifetime. Start and StopContext share runtimeMu; the
	// operation gate closes admission before its monitor waits and closes local
	// caches.
	runtimeStart sync.Once         // one-time resource startup guard
	runtimeStop  sync.Once         // one-time resource shutdown guard
	runtimeMu    sync.Mutex        // resource startup shutdown ordering lock
	operations   authOperationGate // admitted authentication work counter
	stopped      chan struct{}     // shared resource teardown completion signal
	// subscriberMu serializes subscription registration with permanent stopping.
	// subscriberStopped prevents a late subscriber from starting after shutdown
	// has already claimed its lifecycle.
	subscriberMu      sync.Mutex // subscription startup shutdown ordering lock
	subscriberStopped bool       // permanent subscription stop flag
	// apiKeyRepo, userRepo, groupRepo, userSubRepo and userGroupRateRepo are
	// retained dependencies. Callers keep them usable through operations and bind
	// optional cache, team, count and invalidation readers before concurrent
	// service use.
	apiKeyRepo            APIKeyRepository           // credential persistence and read dependency
	userRepo              UserRepository             // creator and payer identity reader
	groupRepo             GroupRepository            // binding and runtime group reader
	userSubRepo           UserSubscriptionRepository // specified payer subscription reader
	userGroupRateRepo     UserGroupRateRepository    // payer-group multiplier and RPM reader
	cache                 APIKeyCache                // optional shared authentication cache adapter
	rateLimitCacheInvalid RateLimitCacheInvalidator  // optional spending-cache reset dependency
	concurrencyService    ConcurrencyReader          // optional live Key-count observation reader
	teamRepo              TeamRepository             // optional current team context reader
	// cfg is retained, while authCfg, calendar, lookup capacity and
	// invalidAuthAbuse capture startup choices. Later clock, generation, team and
	// group callback calls can still read cfg, whose mutation is unsynchronized.
	cfg *Options // retained startup and callback Options
	// authCacheL1 and authNegativeCacheL1 publish optional local caches
	// atomically. Start allocates enabled layers; teardown closes them after
	// admitted operations and the subscriber finish. Their pointers remain stored
	// afterward, so direct users coordinate access with resource lifetime.
	authCacheL1         atomic.Pointer[ristretto.Cache[string, any]] // atomic positive local cache pointer
	authNegativeCacheL1 atomic.Pointer[ristretto.Cache[string, any]] // atomic missing-credential cache pointer
	authCfg             KeyApiKeyAuthCacheConfig                     // captured cache enablement and lifetimes
	authGroup           singleflight.Group                           // shared same-credential load coordinator
	// authLookupSlots bounds direct credential loads. A nil channel bypasses
	// accounting; otherwise authLookupTotal counts attempts, authLookupRejected
	// counts overload, and authLookupInFlight counts held positions sampled
	// independently by AuthLookupMetrics.
	authLookupSlots    chan struct{}               // bounded direct storage admission channel
	authLookupTotal    atomic.Uint64               // direct lookup admission attempt count
	authLookupRejected atomic.Uint64               // direct lookup overload rejection count
	authLookupInFlight atomic.Int64                // currently held direct lookup positions
	invalidAuthAbuse   *KeyInvalidAuthAbuseLimiter // optional bounded failed-client tracker
	// authInvalidationStart, authInvalidationStop, authInvalidationCancel and
	// authInvalidationWG coordinate one subscriber's registration, cancellation
	// and join. Connected and failure atomics supply separately sampled health
	// observations after acknowledgement or uncanceled closure.
	authInvalidationStart     sync.Once          // one-time subscriber registration guard
	authInvalidationStop      sync.Once          // one-time subscriber teardown guard
	authInvalidationCancel    context.CancelFunc // owned subscriber context cancellation function
	authInvalidationWG        sync.WaitGroup     // registered subscriber worker lifetime
	authInvalidationConnected atomic.Bool        // observed subscription acknowledgement flag
	authInvalidationFailures  atomic.Uint64      // uncanceled subscription return count
	// lastUsedTouchL1 stores Key-ID deadlines after successful or failed writes.
	// lastUsedTouchSF coalesces concurrent touches and rechecks that deadline
	// inside the winning callback before applying the configured clock.
	lastUsedTouchL1 sync.Map           // Key ID to next-write deadline
	lastUsedTouchSF singleflight.Group // same-Key touch write coordinator
}

// APIKeyAuthLookupMetrics reports independently sampled bounded storage lookup
// activity counters.
type APIKeyAuthLookupMetrics struct {
	Total    uint64 `json:"total"`     // sampled direct admission attempts
	Rejected uint64 `json:"rejected"`  // sampled overload rejection attempts
	InFlight int64  `json:"in_flight"` // sampled held storage positions
	Capacity int    `json:"capacity"`  // configured storage admission positions
}

// NewAPIKeyLimitReachedError returns the Key-limit conflict carrying current
// and configured count metadata.
func NewAPIKeyLimitReachedError(current int64, limit int) error {
	return ErrAPIKeyLimitReached.WithMetadata(map[string]string{
		"current": strconv.FormatInt(current, 10),
		"limit":   strconv.Itoa(limit),
	})
}

// IsEmpty reports whether the field selection requests no persisted Key
// changes.
func (f APIKeyUpdateFields) IsEmpty() bool {
	return f == APIKeyUpdateFields{}
}

// KeyWithAuthCacheSubscriptionReady stores the supplied acknowledgement
// callback in a derived context. The callback can be nil, which
// NotifyAuthCacheSubscriptionReady skips; callers provide a nonnil context and
// coordinate callback use with subscriber startup.
func KeyWithAuthCacheSubscriptionReady(ctx context.Context, ready func()) context.Context {
	return context.WithValue(ctx, KeyAuthCacheSubscriptionReadyKey{}, ready)
}

// NotifyAuthCacheSubscriptionReady invokes the stored nonnil function when its
// type matches the acknowledgement callback. Missing or differently typed
// values do nothing; callers provide a usable context after Redis confirms
// subscription readiness.
func NotifyAuthCacheSubscriptionReady(ctx context.Context) {
	if ready, ok := ctx.Value(KeyAuthCacheSubscriptionReadyKey{}).(func()); ok && ready != nil {
		ready()
	}
}

// ValidateAPIKeyLimit checks a finite nonnegative allowance or multiplier below
// the exclusive DECIMAL integer bound. Rejection returns the field name and maximum
// in error metadata; zero remains valid for callers selecting an unbounded
// allowance.
func ValidateAPIKeyLimit(field string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value >= KeyApiKeyLimitUpperBound {
		return ErrAPIKeyLimitInvalid.WithMetadata(map[string]string{
			"field":         field,
			"max_exclusive": strconv.FormatFloat(KeyApiKeyLimitUpperBound, 'f', 0, 64),
		})
	}
	return nil
}

// ValidateAPIKeyExpiresInDays accepts positive relative validity and rejects
// zero or negative input. Omitted expiry is represented by a nil request
// pointer before this helper is invoked; calendar conversion belongs to
// creation.
func ValidateAPIKeyExpiresInDays(days int) error {
	if days <= 0 {
		return ErrAPIKeyExpiryInvalid
	}
	return nil
}

// KeyValidateCreateAPIKeyRequest checks request-count bounds, the Token rate
// ceiling, and monetary allowance fields before relative expiry. Zero amounts
// are valid defaults, omitted expiry remains unbounded, and invalid amounts report their
// specific field through the shared validator.
func KeyValidateCreateAPIKeyRequest(req CreateAPIKeyRequest) error {
	if err := ValidateRequestLimits(&req.ConcurrencyLimit, &req.RPMLimit); err != nil {
		return err
	}
	limits := []struct {
		field string  // allowance validation field name
		value float64 // supplied numeric allowance input
	}{
		{field: "max_token_rate_multiplier", value: req.MaxTokenRateMultiplier},
		{field: "quota", value: req.Quota},
		{field: "rate_limit_5h", value: req.RateLimit5h},
		{field: "rate_limit_1d", value: req.RateLimit1d},
		{field: "rate_limit_7d", value: req.RateLimit7d},
	}
	for _, limit := range limits {
		if err := ValidateAPIKeyLimit(limit.field, limit.value); err != nil {
			return err
		}
	}
	if req.ExpiresInDays != nil {
		return ValidateAPIKeyExpiresInDays(*req.ExpiresInDays)
	}
	return nil
}

// KeyValidateUpdateAPIKeyRequest checks request-count replacements and supplied
// Token rate and monetary allowances in update input. Nil pointers are omitted,
// zero clears an allowance, and invalid numeric input rejects before repository
// access.
func KeyValidateUpdateAPIKeyRequest(req UpdateAPIKeyRequest) error {
	if err := ValidateRequestLimits(req.ConcurrencyLimit, req.RPMLimit); err != nil {
		return err
	}
	limits := []struct {
		field string   // allowance validation field name
		value *float64 // supplied numeric allowance input
	}{
		{field: "max_token_rate_multiplier", value: req.MaxTokenRateMultiplier},
		{field: "quota", value: req.Quota},
		{field: "rate_limit_5h", value: req.RateLimit5h},
		{field: "rate_limit_1d", value: req.RateLimit1d},
		{field: "rate_limit_7d", value: req.RateLimit7d},
	}
	for _, limit := range limits {
		if limit.value == nil {
			continue
		}
		if err := ValidateAPIKeyLimit(limit.field, *limit.value); err != nil {
			return err
		}
	}
	return nil
}

// NewAPIKeyService constructs an unstarted Key service retaining its
// repositories, cache and Options pointer. It captures cache settings, lookup
// capacity and invalid-client tracking, while later clock, team and generation
// calls read retained Options.
//
// A positive configured lookup capacity overrides the default, and a channel
// bounds storage loads. Nil Options disables cache and abuse configuration
// while allowing authentication-only fixtures; credential generation requires
// usable Options. Callers bind optional dependencies before concurrent
// operations, call Start for cache resources, and stop the service after
// admitted work and external worker ownership are drained.
func NewAPIKeyService(
	apiKeyRepo APIKeyRepository,
	userRepo UserRepository,
	groupRepo GroupRepository,
	userSubRepo UserSubscriptionRepository,
	userGroupRateRepo UserGroupRateRepository,
	cache APIKeyCache,
	cfg *Options,
) *APIKeyService {
	calendar := timezone.NewCalendar(time.Local)
	if cfg != nil {
		calendar = timezone.NewCalendar(cfg.Calendar.Location())
	}
	svc := &APIKeyService{
		calendar:          calendar,
		apiKeyRepo:        apiKeyRepo,
		userRepo:          userRepo,
		groupRepo:         groupRepo,
		userSubRepo:       userSubRepo,
		userGroupRateRepo: userGroupRateRepo,
		cache:             cache,
		cfg:               cfg,
	}
	svc.authCfg = KeyNewAPIKeyAuthCacheConfig(cfg)
	lookupConcurrency := KeyDefaultAuthLookupConcurrency
	if cfg != nil && cfg.APIKeyAuth.LookupConcurrency > 0 {
		lookupConcurrency = cfg.APIKeyAuth.LookupConcurrency
	}
	svc.authLookupSlots = make(chan struct{}, lookupConcurrency)
	svc.invalidAuthAbuse = KeyNewInvalidAuthAbuseLimiter(cfg)
	return svc
}

// AuthLookupMetrics returns separately sampled load totals, rejections,
// in-flight work and capacity. Nil services return zero metrics, and the direct
// no-channel lookup configuration does not populate those counters.
func (s *APIKeyService) AuthLookupMetrics() APIKeyAuthLookupMetrics {
	if s == nil {
		return APIKeyAuthLookupMetrics{}
	}
	return APIKeyAuthLookupMetrics{
		Total:    s.authLookupTotal.Load(),
		Rejected: s.authLookupRejected.Load(),
		InFlight: s.authLookupInFlight.Load(),
		Capacity: cap(s.authLookupSlots),
	}
}

// SetRateLimitCacheInvalidator binds optional spending-cache cleanup used after
// manual window resets. The service retains this dependency without
// synchronization; callers install it during assembly before concurrent Key
// updates begin.
func (s *APIKeyService) SetRateLimitCacheInvalidator(inv RateLimitCacheInvalidator) {
	s.rateLimitCacheInvalid = inv
}

// SetConcurrencyService binds the optional reader for observed active counts in
// console responses. Nil disables live-count enrichment, and callers configure
// this unsynchronized dependency before concurrent listing or detail reads.
func (s *APIKeyService) SetConcurrencyService(concurrencyService ConcurrencyReader) {
	s.concurrencyService = concurrencyService
}

// SetTeamRepository binds current team membership and owner lookups for team
// Key operations. The setter is unsynchronized; callers provide the dependency
// before concurrent team authentication, scope resolution or configuration
// changes.
func (s *APIKeyService) SetTeamRepository(repo TeamRepository) {
	s.teamRepo = repo
}

// KeyCompileAPIKeyIPRules replaces the Key's compiled IP views from its stored
// pattern lists. Nil input does nothing; compilation skips malformed patterns
// while retaining their configured count for restrictive allowlist
// interpretation.
func (s *APIKeyService) KeyCompileAPIKeyIPRules(apiKey *APIKey) {
	if apiKey == nil {
		return
	}
	apiKey.CompiledIPWhitelist = ipmatch.CompileIPRules(apiKey.IPWhitelist)
	apiKey.CompiledIPBlacklist = ipmatch.CompileIPRules(apiKey.IPBlacklist)
}

// GenerateKey returns a cryptographically generated credential using the
// retained Options prefix. Callers provide nonnil Options; an empty or
// whitespace-only prefix selects sk-. The shared generator returns complete
// text with nil error. An io.ReadFull error in crypto/rand.Read terminates the
// process.
func (s *APIKeyService) GenerateKey() (string, error) {
	prefix := s.cfg.Default.APIKeyPrefix
	if prefix == "" {
		prefix = "sk-"
	}
	return GenerateAPIKeyString(prefix)
}

// GenerateAPIKeyString returns prefix plus hexadecimal encoding of thirty-two
// random bytes with nil error. Empty or whitespace-only prefix selects sk-;
// other supplied text is retained unchanged. crypto/rand.Read terminates the
// process when its io.ReadFull operation reports an error.
func GenerateAPIKeyString(prefix string) (string, error) {
	// Thirty-two random bytes become sixty-four hexadecimal characters, giving
	// generated secrets a fixed-size payload after their configured prefix.
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	if strings.TrimSpace(prefix) == "" {
		prefix = "sk-"
	}

	key := prefix + hex.EncodeToString(bytes)
	return key, nil
}

// ValidateCustomKey requires at least sixteen bytes containing ASCII letters,
// digits, underscore or hyphen. It reports length failure before character
// failure; storage length and lookup-header bounds are enforced at their
// separate consumers.
func (s *APIKeyService) ValidateCustomKey(key string) error {
	// Count bytes before the ASCII pass so short multibyte strings receive length
	// rejection before their characters are examined.
	if len(key) < 16 {
		return ErrAPIKeyTooShort
	}

	for _, c := range key {
		if (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '_' || c == '-' {
			continue
		}
		return ErrAPIKeyInvalidChars
	}

	return nil
}

// KeyCheckAPIKeyRateLimit rejects a custom-credential attempt after the saved
// user failure count reaches its threshold. Missing cache or failed counter
// reads permit creation; the configured cache adapter controls the actual count
// lifetime.
func (s *APIKeyService) KeyCheckAPIKeyRateLimit(ctx context.Context, userID int64) error {
	if s.cache == nil {
		return nil
	}

	count, err := s.cache.GetCreateAttemptCount(ctx, userID)
	if err != nil {
		// A failed optional counter read cannot supply an enforceable count, so
		// credential creation continues to its syntax and uniqueness checks.
		return nil
	}

	// The threshold follows the saved counter lifetime, which the Redis adapter
	// refreshes for twenty-four hours after each increment.
	if count >= KeyApiKeyMaxErrorsPerHour {
		return ErrAPIKeyRateLimited
	}

	return nil
}

// KeyIncrementAPIKeyErrorCount attempts to increment the user's
// custom-credential failure counter. Missing cache does nothing, and cache
// failures are suppressed; Create invokes this path for an already used custom
// secret.
func (s *APIKeyService) KeyIncrementAPIKeyErrorCount(ctx context.Context, userID int64) {
	if s.cache == nil {
		return
	}

	// Record the duplicate-secret observation as optional cache work; failure
	// leaves Create returning its already established credential conflict.
	_ = s.cache.IncrementCreateAttemptCount(ctx, userID)
}

// KeyCanUserBindGroup delegates binding eligibility to the supplied user view
// for the selected group. Callers provide a nonnil group; a nil user returns
// false under identity's permission rule.
func (s *APIKeyService) KeyCanUserBindGroup(ctx context.Context, user *User, group *routing.Group) bool {
	return user.CanBindGroup(group.ID, group.IsExclusive)
}

// KeyResolveAPIKeyBillingConfiguration returns the normalized funding mode,
// selected ID and validated specified subscription. Non-subscription modes
// clear the selected ID.
//
// Subscription funding requires a positive identifier and usable payer lookup,
// ownership, current effectiveness and a present plan. Lookup failure or
// invalid state becomes the specified-subscription error. Callers supply the
// current owner ID for team funding and subsequently check every final group
// against the returned plan before storing or admitting the Key.
func (s *APIKeyService) KeyResolveAPIKeyBillingConfiguration(ctx context.Context, billingUserID int64, rawMode string, preferredSubscriptionID *int64) (string, *int64, *UserSubscription, error) {
	mode, ok := NormalizeAPIKeyBillingMode(rawMode)
	if !ok {
		return "", nil, nil, ErrInvalidAPIKeyBillingMode
	}
	if mode != APIKeyBillingModeSubscription {
		return mode, nil, nil, nil
	}
	if preferredSubscriptionID == nil || *preferredSubscriptionID <= 0 {
		return "", nil, nil, ErrPreferredSubscriptionRequired
	}
	if s == nil || s.userSubRepo == nil || billingUserID <= 0 {
		return "", nil, nil, ErrPreferredSubscriptionInvalid
	}

	// Treat failed reads and unusable subscription data as the same
	// specified-funding rejection, since neither supplies a valid payer-owned plan
	// for later group checks.
	subscription, err := s.userSubRepo.GetByID(ctx, *preferredSubscriptionID)
	if err != nil || subscription == nil || subscription.UserID != billingUserID || !subscription.IsEffective() || subscription.Plan == nil {
		return "", nil, nil, ErrPreferredSubscriptionInvalid
	}
	id := subscription.ID
	return mode, &id, subscription, nil
}

// KeyValidatePreferredSubscriptionGroups checks ordinary and composite bindings
// against the supplied plan. Missing subscription or plan rejects; a restricted
// plan also requires some concrete binding, while an unrestricted plan leaves
// group permission to the caller. Every positive binding must fit the plan.
// Callers provide already validated composite identifiers; entries with
// nonpositive IDs are skipped by this range check, so it cannot replace
// binding-input validation.
func KeyValidatePreferredSubscriptionGroups(subscription *UserSubscription, group *routing.Group, compositeGroups []APIKeyCompositeGroup) error {
	if subscription == nil || subscription.Plan == nil {
		return ErrPreferredSubscriptionInvalid
	}
	if len(subscription.Plan.GroupIDs) > 0 && (group == nil || group.ID <= 0) && len(compositeGroups) == 0 {
		return ErrPreferredSubscriptionGroup
	}
	if group != nil && group.ID > 0 && !subscriptionPlanIncludesGroup(subscription.Plan, group.ID) {
		return ErrPreferredSubscriptionGroup
	}
	for _, binding := range compositeGroups {
		if binding.GroupID > 0 && !subscriptionPlanIncludesGroup(subscription.Plan, binding.GroupID) {
			return ErrPreferredSubscriptionGroup
		}
	}
	return nil
}

// KeyBillingUserIDForScope returns the caller ID for every scope except trimmed
// case-insensitive team. Team scope requires enabled support and a current
// context owner; repository errors are returned, and callers supply
// production-complete contexts for successful team reads.
func (s *APIKeyService) KeyBillingUserIDForScope(ctx context.Context, userID int64, scope string) (int64, error) {
	if !strings.EqualFold(strings.TrimSpace(scope), "team") {
		return userID, nil
	}
	if s.cfg != nil && !s.cfg.Team.Enabled {
		return 0, ErrTeamFeatureDisabled
	}
	if s.teamRepo == nil {
		return 0, ErrTeamFeatureDisabled
	}
	teamCtx, err := s.teamRepo.GetContextByUserID(ctx, userID)
	if err != nil {
		return 0, err
	}
	return teamCtx.Owner.UserID, nil
}

// KeyBillingUserForAPIKey resolves the current payer even when a disabled team
// Key retains its creator view. Nil input, a nonpositive creator ID or a nil
// service reports a missing-user error.
//
// Personal Keys reuse a matching User view or load their creator. Team Keys
// require enabled support, current matching team context, owner and a member
// join time no later than Key creation, then load the owner. This helper
// returns the supplied or repository user pointer; callers needing independent
// fields copy that view before request mutation.
func (s *APIKeyService) KeyBillingUserForAPIKey(ctx context.Context, apiKey *APIKey) (*User, error) {
	if apiKey == nil || apiKey.UserID <= 0 || s == nil {
		return nil, ErrUserNotFound
	}
	if apiKey.TeamID == nil {
		if apiKey.User != nil && apiKey.User.ID == apiKey.UserID {
			return apiKey.User, nil
		}
		if s.userRepo == nil {
			return nil, ErrUserNotFound
		}
		return s.userRepo.GetByID(ctx, apiKey.UserID)
	}
	if s.userRepo == nil {
		return nil, ErrUserNotFound
	}
	if s.cfg != nil && !s.cfg.Team.Enabled {
		return nil, ErrTeamFeatureDisabled
	}
	if s.teamRepo == nil {
		return nil, ErrTeamFeatureDisabled
	}
	teamCtx, err := s.teamRepo.GetContextByUserID(ctx, apiKey.UserID)
	if err != nil {
		return nil, err
	}
	if teamCtx == nil || teamCtx.Team == nil || teamCtx.Membership == nil || teamCtx.Owner == nil || teamCtx.Team.ID != *apiKey.TeamID {
		return nil, ErrTeamMembershipRequired
	}
	if teamCtx.Membership.JoinedAt.After(apiKey.CreatedAt) {
		return nil, ErrTeamMembershipRequired
	}
	return s.userRepo.GetByID(ctx, teamCtx.Owner.UserID)
}

// KeyCanUserUseBoundGroup checks composite binding scope and public-group
// permissions for an existing Key view. Missing binding data returns true;
// otherwise a target outside composite bindings rejects before permission
// handling. Exclusive groups pass this check. A loaded restriction set is used
// directly, while missing user data or repository and failed user reads permit
// it. ResolveRuntimeGroup independently validates payer binding permission for
// a selected runtime target.
func (s *APIKeyService) KeyCanUserUseBoundGroup(ctx context.Context, apiKey *APIKey) bool {
	if apiKey == nil || apiKey.GroupID == nil || apiKey.Group == nil {
		return true
	}
	if !apiKey.AllowsRuntimeGroup(*apiKey.GroupID) {
		return false
	}
	if apiKey.Group.IsExclusive {
		return true
	}
	user := apiKey.User
	if user == nil {
		return true
	}
	if user.GroupRestrictionsLoaded {
		return user.CanBindGroup(apiKey.Group.ID, apiKey.Group.IsExclusive)
	}
	if s.userRepo == nil {
		return true
	}
	// Keep the loaded view after this optional refresh fails; public-group
	// permission rejection requires usable restriction data in this path.
	loadedUser, err := s.userRepo.GetByID(ctx, apiKey.UserID)
	if err != nil {
		return true
	}
	return loadedUser.CanBindGroup(apiKey.Group.ID, apiKey.Group.IsExclusive)
}

// Create validates and persists a personal or team Key with its configured
// binding and funding choice. Ordinary input requires a positive group, and
// numeric, policy and model-rule validation precedes repository access.
//
// The creator is loaded before team scope selects the current owner as payer;
// saved UserID remains the creator. IP sets and all ordinary or composite
// bindings are checked before specified-subscription plan coverage. A custom
// secret receives count and duplicate checks; only a duplicate increments the
// failure counter. Successful storage precedes best-effort authentication
// invalidation and IP compilation. The returned ordinary GroupID pointer and IP
// slices retain input storage; composite records are validated replacements.
func (s *APIKeyService) Create(ctx context.Context, userID int64, req CreateAPIKeyRequest) (*APIKey, error) {
	if !req.IsComposite && (req.GroupID == nil || *req.GroupID <= 0) {
		return nil, infraerrors.BadRequest("GROUP_REQUIRED", "API Key must be assigned to a group")
	}
	if err := KeyValidateCreateAPIKeyRequest(req); err != nil {
		return nil, err
	}
	fastModePolicy, ok := NormalizeAPIKeyFastModePolicy(req.FastModePolicy)
	if !ok {
		return nil, ErrInvalidAPIKeyFastModePolicy
	}
	modelMapping, err := NormalizeAPIKeyModelMapping(req.ModelMapping)
	if err != nil {
		return nil, err
	}

	// Keep the supplied creator as actor while resolving personal or team funding;
	// a team owner supplies permissions even when another member creates the
	// credential.
	actor, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	user := actor
	var teamID *int64
	scope := strings.ToLower(strings.TrimSpace(req.Scope))
	if scope == "" {
		scope = "personal"
	}
	if scope != "personal" && scope != "team" {
		return nil, infraerrors.BadRequest("API_KEY_SCOPE_INVALID", "API key scope must be personal or team")
	}
	if scope == "team" {
		if s.cfg != nil && !s.cfg.Team.Enabled {
			return nil, ErrTeamFeatureDisabled
		}
		if s.teamRepo == nil {
			return nil, ErrTeamFeatureDisabled
		}
		teamCtx, teamErr := s.teamRepo.GetContextByUserID(ctx, userID)
		if teamErr != nil {
			return nil, teamErr
		}
		if teamCtx.Team.Status != TeamStatusActive {
			return nil, ErrTeamSuspended
		}
		user, err = s.userRepo.GetByID(ctx, teamCtx.Owner.UserID)
		if err != nil {
			return nil, fmt.Errorf("get team owner: %w", err)
		}
		id := teamCtx.Team.ID
		teamID = &id
	}

	// Use the current payer for subscription selection and group permissions,
	// because a team's saved creator can differ from the account funding requests.
	billingMode, preferredSubscriptionID, preferredSubscription, err := s.KeyResolveAPIKeyBillingConfiguration(
		ctx,
		user.ID,
		req.BillingMode,
		req.PreferredSubscriptionID,
	)
	if err != nil {
		return nil, err
	}

	if len(req.IPWhitelist) > 0 {
		if invalid := ipmatch.ValidateIPPatterns(req.IPWhitelist); len(invalid) > 0 {
			return nil, fmt.Errorf("%w: %v", ErrInvalidIPPattern, invalid)
		}
	}

	if len(req.IPBlacklist) > 0 {
		if invalid := ipmatch.ValidateIPPatterns(req.IPBlacklist); len(invalid) > 0 {
			return nil, fmt.Errorf("%w: %v", ErrInvalidIPPattern, invalid)
		}
	}

	var compositeGroups []APIKeyCompositeGroup
	if req.IsComposite {
		if req.GroupID != nil {
			return nil, ErrCompositeKeyGroupConflict
		}
		compositeGroups, err = s.KeyPrepareCompositeGroups(ctx, user, req.CompositeGroups)
		if err != nil {
			return nil, err
		}
	} else if req.GroupID != nil {
		group, err := s.groupRepo.GetByID(ctx, *req.GroupID)
		if err != nil {
			return nil, fmt.Errorf("get group: %w", err)
		}

		if !s.KeyCanUserBindGroup(ctx, user, group) {
			return nil, ErrGroupNotAllowed
		}
	}
	if billingMode == APIKeyBillingModeSubscription {
		var group *routing.Group
		if req.GroupID != nil {
			group, err = s.groupRepo.GetByID(ctx, *req.GroupID)
			if err != nil {
				return nil, fmt.Errorf("get group: %w", err)
			}
		}
		if err := KeyValidatePreferredSubscriptionGroups(preferredSubscription, group, compositeGroups); err != nil {
			return nil, err
		}
	}

	var key string

	// Check the saved failure count, then syntax, then duplicate storage before
	// selecting a custom secret; storage uniqueness still determines a racing
	// insert.
	if req.CustomKey != nil && *req.CustomKey != "" {
		if err := s.KeyCheckAPIKeyRateLimit(ctx, userID); err != nil {
			return nil, err
		}

		if err := s.ValidateCustomKey(*req.CustomKey); err != nil {
			return nil, err
		}

		exists, err := s.apiKeyRepo.ExistsByKey(ctx, *req.CustomKey)
		if err != nil {
			return nil, fmt.Errorf("check key exists: %w", err)
		}
		if exists {
			// Record duplicate custom secrets before returning conflict; syntax
			// rejection occurs earlier and does not contribute to this saved failure
			// counter.
			s.KeyIncrementAPIKeyErrorCount(ctx, userID)
			return nil, ErrAPIKeyExists
		}

		key = *req.CustomKey
	} else {
		var err error
		key, err = s.GenerateKey()
		if err != nil {
			return nil, fmt.Errorf("generate key: %w", err)
		}
	}

	fallbackWhenGroupUnavailable := true
	if req.FallbackWhenGroupUnavailable != nil {
		fallbackWhenGroupUnavailable = *req.FallbackWhenGroupUnavailable
	}

	// Store submitted names with HTML entities for metacharacters so Create and
	// Update return the saved representation used by management responses.
	apiKey := &APIKey{
		UserID:                       userID,
		TeamID:                       teamID,
		Key:                          key,
		Name:                         html.EscapeString(req.Name),
		GroupID:                      req.GroupID,
		IsComposite:                  req.IsComposite,
		CompositeGroups:              compositeGroups,
		Status:                       StatusActive,
		FastModePolicy:               fastModePolicy,
		BillingMode:                  billingMode,
		PreferredSubscriptionID:      preferredSubscriptionID,
		ModelMapping:                 modelMapping,
		IPWhitelist:                  req.IPWhitelist,
		IPBlacklist:                  req.IPBlacklist,
		Quota:                        req.Quota,
		QuotaUsed:                    0,
		MaxTokenRateMultiplier:       req.MaxTokenRateMultiplier,
		ConcurrencyLimit:             req.ConcurrencyLimit,
		RPMLimit:                     req.RPMLimit,
		RateLimit5h:                  req.RateLimit5h,
		RateLimit1d:                  req.RateLimit1d,
		RateLimit7d:                  req.RateLimit7d,
		FallbackWhenGroupUnavailable: fallbackWhenGroupUnavailable,
	}
	apiKey.ActorUser = actor
	apiKey.User = user

	if req.ExpiresInDays != nil && *req.ExpiresInDays > 0 {
		expiresAt := s.now().AddDate(0, 0, *req.ExpiresInDays)
		apiKey.ExpiresAt = &expiresAt
	}

	// Persist the complete configuration before notifying authentication caches; a
	// failed write leaves existing lookup state eligible to be reused.
	if err := s.apiKeyRepo.Create(ctx, apiKey); err != nil {
		return nil, fmt.Errorf("create api key: %w", err)
	}

	s.InvalidateAuthCacheByKey(ctx, apiKey.Key)
	s.KeyCompileAPIKeyIPRules(apiKey)

	return apiKey, nil
}

// List loads the creator's filtered Key page, then adds optional live
// concurrency observations. Repository failure rejects the page; failed or
// absent live-count reads leave the returned records usable with their existing
// observation fields.
func (s *APIKeyService) List(ctx context.Context, userID int64, params pagination.PaginationParams, filters APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error) {
	keys, pagination, err := s.apiKeyRepo.ListByUserID(ctx, userID, params, filters)
	if err != nil {
		return nil, nil, fmt.Errorf("list api keys: %w", err)
	}
	s.KeyFillCurrentConcurrency(ctx, keys)
	return keys, pagination, nil
}

// KeyFillCurrentConcurrency updates supplied records using one optional batch
// live-count read for positive IDs. Missing dependencies, empty input or failed
// reads leave them unchanged; missing returned counters become zero
// observations.
func (s *APIKeyService) KeyFillCurrentConcurrency(ctx context.Context, keys []APIKey) {
	if s == nil || s.concurrencyService == nil || len(keys) == 0 {
		return
	}
	ids := make([]int64, 0, len(keys))
	for i := range keys {
		if keys[i].ID > 0 {
			ids = append(ids, keys[i].ID)
		}
	}
	// Treat live counts as presentation enrichment; a failed batch read keeps the
	// repository records and any supplied observation fields usable.
	counts, err := s.concurrencyService.GetAPIKeyConcurrencyBatch(ctx, ids)
	if err != nil {
		return
	}
	for i := range keys {
		keys[i].CurrentConcurrency = counts[keys[i].ID]
	}
}

// KeyCurrentConcurrencyForAPIKey returns one optional live-count observation
// for a positive ID. Missing service, reader, ID or counter returns zero, and
// failed reads also become zero for console presentation.
func (s *APIKeyService) KeyCurrentConcurrencyForAPIKey(ctx context.Context, apiKeyID int64) int {
	if s == nil || s.concurrencyService == nil || apiKeyID <= 0 {
		return 0
	}
	// A failed live-count read becomes zero for management presentation, allowing
	// the credential record itself to remain available to the caller.
	counts, err := s.concurrencyService.GetAPIKeyConcurrencyBatch(ctx, []int64{apiKeyID})
	if err != nil {
		return 0
	}
	return counts[apiKeyID]
}

// VerifyOwnership returns requested IDs belonging to userID through the
// repository ownership query. Empty input returns an allocated empty list;
// managed-Key presentation visibility remains a separate service or handler
// decision.
func (s *APIKeyService) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	if len(apiKeyIDs) == 0 {
		return []int64{}, nil
	}

	validIDs, err := s.apiKeyRepo.VerifyOwnership(ctx, userID, apiKeyIDs)
	if err != nil {
		return nil, fmt.Errorf("verify api key ownership: %w", err)
	}
	return validIDs, nil
}

// GetByID loads a Key view and hides managed credentials as missing to
// user-facing consumers. It compiles IP rules and fills optional live
// concurrency after lookup; creator ownership is checked by its console caller
// before disclosure.
func (s *APIKeyService) GetByID(ctx context.Context, id int64) (*APIKey, error) {
	apiKey, err := s.apiKeyRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get api key: %w", err)
	}
	if apiKey != nil && apiKey.ManagedBy != nil {
		// Managed credentials are hidden from console views before presentation
		// enrichment, so their identifiers return the normal missing-Key response.
		return nil, fmt.Errorf("get api key: %w", ErrAPIKeyNotFound)
	}
	s.KeyCompileAPIKeyIPRules(apiKey)
	if apiKey != nil {
		apiKey.CurrentConcurrency = s.KeyCurrentConcurrencyForAPIKey(ctx, apiKey.ID)
	}
	return apiKey, nil
}

// GetByKey returns a request Key view through accepted caches or current
// storage lookup. Empty or oversized credentials are rejected before cache
// access, and stopping authentication rejects operation admission.
//
// Accepted snapshots are reconstructed before ordinary unavailable-group
// selection and public-group permission checks; composite prefix selection
// occurs later. Configured singleflight shares an in-progress credential load
// using its first caller's context, and waiting followers receive that result
// without their own cancellation branch. Cache or snapshot misses can reach a
// direct storage reload. The operation count remains held through all lookup
// and reconstruction paths so shutdown waits before closing caches.
func (s *APIKeyService) GetByKey(ctx context.Context, key string) (*APIKey, error) {
	if key == "" || len(key) > MaxAPIKeyCredentialBytes {
		return nil, ErrAPIKeyNotFound
	}
	if s == nil {
		return nil, fmt.Errorf("get api key: %w", ErrAPIKeyNotFound)
	}
	if !s.operations.enter() {
		return nil, ErrAuthenticationStopped
	}
	defer s.operations.leave()
	cacheKey := s.KeyAuthCacheKey(key)

	if entry, ok := s.KeyGetAuthCacheEntry(ctx, cacheKey); ok {
		if apiKey, used, err := s.KeyApplyAuthCacheEntry(key, entry); used {
			if err != nil {
				return nil, fmt.Errorf("get api key: %w", err)
			}
			return s.prepareKeyForAuthentication(ctx, apiKey)
		}
	}

	// Coalesce same-credential loads using the first callback's context; waiting
	// followers share its repository result, reducing concurrent storage work for
	// one missing cache identity.
	if s.authCfg.singleflight {
		value, err, _ := s.authGroup.Do(cacheKey, func() (any, error) {
			return s.KeyLoadAuthCacheEntry(ctx, key, cacheKey)
		})
		if err != nil {
			return nil, err
		}
		entry, _ := value.(*APIKeyAuthCacheEntry)
		if apiKey, used, err := s.KeyApplyAuthCacheEntry(key, entry); used {
			if err != nil {
				return nil, fmt.Errorf("get api key: %w", err)
			}
			return s.prepareKeyForAuthentication(ctx, apiKey)
		}
	} else {
		entry, err := s.KeyLoadAuthCacheEntry(ctx, key, cacheKey)
		if err != nil {
			return nil, err
		}
		if apiKey, used, err := s.KeyApplyAuthCacheEntry(key, entry); used {
			if err != nil {
				return nil, fmt.Errorf("get api key: %w", err)
			}
			return s.prepareKeyForAuthentication(ctx, apiKey)
		}
	}

	// Reload directly when the load result cannot reconstruct an accepted
	// snapshot, so this path can still return current repository data.
	apiKey, err := s.KeyLookupAPIKeyForAuth(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get api key: %w", err)
	}
	apiKey.Key = key
	return s.prepareKeyForAuthentication(ctx, apiKey)
}

// prepareKeyForAuthentication applies runtime group selection and compiles
// address rules for a loaded Key. Callers supply a nonnil request view after
// loading or snapshot reconstruction. Ordinary Keys resolve configured fallback
// before public-group permission checks; composite Keys defer group selection
// to the request's model prefix. Permission rejection returns the wrapped
// group error before compiling the selected view's IP rules.
func (s *APIKeyService) prepareKeyForAuthentication(ctx context.Context, apiKey *APIKey) (*APIKey, error) {
	if !apiKey.IsComposite {
		// A disabled source group can be forbidden while its configured target is
		// allowed, so permission follows target selection for this request.
		apiKey = s.KeyApplyExplicitGroupFallback(ctx, apiKey)
		if !s.KeyCanUserUseBoundGroup(ctx, apiKey) {
			return nil, fmt.Errorf("get api key: %w", ErrGroupDisabledForUser)
		}
	}
	s.KeyCompileAPIKeyIPRules(apiKey)
	return apiKey, nil
}

// SelectCompositeGroupForRequest builds an effective view for the supplied
// composite binding and rechecks any configured unavailable target. Missing
// Key, composite mode, binding or group reports an unknown prefix.
//
// The Key struct, binding slice and payer struct are copied, while nested
// references, the selected group and override pointer remain shared with the
// supplied request view. The selected group ID has new storage. Callers choose
// a binding from this Key and retain request ownership while later gateway
// policy changes its effective group; final subscription and protocol admission
// occur afterward.
func (s *APIKeyService) SelectCompositeGroupForRequest(ctx context.Context, apiKey *APIKey, binding *APIKeyCompositeGroup) (*APIKey, error) {
	if apiKey == nil || !apiKey.IsComposite || binding == nil || binding.Group == nil {
		return nil, ErrCompositeKeyPrefixNotFound
	}
	// Give the chosen binding and payer override their own top-level request view;
	// nested data remains shared until another helper copies it.
	selected := *apiKey
	selected.CompositeGroups = KeyCloneCompositeBindings(apiKey.CompositeGroups)
	groupID := binding.GroupID
	selected.GroupID = &groupID
	selected.Group = binding.Group
	if apiKey.User != nil {
		userCopy := *apiKey.User
		userCopy.UserGroupRPMOverride = binding.UserGroupRPMOverride
		selected.User = &userCopy
	}
	prepared := s.KeyApplyExplicitGroupFallback(ctx, &selected)
	if !s.KeyCanUserUseBoundGroup(ctx, prepared) {
		return nil, ErrGroupDisabledForUser
	}
	return prepared, nil
}

// KeyApplyExplicitGroupFallback returns an eligible configured
// unavailable-group view or the supplied Key unchanged. Missing binding or
// dependency, disabled selection, active or deleted groups and unusable targets
// skip resolution. A nonnil Key requires a usable service receiver before its
// group dependency is inspected. Target permission or read failure retains the
// prior view so later gateway admission can reject or handle its current state.
// Successful resolution returns a copied Key with the target binding;
// subsequent subscription and protocol admission evaluate that target.
func (s *APIKeyService) KeyApplyExplicitGroupFallback(ctx context.Context, apiKey *APIKey) *APIKey {
	if apiKey == nil || s.groupRepo == nil || apiKey.GroupID == nil || apiKey.Group == nil || !apiKey.FallbackWhenGroupUnavailable {
		return apiKey
	}
	current := apiKey.Group
	if current.IsActive() || strings.EqualFold(current.Status, "deleted") {
		return apiKey
	}
	targetID := current.UnavailableFallbackGroupID
	if targetID == nil || *targetID <= 0 || *targetID == current.ID {
		return apiKey
	}
	// Attempt the configured target under runtime binding and payer rules;
	// resolution failure keeps the current view available for subsequent admission
	// decisions.
	resolved, err := s.ResolveRuntimeGroup(ctx, apiKey, *targetID)
	if err != nil {
		return apiKey
	}
	return resolved
}

// KeyRefreshFallbackUserGroupRPMOverride clears the previous group's payer RPM
// override before optional target lookup. Invalid IDs or absent rate storage
// keep nil; successful reads retain their returned pointer, and failures permit
// scheduler admission to use its normal lookup path.
func (s *APIKeyService) KeyRefreshFallbackUserGroupRPMOverride(ctx context.Context, apiKey *APIKey, groupID int64) {
	if apiKey == nil || apiKey.User == nil {
		return
	}
	// Discard the previous group's override before attempting target lookup;
	// leaving nil after failure lets scheduler admission perform its normal
	// payer-group read.
	apiKey.User.UserGroupRPMOverride = nil
	if s.userGroupRateRepo == nil || apiKey.User.ID <= 0 || groupID <= 0 {
		return
	}
	override, err := s.userGroupRateRepo.GetRPMOverrideByUserAndGroup(ctx, apiKey.User.ID, groupID)
	if err == nil {
		apiKey.User.UserGroupRPMOverride = override
	}
}

// Update validates selected changes on a creator-owned user-visible Key and
// returns its resulting view. Invalid numeric input stops before repository
// access; managed records appear missing, and team state is hydrated before
// configuration processing when eligible.
//
// Funding and final binding choices are resolved together so a specified
// subscription covers every selected group. Configuration masks retain
// unselected billing counters and select usage state only for requested resets.
// Quota or expiry changes can reactivate exhausted or expired status, while
// owner locking still constrains active-status requests. Successful persistence
// precedes authentication invalidation and optional spending-cache cleanup;
// failed cleanup leaves the saved update accepted.
func (s *APIKeyService) Update(ctx context.Context, id int64, userID int64, req UpdateAPIKeyRequest) (*APIKey, error) {
	if err := KeyValidateUpdateAPIKeyRequest(req); err != nil {
		return nil, err
	}
	apiKey, err := s.apiKeyRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get api key: %w", err)
	}
	if apiKey != nil && apiKey.ManagedBy != nil {
		// Hide managed credentials before creator authorization and field processing,
		// so console mutation returns the missing-Key identity for those records.
		return nil, fmt.Errorf("get api key: %w", ErrAPIKeyNotFound)
	}

	if apiKey.UserID != userID {
		return nil, ErrInsufficientPerms
	}
	if apiKey.TeamID != nil {
		apiKey, err = s.KeyHydrateTeamAPIKey(ctx, apiKey, nil)
		if err != nil {
			return nil, err
		}
	}

	if req.IPWhitelist != nil && len(*req.IPWhitelist) > 0 {
		if invalid := ipmatch.ValidateIPPatterns(*req.IPWhitelist); len(invalid) > 0 {
			return nil, fmt.Errorf("%w: %v", ErrInvalidIPPattern, invalid)
		}
	}

	if req.IPBlacklist != nil && len(*req.IPBlacklist) > 0 {
		if invalid := ipmatch.ValidateIPPatterns(*req.IPBlacklist); len(invalid) > 0 {
			return nil, fmt.Errorf("%w: %v", ErrInvalidIPPattern, invalid)
		}
	}
	if req.ModelMapping != nil {
		modelMapping, normalizeErr := NormalizeAPIKeyModelMapping(*req.ModelMapping)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		apiKey.ModelMapping = modelMapping
	}

	// Select columns for this update while billing increments usage atomically.
	// Include usage columns only when the request asks to reset them.
	var fields APIKeyUpdateFields
	// Quota and expiration changes can reactivate the key.
	// Compare the final status with this value to select the status column when needed.
	originalStatus := apiKey.Status
	originalIsComposite := apiKey.IsComposite

	// Combine supplied funding choices with saved values before binding checks, so
	// a plan change and a group change are validated as one final configuration.
	targetBillingMode := APIKeyEffectiveBillingMode(apiKey)
	targetPreferredSubscriptionID := apiKey.PreferredSubscriptionID
	billingConfigurationRequested := req.BillingMode != nil || req.PreferredSubscriptionID != nil
	var billingUser *User
	var preferredSubscription *UserSubscription
	if billingConfigurationRequested {
		billingUser, err = s.KeyBillingUserForAPIKey(ctx, apiKey)
		if err != nil {
			return nil, fmt.Errorf("get billing user: %w", err)
		}
		apiKey.User = billingUser
		if req.BillingMode != nil {
			normalizedMode, ok := NormalizeAPIKeyBillingMode(*req.BillingMode)
			if !ok {
				return nil, ErrInvalidAPIKeyBillingMode
			}
			targetBillingMode = normalizedMode
		}
		if req.PreferredSubscriptionID != nil {
			targetPreferredSubscriptionID = req.PreferredSubscriptionID
		}
		if targetBillingMode != APIKeyBillingModeSubscription {
			targetPreferredSubscriptionID = nil
		} else if req.BillingMode != nil && req.PreferredSubscriptionID == nil && apiKey.PreferredSubscriptionID == nil {
			return nil, ErrPreferredSubscriptionRequired
		}

		resolvedMode, resolvedSubscriptionID, resolvedSubscription, resolveErr := s.KeyResolveAPIKeyBillingConfiguration(
			ctx,
			billingUser.ID,
			targetBillingMode,
			targetPreferredSubscriptionID,
		)
		if resolveErr != nil {
			return nil, resolveErr
		}
		targetBillingMode = resolvedMode
		targetPreferredSubscriptionID = resolvedSubscriptionID
		preferredSubscription = resolvedSubscription
		apiKey.BillingMode = targetBillingMode
		apiKey.PreferredSubscriptionID = targetPreferredSubscriptionID
		fields.BillingConfiguration = true
	}

	// Encode name metacharacters on both management writes so returned names and
	// later repository reads use the same saved HTML-entity representation.
	if req.Name != nil {
		apiKey.Name = html.EscapeString(*req.Name)
		fields.Name = true
	}
	if req.FastModePolicy != nil {
		fastModePolicy, ok := NormalizeAPIKeyFastModePolicy(*req.FastModePolicy)
		if !ok {
			return nil, ErrInvalidAPIKeyFastModePolicy
		}
		apiKey.FastModePolicy = fastModePolicy
		fields.FastModePolicy = true
	}
	if req.ModelMapping != nil {
		fields.ModelMapping = true
	}

	targetComposite := apiKey.IsComposite
	if req.IsComposite != nil {
		targetComposite = *req.IsComposite
	}

	// Select the complete final binding shape before persistence; converting modes
	// also selects ordinary GroupID so stale single-group state cannot survive a
	// composite replacement.
	if targetComposite {
		if req.GroupID != nil {
			return nil, ErrCompositeKeyGroupConflict
		}
		if !apiKey.IsComposite && req.CompositeGroups == nil {
			return nil, ErrCompositeKeyGroupsRequired
		}
		if req.CompositeGroups != nil {
			billingUserID := userID
			if apiKey.TeamID != nil {
				if s.teamRepo == nil {
					return nil, ErrTeamFeatureDisabled
				}
				// Map failed team reads and changed team identity to membership rejection
				// before authorizing replacement bindings against the current owner.
				teamCtx, teamErr := s.teamRepo.GetContextByUserID(ctx, userID)
				if teamErr != nil || teamCtx.Team.ID != *apiKey.TeamID {
					return nil, ErrTeamMembershipRequired
				}
				billingUserID = teamCtx.Owner.UserID
			}
			user, err := s.userRepo.GetByID(ctx, billingUserID)
			if err != nil {
				return nil, fmt.Errorf("get user: %w", err)
			}
			bindings, err := s.KeyPrepareCompositeGroups(ctx, user, *req.CompositeGroups)
			if err != nil {
				return nil, err
			}
			apiKey.CompositeGroups = bindings
			apiKey.User = user
			fields.CompositeConfiguration = true
		}
		apiKey.IsComposite = true
		apiKey.GroupID = nil
		apiKey.Group = nil
	} else if apiKey.IsComposite {
		if req.GroupID == nil || *req.GroupID <= 0 {
			return nil, ErrCompositeKeyTargetRequired
		}
		apiKey.IsComposite = false
		apiKey.CompositeGroups = nil
	}
	if apiKey.IsComposite != originalIsComposite {
		fields.CompositeConfiguration = true
		fields.GroupID = true
	}

	if !targetComposite && req.GroupID != nil {
		billingUserID := userID
		if apiKey.TeamID != nil {
			if s.teamRepo == nil {
				return nil, ErrTeamFeatureDisabled
			}
			// Map failed team reads and changed team identity to membership rejection
			// before using the owner's ordinary-group permissions.
			teamCtx, teamErr := s.teamRepo.GetContextByUserID(ctx, userID)
			if teamErr != nil || teamCtx.Team.ID != *apiKey.TeamID {
				return nil, ErrTeamMembershipRequired
			}
			billingUserID = teamCtx.Owner.UserID
		}
		user, err := s.userRepo.GetByID(ctx, billingUserID)
		if err != nil {
			return nil, fmt.Errorf("get user: %w", err)
		}

		group, err := s.groupRepo.GetByID(ctx, *req.GroupID)
		if err != nil {
			return nil, fmt.Errorf("get group: %w", err)
		}

		if !s.KeyCanUserBindGroup(ctx, user, group) {
			return nil, ErrGroupNotAllowed
		}

		apiKey.GroupID = req.GroupID
		apiKey.Group = group
		apiKey.User = user
		fields.GroupID = true
	}
	if !apiKey.IsComposite && !s.KeyCanUserUseBoundGroup(ctx, apiKey) {
		return nil, ErrGroupDisabledForUser
	}
	// Recheck plan coverage whenever funding or binding inputs are supplied,
	// including mode conversion, so the saved specified subscription covers the
	// complete final group set.
	if targetBillingMode == APIKeyBillingModeSubscription && (billingConfigurationRequested || req.GroupID != nil || req.CompositeGroups != nil || req.IsComposite != nil) {
		if billingUser == nil {
			billingUser, err = s.KeyBillingUserForAPIKey(ctx, apiKey)
			if err != nil {
				return nil, fmt.Errorf("get billing user: %w", err)
			}
			apiKey.User = billingUser
		}
		if preferredSubscription == nil {
			_, _, preferredSubscription, err = s.KeyResolveAPIKeyBillingConfiguration(
				ctx,
				billingUser.ID,
				targetBillingMode,
				targetPreferredSubscriptionID,
			)
			if err != nil {
				return nil, err
			}
		}
		if err := KeyValidatePreferredSubscriptionGroups(preferredSubscription, apiKey.Group, apiKey.CompositeGroups); err != nil {
			return nil, err
		}
	}

	if req.Status != nil {
		if apiKey.TeamOwnerDisabled && *req.Status == StatusAPIKeyActive {
			return nil, ErrInsufficientPerms
		}
		apiKey.Status = *req.Status
		fields.Status = true
		// Clear the custom-secret failure counter when status is supplied, before
		// persistence; a later Key write failure can leave the optional counter
		// cleared.
		if s.cache != nil {
			_ = s.cache.DeleteCreateAttemptCount(ctx, apiKey.UserID)
		}
	}

	// Quota and expiry replacements can promote their exhausted or expired state
	// back to active; compare the final status afterward to include that derived
	// state in persistence.
	if req.Quota != nil {
		apiKey.Quota = *req.Quota
		fields.Quota = true
		if apiKey.Status == StatusAPIKeyQuotaExhausted && (*req.Quota <= 0 || *req.Quota > apiKey.QuotaUsed) {
			apiKey.Status = StatusActive
		}
	}
	if req.ResetQuota != nil && *req.ResetQuota {
		apiKey.QuotaUsed = 0
		fields.QuotaUsed = true
		if apiKey.Status == StatusAPIKeyQuotaExhausted {
			apiKey.Status = StatusActive
		}
	}
	if req.ClearExpiration {
		apiKey.ExpiresAt = nil
		fields.ExpiresAt = true
		if apiKey.Status == StatusAPIKeyExpired {
			apiKey.Status = StatusActive
		}
	} else if req.ExpiresAt != nil {
		apiKey.ExpiresAt = req.ExpiresAt
		fields.ExpiresAt = true
		if apiKey.Status == StatusAPIKeyExpired && s.now().Before(*req.ExpiresAt) {
			apiKey.Status = StatusActive
		}
	}

	if req.IPWhitelist != nil {
		apiKey.IPWhitelist = *req.IPWhitelist
		fields.IPRules = true
	}
	if req.IPBlacklist != nil {
		apiKey.IPBlacklist = *req.IPBlacklist
		fields.IPRules = true
	}

	if req.MaxTokenRateMultiplier != nil {
		apiKey.MaxTokenRateMultiplier = *req.MaxTokenRateMultiplier
		fields.MaxTokenRateMultiplier = true
	}
	if req.ConcurrencyLimit != nil {
		apiKey.ConcurrencyLimit = *req.ConcurrencyLimit
		fields.ConcurrencyLimit = true
	}
	if req.RPMLimit != nil {
		apiKey.RPMLimit = *req.RPMLimit
		fields.RPMLimit = true
	}
	if req.RateLimit5h != nil {
		apiKey.RateLimit5h = *req.RateLimit5h
		fields.RateLimits = true
	}
	if req.RateLimit1d != nil {
		apiKey.RateLimit1d = *req.RateLimit1d
		fields.RateLimits = true
	}
	if req.RateLimit7d != nil {
		apiKey.RateLimit7d = *req.RateLimit7d
		fields.RateLimits = true
	}
	if req.FallbackWhenGroupUnavailable != nil {
		apiKey.FallbackWhenGroupUnavailable = *req.FallbackWhenGroupUnavailable
		fields.FallbackWhenGroupUnavailable = true
	}
	resetRateLimit := req.ResetRateLimitUsage != nil && *req.ResetRateLimitUsage
	// Reset all spending counters and starts together so later usage begins fresh
	// windows, and select their dedicated mask without replacing unrelated billing
	// values.
	if resetRateLimit {
		apiKey.Usage5h = 0
		apiKey.Usage1d = 0
		apiKey.Usage7d = 0
		apiKey.Window5hStart = nil
		apiKey.Window1dStart = nil
		apiKey.Window7dStart = nil
		fields.RateLimitUsage = true
	}

	if apiKey.Status != originalStatus {
		fields.Status = true
	}

	if err := s.apiKeyRepo.Update(ctx, apiKey, fields); err != nil {
		return nil, fmt.Errorf("update api key: %w", err)
	}

	// Notify authentication caches after persistence succeeds, then rebuild IP
	// checks from the returned configuration for callers retaining this Key view.
	s.InvalidateAuthCacheByKey(ctx, apiKey.Key)
	s.KeyCompileAPIKeyIPRules(apiKey)

	// Attempt spending-cache removal after a saved window reset; cleanup failure
	// is suppressed, so this notification does not establish immediate visibility.
	if resetRateLimit && s.rateLimitCacheInvalid != nil {
		_ = s.rateLimitCacheInvalid.InvalidateAPIKeyRateLimit(ctx, apiKey.ID)
	}

	return apiKey, nil
}

// Delete tombstones and soft-deletes a creator-owned user-visible Key through
// storage. Managed credentials appear missing and owner mismatch rejects before
// mutation.
//
// After storage succeeds, optional creation-counter cleanup and authentication
// invalidation are attempted, then the local last-use deadline is removed.
// Notification failures remain best effort. The retained row ID still serves
// already-admitted settlement through billing's separate transaction path,
// while later ordinary lookup excludes deleted credentials. A repeated user
// deletion can fail its initial visible-record read even when the repository
// tombstone operation is idempotent.
func (s *APIKeyService) Delete(ctx context.Context, id int64, userID int64) error {
	existing, err := s.apiKeyRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("get api key: %w", err)
	}
	if existing != nil && existing.ManagedBy != nil {
		// Hide managed credentials before deletion authorization, so user-facing
		// mutation returns the same missing-Key identity as other console operations.
		return fmt.Errorf("get api key: %w", ErrAPIKeyNotFound)
	}

	if existing == nil || existing.UserID != userID {
		return ErrInsufficientPerms
	}

	// Tombstone the secret and soft-delete the row through storage before
	// notification; its retained ID still allows the separate billing path to
	// settle admitted usage.
	if err := s.apiKeyRepo.DeleteWithAudit(ctx, id); err != nil {
		return fmt.Errorf("delete api key: %w", err)
	}

	// After deletion succeeds, attempt counter cleanup and secret invalidation
	// before discarding the local touch deadline; optional cache failure leaves
	// the deletion accepted.
	if s.cache != nil {
		_ = s.cache.DeleteCreateAttemptCount(ctx, userID)
	}
	s.InvalidateAuthCacheByKey(ctx, existing.Key)
	s.lastUsedTouchL1.Delete(id)

	return nil
}

// ValidateTeamKeyLifecycle verifies supplied team, assigned member and payer
// views on a team Key. Nil or personal input passes this check; team input
// requires enabled support, matching membership identity and join time, active
// team, active actor and active payer.
//
// Callers hydrate current team and user data before admission. A rejoined
// member fails when the current join time follows Key creation, even if the
// user ID matches, because this credential belongs to the earlier membership.
// The first failed requirement determines the returned lifecycle error.
func (s *APIKeyService) ValidateTeamKeyLifecycle(apiKey *APIKey) error {
	if apiKey == nil || apiKey.TeamID == nil {
		return nil
	}
	if s != nil && s.cfg != nil && !s.cfg.Team.Enabled {
		return ErrTeamFeatureDisabled
	}
	if apiKey.Team == nil || apiKey.TeamMembership == nil || apiKey.Team.ID != *apiKey.TeamID || apiKey.TeamMembership.TeamID != *apiKey.TeamID {
		return ErrTeamMembershipRequired
	}
	if apiKey.TeamMembership.UserID != apiKey.UserID || apiKey.TeamMembership.JoinedAt.After(apiKey.CreatedAt) {
		return ErrTeamMembershipRequired
	}
	if apiKey.Team.Status != TeamStatusActive {
		return ErrTeamSuspended
	}
	if apiKey.ActorUser == nil || !apiKey.ActorUser.IsActive() {
		return ErrTeamActorInactive
	}
	if apiKey.User == nil || !apiKey.User.IsActive() {
		return ErrTeamBillingOwnerInactive
	}
	return nil
}

// CheckTeamMemberLimits validates team lifecycle before evaluating the supplied
// membership allowances. Callers provide a nonnil Key; owner memberships skip
// the billing quota check, and absent membership on personal input yields no
// member-limit rejection.
func (s *APIKeyService) CheckTeamMemberLimits(apiKey *APIKey) error {
	if err := s.ValidateTeamKeyLifecycle(apiKey); err != nil {
		return err
	}
	return KeyCheckTeamMemberLimitSnapshot(apiKey.TeamMembership)
}

// KeyCheckTeamMemberLimitSnapshot delegates supplied member spending values to
// billing unless membership is absent or owner. It uses the given
// window-normalized values; current period normalization belongs to the team
// repository before this check.
func KeyCheckTeamMemberLimitSnapshot(member *TeamMembership) error {
	if member == nil || member.Role == TeamRoleOwner {
		return nil
	}
	return billing.CheckMemberQuotaSnapshot(billing.MemberQuotaSnapshot{DailyLimitUSD: member.DailyLimitUSD, WeeklyLimitUSD: member.WeeklyLimitUSD, MonthlyLimitUSD: member.MonthlyLimitUSD, DailyUsageUSD: member.DailyUsageUSD, WeeklyUsageUSD: member.WeeklyUsageUSD, MonthlyUsageUSD: member.MonthlyUsageUSD})
}

// TouchLastUsed attempts a synchronous last-use write for a positive Key ID
// while operation admission is open. Nonpositive IDs return successfully before
// the gate, and callers decide whether write failures are best effort.
//
// A cached deadline skips writes until its time, while same-Key singleflight
// shares the first caller's context and result. The winning callback rechecks
// the deadline, writes current service time and records the normal debounce
// deadline or shorter failure backoff. Later skipped calls return nil even
// after an earlier failed write, so a nil result can mean throttled work
// instead of a new persisted timestamp.
func (s *APIKeyService) TouchLastUsed(ctx context.Context, keyID int64) error {
	if keyID <= 0 {
		return nil
	}

	if !s.operations.enter() {
		return ErrAuthenticationStopped
	}
	defer s.operations.leave()

	now := s.now()
	if v, ok := s.lastUsedTouchL1.Load(keyID); ok {
		if nextAllowedAt, ok := v.(time.Time); ok && now.Before(nextAllowedAt) {
			return nil
		}
	}

	// The outer deadline read can race with a completed write; recheck inside the
	// winning same-ID callback before issuing another repository update.
	_, err, _ := s.lastUsedTouchSF.Do(strconv.FormatInt(keyID, 10), func() (any, error) {
		latest := s.now()
		if v, ok := s.lastUsedTouchL1.Load(keyID); ok {
			if nextAllowedAt, ok := v.(time.Time); ok && latest.Before(nextAllowedAt) {
				return nil, nil
			}
		}

		// Record a short failure deadline before returning the write error so
		// immediately following requests do not retry the same failing database
		// operation.
		if err := s.apiKeyRepo.UpdateLastUsed(ctx, keyID, latest); err != nil {
			s.lastUsedTouchL1.Store(keyID, latest.Add(KeyApiKeyLastUsedFailBackoff))
			return nil, fmt.Errorf("touch api key last used: %w", err)
		}
		s.lastUsedTouchL1.Store(keyID, latest.Add(KeyApiKeyLastUsedMinTouch))
		return nil, nil
	})
	return err
}

// IncrementUsage increments an optional date-named daily Key counter using the
// captured calendar. Missing cache succeeds; a failed increment is returned,
// while expiry refresh failure is suppressed and leaves the completed increment
// accepted.
func (s *APIKeyService) IncrementUsage(ctx context.Context, keyID int64) error {
	// Bucket the Redis counter by the configured calendar date.
	if s.cache != nil {
		cacheKey := fmt.Sprintf("apikey:usage:%d:%s", keyID, s.calendar.Now().Format("2006-01-02"))
		if err := s.cache.IncrementDailyUsage(ctx, cacheKey); err != nil {
			return fmt.Errorf("increment usage: %w", err)
		}
		// Refresh retention after the increment succeeds; expiry failure leaves that
		// observation accepted, although this attempt cannot establish its cleanup
		// deadline.
		_ = s.cache.SetDailyUsageExpiry(ctx, cacheKey, 24*time.Hour)
	}
	return nil
}

// GetAvailableGroups loads current user permissions and filters active groups
// by binding eligibility. Repository errors reject the result, and successful
// reads return an allocated slice of selected group values. Exclusive choices
// require a user grant, while public choices disappear when that user has
// disabled them. Returned structs are copied into the slice, but nested group
// data remains shared with the repository values.
func (s *APIKeyService) GetAvailableGroups(ctx context.Context, userID int64) ([]routing.Group, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	allGroups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active groups: %w", err)
	}

	availableGroups := make([]routing.Group, 0)
	for _, group := range allGroups {
		if s.KeyCanUserBindGroupInternal(user, &group) {
			availableGroups = append(availableGroups, group)
		}
	}

	return availableGroups, nil
}

// GetAvailableGroupsForScopeWithSubscription resolves the payer and intersects
// its active permitted groups with the chosen subscription plan. Nil
// subscription ID returns ordinary payer choices; invalid specified funding
// rejects, while an unrestricted plan retains all permission-filtered groups.
//
// Permission filtering happens before subscription lookup, so an invalid
// specified subscription can reject a successfully loaded group list. Surviving
// group structs are copied into a new slice, with their nested references
// retained from repository results.
func (s *APIKeyService) GetAvailableGroupsForScopeWithSubscription(ctx context.Context, userID int64, scope string, subscriptionID *int64) ([]routing.Group, error) {
	billingUserID, err := s.KeyBillingUserIDForScope(ctx, userID, scope)
	if err != nil {
		return nil, err
	}
	groups, err := s.GetAvailableGroups(ctx, billingUserID)
	if err != nil || subscriptionID == nil {
		return groups, err
	}
	_, _, subscription, err := s.KeyResolveAPIKeyBillingConfiguration(ctx, billingUserID, APIKeyBillingModeSubscription, subscriptionID)
	if err != nil {
		return nil, err
	}
	if subscription.Plan == nil || len(subscription.Plan.GroupIDs) == 0 {
		return groups, nil
	}
	filtered := make([]routing.Group, 0, len(groups))
	for i := range groups {
		if subscriptionPlanIncludesGroup(subscription.Plan, groups[i].ID) {
			filtered = append(filtered, groups[i])
		}
	}
	return filtered, nil
}

// ListBillingSubscriptionsForScope offers effective subscriptions with present
// plans for the current payer scope. Missing service or subscription storage
// rejects; plan names follow context language and applicable group IDs are
// copied for response consumers.
func (s *APIKeyService) ListBillingSubscriptionsForScope(ctx context.Context, userID int64, scope string) ([]APIKeyBillingSubscriptionOption, error) {
	if s == nil || s.userSubRepo == nil {
		return nil, ErrPreferredSubscriptionInvalid
	}
	billingUserID, err := s.KeyBillingUserIDForScope(ctx, userID, scope)
	if err != nil {
		return nil, err
	}
	subscriptions, err := s.userSubRepo.ListActiveByUserID(ctx, billingUserID)
	if err != nil {
		return nil, err
	}
	options := make([]APIKeyBillingSubscriptionOption, 0, len(subscriptions))
	for i := range subscriptions {
		subscription := &subscriptions[i]
		if !subscription.IsEffective() || subscription.Plan == nil {
			continue
		}
		options = append(options, APIKeyBillingSubscriptionOption{
			ID:               subscription.ID,
			PlanID:           subscription.PlanID,
			PlanName:         billing.LocalizePlan(subscription.Plan, locale.FromContext(ctx)).Name,
			ExpiresAt:        subscription.ExpiresAt,
			GroupsRestricted: len(subscription.Plan.GroupIDs) > 0,
			ApplicableGroups: append([]int64(nil), subscription.Plan.GroupIDs...),
		})
	}
	return options, nil
}

// KeyCanUserBindGroupInternal delegates binding eligibility to the supplied
// user view for this group. Callers provide a nonnil group; a nil user returns
// false under identity's permission rule.
func (s *APIKeyService) KeyCanUserBindGroupInternal(user *User, group *routing.Group) bool {
	return user.CanBindGroup(group.ID, group.IsExclusive)
}

// SearchAPIKeys delegates creator and name search for administrative or
// configuration consumers. The repository controls limit, sorting and
// managed-Key inclusion; this service returns lookup failures with Key-search
// context.
func (s *APIKeyService) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]APIKey, error) {
	keys, err := s.apiKeyRepo.SearchAPIKeys(ctx, userID, keyword, limit)
	if err != nil {
		return nil, fmt.Errorf("search api keys: %w", err)
	}
	return keys, nil
}

// GetUserGroupRates returns user-specific group charge multipliers from the
// optional billing reader. Missing storage returns nil data with no error; a
// failed read is returned with group-rate lookup context.
func (s *APIKeyService) GetUserGroupRates(ctx context.Context, userID int64) (map[int64]float64, error) {
	if s.userGroupRateRepo == nil {
		return nil, nil
	}
	rates, err := s.userGroupRateRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user group rates: %w", err)
	}
	return rates, nil
}

// GetUserGroupRatesForScope uses the caller ID except for trimmed
// case-insensitive team scope. Team input requires enabled support and a
// current owner; missing or failed context becomes membership rejection before
// owner-specific multipliers are read.
func (s *APIKeyService) GetUserGroupRatesForScope(ctx context.Context, userID int64, scope string) (map[int64]float64, error) {
	if !strings.EqualFold(strings.TrimSpace(scope), "team") {
		return s.GetUserGroupRates(ctx, userID)
	}
	if s.cfg != nil && !s.cfg.Team.Enabled {
		return nil, ErrTeamFeatureDisabled
	}
	if s.teamRepo == nil {
		return nil, ErrTeamFeatureDisabled
	}
	// A failed or incomplete team context cannot supply the payer for rates, so
	// this path maps it to membership rejection before owner lookup.
	teamCtx, err := s.teamRepo.GetContextByUserID(ctx, userID)
	if err != nil || teamCtx == nil || teamCtx.Owner == nil {
		return nil, ErrTeamMembershipRequired
	}
	return s.GetUserGroupRates(ctx, teamCtx.Owner.UserID)
}

// CheckAPIKeyQuotaAndExpiry checks a nonnil Key's current expiry before its
// total quota. Nil expiry skips the clock read, timestamp equality stays
// unexpired, and a positive quota whose saved usage reaches its allowance
// returns exhaustion. The helper reads saved values and returns an admission
// error; it does not persist a replacement lifecycle status. If expiration and
// quota would both reject, expiration wins, so callers use that error identity
// when mapping the rejection.
func (s *APIKeyService) CheckAPIKeyQuotaAndExpiry(apiKey *APIKey) error {
	if apiKey.ExpiresAt != nil && apiKey.IsExpiredAt(s.now()) {
		return ErrAPIKeyExpired
	}

	if apiKey.IsQuotaExhausted() {
		return ErrAPIKeyQuotaExhausted
	}

	return nil
}

// UpdateQuotaUsed records positive usage and attempts cached state invalidation
// when quota is exhausted. Nonpositive cost does nothing.
//
// A repository with the atomic state capability supplies the increment and
// resulting status together; exhausted state with a usable credential triggers
// invalidation. Other repositories increment first, read the current Key, then
// write status alone so the snapshot cannot replace billing counters. Follow-up
// read or status-write failure is suppressed after the completed increment.
// This helper follows repository nondeleted-row rules; admitted settlement uses
// its independent billing write path.
func (s *APIKeyService) UpdateQuotaUsed(ctx context.Context, apiKeyID int64, cost float64) error {
	if cost <= 0 {
		return nil
	}

	// quotaStateReader supplies an optional atomic usage increment and resulting
	// authentication state.
	type quotaStateReader interface {
		// IncrementQuotaUsedAndGetState applies usage and returns the resulting
		// stored credential and status together. Implementations supply an atomic
		// result so service callers can notify exhausted authentication after
		// success.
		IncrementQuotaUsedAndGetState(ctx context.Context, id int64, amount float64) (*APIKeyQuotaUsageState, error)
	}

	if repo, ok := s.apiKeyRepo.(quotaStateReader); ok {
		state, err := repo.IncrementQuotaUsedAndGetState(ctx, apiKeyID, cost)
		if err != nil {
			return fmt.Errorf("increment quota used: %w", err)
		}
		// Use the state returned with the increment for exhaustion notification, so
		// this path does not invalidate from a separately reloaded stale usage view.
		if state != nil && state.Status == StatusAPIKeyQuotaExhausted && strings.TrimSpace(state.Key) != "" {
			s.InvalidateAuthCacheByKey(ctx, state.Key)
		}
		return nil
	}

	// Complete the usage increment before loading configuration; the later
	// status-only mask keeps that loaded view from replacing newly recorded
	// counters.
	newQuotaUsed, err := s.apiKeyRepo.IncrementQuotaUsed(ctx, apiKeyID, cost)
	if err != nil {
		return fmt.Errorf("increment quota used: %w", err)
	}

	// A failed follow-up read leaves the completed increment accepted; exhaustion
	// notification can be attempted after a later usable state read.
	apiKey, err := s.apiKeyRepo.GetByID(ctx, apiKeyID)
	if err != nil {
		return nil
	}

	if apiKey.Quota > 0 && newQuotaUsed >= apiKey.Quota {
		apiKey.Status = StatusAPIKeyQuotaExhausted
		// Write status alone to retain concurrent usage; suppress a failed status
		// write because the preceding increment has already recorded this cost.
		if err := s.apiKeyRepo.Update(ctx, apiKey, APIKeyUpdateFields{Status: true}); err != nil {
			return nil
		}
		// Attempt cached authentication removal after the status write succeeds;
		// notification remains best effort and cannot establish visibility for every
		// concurrent lookup.
		s.InvalidateAuthCacheByKey(ctx, apiKey.Key)
	}

	return nil
}

// GetRateLimitData returns the repository's spending counters and saved window
// starts for id.
func (s *APIKeyService) GetRateLimitData(ctx context.Context, id int64) (*APIKeyRateLimitData, error) {
	return s.apiKeyRepo.GetRateLimitData(ctx, id)
}

// UpdateRateLimitUsage delegates positive USD spending to the repository's
// window increment path. Nonpositive cost does nothing; repository errors are
// returned, and that helper's nondeleted eligibility differs from
// already-admitted settlement writes.
func (s *APIKeyService) UpdateRateLimitUsage(ctx context.Context, apiKeyID int64, cost float64) error {
	if cost <= 0 {
		return nil
	}
	return s.apiKeyRepo.IncrementRateLimitUsage(ctx, apiKeyID, cost)
}
