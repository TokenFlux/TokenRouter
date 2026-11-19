package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tokenestimate"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// openai_count_tokens.go prepares Messages-compatible token counting and
// serves standalone Grok estimates in the Messages count shape.
// Messages-shaped counts route the client model through the effective group's
// plan before querying the selected platform. The original requested name
// remains available for diagnostics while the provider-layer name and mapped
// body guide counting. Grok's standalone count entry instead estimates its
// parsed Anthropic input locally and uses the same response shape.
//
// Authenticated compatibility counting checks funding before its single
// slot-free selection. That request carries its mapping, body reader, session
// hash, and start time into the count adapter. Prompt replacement happens
// before parsing and route planning, so the counted content follows that
// replacement; none of these paths submits a generation result for settlement.

// OpenAICountCall carries authenticated routing input and observations for one
// compatibility count.
type OpenAICountCall struct {
	Key *apikey.APIKey // effective authenticated API key
	// Model, ProviderLayerModel, SessionHash, and Platform describe one
	// count. Model keeps the client-facing identity while
	// ProviderLayerModel follows group routing; SessionHash and Platform
	// constrain the corresponding selection.
	// Model: original client model. ProviderLayerModel: trimmed group
	// model.
	// SessionHash: sticky selection digest. Platform: optional
	// provider-platform filter.
	Model, ProviderLayerModel, SessionHash, Platform string
	Mapping                                          routing.GroupMappingResult // effective group model mapping
	// MappedBody retains a request-local model replacement cache. Returned
	// bytes may share its storage, so the counting target treats them as
	// immutable.
	MappedBody func(bool, string) []byte
	Log        *zap.Logger // request count diagnostic logger
	StartedAt  time.Time   // authentication preparation start time
}

// GrokCountTokens parses Anthropic-shaped input and returns a local token
// estimate. It enters the request activity barrier and validates a nonempty
// body and model. Backend authentication, funding, and provider selection are
// skipped by this method; the gateway route supplies its surrounding admission.
// Successful estimates publish request observations and write input_tokens,
// while parsing failures use Anthropic errors. The estimator runs on the parsed
// body, so no outgoing provider request or generation completion is created.
func (h *OpenAITokensHandler) GrokCountTokens(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "anthropic")
	if !accepted {
		return
	}
	defer done()

	body, err := ReadLenientJSONRequestBodyWithPrealloc(c.Request, h.options.MaxBodyBytes)
	if err != nil {
		if maxErr, ok := openAITextMaxBytesError(err); ok {
			h.anthropicErrorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", BodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", RequestBodyReadErrorMessage(err))
		return
	}
	if len(body) == 0 {
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	bodyRef := requeststate.NewRequestBodyRef(body)
	parsedReq, err := requeststate.ParseGatewayRequest(bodyRef, capability.PlatformAnthropic)
	if err != nil {
		LogRequestBodyParseFailure(RequestLogger(c, "handler.openai_gateway.grok_count_tokens"), body, err)
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", RequestBodyParseErrorMessage(err))
		return
	}
	if parsedReq.Model == "" {
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}

	estimated, err := tokenestimate.Anthropic(parsedReq.Body.Bytes())
	if err != nil {
		RequestLogger(c, "handler.openai_gateway.grok_count_tokens").Warn("grok_count_tokens.local_estimate_failed", zap.Error(err))
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}

	h.backend.ObserveRequest(c, parsedReq.Model, false)
	h.backend.ObserveEndpoint(c, false)
	c.JSON(http.StatusOK, gin.H{"input_tokens": estimated})
}

// CountTokens executes one authenticated Messages-compatible token-count
// request. It requires an effective key, user subject, protocol permission, and
// backend services before reading input. User prompt replacement precedes
// parsing and group route planning, and funding eligibility is checked before
// selecting a provider.
//
// The route's mapped model drives the provider-layer name and mapped-body
// cache, while reqModel remains the client-facing identity. The dedicated text
// adapter performs at most one slot-free selection and count; it records
// selection latency even when selection fails and submits no generation
// completion.
func (h *OpenAITokensHandler) CountTokens(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "anthropic")
	if !accepted {
		return
	}
	defer done()

	requestStart := time.Now()

	apiKey, ok := h.backend.Access(c)
	if !ok {
		h.anthropicErrorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		h.anthropicErrorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := RequestLogger(
		c,
		"handler.openai_gateway.count_tokens",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)

	if !h.backend.AllowsMessages(apiKey) {
		h.backend.PolicyDenied(c)
		h.anthropicErrorResponse(c, http.StatusForbidden, "permission_error",
			"This group does not allow Anthropic Messages requests")
		return
	}

	if !h.backend.Dependencies(c, reqLog) {
		return
	}

	body, err := ReadLenientJSONRequestBodyWithPrealloc(c.Request, h.options.MaxBodyBytes)
	if err != nil {
		if maxErr, ok := openAITextMaxBytesError(err); ok {
			h.anthropicErrorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", BodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", RequestBodyReadErrorMessage(err))
		return
	}
	if len(body) == 0 {
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	h.backend.ObserveRequest(c, "", false)
	if err := wirejson.ValidateRequestObject(body); err != nil {
		LogRequestBodyParseFailure(reqLog, body, err)
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", RequestBodyParseErrorMessage(err))
		return
	}

	body = h.prompt.ApplyUserPromptReplacementToBody(c.Request.Context(), body, "anthropic_messages")
	bodyRef := requeststate.NewRequestBodyRef(body)
	parsedReq, err := requeststate.ParseGatewayRequest(bodyRef, capability.PlatformAnthropic)
	if err != nil {
		LogRequestBodyParseFailure(reqLog, body, err)
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	body = parsedReq.Body.Bytes()
	if parsedReq.Model == "" {
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}

	reqModel := parsedReq.Model
	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", parsedReq.Stream))

	h.backend.ObserveRequest(c, reqModel, false)
	h.backend.ObserveEndpoint(c, false)

	// Bind group routing before deriving provider-layer identity, keeping
	// the client model available for error text while selection reads the
	// route's mapping.
	groupMappingRoutePlan := h.backend.Plan(c.Request.Context(), apiKey, reqModel)
	groupMapping := groupMappingRoutePlan.Mapping()
	h.backend.BindPlan(c, groupMappingRoutePlan)
	groupMappedModel := groupMapping.MappedModel
	if groupMappedModel == "" {
		groupMappedModel = reqModel
	}
	providerLayerModel := h.backend.MessageProviderModel(c.Request.Context(), apiKey, groupMappedModel)
	mappedBodyForMessages := h.backend.MappedBodyCache(body)

	subscription, _ := SubscriptionFromContext(c)
	requestPlatform := h.backend.Platform(apiKey)
	if err := h.backend.Eligibility(c.Request.Context(), apiKey, subscription); err != nil {
		reqLog.Info("openai_count_tokens.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := BillingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.anthropicErrorResponse(c, status, code, message)
		return
	}

	sessionHash := h.backend.SessionHash(c, OpenAISelectionSession, body)
	// Funding has passed before this slot-free count begins. The
	// single-count adapter reports selection latency and error output
	// without entering generation completion.
	textflow.RunSingleCountTokens(h.backend.CountExecution(c, OpenAICountCall{
		Key: apiKey, Model: reqModel, ProviderLayerModel: providerLayerModel, SessionHash: sessionHash,
		Platform: requestPlatform, Mapping: groupMapping, MappedBody: mappedBodyForMessages, Log: reqLog, StartedAt: requestStart,
	}))
}
