// 原生 Responses 输入 token 预检保留独立的准入与重试顺序。
package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

type InputTokensCall struct {
	Key                                        *apikey.APIKey
	Body                                       []byte
	Model, RoutingModel, Platform, SessionHash string
	Log                                        *zap.Logger
}

// ResponsesInputTokens 处理 Codex 使用的 OpenAI 原生 Responses 输入 token 预检。
// 该请求只做计数，不占用用户并发槽位，也不记录用量。
func (h *OpenAITokensHandler) ResponsesInputTokens(c *gin.Context) {
	done, accepted := h.beginRequest(c, "openai")
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
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
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

	// 当前分组和渠道结果进入独立计划，不改变原解析位置。
	channelMappingRoutePlan := h.backend.Plan(c.Request.Context(), apiKey, reqModel)
	channelMapping := channelMappingRoutePlan.Mapping()
	h.backend.BindPlan(c, channelMappingRoutePlan)
	routingModel := reqModel
	if strings.TrimSpace(channelMapping.MappedModel) != "" {
		routingModel = strings.TrimSpace(channelMapping.MappedModel)
		body = h.backend.MappedBodyCache(body)(true, routingModel)
	}
	requestPlatform := h.backend.Platform(apiKey)
	sessionHash := h.backend.SessionHash(c, OpenAISelectionSession, body)
	textflow.RunInputTokens(h.backend.InputTokensExecution(c, InputTokensCall{Key: apiKey, Body: body, Model: reqModel, RoutingModel: routingModel, Platform: requestPlatform, SessionHash: sessionHash, Log: reqLog}), h.options.MaxSwitches)
}
