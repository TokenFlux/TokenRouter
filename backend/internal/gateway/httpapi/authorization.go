package httpapi

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
)

// authorization.go authenticates gateway requests, resolves their group and
// funding, and reserves API-key request capacity. Gateway identity
// authentication precedes model-prefix selection and funding admission. The
// selected key view supplies the payer and group used by handlers, while
// authenticated actor data remains available for ownership reporting. Standard
// and Google endpoints share these checks but write different error shapes and
// bind funding details at their respective points in the flow.
//
// Consumption eligibility and request-capacity reservation are separate decisions.
// Usage and existing-task operations can skip funding enforcement, while model
// listings still check funding but skip key request limits. Responses upgrades
// defer those request limits to each turn. Other admitted requests hold their
// key lease through downstream processing and release it when the handler returns.

// AuthorizationSubscriptions reads and validates subscriptions, including usage window maintenance.
type AuthorizationSubscriptions interface {
	// SubscriptionReader resolves funding subscriptions for the selected request group.
	admission.SubscriptionReader
	// SubscriptionValidator checks quota and completes required subscription usage-window maintenance.
	admission.SubscriptionValidator
}

// APIKeyAuthorizationOptions configures authentication and binding of the authorized key to the request.
type APIKeyAuthorizationOptions struct {
	// Authentication supplies credential readers and observation callbacks. The
	// constructors copy these options and select the protocol-specific Google flag.
	Authentication keyhttp.AuthenticationOptions
	// BindLegacyKey optionally publishes the final key to application consumers
	// after funding admission; the application callback decides whether to copy it.
	BindLegacyKey func(*gin.Context, *apikey.APIKey)
}

// APIKeyIDFromContext returns the authenticated key ID, or zero for a missing or unexpected value.
// A nil context or nil stored pointer also yields zero. Consumers use that
// sentinel when the request has no usable API-key view for observation.
func APIKeyIDFromContext(c *gin.Context) int64 {
	if c == nil {
		return 0
	}
	v, exists := c.Get("api_key")
	if !exists {
		return 0
	}
	apiKey, ok := v.(*apikey.APIKey)
	if !ok || apiKey == nil {
		return 0
	}
	return apiKey.ID
}

// loaded reports the selected key through the optional authentication callback.
// It runs before final group admission, so consumers can retain key details for
// rejected-request diagnostics without treating the callback as authorization success.
func (o APIKeyAuthorizationOptions) loaded(c *gin.Context, k *apikey.APIKey) {
	if o.Authentication.Loaded != nil {
		o.Authentication.Loaded(c, k)
	}
}

// business reports a group-availability rejection through the optional limit callback.
// Every group rejection uses the shared observation reason, while reject records
// the more specific ingress reason before protocol error output.
func (o APIKeyAuthorizationOptions) business(c *gin.Context) {
	if o.Authentication.BusinessLimited != nil {
		o.Authentication.BusinessLimited(c, "api_key_group_unavailable")
	}
}

// reject reports the supplied ingress reason through the optional rejection callback.
// It leaves response writing to the caller so standard and Google handlers can
// use the same observation with their own error envelopes.
func (o APIKeyAuthorizationOptions) reject(c *gin.Context, reason string) {
	if o.Authentication.Rejected != nil {
		o.Authentication.Rejected(c, reason)
	}
}

// bind stores the admitted key view and invokes the optional application binding.
// The effective view is the supplied pointer; application consumers use
// BindLegacyKey to publish any copies or typed group context they require.
func (o APIKeyAuthorizationOptions) bind(c *gin.Context, k *apikey.APIKey) {
	c.Set("gateway_effective_key", k)
	if o.BindLegacyKey != nil {
		o.BindLegacyKey(c, k)
	}
}

// EffectiveAPIKey returns the final key view published for request execution.
// Callers provide a context. The flag reports a successful pointer type assertion,
// so a stored nil APIKey pointer returns true and still requires a nil check.
// Authorization publishes this view before key capacity reservation, so a later
// limiter rejection can leave the view available for request diagnostics.
func EffectiveAPIKey(c *gin.Context) (*apikey.APIKey, bool) {
	value, ok := c.Get("gateway_effective_key")
	if !ok {
		return nil, false
	}
	key, ok := value.(*apikey.APIKey)
	return key, ok
}

