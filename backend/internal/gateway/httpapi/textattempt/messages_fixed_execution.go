package textattempt

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// messages_fixed_execution.go opens protocol-specific text attempt state and
// converts forwarding observations into shared loop results.
// Messages execution opens fresh attempt state while retaining
// application-bound services. The selected text kind determines which bridge
// receives parsed client input, mapped Gemini bytes or protocol-specific
// selection state. Ordinary Messages can install explicit metadata-session
// isolation when the caller has not already supplied that hint.
//
// Observed forwarding results remain available even with errors: positive
// token buckets, images, audio presence or searches establish usage and
// preserve served partial work. Cancellation is reported separately, and
// mutable headers, output-size slices, performance observations and audio data are
// copied before the text loop interprets completion. A nil result retains
// cancellation alone, preventing absent output from becoming invented usage.

// Runtime retains shared Messages services and opens independent per-request
// attempt bridges.
type Runtime struct {
	// dependencies is assembled by New and shared across request-local bridges.
	// Callers configure the operations used by each selected text kind before execution.
	dependencies *messageExecutionDependencies
}

// Open constructs fresh serial attempt ports using the supplied execution
// request and HTTP output. The sink must be a nonnil MessagesOutput with an
// HTTP context; callers also provide its live request, logger, stream flag and
// the runtime’s bound services. Only adapter type and HTTP presence are
// checked here.
//
// The funding Key is copied while parsed request, body and subscription
// references are retained. Ordinary Messages fills a missing isolation hint
// from explicit metadata before installing the supplied context; other kinds
// receive their prepared protocol state, and an unrecognized kind uses the
// base Messages bridge.
func (r *Runtime) Open(ctx context.Context, in execution.Request, sink upstream.OutputSink) (textflow.MessagePorts, error) {
	output, ok := sink.(*gatewayhttp.MessagesOutput)
	if !ok || output.HTTP == nil {
		return nil, errors.New("messages execution requires its HTTP output adapter")
	}
	// Allocate this attempt state per Open call while retaining the bound service
	// pointer, so concurrent requests do not share provider/result fields in one bridge.
	base := messageAttemptBridge{
		fixed: r.dependencies, c: output.HTTP,
		apiKey: apikey.CopyAPIKey(in.Funding.Key), subject: authctx.AuthSubject{UserID: in.UserID, Concurrency: in.Concurrency},
		subscription: in.Funding.Subscription, parsedReq: in.Text.Parsed, body: in.Body, reqModel: in.Model, reqStream: in.Stream,
		isClaudeCodeClient: in.Metadata.ClaudeCode, platform: in.Text.Platform, hasBoundSession: in.Text.HasBoundSession,
		sessionKey: in.SessionHash, sessionBoundProviderID: in.Text.BoundProviderID, streamStarted: output.StreamStarted, reqLog: output.Log,
	}
	// Fill only a missing ordinary-Messages isolation hint from explicit metadata;
	// retain a caller-installed source/hash instead of replacing it with sticky content identity.
	if in.Text.Kind == execution.TextMessages && in.Text.Parsed != nil && requeststate.ExecutionHintsFromContext(ctx).SessionIsolationHash == "" {
		if id := gatewayhttp.MetadataSessionID(in.Text.Parsed.MetadataUserID); id != "" {
			ctx = requeststate.WithSessionIsolation(ctx, session.SessionIsolationSourceGateway, id)
		}
	}
	// Use the caller's request context, including state installed during preparation.
	output.HTTP.Request = output.HTTP.Request.WithContext(ctx)
	switch in.Text.Kind {
	case execution.TextGeminiMessages:
		return &geminiMessageAttemptBridge{messageAttemptBridge: base, forwardModel: in.Text.GeminiModel, forwardBody: in.Text.GeminiBody, groupMapping: in.Route.Mapping()}, nil
	case execution.TextNativeGemini:
		return &nativeGeminiAttemptBridge{
			messageAttemptBridge: base,
			modelName:            in.Text.GeminiModel,
			action:               in.Text.Action,
			stream:               in.Stream,
			geminiConcurrency:    output.Concurrency,
			useDigestFallback:    in.Text.UseDigestFallback,
			geminiDigestChain:    in.Text.DigestChain,
			geminiPrefixHash:     in.Text.PrefixHash,
			geminiSessionUUID:    in.Text.SessionUUID,
			matchedDigestChain:   in.Text.MatchedDigestChain,
			groupMapping:         in.Text.Mapping,
			signatureState:       in.Text.SignatureState,
		}, nil
	}
	// An unrecognized kind returns the initialized base bridge, keeping retained
	// funding and client input available to ordinary Messages preparation and completion.
	return &base, nil
}

// messageObservedAttempt converts forwarding observations even when an error
// accompanies the result. Nil result returns cancellation evidence only; a
// successful nonnil result is Served, while an errored result becomes Served
// only with observed usage.
//
// HasUsage combines positive token buckets, image/search counts and any
// audio-usage pointer. Explicit zero-token counts alone do not satisfy that
// predicate. Headers and output-size slices are cloned, timing and audio
// pointers are copied, and scalar tier/effort values are retained so later
// mutation of those source fields cannot change the attempt metadata used by
// the text loop.
func messageObservedAttempt(result *forwardcore.MessagesResult, err error) upstream.AttemptResult {
	if result == nil {
		return upstream.AttemptResult{Cancelled: errors.Is(err, context.Canceled)}
	}
	// Clone collections before the loop consumes this projection; token latency
	// and audio pointers are copied separately below so source metadata mutations stay outside it.
	out := upstream.AttemptResult{
		RequestID: result.RequestID, Model: result.Model, UpstreamModel: result.UpstreamModel, Usage: result.Usage,
		Stream: result.Stream, Duration: result.Duration, ClientDisconnect: result.ClientDisconnect,
		UpstreamHeaders: http.Header(result.UpstreamHeaders).Clone(), ImageOutputSizes: slices.Clone(result.ImageOutputSizes),
		ObservedImages: result.ImageCount, SearchCount: result.SearchCount, Cancelled: errors.Is(err, context.Canceled),
	}
	// Keep measurable partial service eligible for completion despite an error;
	// the text loop separately interprets this usage evidence and cancellation state.
	out.HasUsage = result.Usage.HasObservedTokens() || result.ImageCount > 0 || result.AudioUsage != nil || result.SearchCount > 0
	out.Served = err == nil || out.HasUsage
	out.Timings = result.Timings.Clone()
	if result.ServiceTier != nil {
		out.ServiceTier = *result.ServiceTier
	}
	if result.ReasoningEffort != nil {
		out.ReasoningEffort = *result.ReasoningEffort
	}
	if result.AudioUsage != nil {
		v := *result.AudioUsage
		out.AudioUsage = &v
	}
	return out
}
