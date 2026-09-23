package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/gin-gonic/gin"
)

type cyberHTTPBackend struct{ core *session.CyberBlocks }

// NewBoundCyberHandler 只绑定原生会话、审核与完成端口，不创建缓存、任务或队列。
func NewBoundCyberHandler(core *session.CyberBlocks, moderator ModerationPort, runtime moderationflow.Runtime) *CyberHandler {
	return NewCyberHandler(cyberHTTPBackend{core}, moderator, GatewayModerationEndpoints{}, runtime)
}
func (p cyberHTTPBackend) Available() bool { return p.core != nil }
func (p cyberHTTPBackend) Enabled(ctx context.Context) bool {
	enabled, _ := p.core.Runtime(ctx)
	return enabled
}
func (p cyberHTTPBackend) Find(ctx context.Context, id int64, c *gin.Context, body []byte) string {
	return FindBlockedCyberSession(ctx, p.core, id, c, body)
}
func (p cyberHTTPBackend) Mark(c *gin.Context) *moderationflow.Mark {
	m := GetOpsCyberPolicy(c)
	if m == nil {
		return nil
	}
	v := moderationflow.Mark(*m)
	return &v
}
func (p cyberHTTPBackend) StopKeepalive(c *gin.Context) bool {
	return StopOpenAICompactSSEKeepaliveCommitted(c)
}
func (p cyberHTTPBackend) MarkStream(c *gin.Context, k, m string, status int) {
	MarkOpsStreamError(c, k, m, status)
}
func (p cyberHTTPBackend) FailedSSE(c *gin.Context, k, m string) bool {
	return WriteResponsesFailedSSE(c, k, "", m, ErrorRequestID(c), ErrorRequestModel(c))
}
func (p cyberHTTPBackend) UpstreamEndpoint(c *gin.Context, platform string) string {
	return GetUpstreamEndpoint(c, platform)
}