// NewAPIKeyAuthorization constructs standard gateway authentication and admission.
// The returned middleware resolves composite groups before key model aliases,
// checks group permissions and funding, then publishes the selected key for
// downstream handlers. Rejections abort with the standard endpoint's error shape.
//
// After final group authorization it binds a request snapshot using the selected
// payer and Fast policy. Actor principal publication follows funding admission,
// and key request capacity is reserved immediately before downstream processing.
// The release callback runs after c.Next returns, covering waiting and streaming
// performed by later handlers. Observation callbacks can run before admission succeeds.
func NewAPIKeyAuthorization(apiKeyService *apikey.APIKeyService, subscriptionService AuthorizationSubscriptions, options APIKeyAuthorizationOptions) gin.HandlerFunc {
	options.Authentication.Google = false
	return func(c *gin.Context) {
		access, ok := keyhttp.Authenticate(c, apiKeyService, options.Authentication)
		if !ok {
			return
		}
		apiKey := access.KeyView()
		var err error

		apiKey, err = ResolveCompositeAPIKeyRequest(c, apiKeyService, apiKey)
		if err != nil {
			AbortCompositeKeyError(c, err)
			return
		}
		options.loaded(c, apiKey)
		if abortAuthorizationGroupUnavailable(c, apiKey, options) {
			return
		}
		if abortAuthorizationGroupNotAllowed(c, apiKey, options) {
			return
		}
		// Composite selection can replace the group-specific key view. Publish its
		// payer and Fast policy after authorization so execution reads the admitted selection.
		requestAccess := *access
		requestAccess.PayerUserID = apiKey.User.ID
		ctx := apikey.WithAccessSnapshot(c.Request.Context(), requestAccess)
		c.Request = c.Request.WithContext(apikey.WithFastModePolicy(ctx, apiKey.FastModePolicy))
		ApplyAPIKeyModelRedirect(c, apiKey)
		// Existing results and usage remain accessible after consumption eligibility
		// ends; identity checks still apply before these operations bypass funding enforcement.
		skipBilling := IsAPIKeyUsageRequest(c.Request.Method, c.Request.URL.Path) ||
			IsBatchImageBillingBypassRequest(c.Request.Method, c.Request.URL.Path) ||
			(apiKey.IsComposite && IsGrokVideoTaskRead(c.Request.Method, c.Request.URL.Path))

		billingContext, billingErr := admission.ResolveFundingFromKey(c.Request.Context(), apiKey, subscriptionService, !skipBilling)
		if billingErr != nil {
			switch {
			case errors.Is(billingErr, billing.ErrPreferredSubscriptionGroup):
				keyhttp.AbortWithError(c, 403, "PREFERRED_SUBSCRIPTION_GROUP_NOT_ALLOWED", billingErr.Error())
			case errors.Is(billingErr, billing.ErrPreferredSubscriptionInvalid):
				keyhttp.AbortWithError(c, 403, "PREFERRED_SUBSCRIPTION_INVALID", billingErr.Error())
			default:
				keyhttp.AbortWithError(c, 500, "INTERNAL_ERROR", "Failed to validate subscription")
			}
			return
		}
		var subscription *billing.UserSubscription
		if billingContext != nil {
			subscription = billingContext.Subscription
		}

		// Consumption checks can replace a subscription after window maintenance.
		// Publish that checked object so later billing uses the subscription admission validated.
		if !skipBilling {
			checked, failure := admission.CheckConsumption(c.Request.Context(), admission.ConsumptionInput{Status: apiKey.Status, Limits: apiKey, Balance: apiKey.User.Balance, Subscription: subscription}, subscriptionService)
			if failure != nil {
				switch failure.Kind {
				case admission.KeyQuotaExceeded:
					AbortAPIKeyQuotaError(c)
				case admission.KeyExpired:
					keyhttp.AbortWithError(c, 403, "API_KEY_EXPIRED", "The API key has expired.")
				case admission.MaintenanceFailed:
					keyhttp.AbortWithError(c, 500, "SUBSCRIPTION_MAINTENANCE_FAILED", "Failed to maintain subscription usage windows")
				case admission.SubscriptionLimitExceeded:
					keyhttp.AbortWithError(c, 429, "USAGE_LIMIT_EXCEEDED", failure.Error())
				case admission.SubscriptionInvalid:
					keyhttp.AbortWithError(c, 403, "SUBSCRIPTION_INVALID", failure.Error())
				case admission.InsufficientBalance:
					keyhttp.AbortWithError(c, 403, "INSUFFICIENT_BALANCE", "Insufficient account balance")
				}
				return
			}
			subscription = checked
			if billingContext != nil && subscription != nil {
				billingContext.Subscription = subscription
				billingContext.Available = true
			}
		}

		if billingContext != nil {
			c.Set("api_key_billing", billingContext)
		}
		if subscription != nil {
			c.Set("subscription", subscription)
		}
		c.Set("api_key", apiKey)
		c.Set("user", authctx.AuthSubject{
			UserID:      apiKey.User.ID,
			Concurrency: apiKey.User.Concurrency,
		})
		c.Set("user_role", apiKey.User.Role)
		options.bind(c, apiKey)
		// Last-used persistence is diagnostic bookkeeping. A failed touch leaves
		// this already authenticated request eligible to reserve capacity and continue processing.
		_ = apiKeyService.TouchLastUsed(c.Request.Context(), apiKey.ID)

		keyhttp.SetAccessPrincipal(c, access)
		release, ok := acquireKeyRequest(c, apiKeyService, apiKey, options)
		if !ok {
			return
		}
		// Hold the key lease while downstream handlers wait or stream. Returning
		// from their chain releases admission capacity even when they finish with an error.
		defer release()
		c.Next()
	}
}

