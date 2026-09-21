package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	gatewaylive "github.com/TokenFlux/TokenRouter/internal/gateway/live"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Live 和 LiveSideband 保留旧聚合入口，HTTP 实现由 gateway/httpapi 唯一持有。
func (h *OpenAIGatewayHandler) Live(c *gin.Context) {
	gatewayhttp.NewLiveHandler(legacyLiveHTTP{h}).Live(c)
}
func (h *OpenAIGatewayHandler) LiveSideband(c *gin.Context) {
	gatewayhttp.NewLiveHandler(legacyLiveHTTP{h}).LiveSideband(c)
}
func parseLiveCallRequest(c *gin.Context) (*session.LiveCallRequest, error) {
	return gatewayhttp.ParseLiveCallRequest(c)
}
func liveSidebandLocation(fullPath, callID string) string {
	return gatewayhttp.LiveSidebandLocation(fullPath, callID)
}
func liveEnabledForAPIKey(key *apikey.APIKey) bool {
	return key != nil && key.Group != nil && key.Group.Platform == capability.PlatformOpenAI && key.Group.AllowLive
}

// NewLiveHTTPHandler 供 server 路由直接绑定，旧聚合 handler 仅提供过渡依赖投影。
func (h *OpenAIGatewayHandler) NewLiveHTTPHandler() *gatewayhttp.LiveHandler {
	return gatewayhttp.NewLiveHandler(legacyLiveHTTP{h})
}

// legacyLiveHTTP 不保存请求上下文，也不持有额外缓存或后台状态。
type legacyLiveHTTP struct{ handler *OpenAIGatewayHandler }

func (a legacyLiveHTTP) APIKey(c *gin.Context) (*gatewayhttp.LiveAPIKey, bool) {
	key, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		return nil, false
	}
	result := &gatewayhttp.LiveAPIKey{ID: key.ID, UserID: key.UserID, TeamID: key.TeamID, GroupID: key.GroupID, ModelMapping: apikey.CloneModelMapping(key.ModelMapping)}
	if key.Group != nil {
		result.Group = &gatewayhttp.LiveGroup{Platform: key.Group.Platform, AllowLive: key.Group.AllowLive}
	}
	return result, true
}
func (a legacyLiveHTTP) Subject(c *gin.Context) (gatewayhttp.LiveSubject, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	return gatewayhttp.LiveSubject{UserID: subject.UserID, Concurrency: subject.Concurrency}, ok
}
func (a legacyLiveHTTP) Subscription(c *gin.Context) (*gatewayhttp.LiveSubscription, bool) {
	sub, ok := middleware2.GetSubscriptionFromContext(c)
	if sub == nil {
		return nil, ok
	}
	return &gatewayhttp.LiveSubscription{ID: sub.ID}, ok
}
func (a legacyLiveHTTP) Redirect(ctx context.Context, key *gatewayhttp.LiveAPIKey, model string) (context.Context, string) {
	// 兼容函数只读取模型映射，不需要填入用户、资金或分组对象。
	return apiKeyModelRedirectContext(ctx, &apikey.APIKey{ID: key.ID, ModelMapping: key.ModelMapping}, model)
}
func (a legacyLiveHTTP) Moderate(c *gin.Context, key *gatewayhttp.LiveAPIKey, subject gatewayhttp.LiveSubject, clientModel, model string, body []byte) bool {
	gatewayhttp.SetOpsRequestContext(c, clientModel, false)
	gatewayhttp.SetOpsEndpointContext(c, "", int16(usage.RequestTypeLive))
	log := gatewayhttp.RequestLogger(c, "handler.openai_gateway.live", zap.Int64("user_id", subject.UserID), zap.Int64("api_key_id", key.ID), zap.Any("group_id", key.GroupID))
	actualKey, _ := middleware2.GetAPIKeyFromContext(c)
	actualSubject, _ := middleware2.GetAuthSubjectFromContext(c)
	gatewayhttp.SetOpenAICyberWarningRequestSnapshot(c, moderation.ContentModerationProtocolOpenAIResponses, body)
	decision := a.handler.checkContentModeration(c, log, actualKey, actualSubject, moderation.ContentModerationProtocolOpenAIResponses, model, body)
	if decision != nil && decision.Blocked {
		a.handler.errorResponse(c, gatewayhttp.ContentModerationStatus(decision), gatewayhttp.ContentModerationErrorCode(decision), decision.Message)
		return false
	}
	return true
}
func (a legacyLiveHTTP) CheckBilling(c *gin.Context) bool {
	h := a.handler
	if h.billingCacheService == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Billing service unavailable")
		return false
	}
	key, _ := middleware2.GetAPIKeyFromContext(c)
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	if err := h.billingCacheService.CheckKey(c.Request.Context(), key, subscription, service.QuotaPlatform(c.Request.Context(), key), false); err != nil {
		status, code, message, retry := gatewayhttp.BillingErrorDetails(err)
		if retry > 0 {
			c.Header("Retry-After", strconv.Itoa(retry))
		}
		h.errorResponse(c, status, code, message)
		return false
	}
	return true
}
func (a legacyLiveHTTP) TryAcquireUserSlot(ctx context.Context, userID int64, limit int) (func(), bool, error) {
	return a.handler.concurrencyHelper.TryAcquireUserSlot(ctx, userID, limit)
}
func (a legacyLiveHTTP) InboundEndpoint(c *gin.Context) string {
	return gatewayhttp.GetInboundEndpoint(c)
}
func (a legacyLiveHTTP) Create(ctx context.Context, request *session.LiveCallRequest, identity session.LiveCallIdentity, limit int) (*gatewaylive.Created, error) {
	created, err := a.handler.gatewayService.CreateLiveCall(ctx, request, identity, limit)
	if err != nil {
		return nil, err
	}
	result := &gatewaylive.Created{SDP: created.SDP, CallID: created.CallID, Location: created.Location}
	if created.Account != nil {
		result.AccountID = created.Account.ID
	}
	return result, nil
}
func (a legacyLiveHTTP) Lookup(ctx context.Context, id string, identity session.LiveCallIdentity) (*session.LiveCallRecord, error) {
	return a.handler.gatewayService.GetLiveCallForIdentity(ctx, id, identity)
}
func (a legacyLiveHTTP) Proxy(ctx context.Context, record *session.LiveCallRecord, conn *coderws.Conn) error {
	return a.handler.gatewayService.ProxyLiveSideband(ctx, record, conn)
}
func (a legacyLiveHTTP) Error(c *gin.Context, status int, code, message string) {
	a.handler.errorResponse(c, status, code, message)
}
func (a legacyLiveHTTP) PolicyDenied(c *gin.Context) {
	gatewayhttp.MarkOpsClientBusinessLimited(c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
}
func (a legacyLiveHTTP) UpstreamStatus(err error) int {
	var upstream *forwardcore.UpstreamFailoverError
	if errors.As(err, &upstream) {
		return upstream.StatusCode
	}
	return 0
}

// writeLiveCreateError 保留原测试及过渡入口，错误映射只有一份。
func (h *OpenAIGatewayHandler) writeLiveCreateError(c *gin.Context, err error) {
	gatewayhttp.NewLiveHandler(legacyLiveHTTP{h}).WriteLiveCreateError(c, err)
}
