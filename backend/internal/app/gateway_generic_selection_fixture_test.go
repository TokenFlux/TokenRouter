package app

import (
	"context"
	"log/slog"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/search"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// newGenericExecutionAndSelectionFixture 显式组合原执行入口与原生选择，不复制窗口或调度规则。
func newGenericExecutionAndSelectionFixture(
	accountRepo gatewayprovider.ExecutionAccountStore,
	groupRepo routing.GroupRepository, usageLogRepo usage.UsageLogRepository,

	cache session.GatewayCache,
	cfg *config.Config,
	schedulerSnapshot *scheduler.SnapshotService,
	concurrencyService *scheduler.ConcurrencyService,

	healthObserver *accountprovider.UpstreamHealth,
	identityService *claude.RequestFingerprint,
	httpUpstream httpclient.UpstreamTransport, deferredService *accountcore.DeferredService,

	messageCredentials *accountcore.MessageCredentialSource,
	sessionLimitCache scheduler.SessionLimitCache,
	windowCostCache billing.WindowCostCache,
	rpmCache scheduler.RPMCache,
	digestStore *session.DigestSessionStore,
	settingService *gatewayprovider.RuntimeReaders,
	tlsFPProfileService *provider.TLSProfiles,
	pricingConfigService *routing.PricingConfigService,
	resolver *billing.PriceResolver,

	headerFilter *egress.CompiledHeaderFilter,
) (*messageExecutionFixture, *selection.Generic, *gatewayhttp.MessagesExecutor) {
	var retryStore accountcore.RetryCooldownStore
	if accountRepo != nil {
		retryStore = fixtureRetryStore{accountRepo}
	}
	source := &messageExecutionFixture{
		Routes: gatewayprovider.NewRoutePlanner(pricingConfigService), Cache: cache, Digest: digestStore,
		Cooldown: accountcore.NewRetryCooldown(retryStore, accountcore.RetryCooldownOptions{}),
	}
	feedback := scheduler.NewRuntimeStats(time.Now)
	window := billing.NewWindowCostGuard(windowCostCache, gatewaytestkit.WindowCosts(usageLogRepo), billing.WindowCostGuardOptions{Now: time.Now, Stats: billing.SharedWindowCostMetrics(), Log: func(format string, args ...any) {
		logging.LegacyPrintf("service.gateway", format, args...)
	}, Debug: slog.Debug})
	var write func(context.Context, int64, string) error
	if accountRepo != nil {
		write = accountRepo.SetError
	}
	choices := selection.NewGeneric(selection.GenericDependencies{
		Reads: selection.Reads{
			Accounts: accountRepo,
			Groups:   groupRepo,
			Snapshot: provideSelectionSnapshots(schedulerSnapshot),
		},
		Shared: selection.Shared{
			Cache:         cache,
			Concurrency:   concurrencyService,
			Health:        healthObserver,
			GroupPolicies: pricingConfigService,

			Feedback: feedback,
		},
		Window:                  window,
		WindowPrefetchAvailable: windowCostCache != nil && usageLogRepo != nil,
		RPM:                     rpmCache,

		Sessions:        sessionLimitCache,
		SetAccountError: write,
	}, selectionOptions(cfg))
	var searchSettings *search.ConfigService
	if settingService != nil {
		searchSettings = settingService.Search
	}
	searchTools := ProvideGatewaySearchTools(searchSettings, pricingConfigService)
	messages := provideMessagesExecution(messageCredentials, identityService, httpUpstream, healthObserver, tlsFPProfileService, settingService, resolver, searchTools, nil, nil, accountRepo, deferredService, cfg, headerFilter, pricingConfigService)
	return source, choices, messages
}

// newEmptyGenericSelectionFixture 保留零值入口的缺省预算，不配置额外账号或窗口来源。
func newEmptyGenericSelectionFixture() *selection.Generic {
	return selection.NewGeneric(selection.GenericDependencies{}, selection.DefaultOptions())
}

// 夹具只保存原生依赖，规则和状态由各模块的生产实现持有。
type messageExecutionFixture struct {
	Routes   *gatewayprovider.RoutePlanner
	Cache    session.GatewayCache
	Digest   *session.DigestSessionStore
	Cooldown *accountcore.RetryCooldown
	Recorder *completion.Recorder
}
type fixtureRetryStore struct {
	gatewayprovider.ExecutionAccountStore
}

func (s fixtureRetryStore) GetByID(ctx context.Context, id int64) (*accountcore.Record, error) {
	value, err := s.ExecutionAccountStore.GetByID(ctx, id)
	return gatewayprovider.ExecutionRecord(value), err
}