// NewGoogleAPIKeyAuthorization authenticates Google endpoints and reports failures in Google format.
// The middleware resolves composite route parameters before key aliases, checks
// the selected group and funding source, then binds the effective key for handlers.
// Usage and existing-task operations retain the funding source even when it is unavailable.
//
// Funding context is published before Google's consumption check, which can
// replace its subscription after usage-window maintenance. Principal publication
// and key capacity reservation follow successful admission; the lease is released
// after downstream handlers return. Google credential and error handling are
// selected on a copy of Authentication options supplied by the caller.
func NewGoogleAPIKeyAuthorization(apiKeyService *apikey.APIKeyService, subscriptionService AuthorizationSubscriptions, options APIKeyAuthorizationOptions) gin.HandlerFunc {
	options.Authentication.Google = true
	return func(c *gin.Context) {
		access, ok := keyhttp.Authenticate(c, apiKeyService, options.Authentication)
		if !ok {
			return
		}
		apiKey := access.KeyView()
		var err error

		apiKey, err = ResolveCompositeAPIKeyRequest(c, apiKeyService, apiKey)
		if err != nil {
			AbortCompositeKeyGoogleError(c, err)
			return
		}
		options.loaded(c, apiKey)
		if code, message, ok := admission.GroupAvailable(apiKey); !ok {
			options.business(c)
			if code == "GROUP_DELETED" {
				options.reject(c, "group_deleted")
			} else {
				options.reject(c, "group_disabled")
			}
			keyhttp.AbortGoogleError(c, 403, message)
			return
		}
		// The selected group can differ from the key's initial view. Recheck its
		// exclusive-group access before resolving funding so revoked membership stops this request.
		if !admission.GroupAllowed(apiKey) {
			options.business(c)
			options.reject(c, "group_not_allowed")
			keyhttp.AbortGoogleError(c, 403, "You do not have access to the selected group.")
			return
		}
		ApplyAPIKeyModelRedirect(c, apiKey)
		skipBilling := IsAPIKeyUsageRequest(c.Request.Method, c.Request.URL.Path) ||
			IsBatchImageBillingBypassRequest(c.Request.Method, c.Request.URL.Path) ||
			(apiKey.IsComposite && IsGrokVideoTaskRead(c.Request.Method, c.Request.URL.Path))

		// Non-consuming requests can read or manage existing data even when their
		// configured funding source is invalid, leaving consumption enforcement to consuming endpoints.
		billingContext, billingErr := admission.ResolveFundingFromKey(c.Request.Context(), apiKey, subscriptionService, !skipBilling)
		if billingErr != nil {
			switch {
			case errors.Is(billingErr, billing.ErrPreferredSubscriptionGroup):
				keyhttp.AbortGoogleError(c, 403, "Preferred subscription does not allow this group")
			case errors.Is(billingErr, billing.ErrPreferredSubscriptionInvalid):
				keyhttp.AbortGoogleError(c, 403, "Preferred subscription is unavailable")
			default:
				keyhttp.AbortGoogleError(c, 500, "Failed to validate subscription")
			}
			return
		}
		var subscription *billing.UserSubscription
		if billingContext != nil {
			subscription = billingContext.Subscription
		}
		if billingContext != nil {
			c.Set("api_key_billing", billingContext)
		}
		if !skipBilling {
			checked, failure := admission.CheckConsumption(c.Request.Context(), admission.ConsumptionInput{Status: apiKey.Status, Limits: apiKey, Balance: apiKey.User.Balance, Subscription: subscription}, subscriptionService)
			if failure != nil {
				status := 403
				message := ""
				switch failure.Kind {
				case admission.KeyQuotaExceeded:
					status = 429
					message = "The API key quota has been exhausted."
				case admission.KeyExpired:
					message = "The API key has expired."
				case admission.MaintenanceFailed:
					status = 500
					message = "Failed to maintain subscription usage windows"
				case admission.SubscriptionLimitExceeded:
					status = 429
					message = failure.Error()
				case admission.SubscriptionInvalid:
					message = failure.Error()
				case admission.InsufficientBalance:
					message = "Insufficient account balance"
				}
				keyhttp.AbortGoogleError(c, status, message)
				return
			}
			subscription = checked
			if subscription != nil {
				billingContext.Subscription = subscription
			}
		}
		if subscription != nil {
			c.Set("subscription", subscription)
		}

		c.Set("api_key", apiKey)
		c.Set("user", authctx.AuthSubject{
			UserID:      apiKey.User.ID,
			Concurrency: apiKey.User.Concurrency,
		})
		c.Set("user_role", apiKey.User.Role)
		options.bind(c, apiKey)
		// Last-used persistence is diagnostic bookkeeping. A failed touch leaves
		// this already authenticated request eligible to reserve capacity and continue processing.
		_ = apiKeyService.TouchLastUsed(c.Request.Context(), apiKey.ID)
		keyhttp.SetAccessPrincipal(c, access)
		release, ok := acquireKeyRequest(c, apiKeyService, apiKey, options)
		if !ok {
			return
		}
		// Hold the key lease while downstream handlers wait or stream. Returning
		// from their chain releases admission capacity even when they finish with an error.
		defer release()
		c.Next()
	}
}

