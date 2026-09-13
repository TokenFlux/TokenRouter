// app 持有错误采集队列；HTTP 捕获只绑定同一消费者。
package app

import (
	"context"
	"log"
	"runtime"
	"runtime/debug"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/handler"
	"github.com/TokenFlux/TokenRouter/internal/ops"
)

type errorQueueReady struct{}

func provideOpsErrorQueue(manager *lifecycle.Manager) *errorQueueReady {
	q := ops.NewErrorLogQueue(ops.ErrorLogQueueOptions{Processors: func() int { return runtime.GOMAXPROCS(0) }, Logf: log.Printf, Stack: debug.Stack})
	restore := handler.BindOpsErrorQueue(q)
	manager.Register(lifecycle.Hook{Name: "OpsErrorLogWorkers", StartOrder: 924, StopOrder: 76, Stop: q.Shutdown})
	manager.Register(lifecycle.Hook{Name: "OpsErrorQueueBinding", StopOrder: 852, Stop: func(context.Context) error { restore(); return nil }})
	return &errorQueueReady{}
}
