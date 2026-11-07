package media

import (
	"context"
	"maps"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/provider"
)

// generation.go runs image and Grok media attempts and snapshots generation
// results for completion consumers.
// Image generation can produce completed images before a tail error. A
// Grok task lookup with positive BoundProviderID requires the provider
// recorded for that task. The loops use those observations to decide
// completion versus recovery, separating repeated-provider counts from provider-transition
// allowance.
//
// Each acquired slot surrounds forwarding and ends before feedback or
// completion. Nonstream image execution retains a keepalive stopper across
// attempts, and its deferred cleanup runs on every exit when present. Grok
// generation can spend transition allowance on eligibility rejection before
// acquiring a slot; VideoLookup closes recovery after forwarding. Result
// snapshots separately copy every current mutable collection and timing
// pointer, preserving nil and empty collection evidence for independent
// completion consumers. The dispatch reference remains attached across result
// conversion so completion can identify retries with missing or repeated IDs.

// GenerationResult carries observed image/video response and usage evidence
// into completion.
type GenerationResult struct {
	RequestAttempt telemetry.RequestAttemptRef `json:"-"` // originating HTTP dispatch reference
	// RequestID and ResponseID identify upstream execution and response/task
	// evidence. Model, BillingModel and UpstreamModel keep client, settlement
	// and provider names separate so completion can apply the appropriate
	// identity.
	RequestID, ResponseID, Model, BillingModel, UpstreamModel string // upstream request identifier, upstream response/task identifier, client response model, selected billing model and provider execution model
	// Headers and ResponseHeaders retain mutable maps and value slices from
	// forwarding. CloneGenerationResult copies both maps and slices before
	// consumers mutate or retain an independent snapshot.
	Headers, ResponseHeaders map[string][]string         // retained upstream header evidence and selected client response headers
	Usage                    protocolopenai.ForwardUsage // observed scalar token usage
	Stream                   bool                        // observed streaming response indicator
	Duration                 time.Duration               // observed media attempt elapsed time
	// Timings is nil when performance was not observed. CloneGenerationResult
	// copies the snapshot and its maps before completion retains or modifies them.
	Timings         *timing.Performance // frozen exchange performance observations
	ImageFrameCount int                 // observed animation frame units
	// ImageCount and VideoCount retain image and video file counts,
	// while VideoDurationSeconds supplies video duration evidence. RunImages
	// uses positive ImageCount to retain partial success even when forwarding
	// returns an error.
	ImageCount, VideoCount, VideoDurationSeconds int // observed image count, observed video count and video duration seconds
	// ImageSize selects the image billing tier, with ImageInputSize,
	// ImageOutputSize and ImageSizeSource retaining its evidence.
	// VideoResolution separately supplies normalized video pricing resolution,
	// so completion keeps that dimension independent of image size.
	ImageSize, ImageInputSize, ImageOutputSize, ImageSizeSource, VideoResolution string // image billing tier, input size evidence, output size evidence, size selection source and video pricing resolution
	// ImageOutputSizes retains the forwarding result slice, including its
	// nil/empty shape. CloneGenerationResult copies its string elements and
	// preserves a nonnil empty input as nonnil empty output.
	ImageOutputSizes []string // shared observed image output sizes
	// ImageSizeBreakdown retains image-tier counts for completion evidence.
	// CloneGenerationResult creates a new map when this field is nonnil,
	// including an empty map.
	ImageSizeBreakdown map[string]int // shared recognized image-tier count map
	VideoState         string         // normalized video task state
}

// GenerationSelection pairs provider snapshot with the selected
// same-provider retry policy.
type GenerationSelection struct {
	Provider   provider.ProviderSnapshot // selected media provider snapshot
	RetryLimit int                       // configured same-provider retry limit
}

