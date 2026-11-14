package httpapi

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	idempotencyhttp "github.com/TokenFlux/TokenRouter/internal/idempotency/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// key_handler.go handles user Key management requests and presents their group
// and subscription choices. Creation converts optional HTTP amounts into
// service defaults and uses the configured idempotency executor, while updates
// retain omission separately from explicit zero or empty collections before
// selecting configuration writes. Service validation decides ownership,
// binding and funding eligibility.
//
// Responses present localized group copies and may attach optional live
// capacity observations. Capacity failures still allow group listing, and
// include_models is passed to the specialized presenter so lightweight choices
// can skip catalogue work. Credential rotation is a direct operation returning
// the generated replacement secret. Request routes rely on their configured
// middleware for authentication, rate controls and auditing before these
// handlers read the subject.

// APIKeyHandler serves user credential operations and payer-scoped
// configuration choices with group presentation.
type APIKeyHandler[G any] struct {
	idempotencyhttp.Executor // configured user creation idempotency executor

	// apiKeyService, groupCapacityService, groupPresentation and presentGroup
	// are bound before concurrent requests. Capacity is optional, the specialized
	// presenter can replace base group output, and credential responses use presentGroup.
	apiKeyService        *apikey.APIKeyService                                                            // configured user Key service
	groupCapacityService GroupCapacityReader                                                              // optional live capacity reader
	groupPresentation    func(context.Context, *routing.Group, *accessview.GroupCapacitySummary, bool) *G // optional specialized group presenter
	presentGroup         func(*routing.Group, *accessview.GroupCapacitySummary) *G                        // base credential group presenter
}

// GroupCapacityReader supplies optional live group observations for authorized
// console choices.
type GroupCapacityReader interface {
	// GetGroupCapacityByIDs returns live observations keyed by group ID for
	// optional presentation. The caller treats failure as absent capacity and
	// continues permission-filtered group listing with the base group data.
	GetGroupCapacityByIDs(context.Context, []int64) (map[int64]accessview.GroupCapacitySummary, error)
}

// CreateAPIKeyRequest carries user creation input before service defaults and
// final validation.
type CreateAPIKeyRequest struct {
	Name            string                             `json:"name" binding:"required"`                       // submitted credential display name
	Scope           string                             `json:"scope" binding:"omitempty,oneof=personal team"` // requested personal or team scope
	GroupID         *int64                             `json:"group_id"`                                      // requested ordinary group binding
	IsComposite     bool                               `json:"is_composite"`                                  // submitted prefix selection mode
	CompositeGroups []apikey.APIKeyCompositeGroupInput `json:"composite_groups"`                              // complete submitted prefix group bindings
	CustomKey       *string                            `json:"custom_key"`                                    // optional custom credential text
	IPWhitelist     []string                           `json:"ip_whitelist"`                                  // submitted allowed address patterns
	IPBlacklist     []string                           `json:"ip_blacklist"`                                  // submitted denied address patterns
	// FastModePolicy and BillingMode retain transport text for final service
	// normalization. An empty policy follows the request; an empty funding mode
	// chooses automatic allocation, with PreferredSubscriptionID used for
	// specified funding.
	FastModePolicy          string            `json:"fast_mode_policy"`          // submitted Key acceleration preference
	BillingMode             string            `json:"billing_mode"`              // submitted funding source mode
	PreferredSubscriptionID *int64            `json:"preferred_subscription_id"` // submitted payer subscription identifier
	ModelMapping            map[string]string `json:"model_mapping"`             // submitted Key model redirect rules
	// Quota, RateLimit5h, RateLimit1d and RateLimit7d are optional USD allowances.
	// The handler turns absent inputs into service zero defaults, while a supplied
	// zero also selects an unbounded allowance.
	Quota *float64 `json:"quota"` // supplied total USD allowance
	// ExpiresInDays is omitted for an unbounded lifetime. Supplied values are
	// validated positive before the service calculates expiry from its clock using
	// calendar days.
	ExpiresInDays *int `json:"expires_in_days"` // supplied relative expiry days

	// MaxTokenRateMultiplier caps the effective group Token multiplier. Zero
	// disables protection; positive values reject requests above the saved ceiling.
	MaxTokenRateMultiplier float64 `json:"max_token_rate_multiplier"` // maximum effective Token multiplier
	// ConcurrencyLimit and RPMLimit apply across the created Key's groups.
	// Their zero defaults leave request allowances unbounded, while positive values
	// are validated and reserved by service and gateway admission.
	ConcurrencyLimit int `json:"concurrency_limit"` // supplied active request allowance
	RPMLimit         int `json:"rpm_limit"`         // supplied per-minute request allowance

	RateLimit5h *float64 `json:"rate_limit_5h"` // supplied five-hour USD allowance
	RateLimit1d *float64 `json:"rate_limit_1d"` // supplied one-day USD allowance
	RateLimit7d *float64 `json:"rate_limit_7d"` // supplied seven-day USD allowance
	// FallbackWhenGroupUnavailable permits the configured target when the bound
	// group is unavailable. Nil selects the service default, and target admission
	// still applies its own eligibility checks.
	FallbackWhenGroupUnavailable *bool `json:"fallback_when_group_unavailable"` // supplied unavailable-group selection preference
}

