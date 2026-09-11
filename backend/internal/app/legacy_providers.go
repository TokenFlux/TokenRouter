package app

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/app/bootstrap"
	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/handler"
	"github.com/TokenFlux/TokenRouter/internal/handler/admin"
	"github.com/TokenFlux/TokenRouter/internal/repository"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"net/http"
	"time"
)

// 以下仅投影旧图需要的绑定；随对应模块迁移删除旧 import。
func providePrivacyClientFactory() service.PrivacyClientFactory {
	return repository.CreatePrivacyReqClient
}
func provideServiceBuildInfo(info BuildInfo) service.BuildInfo {
	return service.BuildInfo{Version: info.Version, BuildType: info.BuildType}
}
func provideHandlerBuildInfo(info BuildInfo) handler.BuildInfo {
	return handler.BuildInfo{Version: info.Version, BuildType: info.BuildType}
}
func provideSecretEncryptor(cfg *config.Config) (service.SecretEncryptor, error) {
	return bootstrap.NewAESEncryptor(cfg)
}
func provideSettingsStore(repo service.SettingRepository) *settings.Store { return settings.New(repo) }
func provideAnnouncementUsers(repo service.UserRepository) site.UserReader {
	return &legacybridge.AnnouncementUsers{Repository: repo}
}
func provideAnnouncementSubscriptions(repo service.UserSubscriptionRepository) site.SubscriptionReader {
	return &legacybridge.AnnouncementSubscriptions{Repository: repo}
}
func provideAnnouncementExpiry(repo site.AnnouncementRepository) *site.AnnouncementExpiryService {
	return site.NewAnnouncementExpiryService(repo, time.Minute)
}
func provideRestartRequester(restarter *lifecycle.Restarter) admin.RestartRequester { return restarter }
func provideApplication(server *http.Server, manager *lifecycle.Manager, _ *runtimeReady) *Application {
	lifecycle.TrackRequests(server, manager)
	manager.Register(lifecycle.Hook{Name: "OpsWSRuntime", StartOrder: 983, StopOrder: 17, Stop: func(context.Context) error { admin.StopOpsWSRuntime(); return nil }})
	manager.Register(lifecycle.Hook{Name: "OpsErrorLogWorkers", StartOrder: 924, StopOrder: 76, Stop: handler.ShutdownOpsErrorLogWorkers})
	return &Application{Server: server, lifecycle: manager}
}

// installLegacyBackground 仅绑定旧调用方的技术完成端口，业务规则不进入 app。
func installLegacyBackground(manager *lifecycle.Manager) *lifecycle.Tasks {
	tasks := lifecycle.NewTasks()
	restore := service.SetBackgroundTaskRunner(tasks)
	manager.Register(lifecycle.Hook{Name: "LegacyBackgroundTasks", StartOrder: 932, StopOrder: 68, Stop: tasks.Stop})
	manager.Register(lifecycle.Hook{Name: "LegacyBackgroundBinding", StartOrder: -998, StopOrder: 850, Stop: func(context.Context) error { restore(); return nil }})
	return tasks
}
