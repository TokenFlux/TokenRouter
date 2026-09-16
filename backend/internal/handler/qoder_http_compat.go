package handler

import (
	"context"
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/gin-gonic/gin"
)

// QoderRequestSessionHash 保留旧入站会话识别，S11 统一请求元数据时退出。
func (h *QoderGatewayHandler) QoderRequestSessionHash(c *gin.Context, body []byte, keyID int64) string {
	return h.qoderSessionHash(c, qoderEndpointChatCompletions, body, keyID)
}

// QoderWaitObserver 只适配原等待心跳格式，不拥有等待循环。
func (h *QoderGatewayHandler) QoderWaitObserver(c *gin.Context, stream bool, started *bool) scheduler.WaitObserver {
	return gatewayWaitObserver(c, SSEPingFormatComment, defaultPingInterval, stream, started, true)
}

// BindQoderSuccess 保留成功专属绑定；失败部分结果不得调用。
func (h *QoderGatewayHandler) BindQoderSuccess(ctx context.Context, groupID *int64, hash string, accountID int64) {
	h.bindQoderStickySessions(ctx, groupID, hash, accountID, qoderEndpointChatCompletions, nil, nil)
}

// SubmitQoderCompletion 委托原唯一完成队列及上下文快照。
func (h *QoderGatewayHandler) SubmitQoderCompletion(c *gin.Context, task service.UsageRecordTask) {
	h.submitUsageRecordTask(c, task)
}

// ObserveQoderSelection 只提交原 HTTP 观测字段。
func ObserveQoderSelection(c *gin.Context, accountID int64, platform string) {
	setOpsSelectedAccount(c, accountID, platform)
}
func ObserveQoderRequest(c *gin.Context, model string, stream bool) {
	setOpsRequestContext(c, model, stream)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(stream, false)))
}
func QoderEndpoints(c *gin.Context, platform string) (string, string) {
	return GetInboundEndpoint(c), GetUpstreamEndpoint(c, platform)
}
func QoderMayFailover(err error) bool {
	var native *qoder.APIError
	return qoderShouldFailover(err) || !errors.As(err, &native)
}
func (h *QoderGatewayHandler) QoderMayRefresh(err error) bool {
	return h.shouldRefreshQoderAccount(err, false)
}

// QoderClientFailure 为新 HTTP 入口投影旧错误改写结果；S10/S11 删除旧规则绑定。
func (h *QoderGatewayHandler) QoderClientFailure(c *gin.Context, err error) *gatewayhttp.HTTPFailure {
	var direct *gatewayhttp.HTTPFailure
	if errors.As(err, &direct) {
		return direct
	}
	result := &gatewayhttp.HTTPFailure{Status: 502, Type: "upstream_error", Message: "Upstream request failed"}
	var failure *gateway.Failure
	if !errors.As(err, &failure) {
		return result
	}
	switch failure.Stage {
	case gateway.FailureBilling:
		result.Status, result.Type, result.Message, result.RetryAfter = billingErrorDetails(failure.Cause)
	case gateway.FailureUserQueue:
		result.Status = 429
		result.Type = "rate_limit_error"
		result.Message = "Too many pending requests, please retry later"
	case gateway.FailureUserSlot, gateway.FailureAccountSlot:
		if failure.Stage == gateway.FailureAccountSlot && failure.Cause != nil && failure.Cause.Error() == "no available accounts" {
			markOpsRoutingCapacityLimited(c)
			result.Status = 503
			result.Type = "api_error"
			result.Message = "No available accounts"
			return result
		}
		slot := "user"
		if failure.Stage == gateway.FailureAccountSlot {
			slot = "account"
		}
		result.Status, result.Type, _, result.Message = concurrencyErrorResponse(failure.Cause, slot)
	case gateway.FailureRefreshPending:
		result.Status = 503
		result.Message = "Qoder account refresh is still in progress, please retry shortly"
		result.RetryAfter = 1
	case gateway.FailureSelection:
		markOpsRoutingCapacityLimitedIfNoAvailable(c, failure.Cause)
		handled := handleGroupSelectionBusinessError(c, failure.Cause, c.Writer.Written(), func(status int, kind, message string, _ bool) {
			result.Status = status
			result.Type = kind
			result.Message = message
		})
		if !handled {
			result.Status = 503
			result.Type = "api_error"
			result.Message = "No available accounts: " + failure.Cause.Error()
		}
	case gateway.FailureUpstream, gateway.FailureExhausted:
		status, kind, message, ok := h.qoderGatewayErrorDetails(c, failure.Cause)
		if ok {
			result.Status = status
			result.Type = kind
			result.Message = message
			service.SetOpsUpstreamError(c, upstreamStatusFromError(failure.Cause), message, "")
		} else if failure.Stage == gateway.FailureExhausted {
			result.Message = "All available accounts exhausted"
		}
	}
	return result
}