// UpdateAPIKeyRequest retains omitted, clear and replacement values for user
// Key configuration.
type UpdateAPIKeyRequest struct {
	// Name and Status are forwarded as changes when their strings are nonempty.
	// Empty values are omitted by the handler; Status binding accepts the listed
	// active or inactive transport choices.
	Name        string `json:"name"`         // submitted credential display name
	GroupID     *int64 `json:"group_id"`     // requested ordinary group binding
	IsComposite *bool  `json:"is_composite"` // submitted prefix selection mode
	// CompositeGroups, IPWhitelist, IPBlacklist and ModelMapping retain pointer
	// presence so omission leaves saved values and empty supplied collections
	// replace them. Service validation checks the complete submitted mapping or IP
	// set.
	CompositeGroups         *[]apikey.APIKeyCompositeGroupInput `json:"composite_groups"`                                 // complete submitted prefix group bindings
	Status                  string                              `json:"status" binding:"omitempty,oneof=active inactive"` // submitted credential lifecycle choice
	IPWhitelist             *[]string                           `json:"ip_whitelist"`                                     // submitted allowed address patterns
	IPBlacklist             *[]string                           `json:"ip_blacklist"`                                     // submitted denied address patterns
	FastModePolicy          *string                             `json:"fast_mode_policy"`                                 // submitted Key acceleration preference
	BillingMode             *string                             `json:"billing_mode"`                                     // submitted funding source mode
	PreferredSubscriptionID *int64                              `json:"preferred_subscription_id"`                        // submitted payer subscription identifier
	ModelMapping            *map[string]string                  `json:"model_mapping"`                                    // submitted Key model redirect rules
	// Quota, ConcurrencyLimit, RPMLimit, RateLimit5h, RateLimit1d and RateLimit7d
	// retain omitted pointers. Supplied zero clears that allowance; the final
	// service validator rejects invalid amounts or request counts.
	Quota *float64 `json:"quota"` // supplied total USD allowance
	// ExpiresAt omission retains the saved expiry; empty text selects
	// ClearExpiration. A nonempty supplied value is parsed as RFC3339 before the
	// service update receives its timestamp.
	ExpiresAt  *string `json:"expires_at"`  // supplied RFC3339 expiry text
	ResetQuota *bool   `json:"reset_quota"` // requested total-spending usage reset

	// MaxTokenRateMultiplier retains the saved ceiling when nil. Supplied zero
	// disables protection; positive values reject higher effective group Token rates.
	MaxTokenRateMultiplier *float64 `json:"max_token_rate_multiplier"` // optional Token multiplier ceiling
	// ConcurrencyLimit and RPMLimit retain omitted pointer values during update.
	// Each supplied zero clears its saved request allowance independently, while
	// positive values become the replacement admission settings after service validation.
	ConcurrencyLimit *int `json:"concurrency_limit"` // supplied active request allowance
	RPMLimit         *int `json:"rpm_limit"`         // supplied per-minute request allowance

	RateLimit5h *float64 `json:"rate_limit_5h"` // supplied five-hour USD allowance
	RateLimit1d *float64 `json:"rate_limit_1d"` // supplied one-day USD allowance
	RateLimit7d *float64 `json:"rate_limit_7d"` // supplied seven-day USD allowance
	// ResetQuota and ResetRateLimitUsage request explicit saved-counter resets
	// when true. Nil and false keep their counters; service write masks
	// distinguish these resets from configuration edits.
	ResetRateLimitUsage *bool `json:"reset_rate_limit_usage"` // requested spending-window usage reset
	// FallbackWhenGroupUnavailable retains the saved selection preference when
	// nil. Supplied false disables unavailable-group selection after the service writes it.
	FallbackWhenGroupUnavailable *bool `json:"fallback_when_group_unavailable"` // supplied unavailable-group selection preference
}

