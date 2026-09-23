// 固定 HTTP 依赖投影与单请求完成快照，独立于核心的尝试循环。
package httpapi

import (
	"context"
	"net/http"
	"time"

	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	admission "github.com/TokenFlux/TokenRouter/internal/gateway/admission"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	usage "github.com/TokenFlux/TokenRouter/internal/usage"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (h *QoderCompatibleRuntime) Enter() (func(), error) {
	if h.options.Enter != nil {
		return h.options.Enter()
	}
	return func() {}, nil
}
func (h *QoderCompatibleRuntime) Access(c *gin.Context) (*apikey.APIKey, bool) {
	if key, ok := EffectiveAPIKey(c); ok {
		return key, true
	}
	key, ok := h.options.ReadAccess(c)
	return apikey.CopyAPIKey(key), ok
}
func (h *QoderCompatibleRuntime) BindErrors(c *gin.Context) {
	if h.options.Rules != nil {
		BindErrorPassthroughService(c, h.options.Rules)
	}
}
func (h *QoderCompatibleRuntime) PrepareClient(c *gin.Context, body []byte, endpoint QoderEndpoint) {
	prepareQoderRequestContext(c, body, QoderEndpoint(endpoint))
}
func (h *QoderCompatibleRuntime) ObserveRequest(c *gin.Context, model string, stream bool) {
	SetOpsRequestContext(c, model, stream)
}
func (h *QoderCompatibleRuntime) ObserveEndpoint(c *gin.Context, stream bool) {
	SetOpsEndpointContext(c, "", int16(usage.RequestTypeFromLegacy(stream, false)))
}
func (h *QoderCompatibleRuntime) ObserveAuthLatency(c *gin.Context, d time.Duration) {
	SetOpsLatencyMs(c, OpsAuthLatencyMsKey, d.Milliseconds())
}
func (h *QoderCompatibleRuntime) Plan(c *gin.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	if h.options.Execution == nil {
		return routing.RoutePlan{}
	}
	old := apikey.CopyAPIKey(key)
	plan := h.options.Execution.Plan(c.Request.Context(), old, model)
	c.Request = c.Request.WithContext(requeststate.WithRoutePlan(c.Request.Context(), plan))
	return plan
}
func (h *QoderCompatibleRuntime) AttemptBody(body []byte, plan routing.RoutePlan) []byte {
	mapping := plan.Mapping()
	if h.options.Execution != nil && mapping.Mapped {
		return openaiprotocol.ReplaceModelInBody(body, mapping.MappedModel)
	}
	return body
}
func (h *QoderCompatibleRuntime) Eligibility(ctx context.Context, key *apikey.APIKey, sub *billing.UserSubscription) error {
	if h.options.Funding == nil {
		return nil
	}
	old := apikey.CopyAPIKey(key)
	return h.options.Funding.CheckKey(ctx, old, sub, admission.QuotaPlatform(ctx, old), false)
}
func (h *QoderCompatibleRuntime) SessionHash(c *gin.Context, endpoint QoderEndpoint, body []byte, id int64) string {
	return h.qoderSessionHash(c, QoderEndpoint(endpoint), body, id)
}
func (h *QoderCompatibleRuntime) Error(c *gin.Context, status int, kind, message string, endpoint QoderEndpoint) {
	h.errorResponse(c, status, kind, message, QoderEndpoint(endpoint))
}
func (h *QoderCompatibleRuntime) ConcurrencyError(c *gin.Context, err error, kind string, started bool, endpoint QoderEndpoint) {
	h.handleConcurrencyError(c, err, kind, started, QoderEndpoint(endpoint))
}
func (h *QoderCompatibleRuntime) Execution(c *gin.Context, call QoderCompatibleCall) textflow.QoderCompatiblePorts {
	apiKey := apikey.CopyAPIKey(call.Key)
	subscription := call.Subscription
	body := call.Body
	reqModel := call.Model
	reqLog := call.Log
	endpoint := QoderEndpoint(call.Endpoint)
	channelMapping := call.Plan.Mapping()

	recordUsage := func(account QoderCompatibleTarget, result *forwardcore.MessagesResult) {
		userAgent := c.GetHeader("User-Agent")
		clientIP := clientip.GetClientIP(c)
		requestPayloadHash := billing.HashUsageRequestPayload(body)
		inboundEndpoint := GetInboundEndpoint(c)
		upstreamEndpoint := GetUpstreamEndpoint(c, account.Snapshot().Platform)
		quotaPlatform := admission.QuotaPlatform(c.Request.Context(), apiKey)
		// 入队前固化资金与报文投影，worker 不再读取请求中的实体。
		completionInput := account.Completion(CompletionContext(c), QoderCompletionCapture{
			Result: result, QuotaPlatform: quotaPlatform, Key: apiKey, Subscription: subscription,
			InboundEndpoint: inboundEndpoint, UpstreamEndpoint: upstreamEndpoint, UserAgent: userAgent, ClientIP: clientIP,
			PayloadHash: requestPayloadHash, Body: append([]byte(nil), body...), Channel: channelMapping.ToUsageFields(reqModel, result.UpstreamModel),
		})
		completionRuntime := h.options.Recorder
		h.submitUsageRecordTask(c, func(ctx context.Context) {
			if err := completionRuntime.Record(ctx, completionInput, false); err != nil {
				reqLog.Error("qoder.record_usage_failed", zap.Int64("account_id", completionInput.Account.ID), zap.Error(err))
			}
		})
	}

	// finishPartial 不把服务失败当成成功，也不允许已有服务的请求进入下一次推理。
	finishPartial := func(account QoderCompatibleTarget, result *forwardcore.MessagesResult, forwardErr error) bool {
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
		SetOpsUpstreamError(c, h.options.Errors.Describe(forwardErr).SourceStatus, message, "")
		h.streamingAwareError(c, status, kind, message, true, endpoint)
		return true
	}
	return &qoderCompatibleAttemptBridge{h: h, c: c, endpoint: endpoint, key: apiKey, subjectID: call.Subject.UserID, hash: call.SessionHash, model: reqModel, stream: call.Stream, streamStarted: call.StreamStarted, body: call.AttemptBody, log: reqLog, record: recordUsage, partial: finishPartial}
}
