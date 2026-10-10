package mediaentry

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/systemone"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// systemOneRun 保存当前请求的提供商选择和完成快照来源。
type systemOneRun struct {
	h            *Runtime
	c            *gin.Context
	input        gatewayhttp.AuxiliaryHTTPInput
	key          *apikey.APIKey
	subscription *billing.UserSubscription
	log          *zap.Logger
	stream       *bool
	selection    *gatewayprovider.SelectionResult
}

// NewSystemOne 固定本次请求的身份、模型和分组信息。
func (p mediaHTTPAdapter) NewSystemOne(c *gin.Context, input gatewayhttp.AuxiliaryHTTPInput, log *zap.Logger, stream *bool) gatewayhttp.SystemOneHTTPExecution {
	key, _ := keyhttp.GetAPIKeyFromContext(c)
	subscription, _ := gatewayhttp.SubscriptionFromContext(c)
	return &systemOneRun{h: p.h, c: c, input: input, key: key, subscription: subscription, log: log, stream: stream}
}

// ModerateSystemOne 将结构化决策内容交给文本审核。
func (p mediaHTTPAdapter) ModerateSystemOne(c *gin.Context, log *zap.Logger, model string, body []byte) bool {
	key, _ := keyhttp.GetAPIKeyFromContext(c)
	subject, _ := authctx.GetAuthSubjectFromContext(c)
	decision := p.h.checkContentModeration(c, log, key, subject, moderation.ContentModerationProtocolOpenAIChat, model, body)
	if decision == nil || !decision.Blocked {
		return false
	}
	gatewayhttp.WriteSystemOneError(c, gatewayhttp.ContentModerationStatus(decision), gatewayhttp.ContentModerationErrorCode(decision), "", "", decision.Message)
	return true
}

// Select 使用决策协议和 Jev 平台筛选当前分组的候选。
func (p *systemOneRun) Select(ctx context.Context, excluded map[int64]struct{}) (provider.ProviderSnapshot, error) {
	selection, _, err := p.h.bindings.Common.Selection.SelectProviderWithSchedulerForCapability(ctx, p.key.GroupID, "", "", p.input.Model, excluded, egress.OpenAIUpstreamTransportHTTPSSE, "", false, false, provider.PlatformJev)
	p.selection = selection
	if selection == nil || selection.Provider == nil {
		return provider.ProviderSnapshot{}, err
	}
	value := gatewayprovider.ExecutionSnapshot(selection.Provider)
	gatewayhttp.SetOpsSelectedProvider(p.c, value.ID, value.Platform)
	return value, err
}

// Acquire 复用调度取得的槽位，或按调度计划等待槽位。
func (p *systemOneRun) Acquire(_ context.Context) (func(), bool) {
	return p.h.bindings.Common.Support.AcquireResponsesProviderSlot(p.c, p.key.GroupID, "", p.selection, false, p.stream, p.log)
}

// Forward 复查资金并调用当前提供商。
func (p *systemOneRun) Forward(ctx context.Context) systemone.Outcome {
	// 提供商等待期间资金可能变化，调用上游前再次检查。
	if failure := (mediaHTTPAdapter{p.h}).checkFunding(p.c, true); failure != nil {
		gatewayhttp.WriteSystemOneError(p.c, failure.Status, failure.Code, "", "", failure.Message)
		return systemone.Outcome{Err: failure.Err}
	}
	body := p.input.Body
	if p.input.Mapping.Mapped {
		body = p.h.bindings.Common.Forward.ReplaceModelInBody(body, p.input.Mapping.MappedModel)
	}
	result, err := p.h.bindings.Platform.SystemOne(ctx, p.c, p.selection.Provider, body)
	_, retry := errors.AsType[*forward.UpstreamFailoverError](err)
	return systemone.Outcome{Result: result, Err: err, Retry: retry}
}

