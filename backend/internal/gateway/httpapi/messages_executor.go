package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/messageforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/gin-gonic/gin"
)

// MessagesExecutor 将 HTTP 输出接到固定运行时，每次调用创建独立的输出边界。
type MessagesExecutor struct {
	runtime *messageforward.Runtime
	filter  *egress.CompiledHeaderFilter
}

func NewMessagesExecutor(runtime *messageforward.Runtime, filter *egress.CompiledHeaderFilter) *MessagesExecutor {
	return &MessagesExecutor{runtime: runtime, filter: filter}
}

func (e *MessagesExecutor) ApplyBedrockCCCompat(c *gin.Context, body []byte, model string, target *provider.ExecutionAccount, groupID *int64) []byte {
	return e.runtime.PrepareBedrockCompatibility(c.Request.Context(), c.Request.Header, body, model, target, groupID)
}

func (e *MessagesExecutor) Forward(ctx context.Context, c *gin.Context, target *provider.ExecutionAccount, parsed *requeststate.ParsedRequest) (*forward.MessagesResult, error) {
	return e.runtime.Execute(ctx, NewMessageForwardBoundary(c, e.filter), target, parsed)
}

func (e *MessagesExecutor) ForwardCountTokens(ctx context.Context, c *gin.Context, target *provider.ExecutionAccount, parsed *requeststate.ParsedRequest) error {
	return e.runtime.Count(ctx, NewMessageForwardBoundary(c, e.filter), target, parsed)
}

func (e *MessagesExecutor) ForwardAsChatCompletions(ctx context.Context, c *gin.Context, target *provider.ExecutionAccount, body []byte, _ *requeststate.ParsedRequest) (*forward.MessagesResult, error) {
	return e.runtime.Chat(ctx, NewMessageForwardBoundary(c, e.filter), target, body)
}

func (e *MessagesExecutor) ForwardAsResponses(ctx context.Context, c *gin.Context, target *provider.ExecutionAccount, body []byte, _ *requeststate.ParsedRequest) (*forward.MessagesResult, error) {
	return e.runtime.Responses(ctx, NewMessageForwardBoundary(c, e.filter), target, body)
}
