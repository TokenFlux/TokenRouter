package app

import (
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/service"
	usage "github.com/TokenFlux/TokenRouter/internal/usage"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"

	"context"
	"log/slog"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
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
	channelService *routing.ChannelService,
	resolver *billing.PriceResolver,

	headerFilter *egress.CompiledHeaderFilter,
) (*service.GatewayService, *selection.Generic) {
	source := service.NewGatewayService(accountRepo, cache, cfg,
		healthObserver, identityService, httpUpstream,

		deferredService, messageCredentials,

		digestStore, settingService, tlsFPProfileService, channelService,

		resolver,
		headerFilter,
	)
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
			Cache:       cache,
			Concurrency: concurrencyService,
			Health:      healthObserver,
			Channels:    channelService,

			Feedback: feedback,
		},
		Window:                  window,
		WindowPrefetchAvailable: windowCostCache != nil && usageLogRepo != nil,
		RPM:                     rpmCache,

		Sessions:        sessionLimitCache,
		SetAccountError: write,
	}, selectionOptions(cfg))
	return source, choices
}

// newEmptyGenericSelectionFixture 保留零值入口的缺省预算，不配置额外账号或窗口来源。
func newEmptyGenericSelectionFixture() *selection.Generic {
	return selection.NewGeneric(selection.GenericDependencies{}, selection.DefaultOptions())
}