// GenerationOutcome carries result evidence and adapter-owned media
// recovery classifications.
type GenerationOutcome struct {
	// Result retains forwarding observations and can be nil on success or
	// failure. RunImages completes a failed attempt when this result has
	// positive ImageCount; consumers own copying retained mutable fields.
	Result *GenerationResult // optional shared media result observations
	Err    error             // media forwarding attempt error
	// Failure supplies retry and status evidence on the ordinary
	// forwarding-error path. Nil classification takes each loop's
	// unexpected-error path; positive partial-image completion in RunImages
	// bypasses that classification.
	Failure *failover.FailureInfo // classified media recovery failure
	// OutputChanged closes classified-error recovery when a loop reaches that
	// check; positive partial-image completion bypasses it. ReportFailure
	// controls Grok failure feedback, while RunImages reports classified
	// failures before its output check.
	OutputChanged, ReportFailure bool // observed writer state change and selected Grok failure report
	// ImageError selects OpenAI image-error handling only when Err has no
	// positive image result. The loop reports health according to
	// ImageErrorRetryable and ends without completion or switching.
	ImageError          bool // classified OpenAI image response error
	ImageErrorRetryable bool // image error is provider-retryable
	ImageErrorStatus    int  // classified image error HTTP status
	// ImageErrorType and ImageErrorCode retain the classified image
	// exception's strings. The observer uses them with ImageErrorStatus on the
	// image_error event after recovery has ended.
	ImageErrorType, ImageErrorCode string // classified image error type and classified image error code
}

// GenerationRequest supplies admitted body and operation-specific routing
// controls to media loops.
type GenerationRequest struct {
	// Body remains shared across forwarding attempts and belongs to the
	// caller. Keep its storage stable until the loop and any retaining adapter
	// consumers finish using it.
	Body []byte // shared admitted request byte payload
	// Stream controls keepalive startup in RunImages: nonstream attempts can
	// start a JSON keepalive. RunGrokMedia does not consult this flag for its
	// own loop lifecycle.
	Stream bool // requested image streaming mode
	// MaxSwitches is used directly by RunImages, including nonpositive values.
	// RunGrokMedia replaces nonpositive values with three, and eligibility
	// rejection spends the same allowance as provider transitions.
	MaxSwitches int // configured provider switch allowance
	// RoutingStarted supplies the origin for routing elapsed observations in
	// both loops. Callers initialize it before selection; zero is passed
	// directly to time.Since rather than receiving a local default.
	RoutingStarted time.Time // caller-supplied routing timing origin
	// Generation enables Grok eligibility checks before activation;
	// VideoLookup forbids post-forward recovery. The flags act independently,
	// so callers choose the combination appropriate to the request operation.
	Generation, VideoLookup bool // requested eligibility probe and task-lookup recovery prohibition
	// BoundProviderID restricts RunGrokMedia when positive: the selected
	// provider must match before activation or eligibility checks. RunImages
	// does not use this task ownership hint.
	BoundProviderID int64 // optional creating-provider identity
}

// GenerationEvent carries the current media-loop stage and its available
// observations.
type GenerationEvent struct {
	Kind      string              // media execution stage label
	Selection GenerationSelection // stage-selected provider and retry policy
	Outcome   GenerationOutcome   // shared forwarding outcome evidence
	// Excluded, Switches and MaxSwitches describe selection/transition state,
	// while RetryCount and RetryLimit describe one provider's repeat attempts.
	// Observers interpret the counters according to Kind because unrelated
	// event fields stay zero.
	Excluded, Switches, MaxSwitches, RetryCount, RetryLimit int           // excluded provider count, used transition count, configured transition allowance, used same-provider retries and configured repeat limit
	Elapsed, Delay                                          time.Duration // observed phase elapsed time and selected same-provider retry delay
	Reason                                                  string        // Grok eligibility rejection explanation
	ProbeFailed                                             bool          // eligibility probe returned an error
}

// GenerationFailure carries terminal media routing state into adapter-owned
// protocol handling.
type GenerationFailure struct {
	Stage               string            // terminal media execution stage
	Err                 error             // direct selection or forwarding error
	Outcome             GenerationOutcome // terminal or last switched outcome
	Excluded            int               // excluded IDs at selection failure
	EligibilityRejected bool              // prior eligibility rejection was observed
	SelectedID          int64             // mismatched bound-task selected provider ID
}

