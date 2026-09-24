package googleforward

import (
	"context"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

func (s *Antigravity) ForwardGemini(ctx context.Context, output Output, account *gatewayprovider.ExecutionAccount, originalModel string, action string, stream bool, body []byte, isStickySession bool, options ...forwardcore.GeminiSessionOption) (*forwardcore.MessagesResult, error) {
	c := &attempt{Output: output}

	start := time.Now()
	opts := forwardcore.GeminiSession{}
	for _, apply := range options {
		if apply != nil {
			apply(&opts)
		}
	}
	adapter := &geminiExecutionAdapter{s: s, c: c, account: account}
	input := forwardcore.GeminiInput{
		StartedAt:      start,
		Model:          originalModel,
		Action:         action,
		Stream:         stream,
		Body:           body,
		Sticky:         isStickySession,
		GroupID:        opts.GroupID,
		SessionHash:    opts.SessionHash,
		AccountID:      account.Record.ID,
		AccountName:    account.Record.Name,
		Platform:       account.Record.Platform,
		TokenAvailable: s.Tokens != nil,
		Prefix:         logPrefix(c.GetHeader("session_id"), account.Record.Name),
	}
	adapter.prefix = input.Prefix
	result, err := forwardcore.Gemini(ctx, adapter, input)
	return messagesResult(result), err
}
