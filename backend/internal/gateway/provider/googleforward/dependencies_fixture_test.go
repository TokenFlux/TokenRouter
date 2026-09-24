package googleforward_test

import (
	"log/slog"
	"os"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
)

// 夹具只组合原生依赖，规则、缓存与流状态仍来自实际拥有者。
type geminiDependencies struct {
	cfg                  *googleforward.Options
	accountRepo          provider.ExecutionAccountStore
	tokenProvider        *account.GeminiTokenSource
	httpUpstream         httpclient.UpstreamTransport
	healthObserver       *accountprovider.UpstreamHealth
	quotaPrecheck        *account.GeminiPrecheck
	responseHeaderFilter *egress.CompiledHeaderFilter
}
type antigravityDependencies struct {
	options          googleforward.Options
	settingService   *provider.RuntimeReaders
	accountRepo      provider.ExecutionAccountStore
	tokenProvider    *account.AntigravityTokenSource
	httpUpstream     httpclient.UpstreamTransport
	healthObserver   *accountprovider.UpstreamHealth
	cache            session.GatewayCache
	internal500Cache account.Internal500CounterCache
}

func fixtureOptions(cfg *googleforward.Options) googleforward.Options {
	if cfg == nil {
		return googleforward.Options{ResponseReadLimit: 128 << 20}
	}
	out := *cfg
	out.Configured = true
	if out.ResponseReadLimit <= 0 {
		out.ResponseReadLimit = 128 << 20
	}
	return out
}

func geminiQuotaLocation() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return time.FixedZone("PST", -8*3600)
	}
	return loc
}
func nextGeminiDailyResetUnix() *int64 {
	value := account.GeminiDailyResetTime(time.Now(), geminiQuotaLocation()).Unix()
	return &value
}
func parseGeminiReset(body []byte) *int64 {
	return gemini.ParseGeminiRateLimitResetTime(body, nextGeminiDailyResetUnix)
}
func newGeminiFixture(d geminiDependencies) *googleforward.Gemini {
	health := &accountprovider.GeminiErrorObserver{
		Other:      d.healthObserver,
		Precheck:   d.quotaPrecheck,
		DailyReset: nextGeminiDailyResetUnix,
		ResetTime:  parseGeminiReset,
	}
	if d.accountRepo != nil {
		health.SetRateLimited = d.accountRepo.SetRateLimited
	}
	return &googleforward.Gemini{
		Options:      fixtureOptions(d.cfg),
		Tokens:       d.tokenProvider,
		Health:       d.healthObserver,
		Errors:       health,
		Transport:    d.httpUpstream,
		HeaderFilter: d.responseHeaderFilter,
	}
}
func newAntigravityFixture(d antigravityDependencies) *googleforward.Antigravity {
	o := d.options
	health := &account.AntigravityHealth{
		Store:     d.accountRepo,
		Counter:   d.internal500Cache,
		ModelKeys: accountprovider.AntigravityModelLimitKeys,
		Error:     slog.Error,
		Warn:      slog.Warn,
		Info:      slog.Info,
		Logf:      func(format string, args ...any) { logging.LegacyPrintf("service.antigravity_gateway", format, args...) },
	}
	retry := &accountprovider.AntigravityRetry{
		Health: health,
		BaseURL: func(value *account.Record) string {
			return antigravity.ResolveAntigravityForwardBaseURL(os.Getenv("GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL"), accountprovider.AntigravityPaidTier(value))
		},
		BodyLimit: func() int64 {
			limit := int64(512 << 10)
			if o.LogErrorBody && o.LogErrorBodyMaxBytes > int(limit) {
				limit = int64(o.LogErrorBodyMaxBytes)
			}
			return limit
		},
		TruncateString: logredact.TruncateUTF8,
		SafeURL:        logredact.SafeUpstreamURL,
	}
	if d.healthObserver != nil {
		retry.Policy = d.healthObserver.Core
	}
	observer := &accountprovider.AntigravityErrorObserver{
		Health:         health,
		Other:          d.healthObserver,
		LogConfig:      o.LogConfig,
		TruncateString: logredact.TruncateUTF8,
		ResetTime:      parseGeminiReset,
		DefaultDuration: func() time.Duration {
			minutes := 0
			return accountprovider.AntigravityFallbackDuration(minutes, os.Getenv("GATEWAY_ANTIGRAVITY_FALLBACK_COOLDOWN_SECONDS"))
		},
	}
	if d.accountRepo != nil {
		observer.SetRateLimited = d.accountRepo.SetRateLimited
	}
	r := &googleforward.Antigravity{
		Options:   o,
		Tokens:    d.tokenProvider,
		Retry:     retry,
		Errors:    observer,
		Store:     d.accountRepo,
		Transport: d.httpUpstream,
		Sticky:    d.cache,
	}
	if d.settingService != nil {
		r.Gateway = d.settingService.Gateway
		r.Routing = d.settingService.Routing
	}
	return r
}
