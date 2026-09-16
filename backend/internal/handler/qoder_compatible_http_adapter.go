// 固定 HTTP 依赖投影与单请求完成快照，独立于核心的尝试循环。
package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ip"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	middleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type qoderCompatibleHTTPBackend struct{ h *QoderGatewayHandler }

func (h *QoderGatewayHandler) NewCompatibleHTTPHandler() *gatewayhttp.QoderCompatibleHandler {
	return gatewayhttp.NewQoderCompatibleHandler(qoderCompatibleHTTPBackend{h}, h.concurrencyHelper, h.maxAccountSwitches)
}
func (p qoderCompatibleHTTPBackend) Enter() (func(), error) {
	if p.h.enter != nil {
		return p.h.enter()
	}
	return func() {}, nil
}
func (p qoderCompatibleHTTPBackend) Access(c *gin.Context) (*apikey.APIKey, bool) {
	if key, ok := gatewayhttp.EffectiveAPIKey(c); ok {
		return key, true
	}
	key, ok := middleware.GetAPIKeyFromContext(c)
	return service.APIKeyView(key), ok
}
func (p qoderCompatibleHTTPBackend) BindErrors(c *gin.Context) {
	if p.h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, p.h.errorPassthroughService)
	}
}
func (p qoderCompatibleHTTPBackend) PrepareClient(c *gin.Context, body []byte, endpoint gatewayhttp.QoderEndpoint) {
	prepareQoderRequestContext(c, body, qoderEndpoint(endpoint))
}
func (p qoderCompatibleHTTPBackend) ObserveRequest(c *gin.Context, model string, stream bool) {
	setOpsRequestContext(c, model, stream)
}
func (p qoderCompatibleHTTPBackend) ObserveEndpoint(c *gin.Context, stream bool) {
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(stream, false)))
}
func (p qoderCompatibleHTTPBackend) ObserveAuthLatency(c *gin.Context, d time.Duration) {
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, d.Milliseconds())
}
func (p qoderCompatibleHTTPBackend) Plan(c *gin.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	if p.h.gatewayService == nil {
		return routing.RoutePlan{}
	}
	old := service.APIKeyFromView(key)
	plan := p.h.gatewayService.PlanRoute(c.Request.Context(), service.APIKeyRouteGroup(old), old.GroupID, model)
	c.Request = c.Request.WithContext(service.WithRoutePlan(c.Request.Context(), plan))
	return plan
}
func (p qoderCompatibleHTTPBackend) AttemptBody(body []byte, plan routing.RoutePlan) []byte {
	mapping := service.ChannelMappingFromRoutePlan(plan)
	if p.h.gatewayService != nil && mapping.Mapped {
		return p.h.gatewayService.ReplaceModelInBody(body, mapping.MappedModel)
	}
	return body
}
func (p qoderCompatibleHTTPBackend) Eligibility(ctx context.Context, key *apikey.APIKey, sub *billing.UserSubscription) error {
	if p.h.billingCacheService == nil {
		return nil
	}
	old := service.APIKeyFromView(key)
	return p.h.billingCacheService.CheckBillingEligibility(ctx, old.User, old, old.Group, sub, service.QuotaPlatform(ctx, old))
}
func (p qoderCompatibleHTTPBackend) SessionHash(c *gin.Context, endpoint gatewayhttp.QoderEndpoint, body []byte, id int64) string {
	return p.h.qoderSessionHash(c, qoderEndpoint(endpoint), body, id)
}
func (p qoderCompatibleHTTPBackend) Error(c *gin.Context, status int, kind, message string, endpoint gatewayhttp.QoderEndpoint) {
	p.h.errorResponse(c, status, kind, message, qoderEndpoint(endpoint))
}
func (p qoderCompatibleHTTPBackend) ConcurrencyError(c *gin.Context, err error, kind string, started bool, endpoint gatewayhttp.QoderEndpoint) {
	p.h.handleConcurrencyError(c, err, kind, started, qoderEndpoint(endpoint))
}
func (p qoderCompatibleHTTPBackend) Execution(c *gin.Context, call gatewayhttp.QoderCompatibleCall) textflow.QoderCompatiblePorts {
	h := p.h
	apiKey := service.APIKeyFromView(call.Key)
	subscription := call.Subscription
	body := call.Body
	reqModel := call.Model
	reqLog := call.Log
	endpoint := qoderEndpoint(call.Endpoint)
	channelMapping := service.ChannelMappingFromRoutePlan(call.Plan)

	recordUsage := func(account *service.Account, result *service.ForwardResult) {
		userAgent := c.GetHeader("User-Agent")
		clientIP := ip.GetClientIP(c)
		requestPayloadHash := service.HashUsageRequestPayload(body)
		inboundEndpoint := GetInboundEndpoint(c)
		upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
		quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
		// 入队前固化资金与报文投影，worker 不再读取请求中的实体。
		completionInput := service.CompletionForwardInput(usageRecordContextFromGin(c), &service.RecordUsageInput{
			Result:             result,
			QuotaPlatform:      quotaPlatform,
			APIKey:             apiKey,
			User:               apiKey.User,
			Account:            account,
			Subscription:       subscription,
			InboundEndpoint:    inboundEndpoint,
			UpstreamEndpoint:   upstreamEndpoint,
			UserAgent:          userAgent,
			IPAddress:          clientIP,
			RequestPayloadHash: requestPayloadHash,
			RequestBody:        append([]byte(nil), body...),
			APIKeyService:      h.apiKeyService,
			ChannelUsageFields: channelMapping.ToUsageFields(reqModel, result.UpstreamModel),
		})
		completionRuntime := h.completionRuntime()
		h.submitUsageRecordTask(c, func(ctx context.Context) {
			if err := completionRuntime.Record(ctx, completionInput, false); err != nil {
				reqLog.Error("qoder.record_usage_failed", zap.Int64("account_id", completionInput.Account.ID), zap.Error(err))
			}
		})
	}

	// finishPartial 不把服务失败当成成功，也不允许已有服务的请求进入下一次推理。
	finishPartial := func(account *service.Account, result *service.ForwardResult, forwardErr error) bool {
		if forwardErr == nil || result == nil {
			return false
		}
		recordUsage(account, result)
		if qoderRequestCanceled(c.Request.Context(), forwardErr) {
			return true
		}
		status, kind, message, ok := h.qoderGatewayErrorDetails(c, forwardErr)
		if !ok {
			status = http.StatusBadGateway
			kind = "upstream_error"
			message = "Upstream request failed"
		}
		service.SetOpsUpstreamError(c, upstreamStatusFromError(forwardErr), message, "")
		h.streamingAwareError(c, status, kind, message, true, endpoint)
		return true
	}
	return &qoderCompatibleAttemptBridge{h: h, c: c, endpoint: endpoint, key: apiKey, subjectID: call.Subject.UserID, hash: call.SessionHash, model: reqModel, stream: call.Stream, streamStarted: call.StreamStarted, body: call.AttemptBody, log: reqLog, record: recordUsage, partial: finishPartial}
}
