package creative

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// executor.go selects a creative provider and prepares an execution target
// with validated model attributes.
// A queued creative run keeps its public model name while provider selection
// reads current group permissions. Preparation applies one group mapping, then
// resolves the selected provider model and rechecks the upstream restriction
// before fixing the model used by the execution target. A stored platform
// confines the attempt to that platform; an unset platform tries OpenAI,
// Gemini, then Grok.
//
// Provider capacity belongs to the selected execution returned to the worker.
// Preparation releases acquired capacity when route validation fails, while
// the worker releases an accepted selection after generation. Captured model
// attributes and group context are carried into execution so validation and
// the upstream request use the prepared route rather than remapping the public
// name during the call.

// ExecutionGroup supplies current model policy and protocol settings for
// provider selection.
type ExecutionGroup struct {
	// current group model admission policy
	RoutingPolicy routing.GroupRoutingPolicy
	// AllowsOperation optionally checks current group protocol permission before
	// selection; a nil callback leaves all implemented platform operations available.
	AllowsOperation func(string, string) bool
	// ConfigureContext optionally adds group and protocol data for selection and
	// execution; both phases receive the same prepared platform and operation.
	ConfigureContext func(context.Context, string, string) context.Context
}

// Selection binds one provider admission result to model resolution,
// execution, and feedback.
type Selection struct {
	// ResolveModel, Execute, Release, and Report belong to the selected provider.
	// Preparation fills attributes, attributeContext, and configureContext before
	// publishing the execution target; callers keep this selection stable afterward.
	// ResolveAttributes optionally resolves the selected model after admission;
	// failures release the acquired slot and stop preparation with that error.
	ResolveAttributes func(context.Context, string) (modelcatalog.Resolution, error)
	// resolved selected model attributes
	attributes       *modelcatalog.Resolution
	attributeContext context.Context // captured model attribute session
	// captured group request context callback
	configureContext func(context.Context) context.Context
	ProviderID       int64  // selected execution provider identifier
	Platform         string // selected execution provider platform
	// Acquired reports owned provider capacity; Waiting reports deferred admission.
	// Prepare returns a pending signal for a waiting selection and transfers an
	// acquired selection to either execution or rejected-route cleanup.
	Acquired, Waiting bool
	// ResolveModel maps the group-mapped request to one upstream name after
	// capacity admission. Prepare trims and validates that result before fixing
	// the model passed to Execute.
	ResolveModel func(context.Context, string) string
	// Release returns acquired provider capacity. Preparation invokes it for
	// rejected routes; accepted executions transfer that responsibility to
	// the worker.
	Release func() // optional acquired provider slot release
	// Execute sends the prepared final model through this selected provider.
	// It receives the execution timeout context and returns candidate image bytes
	// for NormalizeCreativeOutputs before success feedback.
	Execute func(context.Context, CreativeRun, CreativeRunPayload, string) ([]CreativeOutput, error)
	// Report optionally receives the fixed model and normalized execution success.
	// Local validation failures before transport return without invoking
	// this callback.
	Report func(string, bool)
}

// Executor prepares current group rules and provider capacity for creative
// execution attempts.
type Executor struct {
	// shared model attribute resolution service
	Attributes *routing.ModelAttributeService
	// required current group rules reader
	Group func(context.Context, int64) (*ExecutionGroup, error)
	// OpenAI selects OpenAI; Grok selects Grok; Gemini selects Gemini providers.
	// Nil callbacks omit that platform, while acquired selections transfer their
	// Release obligation to a returned CreativeExecution or a rejected-route cleanup.
	OpenAI, Grok, Gemini func(context.Context, CreativeRun) (*Selection, error)
	Timeout              time.Duration // per-attempt execution time budget
}

// preparedExecution retains a selected provider and fixed upstream model for
// one attempt.
type preparedExecution struct {
	// selection retains callbacks while model fixes the validated upstream name.
	// The worker owns provider capacity separately and keeps both values stable
	// until the target returns or its execution context is cancelled.
	selection *Selection    // accepted provider selection pointer
	model     string        // fixed validated upstream model
	timeout   time.Duration // upstream execution time budget
}

