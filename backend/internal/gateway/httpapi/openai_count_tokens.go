// OpenAI 计数保留资金校验与单次无槽选择，Grok 本地计数继续只依赖路由认证。
package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tokenestimate"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// OpenAICountCall 不携带账号凭据、Gin 或资金写入能力。
type OpenAICountCall struct {
	Key                                             *apikey.APIKey
	Model, AccountLayerModel, SessionHash, Platform string
	Mapping                                         routing.ChannelMappingResult
	MappedBody                                      func(bool, string) []byte
	Log                                             *zap.Logger
	StartedAt                                       time.Time
}

func (h *OpenAITokensHandler) GrokCountTokens(c *gin.Context) {
	done, accepted := h.beginRequest(c, "anthropic")
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
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
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
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
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

func (h *OpenAITokensHandler) CountTokens(c *gin.Context) {
	done, accepted := h.beginRequest(c, "anthropic")
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
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	if len(body) == 0 {
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	h.backend.ObserveRequest(c, "", false)

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

	// 当前分组和渠道结果进入独立计划，不改变原解析位置。
	channelMappingRoutePlan := h.backend.Plan(c.Request.Context(), apiKey, reqModel)
	channelMapping := channelMappingRoutePlan.Mapping()
	h.backend.BindPlan(c, channelMappingRoutePlan)
	channelMappedModel := channelMapping.MappedModel
	if channelMappedModel == "" {
		channelMappedModel = reqModel
	}
	accountLayerModel := h.backend.MessageAccountModel(c.Request.Context(), apiKey, channelMappedModel)
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
	// 无槽、利润门豁免仍由专用选择能力执行；此链只尝试一次且不提交用量。
	textflow.RunSingleCountTokens(h.backend.CountExecution(c, OpenAICountCall{
		Key: apiKey, Model: reqModel, AccountLayerModel: accountLayerModel, SessionHash: sessionHash,
		Platform: requestPlatform, Mapping: channelMapping, MappedBody: mappedBodyForMessages, Log: reqLog, StartedAt: requestStart,
	}))
}
