package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/routing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"

	"github.com/TokenFlux/TokenRouter/internal/egress"
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
) (*gatewayExecutionFixture, *selection.Compatible, *gatewayhttp.RequestCredentialExecutor) {
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
	connections := gatewayhttp.NewOpenAIWSConnections(openAIWSPoolOptions(cfg), nil)
	identity := gatewayprovider.NewExecutionAgentIdentity(&accountcore.OpenAITaskCoordinator{}, accountRepo, nil, connections.InvalidateAccount)
	output := provideOpenAIResponseOutput(cfg, provideOpenAIResponseHealth(healthObserver, blocks, modelTransient, deferredService), grokHealth, healthObserver, headerFilter, turnHeaders, proxyCircuit, settingService, stateStore, choices, provideReasoningHistory(cache), identity)
	activity := &gatewayRequestActivity{Operations: lifecycle.NewOperations("GatewayRequestsAndAttempts")}
	grokExecutor := provideGrokExecutor(cfg, credentials, httpUpstream, output, grokHealth, tlsFPProfileService, settingService, blocks, deferredService, accountRepo, activity, resolver, connections)
	var routers *egress.TLSFingerprintRouterService
	if len(tlsFPRouterServices) > 0 {
		routers = tlsFPRouterServices[0]
	}
	text := openAITextExecution(cfg, accountRepo, identity, executionCredentials, httpUpstream, tlsFPProfileService, routers, settingService, grokExecutor, output, provideAnthropicPromptCache(), choices.OpenAIHTTPResponseStickyTTL, provideCompactExecutor(cfg))
	lineage := provideOpenAIEncryptedLineage(stateStore, choices)
	imagePolicy := provideOpenAIImageBridgePolicy(cfg, channelService)
	sockets := provideOpenAIWebSockets(cfg, connections, text, prompts, choices, lineage, imagePolicy, cache)
	responses := provideOpenAIResponses(text, sockets, choices, lineage, imagePolicy)
	var read func(context.Context) (bool, time.Duration)
	if settingService != nil {
		read = settingService.Moderation.GetCyberSessionBlockRuntime
	}
	source := &gatewayExecutionFixture{Text: text, Requests: text.Requests, Responses: responses, WebSockets: sockets, Grok: grokExecutor, Cache: cache, Planner: gatewayprovider.NewRoutePlanner(channelService), Blocks: session.NewCyberBlocks(session.AdaptCyberSessionBlockStore(cache), read, func(format string, args ...any) { logging.LegacyPrintf("service.openai_gateway", format, args...) })}
	source.Auxiliary = provideOpenAIAuxiliary(text, nil, activity)

	return source, choices, &gatewayhttp.RequestCredentialExecutor{Runtime: credentials}
}

// newEmptyCompatibleSelectionFixture 对应原零值执行入口，仍不配置任何账号来源。
func newEmptyCompatibleSelectionFixture() *selection.Compatible {
	return selection.NewCompatible(selection.CompatibleDependencies{}, selection.DefaultOptions())
}