// Report 将上游完成状态和最终模型反馈给调度器。
func (p *systemOneRun) Report(ctx context.Context, outcome systemone.Outcome) {
	if ctx.Err() != nil && !outcome.Result.Served {
		return
	}
	// 已观测到答案的客户端写入失败仍算上游完成。
	if p.c.Writer.Written() && !outcome.Result.Served && !outcome.Retry {
		return
	}
	model := outcome.Result.UpstreamModel
	if model == "" {
		model = p.input.Mapping.MappedModel
		if model == "" {
			model = p.input.Model
		}
		model = gatewayprovider.ExecutionModelPolicy(p.selection.Provider).Mapped(model)
	}
	p.h.bindings.Common.Selection.ReportOpenAIProviderScheduleResult(p.selection.Provider, model, outcome.Result.Served, nil, outcome.Err)
}

// Complete 捕获资金归属和用量，提交共享完成队列。
func (p *systemOneRun) Complete(ctx context.Context, result upstream.AttemptResult) {
	value := &forward.OpenAIResult{RequestID: result.RequestID, Model: p.input.Mapping.MappedModel, UpstreamModel: result.UpstreamModel, UpstreamResponseModel: result.UpstreamResponseModel, UpstreamHeaders: result.UpstreamHeaders, Usage: openai.ForwardUsage{InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens}, Duration: result.Duration}
	if value.Model == "" {
		value.Model = p.input.Model
	}
	input := gatewayprovider.CaptureOpenAI(ctx, &gatewayprovider.OpenAICapture{RequestPayloadHash: billing.HashUsageRequestPayload(p.input.Body), Result: value, APIKey: p.key, User: p.key.User, Provider: gatewayprovider.ExecutionCompletionRecord(p.selection.Provider), Subscription: p.subscription, InboundEndpoint: gatewayhttp.EndpointSystemOne, UpstreamEndpoint: gatewayhttp.EndpointSystemOne, UserAgent: p.c.GetHeader("User-Agent"), IPAddress: clientip.GetClientIP(p.c), APIKeyService: p.h.bindings.Quota, PricingUsageFields: p.input.Mapping.ToUsageFields(p.input.Model, result.UpstreamModel)})
	recorder, log := p.h.bindings.Common.Recorder, p.log
	p.h.submitOpenAIUsageRecordTask(p.c, value, func(completionContext context.Context) {
		if err := recorder.Record(completionContext, input, true); err != nil {
			log.Error("systemone.record_usage_failed", zap.Error(err))
		}
	})
}

// MissingUsage 记录用量告警，并把上游尝试关联到请求详情。
func (p *systemOneRun) MissingUsage(_ context.Context, result upstream.AttemptResult) {
	value := p.selection.Provider.Record
	p.log.Warn("systemone.usage_invalid", zap.Int64("provider_id", value.ID), zap.String("upstream_request_id", result.RequestID))
	gatewayhttp.AppendOpsUpstreamError(p.c, ops.OpsUpstreamErrorEvent{Platform: provider.PlatformJev, ProviderID: value.ID, ProviderName: value.Name, UpstreamStatusCode: http.StatusOK, UpstreamRequestID: result.RequestID, Kind: "usage_invalid", Message: "SystemOne usage is missing or invalid; billing skipped"})
}

// Switch 记录本次提供商切换。
func (p *systemOneRun) Switch() {
	p.h.bindings.Common.Selection.RecordOpenAIProviderSwitchForSelection(p.selection)
}

// End 输出尚未交付的最终错误。
func (p *systemOneRun) End(err error) {
	if err == nil || p.c.Writer.Written() || p.c.Request.Context().Err() != nil {
		return
	}
	if failure, ok := errors.AsType[*forward.UpstreamFailoverError](err); ok {
		if retry := http.Header(failure.ResponseHeaders).Get("Retry-After"); retry != "" {
			p.c.Header("Retry-After", retry)
		}
		p.h.bindings.Common.Support.HandleFailoverExhausted(p.c, failure, false)
		return
	}
	if p.h.bindings.Common.Support.HandleOpenAISelectionBusinessError(p.c, err, false) {
		return
	}
	status := http.StatusBadGateway
	message := "SystemOne upstream request failed"
	if p.selection == nil || p.selection.Provider == nil {
		status = http.StatusServiceUnavailable
		message = "No available SystemOne providers"
		gatewayhttp.MarkOpsRoutingCapacityLimited(p.c)
	}
	gatewayhttp.WriteSystemOneError(p.c, status, "upstream_error", "", "", message)
}
