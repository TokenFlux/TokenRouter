package service

import (
	"context"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/gin-gonic/gin"
)

func (s *AntigravityGatewayService) ForwardGemini(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, originalModel string, action string, stream bool, body []byte, isStickySession bool, options ...forwardcore.GeminiSessionOption) (*forwardcore.MessagesResult, error) {
	start := time.Now()
	opts := forwardcore.GeminiSession{}
	for _, apply := range options {
		if apply != nil {
			apply(&opts)
		}
	}
	adapter := &geminiExecutionAdapter{s: s, c: c, account: account}
	input := forwardcore.GeminiInput{StartedAt: start, Model: originalModel, Action: action, Stream: stream, Body: body, Sticky: isStickySession, GroupID: opts.GroupID, SessionHash: opts.SessionHash, AccountID: account.Record.ID, AccountName: account.Record.Name, Platform: account.Record.Platform, TokenAvailable: s.tokenProvider != nil, Prefix: logPrefix(getSessionID(c), account.Record.Name)}
	adapter.prefix = input.Prefix
	result, err := forwardcore.Gemini(ctx, adapter, input)
	return legacyForwardExecutionResult(result), err
}
