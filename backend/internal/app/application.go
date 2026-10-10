package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
)

// BuildInfo 接收进程入口传入的构建信息，各模块取得自己需要的字段。
type BuildInfo struct {
	Version   string
	BuildType string
}
type Application struct {
	Server    *http.Server
	lifecycle *lifecycle.Manager
}

// Initialize 构造应用依赖，后台 worker 由 Run 启动。构造失败时释放已取得的连接和绑定。
// @project-doc docs/architecture/system_architecture.md#dependency_layers
func Initialize(ctx context.Context, cfg *config.Config, buildInfo BuildInfo, restarter *lifecycle.Restarter) (_ *Application, err error) {
	manager := lifecycle.New(func(name, event string, err error) {
		if name == "LogBackend" && event == "stopped" {
			return
		}
		// 日志级别根据启停结果确定，任务名称按普通字段记录。
		if err != nil {
			logging.S().Errorf("[Lifecycle] %s %s err=%v", event, name, err)
		} else {
			logging.S().Infof("[Lifecycle] %s %s err=<nil>", event, name)
		}
	})
	manager.Register(lifecycle.Hook{Name: "LogBackend", StartOrder: -2000, StopOrder: 1000, Stop: func(context.Context) error { logging.Sync(); return logging.CloseFiles() }})
	tasks := installBackgroundTasks(manager)
	defer func() {
		if err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err = errors.Join(err, manager.Rollback(cleanupCtx))
		}
	}()
	return initializeApplication(ctx, cfg, buildInfo, manager, restarter, tasks)
}

// provideApplication 登记 HTTP 请求跟踪和运维 WebSocket 的关闭任务。
func provideApplication(
	server *http.Server,
	manager *lifecycle.Manager,
	_ *runtimeReady,
	opsService *ops.OpsService,
	_ *ops.ErrorLogQueue,
) *Application {
	lifecycle.TrackRequests(server, manager)
	manager.Register(lifecycle.Hook{
		Name:       "OpsWSRuntime",
		StartOrder: 983,
		StopOrder:  17,
		Stop: func(context.Context) error {
			opsService.Realtime().Stop()
			return nil
		},
	})
	return &Application{Server: server, lifecycle: manager}
}

// installBackgroundTasks 登记各模块共用的后台任务跟踪器。
func installBackgroundTasks(manager *lifecycle.Manager) *lifecycle.Tasks {
	tasks := lifecycle.NewTasks()
	manager.Register(lifecycle.Hook{
		Name:       "ApplicationBackgroundTasks",
		StartOrder: 932,
		StopOrder:  68,
		Stop:       tasks.Stop,
	})
	return tasks
}

// Run 先启动后台任务，再开放 HTTP，返回前按超时预算清理资源。
// @project-doc docs/architecture/system_architecture.md#startup_and_shutdown
func (a *Application) Run(ctx context.Context) (err error) {
	started := false
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if started {
			err = errors.Join(err, a.lifecycle.Stop(cleanupCtx))
		} else {
			err = errors.Join(err, a.lifecycle.Rollback(cleanupCtx))
		}
	}()
	result := make(chan error, 1)
	go func() { result <- a.lifecycle.Start(ctx) }()
	select {
	case err = <-result:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	started = true
	return lifecycle.Serve(ctx, a.Server)
}

// Cleanup 供尚未运行的应用和外部测试释放构造资源。
func (a *Application) Cleanup(ctx context.Context) error { return a.lifecycle.Stop(ctx) }