// ApiKeyLimitInput pairs a supplied numeric allowance with its validation field
// name.
type ApiKeyLimitInput struct {
	field string // allowance validation input name
	// field identifies the error metadata name for value. A nil value
	// skips validation; a present amount receives the shared finite and bounded
	// USD check before the HTTP caller submits its service input.
	value *float64 // optional supplied USD amount
}

// APIKeyBillingSubscriptionOptionResponse presents eligible payer subscriptions
// and their plan group restrictions.
type APIKeyBillingSubscriptionOptionResponse struct {
	ID               int64     `json:"id"`                // offered subscription record identifier
	PlanID           int64     `json:"plan_id"`           // offered subscription plan identifier
	PlanName         string    `json:"plan_name"`         // localized subscription plan name
	ExpiresAt        time.Time `json:"expires_at"`        // offered subscription expiry timestamp
	GroupsRestricted bool      `json:"groups_restricted"` // plan group restriction presence
	ApplicableGroups []int64   `json:"applicable_groups"` // copied applicable group identifiers
}

// NewAPIKeyHandler retains keys and the group mapper used in credential
// responses. Callers bind optional capacity, presentation and idempotency
// dependencies before concurrent handling; the basic presenter accepts nil
// group input and returns a nonnil response for each available-group record.
// A specialized presenter also returns a nonnil value when replacing that record.
func NewAPIKeyHandler[G any](keys *apikey.APIKeyService, present func(*routing.Group, *accessview.GroupCapacitySummary) *G) *APIKeyHandler[G] {
	return &APIKeyHandler[G]{apiKeyService: keys, presentGroup: present}
}

// SetGroupPresentation binds the specialized authorized group presenter used by
// available-group queries. The callback receives include_models and capacity
// observations; callers install it before concurrent requests because this
// setter is unsynchronized. The callback returns a nonnil result for every
// available group, because GetAvailableGroups copies that result into its response.
func (h *APIKeyHandler[G]) SetGroupPresentation(present func(context.Context, *routing.Group, *accessview.GroupCapacitySummary, bool) *G) {
	h.groupPresentation = present
}

// SetGroupCapacityService binds optional live group capacity reads for
// available-group presentation. Nil leaves capacity absent; callers configure
// this unsynchronized dependency before concurrent console queries begin using
// the handler.
func (h *APIKeyHandler[G]) SetGroupCapacityService(c GroupCapacityReader) { h.groupCapacityService = c }

// keyResponse maps a credential using copied group views localized for
// language. Nil group input reaches the base presenter directly; the supplied
// Key retains the DTO mapper's documented pointer and collection sharing.
func (h *APIKeyHandler[G]) keyResponse(k *apikey.APIKey, language string) *dto.APIKey[G] {
	return dto.APIKeyFromKey(k, func(g *routing.Group) *G {
		if g != nil {
			copy := routing.CloneGroup(g)
			display, _ := routing.GroupDisplay(g, language)
			copy.DisplayName, copy.Description = display.DisplayName, display.Description
			return h.presentGroup(copy, nil)
		}
		return h.presentGroup(g, nil)
	})
}

