package googleforward

import (
	"context"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// antigravity_gemini.go starts native Gemini forwarding through a selected
// Antigravity provider.
// Native Antigravity Gemini forwarding retains the original model and client
// stream mode while the shared flow prepares the selected upstream request.
// Optional group/session values supply sticky deletion context, and each call
// starts with fresh image and response state.

// ForwardGemini executes a native Google request through one selected
// Antigravity provider. It installs client protocol and optional attribute
// context, applies supplied session options in order, and returns per-attempt
// completion observations.
func (s *Antigravity) ForwardGemini(ctx context.Context, output Output, provider *gatewayprovider.ExecutionProvider, originalModel string, action string, stream bool, body []byte, isStickySession bool, options ...forwardcore.GeminiSessionOption) (*forwardcore.MessagesResult, error) {
	ctx = requeststate.WithClientProtocol(ctx, protocol.ProtocolGeminiGenerateContent)
	if routing.ModelAttributesFromContext(ctx) == nil {
		ctx = routing.WithModelAttributes(ctx, s.Attributes)
	}
	c := &attempt{Output: output}

	start := time.Now()
	opts := forwardcore.GeminiSession{}
	for _, apply := range options {
		if apply != nil {
			apply(&opts)
		}
	}
	adapter := &geminiExecutionAdapter{s: s, c: c, provider: provider, ctx: ctx, originalModel: originalModel}
	input := forwardcore.GeminiInput{
		StartedAt:      start,
		Model:          originalModel,
		Action:         action,
		Stream:         stream,
		Body:           body,
		Sticky:         isStickySession,
		GroupID:        opts.GroupID,
		SessionHash:    opts.SessionHash,
		ProviderID:     provider.Record.ID,
		ProviderName:   provider.Record.Name,
		Platform:       provider.Record.Platform,
		TokenAvailable: s.Tokens != nil,
		Prefix:         logPrefix(c.GetHeader("session_id"), provider.Record.Name),
	}
	adapter.prefix = input.Prefix
	result, err := forwardcore.Gemini(ctx, adapter, input)
	return messagesResult(result), err
}
