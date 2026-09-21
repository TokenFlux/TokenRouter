package app

import (
	"time"

	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/creative"

	batchhttp "github.com/TokenFlux/TokenRouter/internal/batchimage/httpapi"
	batchimageprovider "github.com/TokenFlux/TokenRouter/internal/batchimage/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"

	creativehttp "github.com/TokenFlux/TokenRouter/internal/creative/httpapi"
)

func provideS13BatchRegistry(cfg *config.Config) *batchimage.Registry[batchimageprovider.BatchImageProvider] {
	return batchimage.NewRegistry[batchimageprovider.BatchImageProvider](batchimageprovider.NewGeminiAPIBatchImageProvider(nil), batchimageprovider.NewVertexBatchImageProvider(batchVertexOptions(cfg), nil, nil, nil))
}
func provideS13CreativeHTTP(s *creative.Public, activity *taskRequestActivity) *creativehttp.CreativeHandler {
	h := creativehttp.NewCreativeHandler(s)
	h.BindActivity(activity.Enter)
	return h
}
func provideS13BatchHTTP(s *batchimage.Public, d *batchimage.Download, c *batchimage.Cleanup, activity *taskRequestActivity) *batchhttp.BatchImageHandler {
	h := batchhttp.NewBatchImageHandler(s, d, c, batchImageAccessPorts())
	h.BindActivity(activity.Enter)
	return h
}

// provideS13BatchDownload 直接绑定原生下载与任务账号读取，复用唯一供应商表。
func provideS13BatchDownload(repo batchimage.BatchImageRepository, accounts *accountpostgres.AccountStore, limiter batchimage.BatchImageDownloadLimiter, cfg *config.Config, registry *batchimage.Registry[batchimageprovider.BatchImageProvider]) *batchimage.Download {
	core := &batchimage.Download{Repo: repo, Limiter: limiter, ResolveProvider: (batchimageprovider.ResultAccess{Registry: registry, Accounts: accounts}).Download}
	if cfg != nil {
		core.Options = batchimage.DownloadOptions{MaxItems: cfg.BatchImage.MaxDownloadItemsZip, MaxBytes: cfg.BatchImage.MaxDownloadBytesPerRequest, Duration: time.Duration(cfg.BatchImage.MaxDownloadDurationSeconds) * time.Second}
	}
	return core
}

// provideS13BatchCleanup 保留原清理选项与观测，运行循环由模块持有。
func provideS13BatchCleanup(repo batchimage.BatchImageRepository, accounts *accountpostgres.AccountStore, cfg *config.Config, registry *batchimage.Registry[batchimageprovider.BatchImageProvider]) *batchimage.Cleanup {
	core := &batchimage.Cleanup{Repo: repo, Now: time.Now, Observe: creativeObserve, ResolveProvider: (batchimageprovider.ResultAccess{Registry: registry, Accounts: accounts}).Cleanup}
	if cfg != nil {
		core.Options = batchimage.CleanupOptions{InputRetention: time.Duration(cfg.BatchImage.InputRetentionAfterTerminalHours) * time.Hour, Interval: time.Duration(cfg.BatchImage.CleanupIntervalMinutes) * time.Minute, BatchSize: cfg.BatchImage.CleanupBatchSize}
	}
	return core
}

// batchCleanupRuntime 区分清理循环与任务消费循环的生命周期实例。
type batchCleanupRuntime struct{ *batchimage.Runtime }

func provideBatchCleanupRuntime(core *batchimage.Cleanup, cfg *config.Config) *batchCleanupRuntime {
	enabled := core != nil && core.Repo != nil && cfg != nil && cfg.BatchImage.Enabled && core.CleanupInterval() > 0
	return &batchCleanupRuntime{batchimage.NewRuntime("batch image cleanup", enabled, core.Run)}
}

// taskRequestActivity 等待提交、下载及管理请求结束，再停止 task worker 和共享存储。
type taskRequestActivity struct{ *lifecycle.Operations }

func provideS13TaskActivity(manager *lifecycle.Manager) *taskRequestActivity {
	activity := &taskRequestActivity{lifecycle.NewOperations("TaskRequestsAndDownloads")}
	manager.Register(lifecycle.Hook{Name: "TaskRequestsAndDownloads", StopOrder: 16, Stop: activity.StopContext})
	return activity
}
