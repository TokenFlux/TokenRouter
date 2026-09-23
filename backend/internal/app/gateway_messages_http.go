package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/textattempt"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"

	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	"github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"go.uber.org/zap"
)

// provideMessagesHTTP 直接构造原生 HTTP；执行依赖的最后兼容装配单独保留。
func provideMessageHTTPBindings(
	source *service.GatewayService,
	openai *service.OpenAIGatewayService,
	funding *admission.FundingAdmission,
	rules *errorpolicy.ErrorPassthroughService,
	moderationService *moderation.ContentModerationService,
	settings *gateway.RuntimeSettings,
	prompts *promptpolicy.Service,
	concurrency *scheduler.ConcurrencyService,
	cfg *config.Config, choices *selection.Generic,
) *messageHTTPBindings {
	options := gatewayhttp.MessagesHTTPOptions{MaxSwitches: 10, MaxGeminiSwitches: 3}
	ping := time.Duration(0)
	if cfg != nil {
		options.MaxBodyBytes = cfg.Gateway.MaxBodySize
		ping = time.Duration(cfg.Concurrency.PingInterval) * time.Second
		if cfg.Gateway.MaxAccountSwitches > 0 {
			options.MaxSwitches = cfg.Gateway.MaxAccountSwitches
		}
		if cfg.Gateway.MaxAccountSwitchesGemini > 0 {
			options.MaxGeminiSwitches = cfg.Gateway.MaxAccountSwitchesGemini
		}
	}
	var moderationPort gatewayhttp.ModerationPort
	if moderationService != nil {
		moderationPort = moderationService
	}
	bindings := gatewayhttp.MessagesBindings{
		PlanRoute: func(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
			var id *int64
			if key != nil {
				id = key.GroupID
			}
			return source.PlanRoute(ctx, service.APIKeyRouteGroup(key), id, model)
		},
		ClientVersions: settings.GetClaudeCodeVersionBounds, Funding: funding, Moderation: moderationPort, Errors: rules,
		IsolateSession: source.EnsureSessionIsolation, CachedSession: choices.GetCachedSessionAccountID,
		ObserveCompatibility: func(log *zap.Logger) {
			gatewayhttp.LogCompatibilityFallback(log, func() gatewayhttp.CompatibilityLogSnapshot {
				value := openai.SnapshotOpenAICompatibilityFallbackMetrics()
				return gatewayhttp.CompatibilityLogSnapshot{ReadTotal: value.SessionHashLegacyReadFallbackTotal, ReadHit: value.SessionHashLegacyReadFallbackHit, DualWrite: value.SessionHashLegacyDualWriteTotal, ReadHitRate: value.SessionHashLegacyReadHitRate, MetadataTotal: value.MetadataLegacyFallbackTotal}
			})
		},
	}
	return &messageHTTPBindings{options: options, bindings: bindings, prompt: prompts, concurrency: gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatClaude, ping)}
}

// messageHTTPBindings 共享原生模块端口与无状态 HTTP helper，不保存请求或业务缓存。
type messageHTTPBindings struct {
	options     gatewayhttp.MessagesHTTPOptions
	bindings    gatewayhttp.MessagesBindings
	prompt      *promptpolicy.Service
	concurrency *gatewayhttp.ConcurrencyHelper
}

func provideMessagesHTTP(
	shared *messageHTTPBindings,
	runtime *textattempt.Runtime,
	activity *gatewayRequestActivity,
) *gatewayhttp.MessagesHandler {
	result := gatewayhttp.NewBoundMessagesHandler(
		shared.options,
		shared.bindings,
		shared.prompt,
		shared.concurrency,
		textflow.NewMessagesExecutor(
			runtime,
			textflow.MessageOptions{
				MaxSwitches:            shared.options.MaxSwitches,
				CompletePartialFailure: true,
				Observe:                telemetry.Failover,
			},
			textflow.MessageOptions{
				MaxSwitches: shared.options.MaxGeminiSwitches,
				Observe:     telemetry.Failover,
			},
		),
	)
	result.BindRequestActivity(activity.Enter)
	return result
}
func provideCompatibleTextHTTP(
	shared *messageHTTPBindings,
	source *service.GatewayService,
	runtime *textattempt.Runtime,
	activity *gatewayRequestActivity,
) *gatewayhttp.CompatibleTextHandler {
	result := gatewayhttp.NewBoundCompatibleTextHandler(
		shared.options,
		shared.bindings,
		source.ReplaceModelInBody,
		shared.prompt,
		shared.concurrency,
		textflow.NewMessagesExecutor(
			runtime,
			textflow.MessageOptions{
				MaxSwitches:           shared.options.MaxSwitches,
				StopOnCanceledContext: true,
				Observe:               telemetry.Failover,
			},
			textflow.MessageOptions{
				MaxSwitches:           shared.options.MaxGeminiSwitches,
				StopOnCanceledContext: true,
				Observe:               telemetry.Failover,
			},
		),
	)
	result.BindRequestActivity(activity.Enter)
	return result
}
func provideGeminiNativeHTTP(
	shared *messageHTTPBindings,
	source *service.GatewayService,
	runtime *textattempt.Runtime,
	activity *gatewayRequestActivity, choices *selection.Generic,
) *gatewayhttp.GeminiNativeHandler {
	result := gatewayhttp.NewBoundGeminiNativeHandler(
		gatewayhttp.GeminiNativeOptions{
			MaxSwitches: shared.options.MaxGeminiSwitches,
		},
		shared.bindings,
		gatewayhttp.GeminiHTTPBindings{
			SafeModelSegment: gemini.IsSafeGeminiModelPathSegment,
			FindSession:      source.FindGeminiSession,
			BindSticky:       choices.BindStickySession,
		},
		shared.prompt,
		shared.concurrency,
		func() string { return uuid.New().String() },
		textflow.NewMessagesExecutor(
			runtime,
			textflow.MessageOptions{
				MaxSwitches: shared.options.MaxGeminiSwitches,
				Observe:     telemetry.Failover,
			},
			textflow.MessageOptions{
				MaxSwitches: shared.options.MaxGeminiSwitches,
				Observe:     telemetry.Failover,
			},
		),
	)
	result.BindRequestActivity(activity.Enter)
	return result
}
