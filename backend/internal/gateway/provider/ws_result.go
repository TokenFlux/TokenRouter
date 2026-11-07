package provider

import (
	"net/http"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	openaiws "github.com/TokenFlux/TokenRouter/internal/upstream/openai/ws"
)

// ws_result.go converts forwarding results to and from WebSocket turn results
// while retaining replay-state presence.
// A WebSocket turn carries observed usage plus replay inputs that differ from
// its public response metadata. Converting between the forwarding and WS
// result types keeps the input-presence flag separate from an empty replay
// slice, and carries the cross-provider recovery history independently. A
// local warmup also remains identifiable so a completed local response does
// not become upstream scheduling success.
//
// These adapters transfer observations by value and retain nested headers,
// usage pointers, image collections, warning bytes, and replay storage.
// ForwardResultFromWS uses the forwarding result setters to restore private
// replay fields; ProjectWSResult reads the turn-state header for the WS owner.
// Callers keep borrowed values stable until consumers finish.

// ProjectWSResult converts forwarding observations and private replay inputs
// to a WS turn result. Nil input returns nil, and nested mutable values remain
// shared with the forwarding result.
//
// Replay input and its presence flag travel together so an explicitly empty
// input survives conversion. Cross-provider replay history remains a separate
// value, and ResponseTurnState comes from the response header. The LocalWarmup
// flag remains available to distinguish local completion from upstream
// success.
func ProjectWSResult(r *forwardcore.OpenAIResult) *gatewayws.ForwardResult {
	if r == nil {
		return nil
	}
	replay, replayExists := r.WSReplayInput()
	out := &gatewayws.ForwardResult{
		LocalWarmup:                 r.LocalWarmup,
		RequestAttempt:              r.RequestAttempt,
		RequestID:                   r.RequestID,
		ResponseID:                  r.ResponseID,
		UpstreamHeaders:             r.UpstreamHeaders,
		Usage:                       r.Usage,
		Model:                       r.Model,
		BillingModel:                r.BillingModel,
		UpstreamModel:               r.UpstreamModel,
		UpstreamResponseServiceTier: r.UpstreamResponseServiceTier,
		UpstreamResponseModel:       r.UpstreamResponseModel,
		UpstreamEndpoint:            r.UpstreamEndpoint,
		ServiceTier:                 r.ServiceTier,
		ReasoningEffort:             r.ReasoningEffort,
		RequestedReasoningEffort:    r.RequestedReasoningEffort,
		Stream:                      r.Stream,
		OpenAIWSMode:                r.OpenAIWSMode,
		UpstreamTerminalEvent:       r.UpstreamTerminalEvent,
		ResponseHeaders:             r.ResponseHeaders,
		Duration:                    r.Duration,
		Timings:                     r.Timings,
		ClientDisconnect:            r.ClientDisconnect,
		ImageCount:                  r.ImageCount,
		ImageFrameCount:             r.ImageFrameCount,
		ImageSize:                   r.ImageSize,
		ImageInputSize:              r.ImageInputSize,
		ImageOutputSize:             r.ImageOutputSize,
		ImageOutputSizes:            r.ImageOutputSizes,
		ImageSizeSource:             r.ImageSizeSource,
		ImageSizeBreakdown:          r.ImageSizeBreakdown,
		VideoCount:                  r.VideoCount,
		VideoResolution:             r.VideoResolution,
		VideoDurationSeconds:        r.VideoDurationSeconds,
		WebSearchCalls:              r.WebSearchCalls,
		SearchCount:                 r.SearchCount,
		AudioUsage:                  r.AudioUsage,
		WSReplayInput:               replay, WSReplayInputExists: replayExists, WSProviderFailoverReplayInput: r.WSProviderFailoverReplayInput(),
		ResponseTurnState: http.Header(r.ResponseHeaders).Get(openaiws.WSTurnStateHeader),
	}
	if r.UpstreamWarning != nil {
		out.UpstreamWarning = &forwardcore.UpstreamWarning{StatusCode: r.UpstreamWarning.StatusCode, ResponseBody: r.UpstreamWarning.ResponseBody, Message: r.UpstreamWarning.Message}
	}
	return out
}

// ForwardResultFromWS converts a WS turn result for completion and health
// consumers. Nil input returns nil, while nested headers, collections, warning
// bytes, and usage pointers continue to share the supplied result's storage.
//
// The forwarding result's private replay fields are populated through its
// setters, carrying both turn input presence and separate provider-failover
// history. LocalWarmup and terminal-event observations transfer with usage so
// later scheduling classification can distinguish a local response from an
// upstream completion.
func ForwardResultFromWS(r *gatewayws.ForwardResult) *forwardcore.OpenAIResult {
	if r == nil {
		return nil
	}
	out := &forwardcore.OpenAIResult{
		LocalWarmup:                 r.LocalWarmup,
		RequestAttempt:              r.RequestAttempt,
		RequestID:                   r.RequestID,
		ResponseID:                  r.ResponseID,
		UpstreamHeaders:             r.UpstreamHeaders,
		Usage:                       r.Usage,
		Model:                       r.Model,
		BillingModel:                r.BillingModel,
		UpstreamModel:               r.UpstreamModel,
		UpstreamResponseServiceTier: r.UpstreamResponseServiceTier,
		UpstreamResponseModel:       r.UpstreamResponseModel,
		UpstreamEndpoint:            r.UpstreamEndpoint,
		ServiceTier:                 r.ServiceTier,
		ReasoningEffort:             r.ReasoningEffort,
		RequestedReasoningEffort:    r.RequestedReasoningEffort,
		Stream:                      r.Stream,
		OpenAIWSMode:                r.OpenAIWSMode,
		UpstreamTerminalEvent:       r.UpstreamTerminalEvent,
		ResponseHeaders:             r.ResponseHeaders,
		Duration:                    r.Duration,
		Timings:                     r.Timings,
		ClientDisconnect:            r.ClientDisconnect,
		ImageCount:                  r.ImageCount,
		ImageFrameCount:             r.ImageFrameCount,
		ImageSize:                   r.ImageSize,
		ImageInputSize:              r.ImageInputSize,
		ImageOutputSize:             r.ImageOutputSize,
		ImageOutputSizes:            r.ImageOutputSizes,
		ImageSizeSource:             r.ImageSizeSource,
		ImageSizeBreakdown:          r.ImageSizeBreakdown,
		VideoCount:                  r.VideoCount,
		VideoResolution:             r.VideoResolution,
		VideoDurationSeconds:        r.VideoDurationSeconds,
		WebSearchCalls:              r.WebSearchCalls,
		SearchCount:                 r.SearchCount,
		AudioUsage:                  r.AudioUsage,
	}
	out.SetWSReplayInput(r.WSReplayInput, r.WSReplayInputExists)
	out.SetWSProviderFailoverReplayInput(r.WSProviderFailoverReplayInput)
	if r.UpstreamWarning != nil {
		out.UpstreamWarning = &forwardcore.UpstreamWarning{StatusCode: r.UpstreamWarning.StatusCode, ResponseBody: r.UpstreamWarning.ResponseBody, Message: r.UpstreamWarning.Message}
	}
	return out
}
