package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/transport"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/search"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// ProvideGatewaySearchTools 由组合根为请求链持有唯一工具编排器；不创建新 Manager 或配额状态。
func ProvideGatewaySearchTools(gateway *service.GatewayService, settings *search.ConfigService, channels *routing.ChannelService) *searchtools.Emulator {
	runtime := gatewayprovider.NewSearchTools(settings, channels)
	gateway.BindSearchToolsRuntime(runtime)
	return runtime
}

// standaloneSearchExecution 只把原有单次选号与执行结果投影给原生搜索入口。
type standaloneSearchExecution struct {
	selector *selection.Compatible
	executor *gatewayprovider.GrokSearchExecutor
}

func (p standaloneSearchExecution) Select(ctx context.Context, group int64, model string, excluded map[int64]struct{}) (gatewayhttp.StandaloneSearchTarget, searchtools.Selection, bool, error) {
	selected, _, err := p.selector.SelectAccountWithSchedulerForCapability(ctx, &group, "", "", model, excluded, egress.OpenAIUpstreamTransportHTTPSSE, account.OpenAIEndpointCapabilityTextGeneration, false, false, capability.PlatformGrok)
	if err != nil {
		return nil, searchtools.Selection{}, false, err
	}
	if selected == nil || selected.Account == nil {
		return nil, searchtools.Selection{}, false, nil
	}
	target := standaloneSearchTarget{source: p.executor, account: gatewayprovider.ExecutionRecord(selected.Account)}
	return target, searchtools.Selection{AccountID: selected.Account.Record.ID, Acquired: selected.Acquired, Release: selected.ReleaseFunc, WaitPlan: selected.WaitPlan}, true, nil
}

// 目标只在该请求内保存已选实例，完成入队时才投影独立快照。
type standaloneSearchTarget struct {
	source  *gatewayprovider.GrokSearchExecutor
	account *account.Record
}

func (t standaloneSearchTarget) CompletionRecord() *account.Record {
	return t.account
}
func (t standaloneSearchTarget) Execute(ctx context.Context, body []byte) ([]byte, error) {
	return t.source.Execute(ctx, t.account, body)
}

// ProvideGatewaySearchHTTP 直接构造原生入口，沿用唯一选号、资金、审核和完成运行时。
func ProvideGatewaySearchHTTP(executor *gatewayprovider.GrokSearchExecutor, selector *selection.Compatible, funding *admission.FundingAdmission, concurrency *scheduler.ConcurrencyService, keys *apikey.APIKeyService, moderator *moderation.ContentModerationService, workers *completion.UsageRecordWorkerPool, cfg *config.Config, _ *searchtools.Emulator, activity *gatewayRequestActivity, recorders GatewayCompletionRecorders) *gatewayhttp.SearchHandler {
	interval := time.Duration(0)
	if cfg != nil {
		interval = time.Duration(cfg.Concurrency.PingInterval) * time.Second
	}
	ports := gatewayhttp.SearchPorts{Selector: standaloneSearchExecution{selector, executor}, Funding: funding, Concurrency: gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatClaude, interval), Recorder: recorders.Forward, Workers: workers}
	if keys != nil {
		ports.Keys = keys
	}
	if moderator != nil {
		ports.Moderation = moderator
	}
	result := gatewayhttp.NewSearchHandler(ports)
	if activity != nil {
		result.BindRequestActivity(activity.Enter)
	}
	return result
}

// provideGrokSearchExecutor 共享已有传输，动态地址仍在每次请求的原读取时点取得。
func provideGrokSearchExecutor(client *transport.Client, readers *gatewayprovider.RuntimeReaders) *gatewayprovider.GrokSearchExecutor {
	out := &gatewayprovider.GrokSearchExecutor{Transport: client}
	if readers != nil {
		out.DefaultBaseURL = func() string {
			return gatewayprovider.GrokBaseURLForMode(readers.Gateway.GetGrokDefaultBaseURLMode(context.Background()))
		}
	}
	return out
}