// GenerationPorts supplies media selection, admission, output handling,
// feedback and completion.
type GenerationPorts interface {
	// SelectGeneration chooses a provider under ctx using excluded IDs. Error
	// or absence ends selection; loops may include their prior outcome and
	// eligibility evidence in the adapter's terminal handling.
	SelectGeneration(context.Context, map[int64]struct{}) (GenerationSelection, bool, error)
	// ActivateGeneration installs the selected provider in adapter request
	// state before slot acquisition. RunGrokMedia performs bound-provider and
	// requested eligibility checks first; RunImages activates immediately
	// after successful selection.
	ActivateGeneration(GenerationSelection)
	// GenerationEligible reports selected Grok provider eligibility and
	// optional reason/probe error. RunGrokMedia invokes it when Generation is
	// true, excluding a rejected provider and spending transition allowance
	// without acquiring its slot.
	GenerationEligible(context.Context, GenerationSelection) (bool, string, error)
	// AcquireGeneration waits for the selected provider slot and reports
	// admission. False ends the loop with adapter-owned handling; any nonnil
	// release runs after forwarding and before feedback or completion.
	AcquireGeneration(context.Context, GenerationSelection) (func(), bool)
	// StartGenerationKeepalive starts nonstream image output activity and
	// returns its stopper. Return a usable stop function for started activity;
	// RunImages retains it across attempts and invokes it on exit.
	StartGenerationKeepalive() func()
	// ForwardGeneration executes one selected media attempt with shared
	// request bytes. The outcome carries writer observations, media results
	// and classified recovery rules; the adapter owns protocol conversion,
	// output and result storage.
	ForwardGeneration(context.Context, GenerationSelection, []byte) GenerationOutcome
	// ReportGeneration supplies scheduling feedback after the slot is
	// released. Image user errors can report healthy status, while Grok
	// classified errors use ReportFailure; partial image completion follows a
	// separate callback path.
	ReportGeneration(context.Context, GenerationSelection, *GenerationResult, bool, error)
	// CompleteGeneration handles successful observations and retained partial
	// image results after slot release. Result can be nil on nil-error
	// forwarding; the implementation owns missing observations and the outcome
	// of usage persistence.
	CompleteGeneration(context.Context, GenerationSelection, *GenerationResult)
	// SwitchGeneration records adapter transition state before exclusion and
	// allowance checking. It runs even on the classified attempt that exhausts
	// transitions; eligibility rejection uses its separate exclusion path
	// without this callback.
	SwitchGeneration(GenerationSelection)
	// StopGeneration429 can end recovery after transition count advances for a
	// classified failure. True sends terminal exhausted handling before
	// another selection; eligibility rejection does not invoke this
	// status-specific callback.
	StopGeneration429(GenerationSelection, int, int) bool
	// GenerationClientGone reports adapter-observed client departure for early
	// termination. Grok checks it before selection, while image execution
	// checks selection/forwarding failure paths; it is distinct from ctx
	// cancellation during retry waits.
	GenerationClientGone() bool
	// ObserveGeneration handles a stage event synchronously when the loop
	// emits it. Outcome pointers and selected provider data remain shared, so
	// observers copy data they retain for independent later processing.
	ObserveGeneration(GenerationEvent)
	// EndGeneration performs final protocol handling for terminal routing or
	// forwarding state. The loop releases any attempt slot first, while a
	// retained image keepalive stops through the deferred exit callback.
	EndGeneration(GenerationFailure)
}

