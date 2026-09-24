package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	selectionadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// selectionOptionsForTest 投影当前测试配置，未配置与显式零值沿用生产约定。
func selectionOptionsForTest(cfg *config.Config) selectionadapter.Options {
	options := selectionadapter.DefaultOptions()
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

// bindCompatibleSelectionFixture 只组合同一实际执行组件的原生依赖，不复制选择、缓存或状态。
func bindCompatibleSelectionFixture(source *OpenAIGatewayService) {
	if source == nil {
		return
	}
	if source.requestCredentials == nil {
		source.requestCredentials = gatewaytestkit.RequestCredentials(source.accountRepo, source.executionCredentials, nil, source.runtimeBlockState())
		source.executionCredentials = source.requestCredentials.Source
	}

	source.grokHealth = &accountprovider.GrokHealth{
		Store: source.accountRepo, Health: source.healthObserver,
		Throttle: source.codexSnapshotThrottle, Runtime: source.runtimeBlockState(),
		ModelTransient: source.getOpenAIAccountModelTransientState(),
		NormalizeModel: func(value *account.Record, model string) string {
			return (gatewayprovider.ModelPolicy{Record: value}).NormalizeOpenAI(model)
		},
	}
	source.compactExecutor = &gatewayhttp.CompactExecutor{}
	if source.cfg != nil {
		source.compactExecutor.Models = gatewayprovider.CompactModels{Default: source.cfg.Gateway.OpenAICompactModel}
		source.compactExecutor.LogBody = source.cfg.Gateway.LogUpstreamErrorBody
		source.compactExecutor.LogBodyMaxBytes = source.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
	}

	var quota *account.QuotaSettingsCache
	if source.settingService != nil {
		quota = source.settingService.Quota
	}
	source.selection = selectionadapter.NewCompatible(selectionadapter.CompatibleDependencies{
		Reads:     selectionadapter.Reads{Accounts: source.accountRepo},
		Shared:    selectionadapter.Shared{Cache: source.cache, Concurrency: source.concurrencyService, Health: source.healthObserver, Channels: source.channelService, Parameters: scheduler.NewParameters(scheduler.NewSettingsRuntime(scheduler.Diagnostics{}), nil, schedulerParameterDefaultsForTest(source.cfg))},
		Responses: source.ResponseStateStore(), QuotaSettings: quota, RuntimeBlocks: source.runtimeBlockState(), ModelTransient: source.getOpenAIAccountModelTransientState(), ProxyCircuit: source.getOpenAIProxyStreamCircuit(), StickyStats: source.stickyStats(),
	}, selectionOptionsForTest(source.cfg))
	if source.turnStateHeaders == nil {
		source.turnStateHeaders = &gatewayhttp.CodexTurnStateHeaders{Origins: session.NewCodexTurnOrigins(time.Now)}
	}
	source.turnStateHeaders.TTL = source.selection.SessionStickyTTL
	reasoningCache, _ := source.cache.(session.ReasoningContentCache)
	source.responseOutput = &gatewayhttp.OpenAIResponseOutput{
		Reasoning:  &session.ReasoningHistory{Cache: reasoningCache, Warn: gatewayprovider.WarnReasoningCacheFailure},
		Health:     &accountprovider.OpenAIResponseHealth{Health: source.healthObserver, Runtime: source.runtimeBlockState(), ModelTransient: source.getOpenAIAccountModelTransientState(), Deferred: source.deferredService},
		GrokHealth: source.grokHealth, Observer: source.healthObserver, Headers: source.responseHeaderFilter, Turns: source.turnStateHeaders,
		Corrector: source.toolCorrector, ProxyCircuit: source.getOpenAIProxyStreamCircuit(), Responses: source.ResponseStateStore(), ResponseTTL: source.selection.OpenAIHTTPResponseStickyTTL,
		Redact: source.agentIdentity.Redact, Options: gatewayhttp.OpenAIResponseOptions{ReadLimit: config.DefaultUpstreamResponseReadMaxBytes},
	}
	if source.settingService != nil {
		source.responseOutput.TTFT = source.settingService.Gateway.GetOpenAITTFTMode
	}
	if source.cfg != nil {
		v := source.cfg.Gateway
		source.responseOutput.Options = gatewayhttp.OpenAIResponseOptions{Configured: true, MaxLineSize: v.MaxLineSize, StreamDataIntervalTimeout: v.StreamDataIntervalTimeout, StreamKeepaliveInterval: v.StreamKeepaliveInterval, ImageStreamDataIntervalTimeout: v.ImageStreamDataIntervalTimeout, ImageStreamKeepaliveInterval: v.ImageStreamKeepaliveInterval, OpenAIFirstOutputTimeoutSeconds: v.OpenAIFirstOutputTimeoutSeconds, OpenAIHighEffortFirstOutputTimeoutSeconds: v.OpenAIHighEffortFirstOutputTimeoutSeconds, LogUpstreamErrorBody: v.LogUpstreamErrorBody, LogUpstreamErrorBodyMaxBytes: v.LogUpstreamErrorBodyMaxBytes, ResponseHeadersEnabled: source.cfg.Security.ResponseHeaders.Enabled, ReadLimit: resolveUpstreamResponseReadLimit(source.cfg)}
	}

	routes := gatewayprovider.GrokRoutes{Validate: grok.ValidateBaseURL}
	if source.cfg != nil {
		v := source.cfg.Security.URLAllowlist
		routes.Validate = (egress.OperatorURLPolicy{Enabled: v.Enabled, AllowInsecureHTTP: v.AllowInsecureHTTP, AllowPrivateHosts: v.AllowPrivateHosts, UpstreamHosts: v.UpstreamHosts}).Validate
	}
	if source.settingService != nil {
		routes.DefaultMode = source.settingService.Gateway.GetGrokDefaultBaseURLMode
	}
	source.BindGrokExecution(&gatewayhttp.GrokExecutor{FastPolicy: &gatewayprovider.ExecutionFastPolicy{Readers: source.settingService, Prices: source.resolver}, Credentials: source.requestCredentials, Transport: source.httpUpstream, Output: source.responseOutput, Health: source.grokHealth, Routes: routes, TLS: source.tlsFPProfileService, Dialer: source.getOpenAIWSPassthroughDialer(), Enter: source.nativeAttemptActivity, Failure: &gatewayhttp.UpstreamTransportFailure{Health: &accountprovider.TransportHealth{Runtime: source.runtimeBlockState(), Deferred: source.deferredService, Store: source.accountRepo}}})
}

// streamSelectionDiagnosticSource 为真实流执行后的下一次选择提供可调度查询投影。
// 流夹具原本只提供传输字段；诊断 API 所需的分组与活动状态只补在查询副本中。
type streamSelectionDiagnosticSource struct {
	value gatewayprovider.ExecutionAccount
	group routing.Group
}

func (s streamSelectionDiagnosticSource) GetAccount(context.Context, int64) (*gatewayprovider.ExecutionAccount, error) {
	return &s.value, nil
}
func (s streamSelectionDiagnosticSource) GetGroup(context.Context, int64) (*routing.Group, error) {
	return &s.group, nil
}
func (s streamSelectionDiagnosticSource) ListAccountsForSchedulerScoreFilter(context.Context, string, string, string, string, int64, string) ([]gatewayprovider.ExecutionAccount, error) {
	return []gatewayprovider.ExecutionAccount{s.value}, nil
}
func (s streamSelectionDiagnosticSource) ListSchedulableAccountsForAdvancedSchedulerScore(context.Context, *int64, string) ([]gatewayprovider.ExecutionAccount, error) {
	return []gatewayprovider.ExecutionAccount{s.value}, nil
}

// selectionDiagnosticForStreamTest 通过生产诊断接口核对同一代理熔断状态及原错误原因。
func selectionDiagnosticForStreamTest(t *testing.T, source *OpenAIGatewayService, value *gatewayprovider.ExecutionAccount) (bool, string) {
	t.Helper()
	// 原场景在构造后替换熔断参数，显式刷新装配投影，保持与实际转发共用同一实例。
	bindCompatibleSelectionFixture(source)
	copy := *value
	copy.Record.Status = "active"
	copy.Record.Schedulable = true
	copy.Record.GroupIDs = []int64{1}
	projection := streamSelectionDiagnosticSource{value: copy, group: routing.Group{ID: 1, Platform: value.Record.Platform, Status: "active", Hydrated: true, SchedulerType: routing.GroupSchedulerTypeAdvanced}}
	diagnostic := selectionadapter.NewDiagnostics(projection, selectionadapter.Shared{Parameters: scheduler.NewParameters(scheduler.NewSettingsRuntime(scheduler.Diagnostics{}), nil, schedulerParameterDefaultsForTest(source.cfg))}, nil, source.selection)
	result, err := diagnostic.GetDetail(context.Background(), value.Record.ID, policy.AdvancedSchedulerScoreDiagnosticRequest{GroupID: 1})
	require.NoError(t, err)
	require.NotNil(t, result.Detail)
	return result.Detail.Eligible, strings.Join(result.Detail.HardFilterReasons, ",")
}
