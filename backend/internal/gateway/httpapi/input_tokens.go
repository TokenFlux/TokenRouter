package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// input_tokens.go validates Responses input-token requests and prepares mapped
// counting input after funding admission.
// Responses input-token preflight checks a request without generating an answer
// or submitting usage completion. Funding eligibility precedes group route
// planning here, and the original requested model remains separate from the
// mapped routing name passed into the counting attempt adapter.
//
// A nonempty mapped model rewrites the counting body and supplies
// RoutingModel, while Model retains the trimmed client name for subsequent
// error presentation. Session hashing uses those prepared bytes after mapping,
// matching the body passed to the count adapter. The handler binds the same
// route plan in request context before constructing that adapter, so
// downstream selection can interpret the mapped payload with the group
// decision that produced it.

// InputTokensCall retains requested and routed identity for Responses counting
// execution.
type InputTokensCall struct {
	Key  *apikey.APIKey // authenticated effective API Key
	Body []byte         // mapped counting request bytes
	// Model, RoutingModel, Platform and SessionHash describe the same
	// counting request. Model retains the client request name; RoutingModel
	// names the mapped selection model; Platform constrains eligible
	// provider candidates; SessionHash is the sticky selection seed for
	// that counting request.
	Model, RoutingModel, Platform, SessionHash string
	// request counting diagnostic logger
	Log *zap.Logger
}

// ResponsesInputTokens executes authenticated Responses input-token preflight
// under the request activity barrier. It validates protocol permission and JSON
// model input, checks funding eligibility, then binds the group route and
// mapped body before opening the bounded text counting loop. This path performs
// no generation completion submission; selected resource handling belongs to
// its execution adapter.
func (h *OpenAITokensHandler) ResponsesInputTokens(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	requestStart := time.Now()
	apiKey, ok := h.backend.Access(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := RequestLogger(c, "handler.openai_gateway.responses_input_tokens",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID))
	if apiKey.Group != nil && !(&routing.Group{AllowedProtocols: apiKey.Group.AllowedProtocols}).AllowsClientProtocol(capability.ProtocolOpenAIResponses) {
		h.backend.PolicyDenied(c)
		h.errorResponse(c, http.StatusForbidden, "permission_error", "This group does not allow OpenAI Responses requests")
		return
	}
	if !h.backend.Dependencies(c, reqLog) {
		return
	}

	body, err := ReadLenientJSONRequestBodyWithPrealloc(c.Request, h.options.MaxBodyBytes)
	if err != nil {
		if maxErr, ok := openAITextMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", BodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", RequestBodyReadErrorMessage(err))
		return
	}
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	if err := wirejson.ValidateRequestObject(body); err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", RequestBodyParseErrorMessage(err))
		return
	}
	modelResult := gjson.GetBytes(body, "model")
	if !modelResult.Exists() || modelResult.Type != gjson.String || strings.TrimSpace(modelResult.String()) == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	reqModel := strings.TrimSpace(modelResult.String())
	h.backend.ObserveRequest(c, reqModel, false)
	h.backend.ObserveEndpoint(c, false)

	subscription, _ := SubscriptionFromContext(c)
	if err := h.backend.Eligibility(c.Request.Context(), apiKey, subscription); err != nil {
		reqLog.Info("openai_responses_input_tokens.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := BillingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}
	h.backend.AuthLatency(c, time.Since(requestStart).Milliseconds())

	// Plan after funding eligibility and retain the client model
	// separately, so mapped request bytes and selection use the group route
	// without losing request attribution.
	groupMappingRoutePlan := h.backend.Plan(c.Request.Context(), apiKey, reqModel)
	groupMapping := groupMappingRoutePlan.Mapping()
	h.backend.BindPlan(c, groupMappingRoutePlan)
	routingModel := reqModel
	if strings.TrimSpace(groupMapping.MappedModel) != "" {
		routingModel = strings.TrimSpace(groupMapping.MappedModel)
		body = h.backend.MappedBodyCache(body)(true, routingModel)
	}
	requestPlatform := h.backend.Platform(apiKey)
	sessionHash := h.backend.SessionHash(c, OpenAISelectionSession, body)
	textflow.RunInputTokens(h.backend.InputTokensExecution(c, InputTokensCall{Key: apiKey, Body: body, Model: reqModel, RoutingModel: routingModel, Platform: requestPlatform, SessionHash: sessionHash, Log: reqLog}), h.options.MaxSwitches)
}
