package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/openaiattempt"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// provideOpenAITextAttemptRuntime 直接固定平台单次调用、原生槽位与完成端口。
func provideOpenAITextAttemptRuntime(
	source *service.OpenAIGatewayService,
	keys *apikey.APIKeyService,
	resources *gatewayhttp.OpenAIHTTPResources,
	cyber *gatewayhttp.CyberHandler,
	rules *errorpolicy.ErrorPassthroughService,
	moderator *moderation.ContentModerationService,
	records GatewayCompletionRecorders,
	worker *completion.UsageRecordWorkerPool,
) *openaiattempt.Runtime {
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
	b := openaiattempt.Bindings{Support: support, Recorder: records.OpenAI, Diagnoser: source}
	if source != nil {
		support.Sticky = source
		b.ResolvedDiagnoser = openAIResolvedModelReader(source.DiagnoseRoutingModelAvailabilityForPlatform)
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
	return openaiattempt.New(b)
}

// 已解析型号的诊断仅转发到同一查询能力，不再次套用渠道模型映射。
type openAIResolvedModelReader func(context.Context, *int64, string, string) routing.ModelAvailabilityDiagnosis

func (read openAIResolvedModelReader) DiagnoseModelAvailabilityForPlatform(ctx context.Context, group *int64, model, platform string) routing.ModelAvailabilityDiagnosis {
	return read(ctx, group, model, platform)
}