// ValidateAPIKeyLimitFields validates each supplied amount through the service
// allowance rule. Nil pointers are omitted, zero is accepted as unbounded, and
// the first invalid supplied input returns its field-specific error.
func ValidateAPIKeyLimitFields(limits ...ApiKeyLimitInput) error {
	for _, limit := range limits {
		if limit.value == nil {
			continue
		}
		if err := apikey.ValidateAPIKeyLimit(limit.field, *limit.value); err != nil {
			return err
		}
	}
	return nil
}

// ValidateAPIKeyCreateRequest validates supplied HTTP monetary allowances and a
// positive relative expiry. Omitted values pass this early check; request
// concurrency and RPM bounds are validated by the final service input
// validation.
func ValidateAPIKeyCreateRequest(req CreateAPIKeyRequest) error {
	if err := ValidateAPIKeyLimitFields(
		ApiKeyLimitInput{field: "quota", value: req.Quota},
		ApiKeyLimitInput{field: "rate_limit_5h", value: req.RateLimit5h},
		ApiKeyLimitInput{field: "rate_limit_1d", value: req.RateLimit1d},
		ApiKeyLimitInput{field: "rate_limit_7d", value: req.RateLimit7d},
	); err != nil {
		return err
	}
	if req.ExpiresInDays != nil {
		return apikey.ValidateAPIKeyExpiresInDays(*req.ExpiresInDays)
	}
	return nil
}

// ValidateAPIKeyUpdateRequest validates supplied HTTP monetary allowance
// replacements using the service rule. Nil fields remain omitted, and request
// concurrency or RPM validation occurs again in the final service update path.
func ValidateAPIKeyUpdateRequest(req UpdateAPIKeyRequest) error {
	return ValidateAPIKeyLimitFields(
		ApiKeyLimitInput{field: "quota", value: req.Quota},
		ApiKeyLimitInput{field: "rate_limit_5h", value: req.RateLimit5h},
		ApiKeyLimitInput{field: "rate_limit_1d", value: req.RateLimit1d},
		ApiKeyLimitInput{field: "rate_limit_7d", value: req.RateLimit7d},
	)
}

// List returns filtered credentials for the authenticated subject with
// pagination and localized group data. Search is trimmed and capped at 100
// bytes, malformed group IDs are ignored, and scope is normalized before the
// repository filter. Service errors are returned through the application
// response.
func (h *APIKeyHandler[G]) List(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	page, pageSize := response.ParsePagination(c)
	params := pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    c.DefaultQuery("sort_by", "created_at"),
		SortOrder: c.DefaultQuery("sort_order", "desc"),
	}

	// The console search cap limits query text in bytes; cutting a multibyte
	// name can retain an incomplete encoding, while malformed group IDs are ignored.
	var filters apikey.APIKeyListFilters
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		if len(search) > 100 {
			search = search[:100]
		}
		filters.Search = search
	}
	filters.Status = c.Query("status")
	filters.Scope = strings.ToLower(strings.TrimSpace(c.Query("scope")))
	if groupIDStr := c.Query("group_id"); groupIDStr != "" {
		gid, err := strconv.ParseInt(groupIDStr, 10, 64)
		if err == nil {
			filters.GroupID = &gid
		}
	}

	keys, result, err := h.apiKeyService.List(c.Request.Context(), subject.UserID, params, filters)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]dto.APIKey[G], 0, len(keys))
	for i := range keys {
		out = append(out, *h.keyResponse(&keys[i], locale.FromContext(c.Request.Context())))
	}
	response.Paginated(c, out, result.Total, page, pageSize)
}

