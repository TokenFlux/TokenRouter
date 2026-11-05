package app

import (
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai/liveattestation"
)

// gateway_live_http.go constructs Live execution and HTTP admission and
// registers observer shutdown.
// Live creation shares OpenAI request construction and Grok WebSocket dialing
// with other gateway execution. Optional cache and concurrency capabilities
// provide stored call state and leases, while local observer shutdown is
// registered before later request and storage cleanup phases.

// provideLiveExecution assembles Live execution and registers its local
// observer stop hook. Default session duration is one hour and retry interval
// one second; a positive configured duration overrides it, and a nonempty JWT
// secret supplies the attestation cipher. Call before lifecycle startup with
// required request, dialer, task, and manager dependencies.
func provideLiveExecution(cfg *config.Config, auxiliary *gatewayhttp.OpenAIAuxiliary, cache session.GatewayCache, concurrency *scheduler.ConcurrencyService, choices *selection.Compatible, routes *gatewayadapter.RoutePlanner, recorders GatewayCompletionRecorders, tasks *lifecycle.Tasks, manager *lifecycle.Manager, grok *gatewayhttp.GrokExecutor) *gatewayhttp.OpenAILiveExecutor {
	out := &gatewayhttp.OpenAILiveExecutor{
		Options:  gatewayhttp.OpenAILiveOptions{MaxSessionDuration: time.Hour, ObserverRetryInterval: time.Second},
		Requests: auxiliary.Requests, Selection: choices, Routes: routes, Usage: recorders.OpenAI,
		Dialer: grok.Dialer, Attestation: liveattestation.NewProvider(), Background: tasks.Go,
	}
	out.Store, _ = cache.(session.LiveCallStore)
	if concurrency != nil {
		out.Leases = concurrency.LiveLeases()
	}
	if cfg != nil {
		if cfg.Gateway.OpenAI.Live.MaxSessionDurationSeconds > 0 {
			// Configured session seconds become the executor's maximum lifetime duration.
			out.Options.MaxSessionDuration = time.Duration(cfg.Gateway.OpenAI.Live.MaxSessionDurationSeconds) * time.Second
		}
		if strings.TrimSpace(cfg.Auth.JWT.Secret) != "" {
			out.AttestationCipher = openai.NewLiveAttestationCipher(cfg.Auth.JWT.Secret)
		}
	}
	// Close local Live observers at the early shutdown level so their tracked
	// work can finish before request completion and shared dependency cleanup.
	manager.Register(lifecycle.Hook{Name: "OpenAILiveObservers", StartOrder: 995, StopOrder: shutdownStageLocalObservers, Stop: out.StopLiveObservers})
	return out
}

// provideLiveHTTP assembles Live admission, slots, and optional moderation and
// execution. It binds the shared request activity when supplied, allowing that
// barrier to reject or track Live calls before handler preparation.
func provideLiveHTTP(source *gatewayhttp.OpenAILiveExecutor, funding *admission.FundingAdmission, concurrency *scheduler.ConcurrencyService, moderator *moderation.ContentModerationService, activity *gatewayRequestActivity) *gatewayhttp.LiveHandler {
	ports := gatewayhttp.LivePorts{Slots: concurrency}
	if source != nil {
		ports.Execution = source
	}
	if funding != nil {
		ports.Funding = funding
	}
	if moderator != nil {
		ports.Moderation = moderator
	}
	result := gatewayhttp.NewLiveHandler(ports)
	if activity != nil {
		result.BindRequestActivity(activity.Enter)
	}
	return result
}
