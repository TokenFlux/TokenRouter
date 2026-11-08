package forward

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// messages_result.go defines converted Messages results and extracts them from
// upstream attempts. Converted Messages results retain the upstream token
// buckets needed for cache-aware pricing. The logical model, sent model, and
// upstream response declaration remain separate, allowing completion and client
// output to use the observation appropriate to their own decision.

// MessagesResult contains raw protocol usage and metadata from Messages-compatible execution.
type MessagesResult struct {
	// UpstreamResponseModel is the declaration captured before protocol conversion.
	// Empty means that the upstream supplied no usable model evidence for this result.
	UpstreamResponseModel string
	RequestID             string // upstream exchange request identifier
	// UpstreamHeaders contains direct upstream headers used for provider-specific
	// request-ID extraction. Result conversions retain its map and header slices;
	// consumers copy them before mutation or independent ownership.
	UpstreamHeaders map[string][]string
	Usage           upstream.TokenUsage // observed protocol token buckets
	Model           string              // logical execution request model
	// UpstreamModel identifies the producer's selected upstream model after
	// mapping. It can equal Model when no mapping applies, and the Gemini wrapper
	// retains its initial selection after provider-local model recovery. Empty
	// leaves completion to its configured model-source and logical-model rules.
	UpstreamModel    string
	Stream           bool                // client streaming output mode
	Duration         time.Duration       // forwarding elapsed duration
	Timings          *timing.Performance // frozen exchange performance observations
	ClientDisconnect bool                // observed client disconnect flag
	ReasoningEffort  *string             // executed reasoning effort
	// RequestedReasoningEffort captures client effort before compatibility mapping.
	// It remains separate from ReasoningEffort so completion can record requested
	// and executed policies independently when a rewrite changes the effort.
	RequestedReasoningEffort *string
	// UpstreamResponseServiceTier is the observed tier rather than the outbound
	// request value. Empty means no usable evidence was captured for billing reconciliation.
	UpstreamResponseServiceTier string // captured upstream execution tier
	// ServiceTier carries the execution tier captured by the producer. Completion
	// reconciles it with upstream observations using the credential protocol's billing rules.
	ServiceTier *string
	// ImageCount and the size fields carry image observations for completion's
	// resolution-based pricing. Forwarding can supply hints that completion later
	// normalizes instead of treating every raw size as a final billing category.
	// ImageCount is the producer's image quantity for completion billing. Some
	// producers count response objects; Gemini assigns one after a successful
	// Antigravity attempt for a selected image model. The quantity can therefore
	// be a model-derived billing estimate.
	ImageCount      int    // producer image quantity
	ImageFrameCount int    // encoded output frame billing units
	ImageSize       string // image billing size hint
	ImageInputSize  string // input image size hint
	ImageOutputSize string // output image size hint
	// ImageOutputSizes and ImageSizeBreakdown retain collected output size data.
	// Their slice and map storage can remain shared after a MessagesResult or
	// Result conversion, so mutable consumers copy these collections first.
	ImageOutputSizes   []string       // per-image output size hints
	ImageSizeSource    string         // image size evidence source
	ImageSizeBreakdown map[string]int // image counts by billing size
	SearchCount        int            // observed billable search calls
	// AudioUsage points to the observed audio mode and pricing quantity when
	// present. A result assignment shares that pointed-to value rather than allocating a copy.
	AudioUsage *protocol.AudioUsage
}

// MessagesFromAttempt creates a MessagesResult from a platform attempt's model,
// token usage, timing, and disconnect observations. Callers add headers, media,
// and policy metadata separately; Timings retains the attempt's pointer
// rather than allocating an independent measurement.
func MessagesFromAttempt(result upstream.AttemptResult) *MessagesResult {
	return &MessagesResult{
		RequestID:             result.RequestID,
		Model:                 result.Model,
		UpstreamModel:         result.UpstreamModel,
		UpstreamResponseModel: result.UpstreamResponseModel,
		Usage:                 result.Usage,
		Stream:                result.Stream,
		Duration:              result.Duration,
		ClientDisconnect:      result.ClientDisconnect,
		Timings:               result.Timings,
	}
}
