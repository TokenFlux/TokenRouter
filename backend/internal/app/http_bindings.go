package app

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/site"

	sitehttp "github.com/TokenFlux/TokenRouter/internal/site/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	gatewayhttpapi "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	redisinfra "github.com/TokenFlux/TokenRouter/internal/infra/redis"
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
func provideRouterRuntime(public *site.PublicService, pages *sitehttp.PageHandler, settingService *service.SettingService, store *settings.Store, redisClient *redis.Client, manager *lifecycle.Manager) (*server.RouterRuntime, error) {

	var origins atomic.Pointer[[]string]
	empty := []string{}
	origins.Store(&empty)
	refresh := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		next, err := public.GetFrameSrcOrigins(ctx)
		if err == nil {
			origins.Store(&next)
		}
	}
	rt := &server.RouterRuntime{Pages: pages, FrameOrigins: func() []string { return *origins.Load() }}
	endpoints := make(map[protocol.ProtocolID]string)
	for _, entry := range gatewayhttpapi.ProtocolEndpoints() {
		endpoints[entry.ID] = entry.Endpoint
	}
	rt.ProtocolCatalog = routinghttpapi.NewProtocolCatalogHandler(endpoints)
	notify := refresh
	if web.HasEmbeddedFrontend() {
		frontend, err := web.NewFrontendServer(public)
		if err != nil {
			return nil, err
		}
		rt.Frontend = frontend.Middleware()
		notify = func() { frontend.InvalidateCache(); refresh() }
	}
	manager.Register(lifecycle.Hook{Name: "HTTPSettingsInitialization", StartOrder: 182, StopOrder: 800, Start: func(context.Context) error {

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