// AbortAPIKeyQuotaError rejects exhausted key quota using the endpoint's error shape.
// Responses-compatible paths receive insufficient_quota; other standard endpoints
// receive API_KEY_QUOTA_EXHAUSTED. Both responses use HTTP 429 and abort Gin processing.
func AbortAPIKeyQuotaError(c *gin.Context) {
	const message = "The API key quota has been exhausted."
	if IsOpenAICompatibleAPIKeyRequest(c) {
		AbortOpenAIQuotaError(c, http.StatusTooManyRequests, message)
		return
	}
	keyhttp.AbortWithError(c, http.StatusTooManyRequests, "API_KEY_QUOTA_EXHAUSTED", message)
}

// IsOpenAICompatibleAPIKeyRequest reports whether quota errors use the Responses shape.
// Nil request data returns false. Recognized Responses roots and their subpaths
// match after trailing slashes are removed, including the Codex and OpenAI aliases.
func IsOpenAICompatibleAPIKeyRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}

	path := strings.TrimRight(c.Request.URL.Path, "/")
	for _, root := range []string{
		"/v1/responses",
		"/openai/v1/responses",
		"/responses",
		"/backend-api/codex/responses",
	} {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

// AbortOpenAIQuotaError writes an OpenAI-compatible insufficient-quota response.
// Callers choose the status and safe message. The type and code remain
// insufficient_quota so Responses clients can classify quota exhaustion before Gin processing is aborted.
func AbortOpenAIQuotaError(c *gin.Context, statusCode int, message string) {
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"message": message,
			"type":    "insufficient_quota",
			"param":   nil,
			"code":    "insufficient_quota",
		},
	})
	c.Abort()
}

// abortAuthorizationGroupUnavailable rejects missing or unavailable groups on standard endpoints.
// Unbound ordinary keys can still access usage and existing-task operations.
// A true result means observation callbacks ran and a terminal protocol response
// was written, so the caller stops before funding admission.
func abortAuthorizationGroupUnavailable(c *gin.Context, key *apikey.APIKey, o APIKeyAuthorizationOptions) bool {
	// Reject an unbound ordinary key before funding checks, while allowing existing-task and local usage access.
	if key != nil && !key.IsComposite && key.GroupID == nil && !IsAPIKeyUsageRequest(c.Request.Method, c.Request.URL.Path) && !IsBatchImageBillingBypassRequest(c.Request.Method, c.Request.URL.Path) && !IsGrokVideoTaskRead(c.Request.Method, c.Request.URL.Path) {
		o.business(c)
		o.reject(c, "group_unassigned")
		keyhttp.AbortWithError(c, http.StatusForbidden, "GROUP_REQUIRED", "Select a group for this API key in the console")
		return true
	}

	code, message, ok := admission.GroupAvailable(key)
	if ok {
		return false
	}
	o.business(c)
	if code == "GROUP_DELETED" {
		o.reject(c, "group_deleted")
	} else {
		o.reject(c, "group_disabled")
	}
	keyhttp.AbortWithError(c, 403, code, message)
	return true
}