// RunImages performs admitted image forwarding with independent
// same-provider retry counts and request.MaxSwitches used directly. Callers
// keep body storage stable and provide output/admission handling; nonstream
// execution retains a returned keepalive stopper until loop exit.
//
// Every slot ends before result handling. A failed attempt with positive
// ImageCount reaches completion as partial output and skips the
// failure-reporting branch. Otherwise image exceptions end after health
// feedback, classified failures stop recovery on changed output or client
// departure, and permitted same-provider retries wait under ctx
// cancellation before switching. Completion can receive nil after nil-error
// forwarding, so the adapter owns that case.
func RunImages(ctx context.Context, request GenerationRequest, p GenerationPorts) {
	excluded := map[int64]struct{}{}
	retries := map[int64]int{}
	switches := 0
	var last GenerationOutcome
	var stopKeepalive func()
	// Keep nonstream output activity alive across attempts, then invoke its
	// retained stopper on every exit after any forwarding slot has been
	// released.
	defer func() {
		if stopKeepalive != nil {
			stopKeepalive()
		}
	}()
	for {
		p.ObserveGeneration(GenerationEvent{Kind: "selecting", Excluded: len(excluded)})
		selected, present, err := p.SelectGeneration(ctx, excluded)
		if err != nil {
			if p.GenerationClientGone() {
				p.ObserveGeneration(GenerationEvent{Kind: "select_canceled", Outcome: GenerationOutcome{Err: err}})
				return
			}
			p.ObserveGeneration(GenerationEvent{Kind: "select_failed", Outcome: GenerationOutcome{Err: err}, Excluded: len(excluded)})
			p.EndGeneration(GenerationFailure{Stage: "selection", Err: err, Outcome: last, Excluded: len(excluded)})
			return
		}
		if !present {
			p.EndGeneration(GenerationFailure{Stage: "empty_selection"})
			return
		}
		p.ActivateGeneration(selected)
		release, acquired := p.AcquireGeneration(ctx, selected)
		if !acquired {
			return
		}
		p.ObserveGeneration(GenerationEvent{Kind: "routing", Elapsed: time.Since(request.RoutingStarted)})
		// Start a nonstream keepalive only while no stopper has been retained, so
		// a valid returned callback spans recovery and is closed by the loop
		// defer.
		if !request.Stream && stopKeepalive == nil {
			stopKeepalive = p.StartGenerationKeepalive()
		}
		started := time.Now()
		// Release attempt admission as forwarding returns, before result
		// classification, scheduling feedback or completion can begin independent
		// work on the observed response.
		result := func() GenerationOutcome {
			if release != nil {
				defer release()
			}
			return p.ForwardGeneration(ctx, selected, request.Body)
		}()
		p.ObserveGeneration(GenerationEvent{Kind: "response", Selection: selected, Outcome: result, Elapsed: time.Since(started)})
		// Positive observed image output can survive a tail error and reach
		// completion; only errors with no such result enter ordinary image
		// failure policy.
		if result.Err != nil && (result.Result == nil || result.Result.ImageCount <= 0) {
			// Image exceptions end recovery here; report provider failure for
			// retryable exceptions and healthy status for user errors, leaving their
			// existing output with the adapter.
			if result.ImageError {
				if result.ImageErrorRetryable {
					p.ReportGeneration(ctx, selected, result.Result, false, result.Err)
				} else {
					p.ReportGeneration(ctx, selected, result.Result, true, nil)
				}
				p.ObserveGeneration(GenerationEvent{Kind: "image_error", Selection: selected, Outcome: result})
				return
			}
			if result.Failure == nil {
				p.EndGeneration(GenerationFailure{Stage: "unexpected", Err: result.Err, Outcome: result})
				return
			}
			p.ReportGeneration(ctx, selected, result.Result, false, result.Err)
			// Failure feedback has already been sent when writer changes stop
			// recovery, preventing another image attempt from appending output after
			// this response has progressed.
			if result.OutputChanged {
				p.ObserveGeneration(GenerationEvent{Kind: "after_output", Selection: selected, Outcome: result})
				p.EndGeneration(GenerationFailure{Stage: "exhausted", Outcome: result})
				return
			}
			if p.GenerationClientGone() {
				p.ObserveGeneration(GenerationEvent{Kind: "forward_canceled", Selection: selected, Outcome: result})
				return
			}
			retryLimit := failover.EffectiveSameProviderRetryLimit(result.Failure, selected.RetryLimit)
			// Spend this provider's repeat allowance before switching, then wait on
			// its delay and ctx.Done together; when cancellation wins that select,
			// return before another selection.
			if result.Failure.RetryableOnSameProvider && failover.SameProviderRetryAllowed(result.Failure, retries[selected.Provider.ID], retryLimit) {
				retries[selected.Provider.ID]++
				delay := failover.SameProviderRetryDelayFor(result.Failure, retries[selected.Provider.ID])
				p.ObserveGeneration(GenerationEvent{Kind: "retry", Selection: selected, Outcome: result, RetryCount: retries[selected.Provider.ID], RetryLimit: retryLimit, Delay: delay})
				select {
				case <-ctx.Done():
					return
				case <-time.After(delay):
				}
				continue
			}
			// Record transition bookkeeping and exclude this provider before testing
			// exhaustion, keeping the failed outcome available if a subsequent
			// selection cannot choose a candidate.
			p.SwitchGeneration(selected)
			excluded[selected.Provider.ID] = struct{}{}
			last = result
			if switches >= request.MaxSwitches {
				p.EndGeneration(GenerationFailure{Stage: "exhausted", Outcome: result})
				return
			}
			switches++
			if p.StopGeneration429(selected, result.Failure.StatusCode, switches) {
				p.EndGeneration(GenerationFailure{Stage: "exhausted", Outcome: result})
				return
			}
			p.ObserveGeneration(GenerationEvent{Kind: "switch", Selection: selected, Outcome: result, Switches: switches, MaxSwitches: request.MaxSwitches})
			continue
		}
		if result.Err != nil {
			p.ObserveGeneration(GenerationEvent{Kind: "partial", Selection: selected, Outcome: result})
		}
		p.CompleteGeneration(ctx, selected, result.Result)
		p.ObserveGeneration(GenerationEvent{Kind: "completed", Selection: selected, Switches: switches})
		return
	}
}

