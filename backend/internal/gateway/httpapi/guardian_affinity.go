package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// WithOpenAIGuardianParentAffinity 只提取 HTTP 线索，散列算法和缓存命名空间仍由 scheduler 拥有。
func WithOpenAIGuardianParentAffinity(ctx context.Context, c *gin.Context, body []byte, model string) context.Context {
	if ctx == nil || c == nil || !clientmeta.IsCodexReviewModel(model) {
		return ctx
	}
	parent := clientmeta.CodexReviewParent(clientmeta.CodexReviewInput{Model: model, Body: body, Subagent: c.GetHeader(clientmeta.OpenAISubagentHeader), ParentThreadID: c.GetHeader(clientmeta.CodexParentThreadIDHeader), TurnMetadata: c.GetHeader(clientmeta.CodexTurnMetadataHeader)})
	if parent == "" {
		return ctx
	}
	current, legacy := scheduler.DeriveSessionHashes(parent)
	if current == "" {
		return ctx
	}
	return requeststate.WithGuardianParentAffinity(ctx, requeststate.GuardianParentAffinity{CurrentSessionHash: current, LegacySessionHash: legacy})
}