// abortAuthorizationGroupNotAllowed rejects access to the selected exclusive group.
// It reports both business-limit and ingress observations before writing the
// standard forbidden response. A true result tells the admission caller to stop processing.
func abortAuthorizationGroupNotAllowed(c *gin.Context, key *apikey.APIKey, o APIKeyAuthorizationOptions) bool {
	if admission.GroupAllowed(key) {
		return false
	}
	o.business(c)
	o.reject(c, "group_not_allowed")
	keyhttp.AbortWithError(c, 403, "GROUP_NOT_ALLOWED", "You do not have access to the selected group.")
	return true
}

// acquireKeyRequest reserves request capacity before protocol handling; non-consuming and per-turn endpoints bypass it.
// Successful callers use the request context it installs and invoke the returned
// release function after processing finishes. Rejections write the protocol's
// error and return false. Price ceilings use 403, capacity limits use 429, and
// unavailable pricing or limiter storage uses 503.
func acquireKeyRequest(c *gin.Context, keys *apikey.APIKeyService, key *apikey.APIKey, options APIKeyAuthorizationOptions) (func(), bool) {
	if IsAPIKeyNonConsumingRequest(c.Request.Method, c.Request.URL.Path) || isResponsesTurnAdmission(c) {
		// Reads bypass key request limits, and Responses upgrades count each turn
		// separately so an idle connection does not occupy a request-capacity lease.
		return func() {}, true
	}
	ctx, release, retry, err := keys.AcquireRequest(c.Request.Context(), key)
	if err == nil {
		c.Request = c.Request.WithContext(ctx)
		return release, true
	}
	message := err.Error()
	status := http.StatusTooManyRequests
	code := "API_KEY_RPM_LIMIT_EXCEEDED"
	// Unknown limiter failures cannot establish which configured limit was reached.
	// Classify them as service unavailable so callers distinguish store failure from an enforced capacity limit.
	switch {
	case errors.Is(err, apikey.ErrTokenRateLimitExceeded):
		status = http.StatusForbidden
		code = "API_KEY_TOKEN_RATE_LIMIT_EXCEEDED"
		message = apikey.ErrTokenRateLimitExceeded.Message
	case errors.Is(err, apikey.ErrTokenRateUnavailable):
		status = http.StatusServiceUnavailable
		code = "API_KEY_TOKEN_RATE_UNAVAILABLE"
		message = apikey.ErrTokenRateUnavailable.Message
	case errors.Is(err, apikey.ErrKeyConcurrencyExceeded):
		code = "API_KEY_CONCURRENCY_LIMIT_EXCEEDED"
	case errors.Is(err, apikey.ErrKeyRPMExceeded):
	default:
		status = http.StatusServiceUnavailable
		code = "API_KEY_LIMITER_UNAVAILABLE"
	}
	if retry > 0 {
		// Round the limiter duration upward to whole header seconds so clients
		// do not retry before its reported request-capacity wait has elapsed.
		c.Header("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
	}
	if (status == http.StatusTooManyRequests || status == http.StatusForbidden) && options.Authentication.BusinessLimited != nil {
		options.Authentication.BusinessLimited(c, code)
	}
	if options.Authentication.Google {
		keyhttp.AbortGoogleError(c, status, message)
	} else {
		keyhttp.AbortWithError(c, status, code, message)
	}
	return nil, false
}

// isResponsesTurnAdmission identifies Responses upgrades whose key limits are enforced on each turn.
// It requires a GET WebSocket upgrade on a supported Responses root. Plain GET,
// POST, and other WebSocket endpoints retain connection or request-level admission.
func isResponsesTurnAdmission(c *gin.Context) bool {
	if c.Request.Method != http.MethodGet || !IsResponsesWSUpgrade(c.Request) {
		return false
	}
	switch strings.TrimRight(c.Request.URL.Path, "/") {
	case "/responses", "/v1/responses", "/backend-api/codex/responses":
		return true
	default:
		return false
	}
}

// GetExecutionAPIKey reads the authenticated API key from the request context.
// A nil interface, absent value, or another dynamic type yields nil. Execution
// adapters use this pointer view after authentication has installed the key.
func GetExecutionAPIKey(c interface {
	// Get reads a context value and reports whether its key exists.
	Get(string) (any, bool)
},
) *apikey.APIKey {
	if c == nil {
		return nil
	}
	v, exists := c.Get("api_key")
	if !exists {
		return nil
	}
	apiKey, _ := v.(*apikey.APIKey)
	return apiKey
}
