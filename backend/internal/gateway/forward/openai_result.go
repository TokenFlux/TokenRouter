package forward

import (
	"encoding/json"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// openai_result.go holds OpenAI-compatible result evidence and WebSocket replay
// state and classifies scheduling success. An OpenAI-compatible result can come
// from direct OpenAI execution or another protocol converted into OpenAI
// output. It therefore keeps native token buckets, explicit billing candidates,
// and sent model/tier values separate from client-facing metadata and upstream
// response declarations. Completion applies the configured pricing source and
// credential-specific tier rules to those observations.
//
// WebSocket turns also need replay history and terminal outcome classification.
// Replay slices remain private to the Go result and are stored by reference;
// the explicit input-presence flag distinguishes an empty supplied input from
// an absent input. Scheduling treats completed and legacy-empty results as
// successful, while local warmups and declared unsuccessful WebSocket terminals
// remain distinct from a billable upstream exchange.

// OpenAIResult holds OpenAI-compatible execution observations and private WebSocket replay state.
type OpenAIResult struct {
	RequestAttempt telemetry.RequestAttemptRef `json:"-"` // originating diagnostic dispatch reference
	// LocalWarmup marks a locally completed warmup. Gateway turn completion skips
	// its normal usage and scheduling feedback, and SucceededForScheduling returns false.
	LocalWarmup bool `json:"-"`
	// UpstreamResponseModel retains a usable upstream declaration before protocol
	// conversion. Empty means no declaration was captured for later request audit.
	UpstreamResponseModel string
	// NativeUsage retains separate native token buckets when a non-OpenAI executor
	// produced this compatible result. Completion uses that evidence to avoid
	// reinterpreting native cache buckets as inclusive OpenAI input totals.
	NativeUsage *protocolcore.TokenUsage
	RequestID   string // upstream exchange request identifier
	ResponseID  string // protocol response or task identifier
	// UpstreamHeaders contains the direct upstream's headers for request-ID
	// extraction. Assigning this result shares the map and value slices; a consumer
	// that needs independent mutable headers clones both levels.
	UpstreamHeaders map[string][]string
	Usage           protocolopenai.ForwardUsage // observed protocol token buckets
	Model           string                      // Client-facing model used in responses and logs.
	// BillingModel supplies an explicit pricing candidate captured by conversion
	// or media execution. Completion starts with this candidate when nonempty,
	// then applies the configured requested, group-mapped, or upstream model source.
	BillingModel string
	// UpstreamModel retains the producer's selected upstream model when available.
	// Native protocol results can keep their initial selection after provider-local
	// model recovery, while UpstreamResponseModel retains the response declaration.
	// Empty delegates model selection to completion's logical or billing candidate.
	UpstreamModel string
	// UpstreamResponseServiceTier is captured upstream tier evidence. Completion
	// reconciles it with ServiceTier using the credential protocol, which determines
	// when an observed declaration can lower the charged tier.
	UpstreamResponseServiceTier string // captured upstream execution tier
	// UpstreamEndpoint identifies the path used for this exchange. A client-facing
	// protocol can select different paths after protocol routing or conversion.
	UpstreamEndpoint string
	// ServiceTier is the final tier sent upstream after policy rewriting.
	// The upstream response declaration remains separate above and is reconciled
	// at usage-recording time, where the credential protocol is available.
	ServiceTier *string
	// ReasoningEffort carries the producer's final upstream effort after request
	// policy changes. Nil means that no applicable executed-effort value was captured.
	ReasoningEffort *string
	// RequestedReasoningEffort captures the client's effort before policy and
	// model mapping, allowing completion to record it separately from the executed effort.
	RequestedReasoningEffort *string
	Stream                   bool // client streaming output mode
	OpenAIWSMode             bool // WebSocket execution result marker
	// UpstreamTerminalEvent is the normalized Responses WebSocket terminal event.
	// SucceededForScheduling treats an empty value as legacy success unless the
	// result is a local warmup; non-WebSocket calls bypass this terminal classification.
	UpstreamTerminalEvent string
	// ResponseHeaders retains headers captured for downstream output. Result
	// assignment shares the map and value slices, so consumers clone both levels
	// before independently mutating the captured headers.
	ResponseHeaders  map[string][]string
	Duration         time.Duration       // forwarding elapsed duration
	Timings          *timing.Performance // frozen exchange performance observations
	ClientDisconnect bool                // observed client disconnect flag
	// ImageCount carries the producer's image quantity for completion billing.
	// Native protocol conversion can pass a model-derived estimate, while image
	// response readers can supply counted output objects.
	ImageCount      int    // producer image quantity
	ImageFrameCount int    // encoded output frame billing units
	ImageSize       string // image billing size hint
	ImageInputSize  string // input image size hint
	ImageOutputSize string // output image size hint
	// ImageOutputSizes and ImageSizeBreakdown retain output resolution details
	// for completion. Their slice and map storage can remain shared through result
	// projections; mutable consumers create their own copies.
	ImageOutputSizes   []string       // per-image output size hints
	ImageSizeSource    string         // image size evidence source
	ImageSizeBreakdown map[string]int // image counts by billing size
	// UpstreamWarning carries a terminal policy refusal observed after transport
	// succeeds. Execution and completion decide separately whether that outcome
	// qualifies for settlement, notification, or scheduling feedback.
	UpstreamWarning *UpstreamWarning
	VideoCount      int    // observed generated video count
	VideoResolution string // video output resolution hint
	// VideoState carries the producer's asynchronous task state, such as running,
	// completed, failed, or canceled. Successful HTTP transport alone does not
	// establish that the generated video has completed.
	VideoState string
	// VideoDurationSeconds carries the producer's duration observation in seconds.
	// Completion normalizes a positive value or selects its default when recording
	// video pricing, so this raw field alone does not promise a bounded duration.
	VideoDurationSeconds int
	// WebSearchCalls counts successful Codex alpha/search requests. A positive
	// value selects completion's per-call web-search price calculation before its
	// ordinary token or media cost branches.
	WebSearchCalls int
	// SearchCount records native or emulated search-call observations. Completion
	// applies its per-thousand-search calculation to this separate quantity when positive.
	SearchCount int
	// AudioUsage carries mode-specific audio pricing quantities when present.
	// Assigning the result retains this pointer, so consumers clone the pointed-to
	// value before mutating it independently.
	AudioUsage *protocolcore.AudioUsage

	// wsReplayInput and wsReplayInputExists retain replay data and its presence
	// independently. An empty supplied array still has presence; assignments keep
	// the caller's slice and raw-message storage shared with result readers.
	wsReplayInput       []json.RawMessage // stored turn replay items
	wsReplayInputExists bool              // explicit turn-input presence flag
	// wsProviderFailoverReplayInput supplies recovered items when a turn needs
	// another provider. Its slice and raw-message bytes are retained by reference.
	wsProviderFailoverReplayInput []json.RawMessage
}

// SucceededForScheduling classifies this result for upstream scheduling
// feedback. Nil results, ordinary non-WebSocket results, and WebSocket results
// with no terminal declaration follow the legacy success path. A local warmup
// returns false even when its terminal event otherwise indicates completion.
//
// A declared WebSocket terminal succeeds for response.completed or response.done
// and fails for every other string. These observations determine scheduling
// feedback, rather than proving that usage is billable or that the downstream
// client consumed the response; those decisions belong to their respective
// completion and transport callers.
func (r *OpenAIResult) SucceededForScheduling() bool {
	if r != nil && r.LocalWarmup {
		return false
	}
	// Ordinary HTTP results do not carry WebSocket terminal state. Keep their
	// legacy scheduling classification available even though turn-specific
	// terminal failures need the stricter event check below.
	if r == nil || !r.OpenAIWSMode || r.UpstreamTerminalEvent == "" {
		return true
	}
	switch r.UpstreamTerminalEvent {
	case "response.completed", "response.done":
		return true
	default:
		return false
	}
}

// SetWSReplayInput stores normalized turn replay items with an explicit
// input-presence flag. It retains the supplied slice and raw-message bytes;
// callers and readers keep that shared storage unchanged or copy it before
// independent mutation.
func (r *OpenAIResult) SetWSReplayInput(input []json.RawMessage, exists bool) {
	r.wsReplayInput = input
	r.wsReplayInputExists = exists
}

// WSReplayInput returns the stored turn replay slice and explicit presence flag.
// An empty slice can still be present. The returned slice and raw-message bytes
// remain shared with the setter's input, so mutable consumers copy them first.
func (r *OpenAIResult) WSReplayInput() ([]json.RawMessage, bool) {
	return r.wsReplayInput, r.wsReplayInputExists
}

// SetWSProviderFailoverReplayInput stores items collected for recovery on a
// replacement provider. It retains the supplied slice and raw-message storage,
// so callers leave it unchanged while result consumers read that replay history.
func (r *OpenAIResult) SetWSProviderFailoverReplayInput(input []json.RawMessage) {
	r.wsProviderFailoverReplayInput = input
}

// WSProviderFailoverReplayInput returns items collected for cross-provider turn
// recovery. The returned slice and raw-message bytes share setter-owned storage;
// consumers copy them before independently modifying or extending that history.
func (r *OpenAIResult) WSProviderFailoverReplayInput() []json.RawMessage {
	return r.wsProviderFailoverReplayInput
}
