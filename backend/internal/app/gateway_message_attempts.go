package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/textattempt"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	openaiwire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// provideMessageAttemptRuntime 固定生产调用端口，HTTP 输出与每次尝试状态归 textattempt。
func messageAttemptBindings(
	cooldown *account.RetryCooldown,
	digest *session.DigestSessionStore,
	messages *gatewayhttp.MessagesExecutor,
	antigravity *service.AntigravityGatewayService,
	gemini *service.GeminiMessagesCompatService,
	funding *admission.FundingAdmission,
	keys *apikey.APIKeyService,
	recorders GatewayCompletionRecorders,
	shared *messageHTTPBindings,
	rules *errorpolicy.ErrorPassthroughService,
	worker *completion.UsageRecordWorkerPool,
	queue *scheduler.UserMessageQueueService,
	cfg *config.Config, availability *gatewayModelAvailability, choices *selection.Generic,
) textattempt.Bindings {
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
	if choices != nil {
		b.Selection.SelectAccount = choices.SelectAccountWithLoadAwareness
		b.Selection.TrackSession = choices.TrackSessionAttempt
		b.Selection.NewSessionAttempts = choices.NewSessionAttempts
		b.Selection.SingleAccountGroup = choices.IsSingleAntigravityAccountGroup
		b.Selection.ReportSchedule = choices.ReportAdvancedAccountScheduleResult
		b.Selection.IncrementRPM = choices.IncrementAccountRPM
		b.Selection.BindSticky = choices.BindStickySession
		b.Selection.ResolveGroup = choices.ResolveGroupByID
		b.Selection.AccountSwitched = choices.RecordAdvancedAccountSwitch
	}
	b.Selection.TempUnschedule = messageRetryCooldown(cooldown)

	if messages != nil {
		b.Forward.BedrockCompat = messages.ApplyBedrockCCCompat
		b.Forward.ForwardMessages = messages.Forward
		b.Forward.ForwardResponses = messages.ForwardAsResponses
		b.Forward.ForwardChat = messages.ForwardAsChatCompletions
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
	b.Forward.SaveGeminiSession = messageDigestSave(digest)
	b.Forward.ReplaceModel = openaiwire.ReplaceModelInBody
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

	return b

}

// 固定依赖投影只构造一次；Wire 入口直接创建原生执行器。
func provideMessageAttemptRuntime(
	cooldown *account.RetryCooldown,
	digest *session.DigestSessionStore,
	messages *gatewayhttp.MessagesExecutor,
	antigravity *service.AntigravityGatewayService,
	gemini *service.GeminiMessagesCompatService,
	funding *admission.FundingAdmission,
	keys *apikey.APIKeyService,
	recorders GatewayCompletionRecorders,
	shared *messageHTTPBindings,
	rules *errorpolicy.ErrorPassthroughService,
	worker *completion.UsageRecordWorkerPool,
	queue *scheduler.UserMessageQueueService,
	cfg *config.Config, availability *gatewayModelAvailability, choices *selection.Generic,
) *textattempt.Runtime {
	return textattempt.New(messageAttemptBindings(cooldown, digest, messages, antigravity, gemini, funding, keys, recorders, shared, rules, worker, queue, cfg, availability, choices))
}
