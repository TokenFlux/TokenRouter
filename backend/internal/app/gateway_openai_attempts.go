package app

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/openaiattempt"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
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
	worker *completion.UsageRecordWorkerPool, availability *gatewayModelAvailability, choices *selection.Compatible,
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
	if s := source; s != nil {
		b.Forward.EnforceOpenAIClientPolicyForRequest = s.Requests.EnforceClient
		b.Forward.Forward = s.Forward
		b.Forward.ForwardAsAnthropic = s.Text.Messages
		b.Forward.ForwardAsChatCompletions = s.Text.Chat
		b.Forward.MatchOpenAITLSFingerprintRouterForRequest = s.Requests.MatchTLS
		b.Forward.ReplaceModelInBody = s.ReplaceModelInBody
		b.Selection.UpdateCodexUsageSnapshotFromHeaders = s.Text.CodexUsage.Headers
	}
	if choices != nil {
		support.Sticky = choices
		b.Selection.ObserveOpenAIAccountHealthFailure = choices.ObserveOpenAIAccountHealthFailure
		b.Selection.RecordOpenAIAccountSwitchForSelection = choices.RecordOpenAIAccountSwitchForSelection
		b.Selection.ReportOpenAIAccountScheduleResult = func(a *gatewaycapture.ExecutionAccount, model string, success bool, first *int, errs ...error) bool {
			return choices.ReportOpenAIAccountScheduleResult(a, model, success, first, errs...)
		}
		b.Selection.SelectAccountWithSchedulerForCapability = choices.SelectAccountWithSchedulerForCapability
		b.Selection.SelectAccountWithSchedulerForCapabilityAndRoutingModel = choices.SelectAccountWithSchedulerForCapabilityAndRoutingModel
		b.Selection.SelectImages = choices.SelectAccountWithSchedulerForImages
		b.Selection.RecordSwitch = choices.RecordOpenAIAccountSwitch
		b.Selection.ReportSelection = choices.ReportOpenAIAccountScheduleResultForSelection
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
