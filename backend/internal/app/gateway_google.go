package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
)

// gateway_google.go constructs Gemini and Antigravity forwarding runtimes and
// their HTTP executors.
// Gemini and Antigravity forwarding share the application's transport and
// request activity. Gemini rate-limit observation uses its daily quota reset as
// a parser fallback, while both runtimes receive startup response limits and
// platform-specific credential and health services.

// googleForwardOptions copies Google forwarding limits and destination policy
// from required cfg. A nonpositive response-read limit selects the
// configuration default, and AllowedHosts is copied so this option slice can be
// held separately.
func googleForwardOptions(cfg *config.Config) googleforward.Options {
	limit := cfg.Gateway.UpstreamResponseMaxBytes
	if limit <= 0 {
		limit = config.DefaultUpstreamResponseReadMaxBytes
	}
	return googleforward.Options{
		Configured:           true,
		LogErrorBody:         cfg.Gateway.UpstreamErrorBodyLog.Enabled,
		LogErrorBodyMaxBytes: cfg.Gateway.UpstreamErrorBodyLog.MaxBytes,
		DebugHeaders:         cfg.Gateway.Gemini.DebugResponseHeaders,
		ResponseReadLimit:    limit,
		MaxLineSize:          cfg.Gateway.MaxSSELineBytes,
		StreamInterval:       cfg.Gateway.Streaming.DataIdleTimeoutSeconds,
		URLAllowlistEnabled:  cfg.Security.URLAllowlist.Enabled,
		AllowInsecureHTTP:    cfg.Security.URLAllowlist.AllowInsecureHTTP,
		AllowedHosts:         append([]string(nil), cfg.Security.URLAllowlist.UpstreamHosts...),
		AllowPrivateHosts:    cfg.Security.URLAllowlist.AllowPrivateHosts,
	}
}

// provideGeminiForward assembles Gemini credentials, transport, quota
// observation, and activity tracking. cfg and activity are required; the reset-
// time parser receives a daily fallback computed at observation time in the
// Gemini quota location.
func provideGeminiForward(store gatewayadapter.ExecutionProviderStore, tokens *provider.GeminiTokenSource, health *provideradapter.UpstreamHealth, precheck *provider.GeminiPrecheck, transport httpclient.UpstreamTransport, filter *egress.CompiledHeaderFilter, cfg *config.Config, activity *gatewayRequestActivity, attributes *routing.ModelAttributeService) *googleforward.Gemini {
	daily := func() *int64 {
		// The reset parser expects a Unix-second timestamp for the quota deadline.
		value := provider.GeminiDailyResetTime(time.Now(), geminiQuotaLocation()).Unix()
		return &value
	}
	return &googleforward.Gemini{
		Attributes:   attributes,
		Options:      googleForwardOptions(cfg),
		Tokens:       tokens,
		Health:       health,
		Transport:    transport,
		HeaderFilter: filter,
		Enter:        activity.Enter,
		Errors: &provideradapter.GeminiErrorObserver{
			Other:          health,
			Precheck:       precheck,
			SetRateLimited: store.SetRateLimited,
			DailyReset:     daily,
			ResetTime:      func(body []byte) *int64 { return gemini.ParseGeminiRateLimitResetTime(body, daily) },
		},
	}
}

// provideGeminiExecutor exposes the assembled Gemini runtime to gateway HTTP
// execution.
func provideGeminiExecutor(runtime *googleforward.Gemini) *gatewayhttp.GeminiExecutor {
	return &gatewayhttp.GeminiExecutor{Runtime: runtime}
}

// provideAntigravityForward assembles Antigravity execution with retry, health,
// and runtime setting readers. cfg, readers, and activity are required by
// construction; credential and sticky-session dependencies are shared with the
// application graph.
func provideAntigravityForward(store gatewayadapter.ExecutionProviderStore, tokens *provider.AntigravityTokenSource, retry *provideradapter.AntigravityRetry, observer *provideradapter.AntigravityErrorObserver, transport httpclient.UpstreamTransport, readers *gatewayadapter.RuntimeReaders, sticky session.GatewayCache, cfg *config.Config, activity *gatewayRequestActivity) *googleforward.Antigravity {
	return &googleforward.Antigravity{
		Attributes: readers.Attributes,
		Options:    googleForwardOptions(cfg),
		Tokens:     tokens,
		Retry:      retry,
		Errors:     observer,
		Store:      store,
		Transport:  transport,
		Gateway:    readers.Gateway,
		Routing:    readers.Routing,
		Sticky:     sticky,
		Enter:      activity.Enter,
	}
}

// provideAntigravityExecutor exposes Antigravity forwarding through the gateway
// HTTP executor.
func provideAntigravityExecutor(runtime *googleforward.Antigravity) *gatewayhttp.AntigravityExecutor {
	return &gatewayhttp.AntigravityExecutor{Runtime: runtime}
}
