package app

import (
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai/liveattestation"
)

// provideLiveExecution 构造不启动观察者；Live与HTTP/WS共用凭据、拨号器和后台任务拥有者。
func provideLiveExecution(cfg *config.Config, auxiliary *gatewayhttp.OpenAIAuxiliary, cache session.GatewayCache, concurrency *scheduler.ConcurrencyService, choices *selection.Compatible, routes *provider.RoutePlanner, recorders GatewayCompletionRecorders, tasks *lifecycle.Tasks, manager *lifecycle.Manager, grok *gatewayhttp.GrokExecutor) *gatewayhttp.OpenAILiveExecutor {
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
		if cfg.Gateway.Live.MaxSessionDurationSeconds > 0 {
			out.Options.MaxSessionDuration = time.Duration(cfg.Gateway.Live.MaxSessionDurationSeconds) * time.Second
		}
		if strings.TrimSpace(cfg.JWT.Secret) != "" {
			out.AttestationCipher = openai.NewLiveAttestationCipher(cfg.JWT.Secret)
		}
	}
	manager.Register(lifecycle.Hook{Name: "OpenAILiveObservers", StartOrder: 995, StopOrder: 5, Stop: out.StopLiveObservers})
	return out
}
