package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/gin-gonic/gin"
)

// GeminiExecutor 在路由边界创建同步输出，所有协议入口复用同一原生准备器。
type GeminiExecutor struct{ Runtime *googleforward.Gemini }

func (s *GeminiExecutor) Forward(ctx context.Context, c *gin.Context, a *provider.ExecutionAccount, body []byte) (*forward.MessagesResult, error) {
	return s.Runtime.Forward(ctx, NewGoogleBoundary(c, s.Runtime.Options, false), a, body)
}
func (s *GeminiExecutor) ForwardNative(ctx context.Context, c *gin.Context, a *provider.ExecutionAccount, model, action string, stream bool, body []byte) (*forward.MessagesResult, error) {
	return s.Runtime.ForwardNative(ctx, NewGoogleBoundary(c, s.Runtime.Options, false), a, model, action, stream, body)
}
func (s *GeminiExecutor) ForwardAsResponses(ctx context.Context, c *gin.Context, a *provider.ExecutionAccount, body []byte, parsed *requeststate.ParsedRequest) (*forward.MessagesResult, error) {
	return s.Runtime.ForwardAsResponses(ctx, NewGoogleBoundary(c, s.Runtime.Options, false), a, body, parsed)
}
func (s *GeminiExecutor) ForwardAsChatCompletions(ctx context.Context, c *gin.Context, a *provider.ExecutionAccount, body []byte) (*forward.MessagesResult, error) {
	return s.Runtime.ForwardAsChatCompletions(ctx, NewGoogleBoundary(c, s.Runtime.Options, false), a, body)
}

// AntigravityExecutor 保留旧路由的协议入口，HTTP 错误和输出均由当前边界持有。
type AntigravityExecutor struct{ Runtime *googleforward.Antigravity }

func (s *AntigravityExecutor) Forward(ctx context.Context, c *gin.Context, a *provider.ExecutionAccount, body []byte, sticky bool) (*forward.MessagesResult, error) {
	return s.Runtime.Forward(ctx, NewGoogleBoundary(c, s.Runtime.Options, true), a, body, sticky)
}
func (s *AntigravityExecutor) ForwardGemini(ctx context.Context, c *gin.Context, a *provider.ExecutionAccount, model, action string, stream bool, body []byte, sticky bool, options ...forward.GeminiSessionOption) (*forward.MessagesResult, error) {
	return s.Runtime.ForwardGemini(ctx, NewGoogleBoundary(c, s.Runtime.Options, true), a, model, action, stream, body, sticky, options...)
}
func (s *AntigravityExecutor) ForwardAsResponses(ctx context.Context, c *gin.Context, a *provider.ExecutionAccount, body []byte, parsed *requeststate.ParsedRequest) (*forward.MessagesResult, error) {
	return s.Runtime.ForwardAsResponses(ctx, NewGoogleBoundary(c, s.Runtime.Options, true), a, body, parsed)
}
func (s *AntigravityExecutor) ForwardAsChatCompletions(ctx context.Context, c *gin.Context, a *provider.ExecutionAccount, body []byte, parsed *requeststate.ParsedRequest) (*forward.MessagesResult, error) {
	return s.Runtime.ForwardAsChatCompletions(ctx, NewGoogleBoundary(c, s.Runtime.Options, true), a, body, parsed)
}
func (s *AntigravityExecutor) WriteMappedClaudeError(c *gin.Context, a *provider.ExecutionAccount, status int, id string, body []byte) error {
	return NewGoogleBoundary(c, s.Runtime.Options, true).MappedClaudeError(a, status, id, body)
}