// RunGrokMedia performs admitted Grok attempts and defaults nonpositive
// transition allowance to three. Positive BoundProviderID requires a
// matching candidate before activation, and Generation enables eligibility
// rejection that spends allowance without slot acquisition.
//
// Forwarding releases its slot before feedback or completion. A classified
// error must permit RetryNext, have unchanged output and have VideoLookup
// false before any recovery, including same-provider retry. Retry waits
// honor ctx cancellation; transitions exclude the current provider before
// exhaustion and status-specific stopping. Client departure ends handling,
// and terminal callbacks receive the available selection, ownership or
// forwarding evidence.
func RunGrokMedia(ctx context.Context, request GenerationRequest, p GenerationPorts) {
	if request.MaxSwitches <= 0 {
		request.MaxSwitches = 3
	}
	excluded := map[int64]struct{}{}
	retries := map[int64]int{}
	switches := 0
	rejected := false
	var last GenerationOutcome
	for {
		if p.GenerationClientGone() {
			return
		}
		selected, present, err := p.SelectGeneration(ctx, excluded)
		if err != nil {
			if p.GenerationClientGone() {
				p.ObserveGeneration(GenerationEvent{Kind: "select_canceled", Outcome: GenerationOutcome{Err: err}})
				return
			}
			p.ObserveGeneration(GenerationEvent{Kind: "select_failed", Outcome: GenerationOutcome{Err: err}, Excluded: len(excluded)})
			p.EndGeneration(GenerationFailure{Stage: "selection", Err: err, Outcome: last, Excluded: len(excluded), EligibilityRejected: rejected})
			return
		}
		if !present {
			p.EndGeneration(GenerationFailure{Stage: "empty_selection"})
			return
		}
		if request.BoundProviderID > 0 && selected.Provider.ID != request.BoundProviderID {
			p.EndGeneration(GenerationFailure{Stage: "bound_unavailable", SelectedID: selected.Provider.ID})
			return
		}
		p.ObserveGeneration(GenerationEvent{Kind: "schedule", Selection: selected})
		// Probe eligibility only for the caller's generation flag; rejection
		// excludes this provider and spends the transition allowance before
		// activation or slot acquisition.
		if request.Generation {
			eligible, reason, err := p.GenerationEligible(ctx, selected)
			if !eligible {
				rejected = true
				excluded[selected.Provider.ID] = struct{}{}
				p.ObserveGeneration(GenerationEvent{Kind: "ineligible", Selection: selected, Reason: reason, ProbeFailed: err != nil})
				if switches >= request.MaxSwitches {
					p.EndGeneration(GenerationFailure{Stage: "ineligible"})
					return
				}
				switches++
				continue
			}
		}
		p.ActivateGeneration(selected)
		release, acquired := p.AcquireGeneration(ctx, selected)
		if !acquired {
			return
		}
		p.ObserveGeneration(GenerationEvent{Kind: "routing", Elapsed: time.Since(request.RoutingStarted)})
		started := time.Now()
		// Release attempt admission as forwarding returns, before result
		// classification, scheduling feedback or completion can begin independent
		// work on the observed response.
		result := func() GenerationOutcome {
			if release != nil {
				defer release()
			}
			return p.ForwardGeneration(ctx, selected, request.Body)
		}()
		p.ObserveGeneration(GenerationEvent{Kind: "response", Selection: selected, Outcome: result, Elapsed: time.Since(started)})
		if result.Err != nil {
			if result.Failure == nil {
				p.ReportGeneration(ctx, selected, nil, false, nil)
				p.EndGeneration(GenerationFailure{Stage: "unexpected", Err: result.Err, Outcome: result})
				return
			}
			if p.GenerationClientGone() {
				p.ObserveGeneration(GenerationEvent{Kind: "forward_canceled", Selection: selected, Outcome: result})
				return
			}
			if result.ReportFailure {
				p.ReportGeneration(ctx, selected, nil, false, nil)
			}
			// Committed output, denied RetryNext or task lookup closes all recovery,
			// keeping lookup on its creating provider and preventing repeated output
			// through another attempt.
			if result.OutputChanged || !result.Failure.RetryNext || request.VideoLookup {
				p.EndGeneration(GenerationFailure{Stage: "exhausted", Outcome: result})
				return
			}
			retryLimit := failover.EffectiveSameProviderRetryLimit(result.Failure, selected.RetryLimit)
			// Spend this provider's repeat allowance before switching, then wait on
			// its delay and ctx.Done together; when cancellation wins that select,
			// return before another selection.
			if result.Failure.RetryableOnSameProvider && failover.SameProviderRetryAllowed(result.Failure, retries[selected.Provider.ID], retryLimit) {
				retries[selected.Provider.ID]++
				delay := failover.SameProviderRetryDelayFor(result.Failure, retries[selected.Provider.ID])
				p.ObserveGeneration(GenerationEvent{Kind: "retry", Selection: selected, Outcome: result, RetryCount: retries[selected.Provider.ID], RetryLimit: retryLimit, Delay: delay})
				select {
				case <-ctx.Done():
					return
				case <-time.After(delay):
				}
				continue
			}
			// Record transition bookkeeping and exclude this provider before testing
			// exhaustion, keeping the failed outcome available if a subsequent
			// selection cannot choose a candidate.
			p.SwitchGeneration(selected)
			excluded[selected.Provider.ID] = struct{}{}
			last = result
			if switches >= request.MaxSwitches {
				p.EndGeneration(GenerationFailure{Stage: "exhausted", Outcome: result})
				return
			}
			switches++
			if p.StopGeneration429(selected, result.Failure.StatusCode, switches) {
				p.EndGeneration(GenerationFailure{Stage: "exhausted", Outcome: result})
				return
			}
			p.ObserveGeneration(GenerationEvent{Kind: "switch", Selection: selected, Outcome: result, Switches: switches, MaxSwitches: request.MaxSwitches})
			continue
		}
		p.ReportGeneration(ctx, selected, result.Result, true, nil)
		p.CompleteGeneration(ctx, selected, result.Result)
		p.ObserveGeneration(GenerationEvent{Kind: "completed", Selection: selected, Switches: switches})
		return
	}
}

