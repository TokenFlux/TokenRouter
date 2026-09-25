package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/service"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/routing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"

	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// newOpenAIExecutionAndSelectionFixture 组合真实执行组件与原生选择器，显式共享所有可变状态。
func newOpenAIExecutionAndSelectionFixture(
	accountRepo gatewayprovider.ExecutionAccountStore,
	cache session.GatewayCache,
	cfg *config.Config,
	schedulerSnapshot *scheduler.SnapshotService,
	concurrencyService *scheduler.ConcurrencyService,

	healthObserver *accountprovider.UpstreamHealth,
	httpUpstream httpclient.UpstreamTransport,
	tlsFPProfileService *provider.TLSProfiles,
	deferredService *accountcore.DeferredService,
	executionCredentials *accountcore.OpenAIExecutionCredentials,
	grokTokenProvider *accountcore.GrokTokenSource,
	resolver *billing.PriceResolver,
	channelService *routing.ChannelService,

	settingService *gatewayprovider.RuntimeReaders,
	prompts *promptpolicy.Service, headerFilter *egress.CompiledHeaderFilter, stateStore session.OpenAIWSStateStore, modelTransient *accountcore.ModelTransientState, proxyCircuit *egress.ProxyStreamCircuit,
	tlsFPRouterServices ...*egress.TLSFingerprintRouterService,
) (*service.OpenAIGatewayService, *selection.Compatible, *gatewayhttp.RequestCredentialExecutor) {
	if modelTransient ==
		nil {
		modelTransient = provideSelectionModelTransient()
	}
	if proxyCircuit ==
		nil {
		proxyCircuit =
			provideSelectionProxyCircuit(cfg)
	}
	if stateStore == nil {
		stateStore = session.NewOpenAIWSStateStore(cache, gatewayprovider.LogOpenAIWSModeInfo)
	}
	blocks := accountcore.NewRuntimeBlockState(time.Now)
	feedback := scheduler.NewRuntimeStats(time.Now)
	sticky := &scheduler.StickyStats{}
	var quota *accountcore.QuotaSettingsCache
	if settingService != nil {
		quota = settingService.Quota
	}
	choices := selection.NewCompatible(selection.CompatibleDependencies{
		Reads: selection.Reads{Accounts: accountRepo, Snapshot: provideSelectionSnapshots(schedulerSnapshot)},
		Shared: selection.Shared{
			Cache: cache,

			Concurrency: concurrencyService,
			Health:      healthObserver,
			Channels:    channelService,

			Feedback: feedback,
		},
		Responses:      stateStore,
		QuotaSettings:  quota,
		RuntimeBlocks:  blocks,
		ModelTransient: modelTransient,

		ProxyCircuit: proxyCircuit,
		StickyStats:  sticky,
	}, selectionOptions(cfg))
	credentials := gatewaytestkit.RequestCredentials(accountRepo, executionCredentials, grokTokenProvider, blocks)
	executionCredentials = credentials.Source

	turnHeaders := provideCodexTurnStateHeaders(choices)
	grokHealth := &accountprovider.GrokHealth{Store: accountRepo, Health: healthObserver, Runtime: blocks, ModelTransient: modelTransient, Throttle: accountcore.NewWriteThrottle(30 * time.Second), NormalizeModel: func(value *accountcore.Record, model string) string {
		return (gatewayprovider.ModelPolicy{Record: value}).NormalizeOpenAI(model)
	}}
	output := provideOpenAIResponseOutput(cfg, provideOpenAIResponseHealth(healthObserver, blocks, modelTransient, deferredService), grokHealth, healthObserver, headerFilter, turnHeaders, proxyCircuit, settingService, stateStore, choices, provideReasoningHistory(cache))
	connections := gatewayhttp.NewOpenAIWSConnections(openAIWSPoolOptions(cfg), nil)
	source := service.NewOpenAIGatewayService(connections, accountRepo, cache, cfg, concurrencyService,
		healthObserver, httpUpstream, tlsFPProfileService, deferredService,

		executionCredentials, credentials, resolver, channelService,

		settingService, prompts, headerFilter, stateStore, turnHeaders, modelTransient, proxyCircuit, choices, grokHealth, provideCompactExecutor(cfg), output, tlsFPRouterServices...)
	source.BindGrokExecution(provideGrokExecutor(cfg, credentials, httpUpstream, output, grokHealth, tlsFPProfileService, settingService, blocks, deferredService, accountRepo, &gatewayRequestActivity{Operations: lifecycle.NewOperations("GatewayRequestsAndAttempts")}, resolver, connections))
	var routers *egress.TLSFingerprintRouterService
	if len(tlsFPRouterServices) > 0 {
		routers = tlsFPRouterServices[0]
	}
	source.BindTextExecution(openAITextExecution(cfg, accountRepo, nil, executionCredentials, httpUpstream, tlsFPProfileService, routers, settingService, source.Grok, output, source.PromptCacheBindings(), choices.OpenAIHTTPResponseStickyTTL, provideCompactExecutor(cfg)))
	source.Auxiliary = &gatewayhttp.OpenAIAuxiliary{Requests: source.Requests, Output: output, CodexUsage: source.Text.CodexUsage}
	bindOpenAIResponses(source, cfg, channelService, choices, cache, prompts)
	source.BindRuntimeBlockState(blocks)
	source.BindSchedulerStickyStats(sticky)
	return source, choices, &gatewayhttp.RequestCredentialExecutor{Runtime: credentials}
}

// newEmptyCompatibleSelectionFixture 对应原零值执行入口，仍不配置任何账号来源。
func newEmptyCompatibleSelectionFixture() *selection.Compatible {
	return selection.NewCompatible(selection.CompatibleDependencies{}, selection.DefaultOptions())
}