// GetByID loads one visible credential for the authenticated subject and
// presents localized group data. A differently owned Key uses the same
// missing-key response as absence; parseable signed IDs reach the service,
// while malformed input returns a bad request.
func (h *APIKeyHandler[G]) GetByID(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid key ID")
		return
	}

	key, err := h.apiKeyService.GetByID(c.Request.Context(), keyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// Return the same error for missing keys and keys owned by another user.
	if key.UserID != subject.UserID {
		response.ErrorFrom(c, apikey.ErrAPIKeyNotFound)
		return
	}

	response.Success(c, h.keyResponse(key, locale.FromContext(c.Request.Context())))
}

// Create strictly decodes user creation input, validates supplied allowances
// and submits it through the configured executor. Missing subject, malformed JSON
// or early validation rejects before persistence.
//
// Optional monetary fields become service zero defaults when omitted; pointer
// and collection choices retain their supplied presence. The configured
// coordinator scopes creation to the authenticated user and request payload;
// an unbound executor invokes the callback directly for fixtures. The service
// performs final group, funding and credential checks. Successful
// execution returns a localized Key response; service errors become the
// executor's application error result.
func (h *APIKeyHandler[G]) Create(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var req CreateAPIKeyRequest
	if err := response.BindJSONStrict(c, &req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := ValidateAPIKeyCreateRequest(req); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	svcReq := apikey.CreateAPIKeyRequest{
		Name:                         req.Name,
		Scope:                        req.Scope,
		GroupID:                      req.GroupID,
		IsComposite:                  req.IsComposite,
		CompositeGroups:              req.CompositeGroups,
		CustomKey:                    req.CustomKey,
		IPWhitelist:                  req.IPWhitelist,
		IPBlacklist:                  req.IPBlacklist,
		FastModePolicy:               req.FastModePolicy,
		BillingMode:                  req.BillingMode,
		PreferredSubscriptionID:      req.PreferredSubscriptionID,
		MaxTokenRateMultiplier:       req.MaxTokenRateMultiplier,
		ConcurrencyLimit:             req.ConcurrencyLimit,
		RPMLimit:                     req.RPMLimit,
		ModelMapping:                 req.ModelMapping,
		ExpiresInDays:                req.ExpiresInDays,
		FallbackWhenGroupUnavailable: req.FallbackWhenGroupUnavailable,
	}
	if req.Quota != nil {
		svcReq.Quota = *req.Quota
	}
	if req.RateLimit5h != nil {
		svcReq.RateLimit5h = *req.RateLimit5h
	}
	if req.RateLimit1d != nil {
		svcReq.RateLimit1d = *req.RateLimit1d
	}
	if req.RateLimit7d != nil {
		svcReq.RateLimit7d = *req.RateLimit7d
	}

	h.ExecuteUserIdempotentJSON(c, "user.api_keys.create", req, h.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		key, err := h.apiKeyService.Create(ctx, subject.UserID, svcReq)
		if err != nil {
			return nil, err
		}
		return h.keyResponse(key, locale.FromContext(c.Request.Context())), nil
	})
}

