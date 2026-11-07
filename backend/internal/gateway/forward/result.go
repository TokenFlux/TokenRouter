package forward

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// result.go defines Messages forwarding results with separate request,
// upstream, and response model observations. Messages forwarding needs both the
// model requested for execution and any model declared in the upstream
// response. Results retain those observations separately with usage and timing
// so later protocol conversion can restore client metadata while completion
// resolves the configured billing model.

// Result holds forwarding observations passed between protocol adapters and completion.
type Result struct {
	// UpstreamResponseModel records the upstream declaration before conversion.
	// An empty value means no usable declaration was captured for this attempt.
	UpstreamResponseModel string
	RequestID             string // upstream exchange request identifier
	// UpstreamHeaders retains the direct exchange's header map and slices.
	// Result assignments share those collections, so mutable consumers clone both levels first.
	UpstreamHeaders map[string][]string
	Usage           upstream.TokenUsage // observed protocol token buckets
	// Model retains logical input, and UpstreamModel retains the producer's
	// selected upstream name. Gemini keeps its initial mapping after provider-local
	// model recovery. Completion applies its configured source, while the response
	// declaration remains independent evidence.
	Model, UpstreamModel string              // logical request model; captured upstream model
	Stream               bool                // client streaming output mode
	Duration             time.Duration       // forwarding elapsed duration
	Timings              *timing.Performance // frozen exchange performance observations
	ClientDisconnect     bool                // observed client disconnect flag
	// ReasoningEffort records the producer's executed policy, while
	// RequestedReasoningEffort retains client input before compatibility mapping.
	// Completion records those separate observations when request preparation
	// changes the selected effort.
	// executed reasoning effort; requested reasoning effort
	ReasoningEffort, RequestedReasoningEffort *string
	UpstreamResponseServiceTier               string // captured upstream execution tier
	// ServiceTier carries the producer's execution tier when present. Completion
	// reconciles it with upstream evidence using the credential-specific pricing rules.
	ServiceTier *string
	// ImageCount is the producer's image quantity for completion billing. Some
	// producers count response objects; Gemini assigns one after a successful
	// Antigravity attempt for a selected image model. The quantity can therefore
	// be a model-derived billing estimate.
	ImageCount      int // producer image quantity
	ImageFrameCount int // encoded output frame billing units
	// billing image size hint; input image size hint; output image size hint
	ImageSize, ImageInputSize, ImageOutputSize string
	// ImageOutputSizes and ImageSizeBreakdown can share their producer's slice
	// and map storage through result conversions. Consumers that mutate those
	// collections need their own copy after the forwarding producer finishes.
	ImageOutputSizes   []string       // per-image output size hints
	ImageSizeSource    string         // image size evidence source
	ImageSizeBreakdown map[string]int // image counts by billing size
	SearchCount        int            // observed billable search calls
	// AudioUsage supplies mode-specific pricing quantities when audio was
	// observed. Result copies retain this pointer, so mutation requires a caller-owned copy.
	AudioUsage *protocol.AudioUsage
}
