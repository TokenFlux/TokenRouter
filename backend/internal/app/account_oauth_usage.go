package app

import (
	"context"
	"log"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	usagepostgres "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

// provideOAuthUsageStats 直接组合原存储批量查询与同一个展示缓存。
func provideOAuthUsageStats(store *usagepostgres.Store, cache *account.OAuthUsageCache, calendar timezone.Calendar) *account.LocalUsageStatistics {
	return account.NewLocalUsageStatistics(newAccountLocalUsageStats(store), cache, account.LocalUsageStatisticsOptions{Now: time.Now, Today: calendar.Today, Log: log.Printf})
}

// provideOAuthUsageCore 直接绑定原生读取、平台查询和生命周期，不通过旧服务取回实例。
func provideOAuthUsageCore(store *accountpostgres.AccountStore, usageStore *usagepostgres.Store, cache *account.OAuthUsageCache, stats *account.LocalUsageStatistics, gemini *account.GeminiQuotaService, antigravity *account.AntigravityQuota, grokView *account.GrokQuotaView, grok *account.GrokQuotaService, openAI *account.OpenAIQuotaService, fetcher accountprovider.ClaudeUsageClient, fingerprints anthropic.FingerprintCache, profiles *egressprovider.TLSProfiles, transport httpclient.UpstreamTransport, settings *account.QuotaSettingsCache, gateway *service.OpenAIGatewayService, manager *lifecycle.Manager, coordinator *account.OpenAITaskCoordinator) *account.OAuthUsageService {
	taskOptions := account.OpenAITaskOptions{
		Read: store.GetByID,
		Register: func(ctx context.Context, value *account.Record) (string, error) {
			return accountprovider.RegisterAgentIdentityTask(ctx, value, "https://auth.openai.com/api/accounts")
		},
		Persist: func(ctx context.Context, value *account.Record, credentials map[string]any) error {
			_, err := account.PersistCredentials(ctx, store, value, credentials, slog.Warn)
			return err
		},
		Invalidate: gateway.InvalidateAgentIdentityWSConnections,
	}
	requests := &accountprovider.OAuthUsageTransport{
		Transport: transport, Profiles: profiles, Fingerprints: fingerprints,
		Tasks: coordinator, TaskOptions: taskOptions,
	}
	// 用量查询的会话保持原独立作用域，不与请求执行的会话缓存合并。
	sessions := accountprovider.NewQoderTokenProvider(qoder.SessionBuilder{})
	sessions.SetHTTPUpstream(transport, profiles)
	qoderQuery := &accountprovider.QoderUsage{Sessions: sessions, Transport: transport, Profiles: profiles}
	qoderOptions := qoderQuery.Options()
	qoderOptions.Enrich = accountprovider.EnrichUsageWithAccountError
	options := account.OAuthUsageOptions{
		Now: time.Now, Jitter: rand.Int64N, Log: log.Printf, Warn: slog.Warn,
		Qoder: qoderOptions,
		Anthropic: func(ctx context.Context, value *account.Record) (*account.ClaudeUsageResponse, error) {
			return requests.FetchAnthropic(ctx, value, fetcher)
		},
		OpenAI: account.OpenAIUsageOptions{
			Probe: requests.ProbeOpenAI,
			Shadow: func(ctx context.Context, id int64, now time.Time) (map[string]any, error) {
				value, err := openAI.QueryUsage(ctx, id)
				if err != nil {
					return nil, err
				}
				return account.BuildCodexSparkWindowExtraUpdates(value, now), nil
			},
		},
		OpenAIQuotaPause: func(ctx context.Context, value *account.Record, info *account.UsageInfo) {
			if value != nil && info != nil && value.IsOpenAI() {
				info.QuotaAutoPaused, _ = account.EvaluateQuotaAutoPause(value.Platform, value.Extra, settings.GetOpenAIQuotaAutoPauseSettings(ctx), time.Now())
			}
		},
		Antigravity: account.AntigravityUsageOptions{
			CanFetch: antigravity.CanFetch,
			Fetch: func(ctx context.Context, value *account.Record) (*account.UsageInfo, error) {
				result, err := antigravity.FetchQuota(ctx, value, antigravity.GetProxyURL(ctx, value))
				if result == nil {
					return nil, err
				}
				return result.UsageInfo, err
			},
			Degrade: accountprovider.AntigravityDegradedUsage, Enrich: accountprovider.EnrichUsageWithAccountError,
		},
		Gemini: account.GeminiUsageOptions{
			Location: geminiQuotaLocation, Quota: gemini.QuotaForAccount,
			Totals: func(ctx context.Context, id int64, start, end time.Time) (account.GeminiUsageTotals, error) {
				rows, err := usageStore.GetModelStatsWithFilters(ctx, start, end, 0, 0, id, 0, nil, nil, nil)
				if err != nil {
					return account.GeminiUsageTotals{}, err
				}
				values := projectGeminiModelUsage(rows)
				return account.AggregateGeminiUsage(values), nil
			},
		},
		Grok: account.GrokUsageOptions{
			Available: func() bool { return true }, StatsAvailable: func() bool { return true },
			Build: grokView.BuildUsageInfo, Enrich: accountprovider.EnrichUsageWithAccountError,
			Probe: func(ctx context.Context, id int64) (*account.GrokUsageProbe, error) {
				value, err := grok.ProbeBilling(ctx, id)
				if value == nil {
					return nil, err
				}
				return &account.GrokUsageProbe{Billing: value.Billing, LocalUsage24h: value.LocalUsage24h, LocalUsage7d: value.LocalUsage7d, LocalUsageMonthly: value.LocalUsageMonthly}, err
			},
		},
	}
	core := account.NewOAuthUsageService(store, cache, stats, options)
	manager.Register(lifecycle.Hook{Name: "AccountOAuthUsage", StopOrder: 25, Stop: core.StopContext})
	return core
}

// geminiQuotaLocation 保留原洛杉矶日界及加载失败后的固定时区。
func geminiQuotaLocation() *time.Location {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return time.FixedZone("PST", -8*3600)
	}
	return location
}
