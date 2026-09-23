package app

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/openaiattempt"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// provideOpenAIAttemptBindings 为 HTTP 与 WS 固定同一平台单次调用、槽位与完成端口。
func provideOpenAIAttemptBindings(
	source *service.OpenAIGatewayService,
	keys *apikey.APIKeyService,
	resources *gatewayhttp.OpenAIHTTPResources,
	cyber *gatewayhttp.CyberHandler,
	rules *errorpolicy.ErrorPassthroughService,
	moderator *moderation.ContentModerationService,
	records GatewayCompletionRecorders,
	worker *completion.UsageRecordWorkerPool, availability *gatewayModelAvailability,
) openaiattempt.Bindings {
	support := &openaiattempt.Support{Rules: rules, Cyber: cyber, Submission: gatewayhttp.NewCompletionSubmission(worker, true)}
	if resources != nil {
		support.Concurrency = resources.Concurrency
	}
	if keys != nil {
		support.Quota = keys
	}
	if moderator != nil {
		support.Moderation = moderator
	}
	b := openaiattempt.Bindings{Support: support, Recorder: records.OpenAI}
	if source != nil {
		support.Sticky = source
	}
	if s := source; s != nil {
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
	if availability != nil {
		b.Diagnoser = availability.Compatible
		b.ResolvedDiagnoser = availability.Resolved
	}
	return b
}

// provideOpenAITextAttemptRuntime 复用已装配的原生支持与平台能力。
func provideOpenAITextAttemptRuntime(b openaiattempt.Bindings) *openaiattempt.Runtime {
	return openaiattempt.New(b)
}
