package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
)

// googleForwardOptions 投影原有响应、日志和目标限制，不读取运行时设置。
func googleForwardOptions(cfg *config.Config) googleforward.Options {
	limit := cfg.Gateway.UpstreamResponseReadMaxBytes
	if limit <= 0 {
		limit = config.DefaultUpstreamResponseReadMaxBytes
	}
	return googleforward.Options{
		Configured:           true,
		LogErrorBody:         cfg.Gateway.LogUpstreamErrorBody,
		LogErrorBodyMaxBytes: cfg.Gateway.LogUpstreamErrorBodyMaxBytes,
		DebugHeaders:         cfg.Gateway.GeminiDebugResponseHeaders,
		ResponseReadLimit:    limit,
		MaxLineSize:          cfg.Gateway.MaxLineSize,
		StreamInterval:       cfg.Gateway.StreamDataIntervalTimeout,
		StreamKeepalive:      cfg.Gateway.StreamKeepaliveInterval,
		URLAllowlistEnabled:  cfg.Security.URLAllowlist.Enabled,
		AllowInsecureHTTP:    cfg.Security.URLAllowlist.AllowInsecureHTTP,
		AllowedHosts:         append([]string(nil), cfg.Security.URLAllowlist.UpstreamHosts...),
		AllowPrivateHosts:    cfg.Security.URLAllowlist.AllowPrivateHosts,
	}
}

// provideGeminiForward 绑定唯一凭据、配额和健康观察；构造期间不发起请求。
func provideGeminiForward(store provider.ExecutionAccountStore, tokens *account.GeminiTokenSource, health *accountprovider.UpstreamHealth, precheck *account.GeminiPrecheck, transport httpclient.UpstreamTransport, filter *egress.CompiledHeaderFilter, cfg *config.Config, activity *gatewayRequestActivity) *googleforward.Gemini {
	daily := func() *int64 {
		value := account.GeminiDailyResetTime(time.Now(), geminiQuotaLocation()).Unix()
		return &value
	}
	return &googleforward.Gemini{
		Options:      googleForwardOptions(cfg),
		Tokens:       tokens,
		Health:       health,
		Transport:    transport,
		HeaderFilter: filter,
		Enter:        activity.Enter,
		Errors: &accountprovider.GeminiErrorObserver{
			Other:          health,
			Precheck:       precheck,
			SetRateLimited: store.SetRateLimited,
			DailyReset:     daily,
			ResetTime:      func(body []byte) *int64 { return gemini.ParseGeminiRateLimitResetTime(body, daily) },
		},
	}
}
func provideGeminiExecutor(runtime *googleforward.Gemini) *gatewayhttp.GeminiExecutor {
	return &gatewayhttp.GeminiExecutor{Runtime: runtime}
}

// provideAntigravityForward 与账号探测共享重试和健康实例，应用只投影动态读取端口。
func provideAntigravityForward(store provider.ExecutionAccountStore, tokens *account.AntigravityTokenSource, retry *accountprovider.AntigravityRetry, observer *accountprovider.AntigravityErrorObserver, transport httpclient.UpstreamTransport, readers *provider.RuntimeReaders, sticky session.GatewayCache, cfg *config.Config, activity *gatewayRequestActivity) *googleforward.Antigravity {
	return &googleforward.Antigravity{
		Options:   googleForwardOptions(cfg),
		Tokens:    tokens,
		Retry:     retry,
		Errors:    observer,
		Store:     store,
		Transport: transport,
		Gateway:   readers.Gateway,
		Routing:   readers.Routing,
		Sticky:    sticky,
		Enter:     activity.Enter,
	}
}
func provideAntigravityExecutor(runtime *googleforward.Antigravity) *gatewayhttp.AntigravityExecutor {
	return &gatewayhttp.AntigravityExecutor{Runtime: runtime}
}
