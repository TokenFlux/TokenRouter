package selection

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// newGenericSelectionForTest 仅组装已有原生能力和配置投影，保持各场景的显式零值。
func newGenericSelectionForTest(deps GenericDependencies, cfg *config.Config) *Generic {
	if deps.Parameters == nil {
		deps.Parameters = scheduler.NewParameters(scheduler.NewSettingsRuntime(scheduler.Diagnostics{}), nil, diagnosticParameterDefaults(cfg))
	}
	return NewGeneric(deps, selectionOptionsForTest(cfg))
}

func selectionOptionsForTest(cfg *config.Config) Options {
	options := DefaultOptions()
	if cfg == nil {
		return options
	}
	options.Simple = cfg.RunMode == config.RunModeSimple
	value := cfg.Gateway.Scheduling
	options.Scheduling = scheduler.FlowOptions{LoadBatchEnabled: value.LoadBatchEnabled, PreferSoonestReset: value.PreferSoonestReset, FallbackMaxWaiting: value.FallbackMaxWaiting, StickySessionMaxWaiting: value.StickySessionMaxWaiting, FallbackSelectionMode: value.FallbackSelectionMode, FallbackWaitTimeout: value.FallbackWaitTimeout, StickySessionWaitTimeout: value.StickySessionWaitTimeout}
	ws := cfg.Gateway.OpenAIWS
	options.WS = &egress.OpenAIWSOptions{Enabled: ws.Enabled, ForceHTTP: ws.ForceHTTP, OAuthEnabled: ws.OAuthEnabled, APIKeyEnabled: ws.APIKeyEnabled, ModeRouterV2Enabled: ws.ModeRouterV2Enabled, ResponsesWebsockets: ws.ResponsesWebsockets, ResponsesWebsocketsV2: ws.ResponsesWebsocketsV2}
	options.WSIngressMode = ws.IngressModeDefault
	options.ReadLegacySticky = ws.SessionHashReadOldFallback
	options.WriteLegacySticky = ws.SessionHashDualWriteOld
	if ws.StickySessionTTLSeconds > 0 {
		options.StickyTTL = time.Duration(ws.StickySessionTTLSeconds) * time.Second
	}
	if ws.StickyResponseIDTTLSeconds > 0 {
		options.ResponseTTL = time.Duration(ws.StickyResponseIDTTLSeconds) * time.Second
	}
	return options
}

// selectionWindowForTest 复用原用量读取和资金窗口实现，不另建缓存或计算规则。
func selectionWindowForTest(cache billing.WindowCostCache, source usage.UsageLogRepository) *billing.WindowCostGuard {
	return billing.NewWindowCostGuard(cache, gatewaytestkit.WindowCosts(source), billing.WindowCostGuardOptions{Now: time.Now, Stats: billing.SharedWindowCostMetrics(), Log: func(string, ...any) {}, Debug: func(string, ...any) {}})
}

// newGeminiSelectionForTest 将原场景直接接入 Gemini 原生选择器。
func newGeminiSelectionForTest(deps GeminiDependencies, cfg *config.Config) *Gemini {
	if deps.Parameters == nil {
		deps.Parameters = scheduler.NewParameters(scheduler.NewSettingsRuntime(scheduler.Diagnostics{}), nil, diagnosticParameterDefaults(cfg))
	}
	return NewGemini(deps, selectionOptionsForTest(cfg))
}

// newCompatibleSelectionForTest 只注入原生参数，不初始化供应商执行器。
func newCompatibleSelectionForTest(deps CompatibleDependencies, cfg *config.Config) *Compatible {
	// 执行夹具惰性提供同一响应归属存储，并通过测试装配注入。
	if deps.Responses == nil {
		cache, _ := deps.Cache.(session.GatewayCache)
		deps.Responses = session.NewOpenAIWSStateStore(cache, gatewayprovider.LogOpenAIWSModeInfo)
	}
	if deps.Parameters == nil {
		deps.Parameters = scheduler.NewParameters(scheduler.NewSettingsRuntime(scheduler.Diagnostics{}), nil, diagnosticParameterDefaults(cfg))
	}
	return NewCompatible(deps, selectionOptionsForTest(cfg))
}
