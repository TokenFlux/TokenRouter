// Package app 是生产应用的唯一组合根。
package app

import (
	"context"
	"errors"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"net/http"
	"time"
)

// BuildInfo 仅供进程入口传入，模块通过投影取得所需字段。
type BuildInfo struct {
	Version   string
	BuildType string
}
type Application struct {
	Server    *http.Server
	lifecycle *lifecycle.Manager
}

// Initialize 不启动后台 worker；构造失败时释放此前取得的连接与绑定。
// @project-doc docs/architecture/system_architecture.md#dependency_layers
func Initialize(ctx context.Context, cfg *config.Config, buildInfo BuildInfo, restarter *lifecycle.Restarter) (_ *Application, err error) {
	manager := lifecycle.New(func(name, event string, err error) {
		if name == "LogBackend" && event == "stopped" {
			return
		}
		// 生命周期等级取决于实际结果，不让 ErrorPassthrough 等任务名被旧日志启发式误判。
		if err != nil {
			logging.S().Errorf("[Lifecycle] %s %s err=%v", event, name, err)
		} else {
			logging.S().Infof("[Lifecycle] %s %s err=<nil>", event, name)
		}
	})
	manager.Register(lifecycle.Hook{Name: "LogBackend", StartOrder: -2000, StopOrder: 1000, Stop: func(context.Context) error { logging.Sync(); return logging.CloseFiles() }})
	tasks := installLegacyBackground(manager)
	previousCoordinator := idempotency.DefaultIdempotencyCoordinator()
	manager.Register(lifecycle.Hook{Name: "DefaultIdempotencyCoordinator", StartOrder: -999, StopOrder: 850, Stop: func(context.Context) error {
		idempotency.SetDefaultIdempotencyCoordinator(previousCoordinator)
		return nil
	}})
	restore := idempotency.SetObserver(idempotency.ObserverFunc(func(component, message string) { logging.LegacyPrintf(component, "%s", message) }))
	manager.Register(lifecycle.Hook{Name: "IdempotencyObserver", StartOrder: -1000, StopOrder: 850, Stop: func(context.Context) error { restore(); return nil }})
	defer func() {
		if err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err = errors.Join(err, manager.Rollback(cleanupCtx))
		}
	}()
	return initializeApplication(ctx, cfg, buildInfo, manager, restarter, tasks)
}

// Run 先完成后台启动，再开放 HTTP；所有返回路径经过相同的有界清理。
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
