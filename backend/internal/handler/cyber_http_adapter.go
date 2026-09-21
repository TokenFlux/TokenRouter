// Cyber 固定端口只调用既有会话、队列与观测能力，不承载计费或风险处置规则。
package handler

import (
	"context"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

type cyberHTTPBackend struct{ h *OpenAIGatewayHandler }
type cyberBackgroundTasks struct{}

func (cyberBackgroundTasks) Go(name string, fn func()) bool {
	return service.RunBackgroundTask(name, fn)
}

type cyberOpsWriter struct {
	service *ops.OpsService
	queue   gatewayhttp.OpsErrorLogQueue
}

func (w cyberOpsWriter) Enqueue(in *ops.OpsInsertErrorLogInput) { w.queue.Enqueue(w.service, in) }

// NewCyberHTTPHandler 复用唯一 Recorder、moderation、会话与后台跟踪器，不创建队列。
func (h *OpenAIGatewayHandler) NewCyberHTTPHandler() *gatewayhttp.CyberHandler {
	if h != nil && h.cyberHTTP != nil {
		return h.cyberHTTP
	}
	runtime := moderationflow.Runtime{Tasks: cyberBackgroundTasks{}}
	if h != nil && h.gatewayService != nil {
		runtime.Recorder = h.completionRuntime()
		runtime.Blocks = h.gatewayService
	}
	if h != nil && h.opsService != nil && h.opsErrorQueue != nil {
		runtime.Ops = cyberOpsWriter{h.opsService, h.opsErrorQueue}
	}
	var moderator gatewayhttp.ModerationPort
	if h != nil {
		moderator = nativeModerationPort(h.contentModerationService)
	}
	return gatewayhttp.NewCyberHandler(cyberHTTPBackend{h}, moderator, moderationHTTPEndpoints{}, runtime)
}
func (p cyberHTTPBackend) Available() bool { return p.h != nil && p.h.gatewayService != nil }
func (p cyberHTTPBackend) Enabled(ctx context.Context) bool {
	enabled, _ := p.h.gatewayService.CyberSessionBlockRuntime(ctx)
	return enabled
}
func (p cyberHTTPBackend) Find(ctx context.Context, id int64, c *gin.Context, body []byte) string {
	return findBlockedCyberSessionKey(ctx, p.h.gatewayService, id, c, body)
}
func (p cyberHTTPBackend) Mark(c *gin.Context) *moderationflow.Mark {
	m := service.GetOpsCyberPolicy(c)
	if m == nil {
		return nil
	}
	v := moderationflow.Mark(*m)
	return &v
}
func (p cyberHTTPBackend) StopKeepalive(c *gin.Context) bool {
	return gatewayhttp.StopOpenAICompactSSEKeepaliveCommitted(c)
}
func (p cyberHTTPBackend) MarkStream(c *gin.Context, k, m string, status int) {
	gatewayhttp.MarkOpsStreamError(c, k, m, status)
}
func (p cyberHTTPBackend) FailedSSE(c *gin.Context, k, m string) bool {
	return gatewayhttp.WriteResponsesFailedSSE(c, k, "", m, gatewayhttp.ErrorRequestID(c), gatewayhttp.ErrorRequestModel(c))
}
func (p cyberHTTPBackend) UpstreamEndpoint(c *gin.Context, platform string) string {
	return gatewayhttp.GetUpstreamEndpoint(c, platform)
}

// BindCyberHTTPHandler 在应用启动前固定审核编排入口，复用同一完成器。
func (h *OpenAIGatewayHandler) BindCyberHTTPHandler(core *gatewayhttp.CyberHandler) {
	h.cyberHTTP = core
}
