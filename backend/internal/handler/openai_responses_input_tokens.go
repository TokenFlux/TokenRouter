package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// ResponsesInputTokens 处理 Codex 使用的 OpenAI 原生 Responses 输入 token 预检。
// 该请求只做计数，不占用用户并发槽位，也不记录用量。
func (h *OpenAIGatewayHandler) ResponsesInputTokens(c *gin.Context) {
	requestStart := time.Now()
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(c, "handler.openai_gateway.responses_input_tokens",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID))
	if apiKey.Group != nil && !apiKey.Group.AllowsClientProtocol(service.ProtocolOpenAIResponses) {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
		h.errorResponse(c, http.StatusForbidden, "permission_error", "This group does not allow OpenAI Responses requests")
		return
	}
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}

	body, err := readLenientJSONRequestBodyWithPrealloc(c.Request, h.cfg)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
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
	setOpsRequestContext(c, reqModel, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(false, false)))

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		reqLog.Info("openai_responses_input_tokens.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())

	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, reqModel)
	routingModel := reqModel
	if strings.TrimSpace(channelMapping.MappedModel) != "" {
		routingModel = strings.TrimSpace(channelMapping.MappedModel)
		body = h.gatewayService.ReplaceModelInBody(body, routingModel)
	}
	requestPlatform := openAICompatibleRequestPlatform(apiKey)
	sessionHash := h.gatewayService.GenerateSessionHash(c, body)
	failedAccountIDs := make(map[int64]struct{})
	sameAccountRetryCount := make(map[int64]int)
	var lastFailoverErr *service.UpstreamFailoverError
	switchCount := 0

	for {
		routingStart := time.Now()
		selection, _, selectErr := h.gatewayService.SelectAccountWithSchedulerForCapabilityAndRoutingModel(
			c.Request.Context(), apiKey.GroupID, "", sessionHash, reqModel, routingModel,
			failedAccountIDs, service.OpenAIUpstreamTransportAny,
			service.OpenAIEndpointCapabilityTextGeneration, false, false, requestPlatform,
		)
		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
		if selectErr != nil {
			reqLog.Warn("openai_responses_input_tokens.account_select_failed", zap.Error(selectErr))
			if lastFailoverErr != nil {
				h.handleFailoverExhausted(c, lastFailoverErr, false)
				return
			}
			if h.handleOpenAISelectionBusinessError(c, selectErr, false) {
				return
			}
			cls := classifyOpenAICompatibleResolvedRoutingNoAccountErrorFromGin(c, h.gatewayService, apiKey, routingModel, reqModel)
			if !cls.ModelNotFound {
				markOpsRoutingCapacityLimitedIfNoAvailable(c, selectErr)
			}
			h.errorResponse(c, cls.Status, cls.ErrType, cls.Message)
			return
		}
		if selection == nil || selection.Account == nil {
			if lastFailoverErr != nil {
				h.handleFailoverExhausted(c, lastFailoverErr, false)
				return
			}
			markOpsRoutingCapacityLimited(c)
			h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No available accounts")
			return
		}

		account := selection.Account
		setOpsSelectedAccount(c, account.ID, account.Platform)
		forwardStart := time.Now()
		forwardErr := func() error {
			if selection.Acquired && selection.ReleaseFunc != nil {
				defer selection.ReleaseFunc()
			}
			return h.gatewayService.ForwardResponsesInputTokens(c.Request.Context(), c, account, body)
		}()
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, time.Since(forwardStart).Milliseconds())
		if forwardErr == nil {
			return
		}
		var failoverErr *service.UpstreamFailoverError
		if !errors.As(forwardErr, &failoverErr) {
			reqLog.Error("openai_responses_input_tokens.forward_failed", zap.Int64("account_id", account.ID), zap.Error(forwardErr))
			return
		}
		if !failoverErr.ShouldRetryNextAccount() {
			h.handleFailoverExhausted(c, failoverErr, false)
			return
		}
		if failoverErr.RetryableOnSameAccount && sameAccountRetryCount[account.ID] < account.GetPoolModeRetryCount() {
			sameAccountRetryCount[account.ID]++
			select {
			case <-c.Request.Context().Done():
				return
			case <-time.After(sameAccountRetryDelay):
			}
			continue
		}
		failedAccountIDs[account.ID] = struct{}{}
		lastFailoverErr = failoverErr
		if switchCount >= h.maxAccountSwitches {
			h.handleFailoverExhausted(c, failoverErr, false)
			return
		}
		switchCount++
	}
}
