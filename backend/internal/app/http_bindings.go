package app

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	gatewayhttpapi "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	redisinfra "github.com/TokenFlux/TokenRouter/internal/infra/redis"
	"github.com/TokenFlux/TokenRouter/internal/pkg/websearch"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	routinghttpapi "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/server"
	middleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/web"

	"github.com/redis/go-redis/v9"
)

// provideRouterRuntime 只装配旧业务的公开投影与 HTTP 能力，业务解释留 S10/S15。
func provideRouterRuntime(settingService *service.SettingService, store *settings.Store, redisClient *redis.Client, manager *lifecycle.Manager) (*server.RouterRuntime, error) {

	var origins atomic.Pointer[[]string]
	empty := []string{}
	origins.Store(&empty)
	refresh := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		next, err := settingService.GetFrameSrcOrigins(ctx)
		if err == nil {
			origins.Store(&next)
		}
	}
	rt := &server.RouterRuntime{FrameOrigins: func() []string { return *origins.Load() }}
	endpoints := make(map[protocol.ProtocolID]string)
	for _, entry := range gatewayhttpapi.ProtocolEndpoints() {
		endpoints[entry.ID] = entry.Endpoint
	}
	rt.ProtocolCatalog = routinghttpapi.NewProtocolCatalogHandler(endpoints)
	notify := refresh
	if web.HasEmbeddedFrontend() {
		frontend, err := web.NewFrontendServer(settingService)
		if err != nil {
			return nil, err
		}
		rt.Frontend = frontend.Middleware()
		notify = func() { frontend.InvalidateCache(); refresh() }
	}
	manager.Register(lifecycle.Hook{Name: "HTTPSettingsInitialization", StartOrder: 182, StopOrder: 800, Start: func(context.Context) error {

		// Wire up websearch Manager builder so it initializes on startup and rebuilds on config save.
		settingService.SetWebSearchManagerBuilder(context.Background(), func(cfg *service.WebSearchEmulationConfig, proxyURLs map[int64]string) {
			if cfg == nil || !cfg.Enabled || len(cfg.Providers) == 0 {
				service.SetWebSearchManager(nil)
				return
			}
			configs := make([]websearch.ProviderConfig, 0, len(cfg.Providers))
			for _, p := range cfg.Providers {
				if p.APIKey == "" {
					continue
				}
				pc := websearch.ProviderConfig{
					Type:       p.Type,
					APIKey:     p.APIKey,
					QuotaLimit: derefInt64(p.QuotaLimit),
					ExpiresAt:  p.ExpiresAt,
				}
				if p.SubscribedAt != nil {
					pc.SubscribedAt = p.SubscribedAt
				}
				if p.ProxyID != nil {
					pc.ProxyID = *p.ProxyID
					if u, ok := proxyURLs[*p.ProxyID]; ok {
						pc.ProxyURL = u
					} else {
						// Proxy configured but not found — skip this provider to prevent direct connection.
						slog.Warn("websearch: proxy not found for provider, skipping",
							"provider", p.Type, "proxy_id", *p.ProxyID)
						continue
					}
				}
				configs = append(configs, pc)
			}
			service.SetWebSearchManager(websearch.NewManager(configs, redisClient))
		})

		refresh()
		return nil
	}})

	unsubscribe := store.Subscribe(notify)
	manager.Register(lifecycle.Hook{Name: "SettingsHTTPNotification", StopOrder: 800, Stop: func(context.Context) error { unsubscribe(); return nil }})
	counter := redisinfra.NewFixedWindowLimiter(redisClient, "rate_limit:")
	rt.AuthLimiter = middleware.NewRateLimiter(counter)
	var panelCounter *middleware.RateLimiter
	if redisClient != nil {
		panelCounter = middleware.NewRateLimiter(counter)
	}
	rt.PanelLimiter = middleware.NewPanelRateLimiter(panelCounter, settingService)
	return rt, nil
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