// Update strictly decodes selected changes for the authenticated subject and
// returns the service's updated credential. Missing subject or malformed ID,
// JSON, numeric allowance or expiry input stops before the write.
//
// Empty Name and Status strings are omitted, while pointer fields carry
// explicit zero or empty replacements. Empty expires_at sets ClearExpiration;
// nonempty text uses RFC3339 parsing. The service determines final write masks,
// ownership, binding and funding checks, then the handler presents its result
// with request-localized group data.
func (h *APIKeyHandler[G]) Update(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid key ID")
		return
	}

	var req UpdateAPIKeyRequest
	if err := response.BindJSONStrict(c, &req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := ValidateAPIKeyUpdateRequest(req); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	svcReq := apikey.UpdateAPIKeyRequest{
		IsComposite:                  req.IsComposite,
		CompositeGroups:              req.CompositeGroups,
		IPWhitelist:                  req.IPWhitelist,
		IPBlacklist:                  req.IPBlacklist,
		FastModePolicy:               req.FastModePolicy,
		BillingMode:                  req.BillingMode,
		PreferredSubscriptionID:      req.PreferredSubscriptionID,
		MaxTokenRateMultiplier:       req.MaxTokenRateMultiplier,
		ConcurrencyLimit:             req.ConcurrencyLimit,
		RPMLimit:                     req.RPMLimit,
		ModelMapping:                 req.ModelMapping,
		Quota:                        req.Quota,
		ResetQuota:                   req.ResetQuota,
		RateLimit5h:                  req.RateLimit5h,
		RateLimit1d:                  req.RateLimit1d,
		RateLimit7d:                  req.RateLimit7d,
		ResetRateLimitUsage:          req.ResetRateLimitUsage,
		FallbackWhenGroupUnavailable: req.FallbackWhenGroupUnavailable,
	}
	if req.Name != "" {
		svcReq.Name = &req.Name
	}
	svcReq.GroupID = req.GroupID
	if req.Status != "" {
		svcReq.Status = &req.Status
	}
	// Keep an omitted expiry distinct from clearing it. Empty transport text
	// selects the service clearing flag before any stored timestamp is replaced.
	if req.ExpiresAt != nil {
		if *req.ExpiresAt == "" {
			svcReq.ExpiresAt = nil
			svcReq.ClearExpiration = true
		} else {
			t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
			if err != nil {
				response.BadRequest(c, "Invalid expires_at format: "+err.Error())
				return
			}
			svcReq.ExpiresAt = &t
		}
	}

	key, err := h.apiKeyService.Update(c.Request.Context(), keyID, subject.UserID, svcReq)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, h.keyResponse(key, locale.FromContext(c.Request.Context())))
}

// RotateCredential replaces an authenticated subject's eligible credential and
// presents the returned secret. A nonpositive or malformed ID is rejected
// early; ownership, managed-Key exclusion and storage conflicts come from the
// service and use shared application error responses.
func (h *APIKeyHandler[G]) RotateCredential(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || keyID <= 0 {
		response.BadRequest(c, "Invalid key ID")
		return
	}
	key, err := h.apiKeyService.RotateCredential(c.Request.Context(), keyID, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, h.keyResponse(key, locale.FromContext(c.Request.Context())))
}

// Delete requests service tombstone deletion for the authenticated subject's
// credential and returns a success message. Malformed ID or missing subject
// stops early; parseable signed IDs and service ownership failures use the
// existing service response path.
func (h *APIKeyHandler[G]) Delete(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid key ID")
		return
	}

	err = h.apiKeyService.Delete(c.Request.Context(), keyID, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "API key deleted successfully"})
}

// GetAvailableGroups resolves active payer-permitted groups, optionally
// restricted by a selected subscription, for the subject's requested scope.
// include_models defaults true and malformed text is rejected; supplied
// subscription IDs must be positive.
//
// Capacity observations are optional and failed reads leave presentation
// usable. The base presenter is invoked before an optional specialized
// presenter replaces its result, receiving the parsed include_models choice.
// The specialized presenter receives the current request context for its
// localized or catalogue work. The base callback determines its own presentation
// work, and caller-installed middleware establishes subject authorization.
func (h *APIKeyHandler[G]) GetAvailableGroups(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	includeModels, err := strconv.ParseBool(c.DefaultQuery("include_models", "true"))
	if err != nil {
		response.BadRequest(c, "Invalid include_models")
		return
	}

	var subscriptionID *int64
	if rawSubscriptionID := strings.TrimSpace(c.Query("subscription_id")); rawSubscriptionID != "" {
		parsedID, err := strconv.ParseInt(rawSubscriptionID, 10, 64)
		if err != nil || parsedID <= 0 {
			response.BadRequest(c, "Invalid subscription ID")
			return
		}
		subscriptionID = &parsedID
	}

	groups, err := h.apiKeyService.GetAvailableGroupsForScopeWithSubscription(
		c.Request.Context(),
		subject.UserID,
		c.DefaultQuery("scope", "personal"),
		subscriptionID,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]G, 0, len(groups))
	capacityMap := h.getAvailableGroupCapacityMap(c.Request.Context(), groups)
	for i := range groups {
		var capacity *accessview.GroupCapacitySummary
		if value, ok := capacityMap[groups[i].ID]; ok {
			capacity = &value
		}
		groupDTO := h.presentGroup(&groups[i], capacity)
		if h.groupPresentation != nil {
			groupDTO = h.groupPresentation(c.Request.Context(), &groups[i], capacity, includeModels)
		}
		out = append(out, *groupDTO)
	}
	response.Success(c, out)
}