// CloneGenerationResult returns an independent snapshot of every current
// mutable result field, or nil for nil input. It copies Timings,
// image-output strings, size-count map, both header maps and each header
// slice; the current ForwardUsage contains scalar counts.
//
// Nil maps/slices remain nil, and nonnil empty image-output or header-value
// slices remain nonnil empty. A nonnil empty size-count map also stays
// present. These shape distinctions accompany the copied scalar
// observations so output and completion consumers can retain separate
// mutable evidence without rewriting the source result.
func CloneGenerationResult(input *GenerationResult) *GenerationResult {
	if input == nil {
		return nil
	}
	output := *input
	output.Timings = input.Timings.Clone()
	// Copy image-output storage and then restore a present empty slice, since
	// append to nil otherwise collapses nonnil empty evidence into nil.
	output.ImageOutputSizes = append([]string(nil), input.ImageOutputSizes...)
	if input.ImageOutputSizes != nil && output.ImageOutputSizes == nil {
		output.ImageOutputSizes = []string{}
	}
	if input.ImageSizeBreakdown != nil {
		output.ImageSizeBreakdown = make(map[string]int, len(input.ImageSizeBreakdown))
		maps.Copy(output.ImageSizeBreakdown, input.ImageSizeBreakdown)
	}
	output.Headers = cloneGenerationHeaders(input.Headers)
	output.ResponseHeaders = cloneGenerationHeaders(input.ResponseHeaders)
	return &output
}

// cloneGenerationHeaders copies a header map and every value slice,
// returning nil for nil input. Nil value slices stay nil, while nonnil
// empty values stay allocated and independent of the source.
func cloneGenerationHeaders(input map[string][]string) map[string][]string {
	if input == nil {
		return nil
	}
	output := make(map[string][]string, len(input))
	// Copy each header value independently while retaining nil and nonnil
	// empty slice shapes, so snapshot mutation cannot alter either source
	// map's evidence.
	for key, value := range input {
		if value == nil {
			output[key] = nil
		} else {
			output[key] = append([]string{}, value...)
		}
	}
	return output
}
