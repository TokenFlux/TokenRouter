package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/textattempt"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// provideMessageAttemptRuntime 固定生产调用端口，HTTP 输出与每次尝试状态归 textattempt。
func provideMessageAttemptRuntime(
	source *service.GatewayService,
	antigravity *service.AntigravityGatewayService,
	gemini *service.GeminiMessagesCompatService,
	funding *admission.FundingAdmission,
	keys *apikey.APIKeyService,
	recorders GatewayCompletionRecorders,
	shared *messageHTTPBindings,
	rules *errorpolicy.ErrorPassthroughService,
	worker *completion.UsageRecordWorkerPool,
	queue *scheduler.UserMessageQueueService,
	cfg *config.Config, availability *gatewayModelAvailability,
) *textattempt.Runtime {
	var queueHelper *gatewayhttp.UserMsgQueueHelper
	if queue != nil && cfg != nil {
		queueHelper = gatewayhttp.NewUserMsgQueueHelper(queue, gatewayhttp.SSEPingFormatClaude, time.Duration(cfg.Concurrency.PingInterval)*time.Second)
	}

	b := textattempt.Bindings{
		PlanRoute:   shared.bindings.PlanRoute,
		Concurrency: shared.concurrency,
		Queue:       queueHelper,
		Errors:      rules,
		Submission: gatewayhttp.NewCompletionSubmission(
			worker,
			false,
		),
	}

	if availability != nil {
		b.Diagnoser = availability.Messages
	}

	if cfg != nil {
		b.QueueWait = cfg.Gateway.UserMessageQueue.WaitTimeout()
		b.QueueMode = cfg.Gateway.UserMessageQueue.GetEffectiveMode()
	}
	if keys != nil {
		b.Quota = keys
	}
	b.Recorder = recorders.Forward
	if s := source; s != nil {
		b.Selection.SelectAccount = s.SelectAccountWithLoadAwareness
		b.Selection.TrackSession = s.TrackSessionAttempt
		b.Selection.NewSessionAttempts = s.NewSessionAttempts
		b.Selection.SingleAccountGroup = s.IsSingleAntigravityAccountGroup
		b.Selection.ReportSchedule = s.ReportAdvancedAccountScheduleResult
		b.Selection.IncrementRPM = s.IncrementAccountRPM
		b.Selection.BindSticky = s.BindStickySession
		b.Selection.ResolveGroup = s.ResolveGroupByID
		b.Selection.AccountSwitched = s.RecordAdvancedAccountSwitch
		b.Selection.TempUnschedule = s.TempUnscheduleRetryableError
		b.Forward.BedrockCompat = s.ApplyBedrockCCCompat
		b.Forward.ForwardMessages = s.Forward
	}
	if s := antigravity; s != nil {
		b.Forward.ForwardAntigravity = s.Forward
		b.Forward.ForwardAntigravityGemini = s.ForwardGemini
		b.Forward.WriteMappedClaudeError = s.WriteMappedClaudeError
	}
	if s := gemini; s != nil {
		b.Forward.ForwardGemini = s.Forward
	}
	if s := funding; s != nil {
		b.CheckFunding = s.CheckKey
	}
	if s := source; s != nil {
		b.Forward.ForwardResponses = s.ForwardAsResponses
		b.Forward.ForwardChat = s.ForwardAsChatCompletions
		b.Forward.SaveGeminiSession = s.SaveGeminiSession
		b.Forward.ReplaceModel = s.ReplaceModelInBody
	}
	if s := antigravity; s != nil {
		b.Forward.ForwardAntigravityResponses = s.ForwardAsResponses
		b.Forward.ForwardAntigravityChat = s.ForwardAsChatCompletions
	}
	if s := gemini; s != nil {
		b.Forward.ForwardGeminiResponses = s.ForwardAsResponses
		b.Forward.ForwardGeminiChat = s.ForwardAsChatCompletions
		b.Forward.ForwardGeminiNative = s.ForwardNative
	}
	b.Forward.GeminiAvailable = gemini != nil
	b.Forward.AntigravityAvailable = antigravity != nil

	return textattempt.New(b)

}