// GetBillingOptions lists effective subscriptions offered for the authenticated
// subject's requested payer scope. Team scope resolves the owner in the
// service; the response copies applicable group IDs and uses service-localized
// plan names.
func (h *APIKeyHandler[G]) GetBillingOptions(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	options, err := h.apiKeyService.ListBillingSubscriptionsForScope(
		c.Request.Context(),
		subject.UserID,
		c.DefaultQuery("scope", "personal"),
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]APIKeyBillingSubscriptionOptionResponse, 0, len(options))
	for i := range options {
		option := options[i]
		out = append(out, APIKeyBillingSubscriptionOptionResponse{
			ID:               option.ID,
			PlanID:           option.PlanID,
			PlanName:         option.PlanName,
			ExpiresAt:        option.ExpiresAt,
			GroupsRestricted: option.GroupsRestricted,
			ApplicableGroups: append([]int64(nil), option.ApplicableGroups...),
		})
	}
	response.Success(c, out)
}

// getAvailableGroupCapacityMap reads optional capacity observations for
// supplied group IDs. Empty input or missing dependency returns nil, and a
// failed read also returns nil so authorized group listing continues with
// absent capacity.
func (h *APIKeyHandler[G]) getAvailableGroupCapacityMap(ctx context.Context, groups []routing.Group) map[int64]accessview.GroupCapacitySummary {
	if h.groupCapacityService == nil || len(groups) == 0 {
		return nil
	}
	groupIDs := make([]int64, 0, len(groups))
	for i := range groups {
		groupIDs = append(groupIDs, groups[i].ID)
	}

	// Capacity is a load observation after permission filtering. Treat failed
	// reads as absent observations so group configuration choices still remain available.
	capacityMap, err := h.groupCapacityService.GetGroupCapacityByIDs(ctx, groupIDs)
	if err != nil {
		return nil
	}
	return capacityMap
}

// GetUserGroupRates returns payer-specific group charge multipliers for the
// authenticated subject's requested scope. Team scope resolves the current
// owner, missing rate storage can produce nil data, and service errors use the
// shared application response. With token_rates=true, the response instead
// previews current group Token ceilings under the requested funding selection.
func (h *APIKeyHandler[G]) GetUserGroupRates(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	tokenRates, err := strconv.ParseBool(c.DefaultQuery("token_rates", "false"))
	if err != nil {
		response.BadRequest(c, "Invalid token_rates")
		return
	}
	if tokenRates {
		var subscriptionID *int64
		if raw := c.Query("subscription_id"); raw != "" {
			id, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr != nil || id <= 0 {
				response.BadRequest(c, "Invalid subscription ID")
				return
			}
			subscriptionID = &id
		}
		rates, readErr := h.apiKeyService.TokenRatesForScope(c.Request.Context(), subject.UserID,
			c.DefaultQuery("scope", "personal"), c.DefaultQuery("billing_mode", "auto"), subscriptionID)
		if readErr != nil {
			response.ErrorFrom(c, readErr)
			return
		}
		response.Success(c, rates)
		return
	}
	rates, err := h.apiKeyService.GetUserGroupRatesForScope(c.Request.Context(), subject.UserID, c.DefaultQuery("scope", "personal"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, rates)
}
