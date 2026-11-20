package messageforward

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// runtime.go starts Messages, counting, Chat, and Responses flows with fresh
// attempt state.
// Each Messages entry creates its own preparation state before invoking
// gateway/forward. Chat and Responses retain separate output modes while using
// the same selected provider and request-local tool restoration, and counting
// follows a distinct error format. Count completion uses the retained HTTP
// response to finish its diagnostic attempt because counting has no usage writer.

// Execute forwards one parsed Messages request using fresh attempt state for
// the selected provider. gateway/forward controls preparation and partial
// results; the outer gateway selects any replacement provider.
// @project-doc docs/architecture/gateway_request_lifecycle.md#protocol_conversion_boundary
func (r *Runtime) Execute(ctx context.Context, output HTTPBoundary, target *gatewayadapter.ExecutionProvider, parsed *requeststate.ParsedRequest) (*forward.MessagesResult, error) {
	var preStripEffort *string
	if parsed != nil && gatewayadapter.ExecutionModelPolicy(target).ShouldStripWireEffort(ctx, parsed.Model) {
		preStripEffort = protocol.NormalizeClaudeOutputEffort(parsed.OutputEffort)
	}
	adapter := newAttemptWithContext(ctx, r, output, target)
	result, err := forward.Messages(ctx, adapter, adapter.input(), parsed)
	out := messagesResult(result)
	// Preserve pre-strip Claude effort for usage recording when wire effort
	// stripping cleared output_config.effort on the adopted outbound payload.
	if out != nil && out.ReasoningEffort == nil && preStripEffort != nil {
		out.ReasoningEffort = preStripEffort
	}
	return out, err
}

// Count runs token counting with fresh preparation state for the selected
// provider. The counting flow owns its response format and reports unsupported
// routes separately from ordinary generation errors. The latest retained HTTP
// response identifies the final dispatch after signature repair; prior HTTP
// failures and failed sends keep their already observed outcomes.
func (r *Runtime) Count(ctx context.Context, output HTTPBoundary, target *gatewayadapter.ExecutionProvider, parsed *requeststate.ParsedRequest) (failure error) {
	adapter := &countAttempt{attempt: newAttempt(r, output, target)}
	defer func() {
		telemetry.ObserveRequestResult(ctx, telemetry.RequestAttemptFromResponse(adapter.response), failure)
	}()
	return forward.CountTokens(ctx, adapter, adapter.input(), parsed)
}

// Chat converts the supplied OpenAI body through an Anthropic attempt and
// returns Messages completion metadata. Fresh conversion state carries tool
// restoration; output and target remain usable through synchronous forwarding.
func (r *Runtime) Chat(ctx context.Context, output HTTPBoundary, target *gatewayadapter.ExecutionProvider, body []byte) (*forward.MessagesResult, error) {
	adapter := &conversionAttempt{s: r, c: output, provider: target, state: &AttemptState{}}
	result, err := forward.AsChat(ctx, adapter, forward.ConversionInput{OAuth: target.View().IsOAuth()}, body)
	return messagesResult(result), err
}

// Responses converts the supplied Responses body through an Anthropic attempt
// and returns Messages completion metadata. Fresh state carries client tool
// restoration, and returned partial observations remain paired with any error.
func (r *Runtime) Responses(ctx context.Context, output HTTPBoundary, target *gatewayadapter.ExecutionProvider, body []byte) (*forward.MessagesResult, error) {
	adapter := &conversionAttempt{s: r, c: output, provider: target, responses: true, state: &AttemptState{}}
	result, err := forward.AsResponses(ctx, adapter, forward.ConversionInput{OAuth: target.View().IsOAuth()}, body)
	return messagesResult(result), err
}

// messagesResult returns a new outer MessagesResult value when a forwarding
// result exists. Reference-valued fields remain shared with the source result;
// nil input returns nil for absent observations.
func messagesResult(result *forward.Result) *forward.MessagesResult {
	if result == nil {
		return nil
	}
	copy := forward.MessagesResult(*result)
	return &copy
}
