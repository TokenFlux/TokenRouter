package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/pkg/ip"
	middleware2 "github.com/BrandonVee/TokenRouter/internal/server/middleware"
	"github.com/BrandonVee/TokenRouter/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

// Live 创建 ChatGPT Frameless Live 会话并返回 SDP 应答。
func (h *OpenAIGatewayHandler) Live(c *gin.Context) {
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
	if apiKey.Group == nil || apiKey.Group.Platform != service.PlatformOpenAI {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Live is not supported for this platform")
		return
	}
	if !liveEnabledForAPIKey(apiKey) {
		h.errorResponse(c, http.StatusForbidden, "permission_error", "Live is not enabled for this group")
		return
	}
	request, err := parseLiveCallRequest(c)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	clientModel := strings.TrimSpace(gjson.GetBytes(request.Session, "model").String())
	redirectCtx, model := apiKeyModelRedirectContext(c.Request.Context(), apiKey, clientModel)
	if model != clientModel {
		request.Session, err = sjson.SetBytes(request.Session, "model", model)
		if err != nil {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "session.model is invalid")
			return
		}
	}
	request.Session, err = service.RewriteAPIKeyAdditionalModels(request.Session, apiKey.ModelMapping)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "session.tools contains an invalid model")
		return
	}
	c.Request = c.Request.WithContext(redirectCtx)
	setOpsRequestContext(c, clientModel, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeLive))
	reqLog := requestLogger(
		c,
		"handler.openai_gateway.live",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)
	// fork 未保留上游统一安全审计模块，Live 请求继续走现有内容审核链路。
	setOpenAICyberWarningRequestSnapshot(c, service.ContentModerationProtocolOpenAIResponses, request.Session)
	if decision := h.checkContentModeration(
		c,
		reqLog,
		apiKey,
		subject,
		service.ContentModerationProtocolOpenAIResponses,
		model,
		request.Session,
	); decision != nil && decision.Blocked {
		h.errorResponse(c, contentModerationStatus(decision), contentModerationErrorCode(decision), decision.Message)
		return
	}

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	if h.billingCacheService == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Billing service unavailable")
		return
	}
	if err := h.billingCacheService.CheckBillingEligibility(
		c.Request.Context(),
		apiKey.User,
		apiKey,
		apiKey.Group,
		subscription,
		service.QuotaPlatform(c.Request.Context(), apiKey),
	); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}

	userRelease, acquired, err := h.concurrencyHelper.TryAcquireUserSlot(
		c.Request.Context(),
		subject.UserID,
		subject.Concurrency,
	)
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Live concurrency unavailable")
		return
	}
	if !acquired {
		h.errorResponse(c, http.StatusTooManyRequests, "rate_limit_error", "Live concurrency limit reached")
		return
	}
	defer userRelease()

	identity := liveCallIdentity(c, apiKey, subject.UserID, subscription)
	created, err := h.gatewayService.CreateLiveCall(c.Request.Context(), request, identity, subject.Concurrency)
	if err != nil {
		h.writeLiveCreateError(c, err)
		return
	}
	c.Header("Location", liveSidebandLocation(c.FullPath(), created.CallID))
	c.Data(http.StatusOK, "application/sdp", created.SDP)
}

func parseLiveCallRequest(c *gin.Context) (*service.LiveCallRequest, error) {
	contentType := strings.ToLower(c.GetHeader("Content-Type"))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		sdp := c.PostForm("sdp")
		session := json.RawMessage(c.PostForm("session"))
		request := &service.LiveCallRequest{SDP: sdp, Session: session}
		if err := service.ValidateLiveCallRequest(request); err != nil {
			return nil, err
		}
		return request, nil
	}
	var request service.LiveCallRequest
	decoder := json.NewDecoder(c.Request.Body)
	if err := decoder.Decode(&request); err != nil {
		return nil, errors.New("request body must be valid JSON")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("request body must contain one JSON object")
	}
	if err := service.ValidateLiveCallRequest(&request); err != nil {
		return nil, err
	}
	return &request, nil
}

