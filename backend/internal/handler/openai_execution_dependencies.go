package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/openaiattempt"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// 兼容构造仅绑定固定端口，生产 app 直接装配原生运行时。
func openAIAttemptBindings(h *OpenAIGatewayHandler) openaiattempt.Bindings {
	b := openaiattempt.Bindings{Support: h.openAIAttemptSupport()}
	if h == nil {
		b.Support = &openaiattempt.Support{}
		return b
	}

	if h.apiKeyService != nil {
		b.Support.Quota = h.apiKeyService
	}
	if h.completionRecorder != nil {
		b.Recorder = h.completionRecorder
	} else if h.gatewayService != nil {
		b.Recorder = h.completionRuntime()
	}
	b.Diagnoser = h.gatewayService
	b.ResolvedDiagnoser = openAIResolvedRoutingModelDiagnoser{service: h.gatewayService}
	if s := h.gatewayService; s != nil {
		b.Forward.EnforceOpenAIClientPolicyForRequest = s.EnforceOpenAIClientPolicyForRequest
		b.Forward.Forward = s.Forward
		b.Forward.ForwardAsAnthropic = s.ForwardAsAnthropic
		b.Forward.ForwardAsChatCompletions = s.ForwardAsChatCompletions
		b.Forward.MatchOpenAITLSFingerprintRouterForRequest = s.MatchOpenAITLSFingerprintRouterForRequest
		b.Selection.ObserveOpenAIAccountHealthFailure = s.ObserveOpenAIAccountHealthFailure
		b.Selection.RecordOpenAIAccountSwitchForSelection = s.RecordOpenAIAccountSwitchForSelection
		b.Forward.ReplaceModelInBody = s.ReplaceModelInBody
		b.Selection.ReportOpenAIAccountScheduleResult = func(a *gatewaycapture.ExecutionAccount, model string, success bool, first *int, errs ...error) bool {
			return s.ReportOpenAIAccountScheduleResult(a, model, success, first, errs...)
		}
		b.Selection.SelectAccountWithSchedulerForCapability = s.SelectAccountWithSchedulerForCapability
		b.Selection.SelectAccountWithSchedulerForCapabilityAndRoutingModel = s.SelectAccountWithSchedulerForCapabilityAndRoutingModel
		b.Selection.UpdateCodexUsageSnapshotFromHeaders = s.UpdateCodexUsageSnapshotFromHeaders
	}
	return b
}

func (h *OpenAIGatewayHandler) openAIAttemptSupport() *openaiattempt.Support {
	if h == nil {
		return nil
	}
	support := &openaiattempt.Support{Concurrency: h.concurrencyHelper, Sticky: h.gatewayService, Rules: h.errorPassthroughService, Cyber: h.NewCyberHTTPHandler(), Moderation: nativeModerationPort(h.contentModerationService), Submission: gatewayhttp.NewCompletionSubmission(h.usageRecordWorkerPool, true)}
	if h.apiKeyService != nil {
		support.Quota = h.apiKeyService
	}
	return support
}
