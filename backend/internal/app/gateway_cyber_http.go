package app

import (
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/ops"
)

type cyberOpsWriter struct {
	service *ops.OpsService
	queue   *ops.ErrorLogQueue
}

func (w cyberOpsWriter) Enqueue(value *ops.OpsInsertErrorLogInput) { w.queue.Enqueue(w.service, value) }

// provideCyberHTTP 复用已装配的后台跟踪、会话、完成和 Ops 实例。
func provideCyberHTTP(core *session.CyberBlocks, records GatewayCompletionRecorders, tasks *lifecycle.Tasks, moderator *moderation.ContentModerationService, opsService *ops.OpsService, queue *ops.ErrorLogQueue) *gatewayhttp.CyberHandler {
	runtime := moderationflow.Runtime{Recorder: records.OpenAI, Blocks: core, Tasks: tasks}
	if opsService != nil && queue != nil {
		runtime.Ops = cyberOpsWriter{opsService, queue}
	}
	var port gatewayhttp.ModerationPort
	if moderator != nil {
		port = moderator
	}
	return gatewayhttp.NewBoundCyberHandler(core, port, runtime)
}
