package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/textattempt"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	openaiwire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// messageAttemptBindings 绑定执行函数，textattempt 管理 HTTP 输出和每次尝试的状态。
func messageAttemptBindings(
	cooldown *provider.RetryCooldown,
	digest *session.DigestSessionStore,
	messages *gatewayhttp.MessagesExecutor,
	antigravity *gatewayhttp.AntigravityExecutor,
	gemini *gatewayhttp.GeminiExecutor,
	funding *admission.FundingAdmission,
	keys *apikey.APIKeyService,
	recorders GatewayCompletionRecorders,
	shared *messageHTTPBindings,
	rules *errorpolicy.ErrorPassthroughService,
	worker *completion.UsageRecordWorkerPool,
	queue *scheduler.UserMessageQueueService,
	cfg *config.Config, availability *gatewayModelAvailability, choices *selection.Generic,
	subscriptions *billing.SubscriptionService, cache session.GatewayCache,
	prices *billing.PriceResolver,
) textattempt.Bindings {
	var queueHelper *gatewayhttp.UserMsgQueueHelper
	if queue != nil && cfg != nil {
		queueHelper = gatewayhttp.NewUserMsgQueueHelper(queue, gatewayhttp.SSEPingFormatClaude, time.Duration(cfg.Concurrency.PingInterval)*time.Second)
	}

	b := textattempt.Bindings{
		PlanRoute:       shared.bindings.PlanRoute,
		ResolveFallback: provideRuntimeGroupFallbackResolver(keys, funding, subscriptions, cache),
		Concurrency:     shared.concurrency,
		Queue:           queueHelper,
		Errors:          rules,
		Pricing:         &admission.ModelPricing{Resolver: prices},
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
		b.Selection.SelectProvider = choices.SelectProviderWithLoadAwareness
		b.Selection.TrackSession = choices.TrackSessionAttempt
		b.Selection.NewSessionAttempts = choices.NewSessionAttempts
		b.Selection.SingleProviderGroup = choices.IsSingleAntigravityProviderGroup
		b.Selection.ReportSchedule = choices.ReportAdvancedProviderScheduleResult
		b.Selection.IncrementRPM = choices.IncrementProviderRPM
		b.Selection.BindSticky = choices.BindStickySession
		b.Selection.CachedSession = choices.GetCachedSessionProviderID
		b.Selection.ProviderSwitched = choices.RecordAdvancedProviderSwitch
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

// provideMessageAttemptRuntime 为消息执行组件绑定共享依赖。
func provideMessageAttemptRuntime(
	cooldown *provider.RetryCooldown,
	digest *session.DigestSessionStore,
	messages *gatewayhttp.MessagesExecutor,
	antigravity *gatewayhttp.AntigravityExecutor,
	gemini *gatewayhttp.GeminiExecutor,
	funding *admission.FundingAdmission,
	keys *apikey.APIKeyService,
	recorders GatewayCompletionRecorders,
	shared *messageHTTPBindings,
	rules *errorpolicy.ErrorPassthroughService,
	worker *completion.UsageRecordWorkerPool,
	queue *scheduler.UserMessageQueueService,
	cfg *config.Config, availability *gatewayModelAvailability, choices *selection.Generic,
	subscriptions *billing.SubscriptionService, cache session.GatewayCache,
	prices *billing.PriceResolver,
) *textattempt.Runtime {
	return textattempt.New(messageAttemptBindings(cooldown, digest, messages, antigravity, gemini, funding, keys, recorders, shared, rules, worker, queue, cfg, availability, choices, subscriptions, cache, prices))
}
