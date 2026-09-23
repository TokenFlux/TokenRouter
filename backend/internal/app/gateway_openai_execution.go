package app

import (
	"context"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	service "github.com/TokenFlux/TokenRouter/internal/service"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"

	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// provideOpenAIGatewayExecution 只连接旧执行原语与 app 持有的原生倍率、完成实例。
func provideOpenAIGatewayExecution(
	accountRepo gatewayprovider.ExecutionAccountStore,
	usageLogRepo usage.UsageLogRepository,

	cache session.GatewayCache,
	cfg *config.Config,
	schedulerSnapshot *scheduler.SnapshotService,
	concurrencyService *scheduler.ConcurrencyService,

	rateLimitService *service.RateLimitService,

	httpUpstream httpclient.UpstreamTransport,
	tlsFPProfileService *provider.TLSProfiles,
	deferredService *accountcore.DeferredService,
	openAITokenProvider *accountcore.OpenAITokenSource,
	grokTokenProvider *accountcore.GrokTokenSource,
	resolver *billing.PriceResolver,
	channelService *routing.ChannelService,

	settingService *gatewayprovider.RuntimeReaders,

	prompts *promptpolicy.Service,
	headerFilter *egress.CompiledHeaderFilter,
	stateStore session.OpenAIWSStateStore,

	recorders GatewayCompletionRecorders,
	executionCredentials *accountcore.OpenAIExecutionCredentials,
	taskCoordinator *accountcore.OpenAITaskCoordinator,
	tlsFPRouterServices ...*egress.TLSFingerprintRouterService,
) *service.OpenAIGatewayService {
	source := service.NewOpenAIGatewayService(
		accountRepo,
		usageLogRepo,

		cache,
		cfg,
		schedulerSnapshot,
		concurrencyService,

		rateLimitService,

		httpUpstream,
		tlsFPProfileService,
		deferredService,
		executionCredentials,
		grokTokenProvider,
		resolver,
		channelService,

		settingService,

		prompts,
		headerFilter,
		stateStore,

		tlsFPRouterServices...,
	)
	// 构造完成后绑定原运行阻断回调，仍在任何后台或请求启动前完成。
	if openAITokenProvider != nil {
		openAITokenProvider.Block = func(record *accountcore.Record, until time.Time, reason string) {
			source.BlockAccountScheduling(gatewayprovider.NewExecutionAccount(record), until, reason)
		}
	}
	source.BindAgentIdentity(gatewayprovider.NewExecutionAgentIdentity(taskCoordinator, accountRepo, func(ctx context.Context, value *accountcore.Record) (string, error) {
		return accountprovider.RegisterAgentIdentityTask(ctx, value, "https://auth.openai.com/api/accounts")
	}, source.InvalidateAgentIdentityWSConnections))
	source.BindCompletionRecorder(recorders.OpenAI)
	source.BindPromptCacheBindings(session.NewAnthropicPromptCache(time.Now))
	return source
}
