package app

import (
	"context"
	"os"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/messageforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/requestdebug"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// 调试输出共享一个句柄；请求与完成工作退出后再关闭。
func provideGatewayRequestDebug(manager *lifecycle.Manager) *requestdebug.Trace {
	trace := requestdebug.New(os.Getenv("SUB2API_DEBUG_GATEWAY_BODY"), os.Getenv("SUB2API_DEBUG_CLAUDE_MIMIC"))
	manager.Register(lifecycle.Hook{Name: "GatewayRequestDebug", StopOrder: 90, Stop: func(context.Context) error { return trace.Close() }})
	return trace
}

// provideMessagesExecution 固定绑定 Messages、兼容转换和计数的原生依赖。
// @project-doc docs/architecture/system_architecture.md#dependency_layers
func provideMessagesExecution(credentials *account.MessageCredentialSource, fingerprint *anthropic.RequestFingerprint, transport httpclient.UpstreamTransport, health *accountprovider.UpstreamHealth, tls *egressprovider.TLSProfiles, readers *provider.RuntimeReaders, prices *billing.PriceResolver, search *searchtools.Emulator, activity *gatewayRequestActivity, debug *requestdebug.Trace, accounts provider.ExecutionAccountStore, deferred *account.DeferredService, cfg *config.Config, filter *egress.CompiledHeaderFilter, channels *routing.ChannelService) *gatewayhttp.MessagesExecutor {
	deps := messageforward.Dependencies{Credentials: credentials, Fingerprint: fingerprint, Transport: transport, Health: health, TLS: tls, Prices: prices, Search: search, AccountState: accounts, Deferred: deferred}
	if channels != nil {
		deps.Channels = channels
	}
	if debug != nil {
		deps.Debug = debug
	}
	if readers != nil {
		deps.Settings = readers.Gateway
	}
	if activity != nil {
		deps.Enter = activity.Enter
	}
	return gatewayhttp.NewMessagesExecutor(messageforward.NewRuntime(deps, messageExecutionOptions(cfg)), filter)
}

// 静态参数只在装配时投影，动态设置保留请求内的读取位置。
func messageExecutionOptions(cfg *config.Config) messageforward.Options {
	options := messageforward.Options{ResponseReadLimit: config.DefaultUpstreamResponseReadMaxBytes}
	if cfg == nil {
		return options
	}
	options.Configured = true
	options.InjectAPIKeyBeta = cfg.Gateway.InjectBetaForAPIKey
	options.LogErrorBody = cfg.Gateway.LogUpstreamErrorBody
	options.LogErrorBodyMaxBytes = cfg.Gateway.LogUpstreamErrorBodyMaxBytes
	options.FailoverOn400 = cfg.Gateway.FailoverOn400
	options.GeminiDebugHeaders = cfg.Gateway.GeminiDebugResponseHeaders
	options.MaxLineSize = cfg.Gateway.MaxLineSize
	options.StreamInterval = time.Duration(cfg.Gateway.StreamDataIntervalTimeout) * time.Second
	options.StreamKeepalive = time.Duration(cfg.Gateway.StreamKeepaliveInterval) * time.Second
	if cfg.Gateway.UpstreamResponseReadMaxBytes > 0 {
		options.ResponseReadLimit = cfg.Gateway.UpstreamResponseReadMaxBytes
	}
	options.PreserveContentType = !cfg.Security.ResponseHeaders.Enabled
	options.URLAllowlistEnabled = cfg.Security.URLAllowlist.Enabled
	options.AllowInsecureHTTP = cfg.Security.URLAllowlist.AllowInsecureHTTP
	options.URLValidation = egress.ValidationOptions{AllowedHosts: cfg.Security.URLAllowlist.UpstreamHosts, RequireAllowlist: true, AllowPrivate: cfg.Security.URLAllowlist.AllowPrivateHosts}
	return options
}
