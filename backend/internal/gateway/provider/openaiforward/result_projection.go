package openaiforward

import (
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// result_projection.go converts forwarding result records between package,
// gateway, and compatibility reader types.
// Forwarding results carry measured usage alongside caller, billing, and vendor
// identities for completion processing. Conversion allocates a destination
// record and copies values. Headers, mutable collections, and pointers other
// than NativeUsage remain shared; warning conversion additionally allocates its
// outer warning record.
//
// Messages readers also supply normalized native usage with uncached input and
// separate cache buckets, including cache lifetimes and processing speed. Each
// conversion copies this observation independently so downstream updates cannot
// alter the reader result. The inclusive OpenAI usage remains available for
// compatibility consumers; completion selects the native observation as a whole
// to keep cache tokens from contributing twice to the billable input count.

// ToForwardResult converts a package result into gateway completion data,
// returning nil for nil input. Header maps, slices, and pointers other than
// NativeUsage retain their storage. A warning gets a new outer record with its
// response bytes still shared. NativeUsage receives an independent value copy,
// including its uncached input, cache lifetimes, and speed. Keeping this
// observation separate from inclusive OpenAI Usage lets completion choose one
// complete interpretation and prevents later normalization from changing the
// supplied result.
func ToForwardResult(result *Result) *forwardcore.OpenAIResult {
	if result == nil {
		return nil
	}
	value := &forwardcore.OpenAIResult{
		UpstreamEndpoint:            result.UpstreamEndpoint,
		RequestedReasoningEffort:    result.RequestedReasoningEffort,
		OpenAIWSMode:                result.OpenAIWSMode,
		UpstreamTerminalEvent:       result.UpstreamTerminalEvent,
		ResponseHeaders:             result.ResponseHeaders,
		ImageOutputSize:             result.ImageOutputSize,
		ImageSizeSource:             result.ImageSizeSource,
		ImageSizeBreakdown:          result.ImageSizeBreakdown,
		VideoCount:                  result.VideoCount,
		VideoResolution:             result.VideoResolution,
		VideoDurationSeconds:        result.VideoDurationSeconds,
		WebSearchCalls:              result.WebSearchCalls,
		AudioUsage:                  result.AudioUsage,
		ClientDisconnect:            result.ClientDisconnect,
		SearchCount:                 result.SearchCount,
		RequestID:                   result.RequestID,
		ResponseID:                  result.ResponseID,
		UpstreamHeaders:             result.Headers,
		Usage:                       result.Usage,
		Model:                       result.Model,
		BillingModel:                result.BillingModel,
		UpstreamModel:               result.UpstreamModel,
		UpstreamResponseServiceTier: result.UpstreamResponseServiceTier,
		UpstreamResponseModel:       result.UpstreamResponseModel,
		ServiceTier:                 result.ServiceTier,
		ReasoningEffort:             result.ReasoningEffort,
		Stream:                      result.Stream,
		Duration:                    result.Duration,
		Timings:                     result.Timings,
		ImageCount:                  result.ImageCount,
		ImageFrameCount:             result.ImageFrameCount,
		ImageSize:                   result.ImageSize,
		ImageInputSize:              result.ImageInputSize,
		ImageOutputSizes:            result.ImageOutputSizes,
	}
	if result.NativeUsage != nil {
		usage := *result.NativeUsage
		value.NativeUsage = &usage
	}
	if result.UpstreamWarning != nil {
		value.UpstreamWarning = &forwardcore.UpstreamWarning{StatusCode: result.UpstreamWarning.StatusCode, ResponseBody: result.UpstreamWarning.ResponseBody, Message: result.UpstreamWarning.Message}
	}
	return value
}

// FromForwardResult converts gateway completion data into a package result,
// returning nil for nil input. Header maps, slices, and pointers other than
// NativeUsage retain their storage. A warning gets a new outer record with its
// response bytes still shared. NativeUsage receives an independent value copy,
// including its uncached input, cache lifetimes, and speed. Keeping this
// observation separate from inclusive OpenAI Usage lets completion choose one
// complete interpretation and prevents later normalization from changing the
// supplied result.
func FromForwardResult(r *forwardcore.OpenAIResult) *Result {
	if r == nil {
		return nil
	}
	value := &Result{
		UpstreamEndpoint:            r.UpstreamEndpoint,
		RequestedReasoningEffort:    r.RequestedReasoningEffort,
		OpenAIWSMode:                r.OpenAIWSMode,
		UpstreamTerminalEvent:       r.UpstreamTerminalEvent,
		ResponseHeaders:             r.ResponseHeaders,
		ImageOutputSize:             r.ImageOutputSize,
		ImageSizeSource:             r.ImageSizeSource,
		ImageSizeBreakdown:          r.ImageSizeBreakdown,
		VideoCount:                  r.VideoCount,
		VideoResolution:             r.VideoResolution,
		VideoDurationSeconds:        r.VideoDurationSeconds,
		WebSearchCalls:              r.WebSearchCalls,
		AudioUsage:                  r.AudioUsage,
		RequestID:                   r.RequestID,
		ResponseID:                  r.ResponseID,
		Headers:                     r.UpstreamHeaders,
		Usage:                       r.Usage,
		Model:                       r.Model,
		BillingModel:                r.BillingModel,
		UpstreamModel:               r.UpstreamModel,
		UpstreamResponseServiceTier: r.UpstreamResponseServiceTier,
		UpstreamResponseModel:       r.UpstreamResponseModel,
		ServiceTier:                 r.ServiceTier,
		ReasoningEffort:             r.ReasoningEffort,
		Stream:                      r.Stream,
		Duration:                    r.Duration,
		Timings:                     r.Timings,
		ClientDisconnect:            r.ClientDisconnect,
		SearchCount:                 r.SearchCount,
		ImageCount:                  r.ImageCount,
		ImageFrameCount:             r.ImageFrameCount,
		ImageSize:                   r.ImageSize,
		ImageInputSize:              r.ImageInputSize,
		ImageOutputSizes:            r.ImageOutputSizes,
	}
	if r.NativeUsage != nil {
		usage := *r.NativeUsage
		value.NativeUsage = &usage
	}
	if r.UpstreamWarning != nil {
		value.UpstreamWarning = &forwardcore.UpstreamWarning{StatusCode: r.UpstreamWarning.StatusCode, ResponseBody: r.UpstreamWarning.ResponseBody, Message: r.UpstreamWarning.Message}
	}
	return value
}

// FromCompatResult converts compatibility output and attaches the supplied
// billing model, returning nil for nil input. Headers and pointer values remain
// borrowed from the compatibility result, so subsequent mutation requires
// caller-managed copying.
func FromCompatResult(r *openai.CompatResponseResult, billing string) *Result {
	if r == nil {
		return nil
	}
	return &Result{RequestID: r.RequestID, ResponseID: r.ResponseID, Headers: r.UpstreamHeaders, Usage: r.Usage, Model: r.Model, BillingModel: billing, UpstreamModel: r.UpstreamModel, UpstreamResponseServiceTier: r.ServiceTier, ServiceTier: r.ResolvedTier, ReasoningEffort: r.ReasoningEffort, Stream: r.Stream, Duration: r.Duration, ClientDisconnect: r.ClientDisconnect, SearchCount: r.SearchCount}
}
