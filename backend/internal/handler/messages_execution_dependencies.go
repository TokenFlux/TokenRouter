package handler

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/textattempt"
)

// 旧测试入口只投影固定依赖，实际运行时已无旧 Handler 引用。
func messageAttemptBindings(h *GatewayHandler) textattempt.Bindings {
	b := textattempt.Bindings{PlanRoute: h.messagesBindings().PlanRoute, Diagnoser: h.gatewayService, Concurrency: h.concurrencyHelper, Queue: h.userMsgQueueHelper, Errors: h.errorPassthroughService, Submission: gatewayhttp.NewCompletionSubmission(h.usageRecordWorkerPool, false)}

	if h.cfg != nil {
		b.QueueWait = h.cfg.Gateway.UserMessageQueue.WaitTimeout()
		b.QueueMode = h.cfg.Gateway.UserMessageQueue.GetEffectiveMode()
	}
	if h.apiKeyService != nil {
		b.Quota = h.apiKeyService
	}
	if h.completionRecorder != nil {
		b.Recorder = h.completionRecorder
	} else if h.gatewayService != nil {
		b.Recorder = h.completionRuntime()
	}
	if s := h.gatewayService; s != nil {
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
	if s := h.antigravityGatewayService; s != nil {
		b.Forward.ForwardAntigravity = s.Forward
		b.Forward.ForwardAntigravityGemini = s.ForwardGemini
		b.Forward.WriteMappedClaudeError = s.WriteMappedClaudeError
	}
	if s := h.geminiCompatService; s != nil {
		b.Forward.ForwardGemini = s.Forward
	}
	if s := h.billingCacheService; s != nil {
		b.CheckFunding = s.CheckKey
	}
	if s := h.gatewayService; s != nil {
		b.Forward.ForwardResponses = s.ForwardAsResponses
		b.Forward.ForwardChat = s.ForwardAsChatCompletions
		b.Forward.SaveGeminiSession = s.SaveGeminiSession
		b.Forward.ReplaceModel = s.ReplaceModelInBody
	}
	if s := h.antigravityGatewayService; s != nil {
		b.Forward.ForwardAntigravityResponses = s.ForwardAsResponses
		b.Forward.ForwardAntigravityChat = s.ForwardAsChatCompletions
	}
	if s := h.geminiCompatService; s != nil {
		b.Forward.ForwardGeminiResponses = s.ForwardAsResponses
		b.Forward.ForwardGeminiChat = s.ForwardAsChatCompletions
		b.Forward.ForwardGeminiNative = s.ForwardNative
	}
	b.Forward.GeminiAvailable = h.geminiCompatService != nil
	b.Forward.AntigravityAvailable = h.antigravityGatewayService != nil

	return b
}