// Prepare returns provider capacity and a target bound to a validated upstream
// image model. Callers release the returned execution after its upstream call;
// selections rejected during preparation are released here. Current group
// reads must succeed before any platform selection starts.
//
// The scheduler receives the run public model, while provider model resolution
// receives its single group-mapped value. The final upstream model must match
// the attempted platform, support image output, and satisfy the upstream-stage
// group restriction. Attribute resolution can override name-based image
// recognition and its result is carried to execution. When no route succeeds,
// the latest selection or route-validation error is returned; a waiting
// selection supplies ErrCreativeExecutionPending.
// @project-doc docs/domains/creative_studio.md#creative_model_policy
func (e *Executor) Prepare(ctx context.Context, run CreativeRun) (*CreativeExecution, error) {
	if e == nil {
		return nil, errors.New("creative executor is not configured")
	}
	if e.Group == nil {
		return nil, errors.New("creative group repository is not configured")
	}
	ctx = routing.WithModelAttributes(ctx, e.Attributes)
	group, err := e.Group(ctx, run.GroupID)
	if err != nil || group == nil {
		return nil, CreativeNonRetryableError("creative group %d policy is unavailable", run.GroupID)
	}
	policy := newGroupModelPolicy(group.RoutingPolicy)
	groupModel, allowed := policy.resolve(run.Model)
	if !allowed {
		return nil, CreativeNonRetryableError("creative model %s is restricted by group %d", run.Model, run.GroupID)
	}
	platforms := []string{PlatformOpenAI, PlatformGemini, PlatformGrok}
	if run.Platform != "" {
		platforms = []string{run.Platform}
	}
	var lastErr error
	for _, platform := range platforms {
		if group.AllowsOperation != nil && !group.AllowsOperation(platform, run.Operation) {
			continue
		}
		selectProvider := map[string]func(context.Context, CreativeRun) (*Selection, error){PlatformOpenAI: e.OpenAI, PlatformGrok: e.Grok, PlatformGemini: e.Gemini}[platform]
		if selectProvider == nil {
			continue
		}
		selectionCtx := ctx
		if group.ConfigureContext != nil {
			selectionCtx = group.ConfigureContext(ctx, platform, run.Operation)
		}
		selection, selectErr := selectProvider(selectionCtx, run)
		if selectErr != nil {
			lastErr = selectErr
			continue
		}
		if selection == nil {
			continue
		}
		// A waiting selection owns no execution slot. Preserve its
		// pending signal for
		// the worker short requeue path while continuing to try any
		// later platforms.
		if !selection.Acquired {
			if selection.Waiting {
				lastErr = ErrCreativeExecutionPending
			}
			continue
		}
		model := strings.TrimSpace(selection.ResolveModel(selectionCtx, groupModel))
		imageModel := false
		if selection.ResolveAttributes != nil {
			resolved, resolveErr := selection.ResolveAttributes(selectionCtx, model)
			// Attribute resolution failed after admission acquired
			// provider capacity.
			// Return that capacity before propagating the error so
			// a failed preparation
			// does not retain a slot that the worker never receives.
			if resolveErr != nil {
				if selection.Release != nil {
					selection.Release()
				}
				return nil, resolveErr
			}
			selection.attributes = &resolved
			selection.attributeContext = routing.CopyModelAttributes(context.Background(), selectionCtx)
			if resolved.Attributes.OutputModalities != nil {
				imageModel = slices.Contains(*resolved.Attributes.OutputModalities, "image")
			}
		}
		if group.ConfigureContext != nil {
			selection.configureContext = func(ctx context.Context) context.Context { return group.ConfigureContext(ctx, platform, run.Operation) }
		}
		// These route checks run after admission. Release a rejected
		// provider before
		// another platform is tried, because no returned execution can
		// own its slot.
		if selection.Platform != platform || !imageModel || !policy.allowsUpstream(model) {
			if selection.Release != nil {
				selection.Release()
			}
			lastErr = CreativeNonRetryableError("creative upstream model %s is unavailable for group %d", model, run.GroupID)
			continue
		}
		return &CreativeExecution{ProviderID: selection.ProviderID, Platform: selection.Platform, UpstreamModel: model, ReleaseFunc: selection.Release, Target: NewExecutionTarget(selection, model, e.Timeout)}, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, CreativeNonRetryableError("no compatible creative provider available for group %d model %s", run.GroupID, run.Model)
}

// IsRetryable returns the creative worker retry classification for the
// supplied execution error.
func (e *Executor) IsRetryable(err error) bool { return IsRetryableCreativeError(err) }

// NewExecutionTarget retains selection and model for a prepared execution
// attempt. Nonpositive timeouts select five minutes; callers keep the
// selection callbacks and captured attributes stable until execution
// completes.
func NewExecutionTarget(selection *Selection, model string, timeout time.Duration) ExecutionTarget {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &preparedExecution{selection: selection, model: model, timeout: timeout}
}

// Execute invokes the prepared upstream model and returns one normalized image
// with its provider identifier. It requires a nonempty fixed model, an Execute
// callback, and stable selection data; provider capacity cleanup
// belongs to the caller holding CreativeExecution.
//
// Captured attribute and group contexts are restored before validating
// selected-route sizes, reference count, quality, background, aspect ratio,
// and thinking level. Generation receives a copy of run with one requested
// output. The timeout covers the platform call, output normalization
// determines success feedback, and earlier local validation failures return
// before Report is invoked.
func (t *preparedExecution) Execute(ctx context.Context, run CreativeRun, payload CreativeRunPayload) (*CreativeExecuteResult, error) {
	if t == nil || t.selection == nil {
		return nil, errors.New("creative execution context is not configured")
	}
	model := strings.TrimSpace(t.model)
	if model == "" {
		return nil, errors.New("creative execution upstream model is not configured")
	}
	run.RequestedOutputCount = 1
	// Preparation captured attribute and group request data for the admitted route.
	// Restore those inputs on the worker context so platform construction uses the
	// same model rules while still inheriting worker cancellation.
	if t.selection.attributeContext != nil {
		ctx = routing.CopyModelAttributes(ctx, t.selection.attributeContext)
	}
	if t.selection.configureContext != nil {
		ctx = t.selection.configureContext(ctx)
	}
	if resolved := t.selection.attributes; resolved != nil {
		if media := resolved.Attributes.Media; media != nil {
			if run.FrameCount > 0 && !slices.Contains(media.OutputFormats, "gif") {
				return nil, CreativeNonRetryableError("GIF output is not supported by the selected model")
			}
			imageSize := payload.ImageSize
			if imageSize == "" {
				imageSize = run.ImageSize
			}
			if imageSize != "" && media.ImageSizes != nil && !slices.Contains(media.ImageSizes, imageSize) {
				return nil, CreativeNonRetryableError("image size is not supported by the selected route")
			}
			if media.MaxReferenceImages != nil && len(payload.Sources) > *media.MaxReferenceImages {
				return nil, CreativeNonRetryableError("reference image count exceeds the selected route limit")
			}
			for _, item := range []struct {
				value   string   // submitted optional media parameter
				allowed []string // selected route parameter options
			}{{payload.Quality, media.Qualities}, {payload.Background, media.BackgroundOptions}, {payload.AspectRatio, media.AspectRatios}} {
				if item.value != "" && item.allowed != nil && !slices.Contains(item.allowed, item.value) {
					return nil, CreativeNonRetryableError("image parameter is not supported by the selected route")
				}
			}
		}
		if payload.ThinkingLevel != "" {
			if supported := resolved.Attributes.EffortSupport(payload.ThinkingLevel); supported != nil && !*supported {
				return nil, CreativeNonRetryableError("reasoning effort is not supported by the selected route")
			}
		}
	}
	// The worker lease context remains the parent of the execution budget, so
	// shutdown or lease cancellation also interrupts platform transport and result
	// downloads covered by this attempt.
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	s := t.selection
	switch s.Platform {
	case PlatformOpenAI, PlatformGrok, PlatformGemini:
	default:
		return nil, CreativeNonRetryableError("creative executor unsupported provider platform %s", s.Platform)
	}
	outputs, err := s.Execute(ctx, run, payload, model)
	if err == nil {
		outputs, err = NormalizeCreativeOutputs(outputs)
	}
	if err == nil && run.FrameCount > 0 && (len(outputs) != 1 || outputs[0].Mime != "image/gif") {
		err = CreativeImageResultError("INVALID_IMAGE_RESPONSE", "platform did not return GIF output")
	}
	if s.Report != nil {
		s.Report(model, err == nil)
	}
	if err != nil {
		return nil, err
	}
	return &CreativeExecuteResult{Outputs: outputs, ProviderID: s.ProviderID}, nil
}