func liveSidebandLocation(fullPath, callID string) string {
	prefix := "/v1/live/"
	if strings.HasPrefix(fullPath, "/backend-api/codex/") {
		prefix = "/backend-api/codex/"
	}
	return prefix + url.PathEscape(callID)
}

func liveCallIdentity(
	c *gin.Context,
	apiKey *service.APIKey,
	userID int64,
	subscription *service.UserSubscription,
) service.LiveCallIdentity {
	var subscriptionID *int64
	if subscription != nil {
		value := subscription.ID
		subscriptionID = &value
	}
	return service.LiveCallIdentity{
		APIKeyID:        apiKey.ID,
		ActorUserID:     apiKey.UserID,
		UserID:          userID,
		TeamID:          apiKey.TeamID,
		GroupID:         apiKey.GroupID,
		SubscriptionID:  subscriptionID,
		UserAgent:       c.GetHeader("User-Agent"),
		Originator:      c.GetHeader("originator"),
		IPAddress:       ip.GetClientIP(c),
		InboundEndpoint: GetInboundEndpoint(c),
		ModelMapping:    service.CloneModelMapping(apiKey.ModelMapping),
	}
}

func (h *OpenAIGatewayHandler) writeLiveCreateError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrLiveConcurrencyFull):
		h.errorResponse(c, http.StatusTooManyRequests, "rate_limit_error", "Live concurrency limit reached")
	case errors.Is(err, service.ErrLiveClientPolicyDenied):
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
		h.errorResponse(c, http.StatusForbidden, "permission_error", "Live client is not allowed by the available account policy")
	case errors.Is(err, service.ErrLiveUnavailable):
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Live is unavailable")
	default:
		var attestationErr *service.LiveAttestationUnavailableError
		if errors.As(err, &attestationErr) {
			h.errorResponse(c, http.StatusServiceUnavailable, "api_error", attestationErr.Error())
			return
		}
		var upstreamErr *service.UpstreamFailoverError
		if errors.As(err, &upstreamErr) && upstreamErr.StatusCode >= 400 && upstreamErr.StatusCode < 500 {
			h.errorResponse(c, upstreamErr.StatusCode, "invalid_request_error", "Live upstream rejected the request")
			return
		}
		h.errorResponse(c, http.StatusBadGateway, "api_error", "Live upstream request failed")
	}
}

// LiveSideband 为已认证调用方代理 Live 控制 WebSocket。
func (h *OpenAIGatewayHandler) LiveSideband(c *gin.Context) {
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
	if !liveEnabledForAPIKey(apiKey) {
		h.errorResponse(c, http.StatusForbidden, "permission_error", "Live is not enabled for this group")
		return
	}
	identity := service.LiveCallIdentity{
		APIKeyID:    apiKey.ID,
		ActorUserID: apiKey.UserID,
		UserID:      subject.UserID,
		TeamID:      apiKey.TeamID,
		GroupID:     apiKey.GroupID,
	}
	record, err := h.gatewayService.GetLiveCallForIdentity(c.Request.Context(), c.Param("call_id"), identity)
	if err != nil {
		if errors.Is(err, service.ErrLiveIdentityMismatch) {
			h.errorResponse(c, http.StatusForbidden, "permission_error", "Live call belongs to another identity")
			return
		}
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Live call not found")
		return
	}
	downstream, err := coderws.Accept(c.Writer, c.Request, &coderws.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer func() { _ = downstream.CloseNow() }()
	if err := h.gatewayService.ProxyLiveSideband(c.Request.Context(), record, downstream); err != nil {
		_ = downstream.Close(coderws.StatusInternalError, "live sideband closed")
		return
	}
	_ = downstream.Close(coderws.StatusNormalClosure, "")
}

func liveEnabledForAPIKey(apiKey *service.APIKey) bool {
	return apiKey != nil &&
		apiKey.Group != nil &&
		apiKey.Group.Platform == service.PlatformOpenAI &&
		apiKey.Group.AllowLive
}
