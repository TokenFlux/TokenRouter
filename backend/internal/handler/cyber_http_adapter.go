// Cyber 固定端口只调用既有会话、队列与观测能力，不承载计费或风险处置规则。
package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type cyberBackgroundTasks struct{ service *service.OpenAIGatewayService }

func (t cyberBackgroundTasks) Go(name string, fn func()) bool {
	return t.service.RunBackgroundTask(name, fn)
}

type cyberOpsWriter struct {
	service *ops.OpsService
	queue   gatewayhttp.OpsErrorLogQueue
}

func (w cyberOpsWriter) Enqueue(in *ops.OpsInsertErrorLogInput) { w.queue.Enqueue(w.service, in) }

// NewCyberHTTPHandler 仅供尚未改绑的旧调用取得应用实例；独立夹具不复制规则。
func (h *OpenAIGatewayHandler) NewCyberHTTPHandler() *gatewayhttp.CyberHandler {
	if h != nil && h.cyberHTTP != nil {
		return h.cyberHTTP
	}
	runtime := moderationflow.Runtime{Tasks: cyberBackgroundTasks{}}
	var core *session.CyberBlocks
	if h != nil && h.gatewayService != nil {
		core = h.gatewayService.CyberBlocks()
		runtime.Tasks = cyberBackgroundTasks{h.gatewayService}
		runtime.Recorder = h.completionRuntime()
		runtime.Blocks = core
	}
	if h != nil && h.opsService != nil && h.opsErrorQueue != nil {
		runtime.Ops = cyberOpsWriter{h.opsService, h.opsErrorQueue}
	}
	var moderator gatewayhttp.ModerationPort
	if h != nil {
		moderator = nativeModerationPort(h.contentModerationService)
	}
	return gatewayhttp.NewBoundCyberHandler(core, moderator, runtime)
}

// BindCyberHTTPHandler 在应用开始工作前连接同一原生实例。
func (h *OpenAIGatewayHandler) BindCyberHTTPHandler(core *gatewayhttp.CyberHandler) {
	h.cyberHTTP = core
}
