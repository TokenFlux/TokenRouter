package openaiforward

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// passthrough.go prepares and forwards Responses payloads with provider policy
// and bounded recovery before output.
// Responses passthrough can retain unknown payload fields while still applying
// selected-provider model, OAuth identity, client-tool, and image policy.
// Initial model replacement changes the image-intent evidence, and the retained
// canonical body lets later image checks distinguish prelude intent from the
// prepared request. Codex preparation rereads stream mode after its OAuth
// normalization.
//
// HTTP failures are captured and closed before provider-local rejected-field,
// agent-task, or Compact recovery selects another exchange. Reader Compact
// signals delegate adopted-body cleanup to their recovery callback; other read
// failures close the response before final presentation. The HTTP output sink
// owns turn-state header provenance; response ownership and usage snapshots
// follow completed reading. Image observations
// attach image billing identity to the final result.

// PassthroughInput carries prepared bytes, retained image intent, and response observations for one attempt.
type PassthroughInput struct {
	// Body is the prepared exchange payload; CanonicalImageIntentBody retains
	// prelude image-intent evidence. ImageIntentInvalidated tells ImageIntent
	// whether later edits require consulting prepared bytes again.
	Body, CanonicalImageIntentBody []byte
	Model                          string // incoming response model identity
	// ImageIntentInvalidated records prior image evidence changes; the runner also
	// sets it when initial provider model replacement changes the prepared
	// request.
	ImageIntentInvalidated bool
	ReasoningEffort        *string   // previously recorded effective effort
	Stream                 bool      // incoming streaming mode choice
	StartedAt              time.Time // request arrival timestamp
}

// ImageBilling carries the image charge identity and its output/input size tiers.
type ImageBilling struct {
	// Model is the image charge model; SizeTier is the output size tier;
	// InputSize is the input-image tier.
	Model, SizeTier, InputSize string
}

// RunPassthrough prepares raw Responses bytes and reads the selected provider's
// passthrough output. Provider model mapping uses a byte patch, Codex input
// follows identity and instruction rules, and client-tool lowering retains
// response restoration state. For Codex preparation, stream mode is reread
// after OAuth normalization.
//
// Rejected-field, agent-task, and Compact recovery use separate state after
// captured HTTP bodies are closed. An adopted reader Compact repair closes its
// rejected response through the installed callback before replay. Turn-state
// provenance is observed at successful headers, while ownership and usage
// snapshots follow successful reading. The result keeps the incoming response
// model; UpstreamModel records initial mapping or Compact replacement and can
// remain empty, and observed images supply the image billing fields. Compact
// recovery reapplies instruction preparation for the replacement model before
// sending it, including when the first model did not require a default prompt.
func RunPassthrough(ctx context.Context, in PassthroughInput, p PassthroughPorts) (*Result, error) {
	profile := p.Profile()
	body, canonicalImageIntentBody, reqModel := in.Body, in.CanonicalImageIntentBody, in.Model
	attemptImageIntentInvalidated, reasoningEffort, reqStream, startTime := in.ImageIntentInvalidated, in.ReasoningEffort, in.Stream, in.StartedAt
	requestedModel := reqModel
	upstreamPassthroughModel := ""
	responsesLite := profile.OpenAI && ((p.HasContext() && p.LiteHeader()) || p.LitePayloadFlag(body))
	defaultInstructions := ""
	prepareInstructions := func(body []byte, model string) ([]byte, error) {
		// An existing gateway prompt belongs to the previous model. Replace only
		// that segment, then reuse the prepared body on same-model retries.
		if defaultInstructions != "" {
			updated, err := openai.RefreshCodexDefaultInstructions(body, defaultInstructions, model)
			if err != nil {
				return nil, fmt.Errorf("refresh Compact default instructions: %w", err)
			}
			return updated, nil
		}
		if rejectReason := p.InstructionsRejection(model, body); rejectReason != "" {
			rejectMsg := "OpenAI codex passthrough requires a non-empty instructions field"
			p.PolicyDenied()
			p.LogInstructionsRejected(ctx, reqModel, rejectReason, body)
			p.Reject(403, "forbidden_error", rejectMsg, "")
			return nil, fmt.Errorf("openai passthrough rejected before upstream: %s", rejectReason)
		}
		// Lite clients can send their instructions as developer input. Synthesize
		// only when both that input and the top-level field are absent, and retain
		// the generated text for possible Compact recovery.
		if p.CodexModel(model) && !gjson.GetBytes(body, "instructions").Exists() &&
			(!responsesLite || !openai.HasResponsesLiteDeveloperInstructions(body)) {
			defaultInstructions = openai.DefaultCodexSynthInstructions(model)
			nextBody, setErr := sjson.SetBytes(body, "instructions", defaultInstructions)
			if setErr != nil {
				return nil, fmt.Errorf("set passthrough codex instructions: %w", setErr)
			}
			return nextBody, nil
		}
		return body, nil
	}

	// Patch the model field in raw bytes so large bodies and unknown extension
	// fields retain their existing JSON representation through provider mapping.
	instructionModel := p.ForwardModel(reqModel, p.CompactPath())
	if instructionModel == "" {
		instructionModel = reqModel
	}
	if instructionModel != reqModel {
		nextBody, setErr := sjson.SetBytes(body, "model", instructionModel)
		if setErr != nil {
			return nil, fmt.Errorf("set passthrough model: %w", setErr)
		}
		body = nextBody
		upstreamPassthroughModel = instructionModel
		attemptImageIntentInvalidated = true
	}

	if profile.UsesCodex {
		preparedBody, instructionErr := prepareInstructions(body, instructionModel)
		if instructionErr != nil {
			return nil, instructionErr
		}
		body = preparedBody

		// Apply Codex body compatibility before reading its stream flag and scoping
		// metadata, so subsequent request identity handling uses the adopted OAuth
		// representation.
		normalizedBody, normalized, err := p.OAuthBody(body, p.CompactPath())
		if err != nil {
			return nil, err
		}
		if normalized {
			body = normalizedBody
		}
		reqStream = gjson.GetBytes(body, "stream").Bool()

		// Namespace raw metadata before deriving the fingerprint; header construction
		// later consumes the staged IDs for the same selected provider attempt.
		providerScopedBody, providerScoped, scopeErr := p.ProviderIdentityRaw(body)
		if scopeErr != nil {
			return nil, scopeErr
		}
		if providerScoped {
			body = providerScopedBody
		}

		// Clear an earlier attempt's staged fingerprint before this provider derives
		// its own IDs; Compact leaves the cleared value because it skips metadata
		// fingerprint preparation.
		p.StageFingerprint(nil)
		// Use the shared fingerprint rules with a local client_metadata patch to limit parsing of large bodies.
		if !p.CompactPath() {
			fingerprintIDs := p.Fingerprint()
			if fingerprintIDs != nil {
				updatedBody, changed, fingerprintErr := p.FingerprintBody(body, fingerprintIDs)
				if fingerprintErr != nil {
					return nil, fingerprintErr
				}
				if changed {
					body = updatedBody
				}
			}
			// Stage the current derivation even when it is nil, so request construction
			// cannot use header IDs retained from a previous provider attempt.
			p.StageFingerprint(fingerprintIDs)
		}
	}
	// Responses compatibility runs after credential-specific identity preparation;
	// reserved names are then aliased for OAuth-like credentials with reverse
	// names retained for output restoration.
	if profile.OpenAI {
		normalizedBody, normalized, normalizeErr := p.CompatibilityBody(body, responsesLite)
		if normalizeErr != nil {
			return nil, fmt.Errorf("normalize passthrough Responses compatibility: %w", normalizeErr)
		}
		if normalized {
			body = normalizedBody
		}
		if profile.OAuthLike {
			aliasedBody, reverse, aliased, aliasErr := p.ReservedToolNames(body)
			if aliasErr != nil {
				return nil, aliasErr
			}
			p.MergeToolNames(reverse)
			if aliased {
				body = aliasedBody
			}
		}
	}

	// Lower client tools for non-Compact API Key passthrough before exchange,
	// retaining request-local mappings so response wrappers can restore the
	// caller's tool representation.
	if profile.OpenAI && profile.APIKey &&
		!p.CompactPath() && p.NeedsClientTools(body) {
		adaptedBody, adaptErr := p.AdaptClientTools(body)
		if adaptErr != nil {
			return nil, adaptErr
		}
		body = adaptedBody
	}

	// Clean empty base64 image placeholders after client-tool preparation so late
	// byte cleanup does not bypass model mapping or response restoration state.
	sanitizedBody, sanitized, err := openai.SanitizeEmptyBase64InputImagesInOpenAIBody(body)
	if err != nil {
		return nil, err
	}
	if sanitized {
		body = sanitizedBody
	}
	// Earlier OAuth/API key normalization can remove parallel_tool_calls on requests
	// without tools. Restore false here for Responses Lite.
	if p.HasContext() && p.LiteHeader() {
		liteBody, liteChanged, liteErr := p.NormalizeLite(body)
		if liteErr != nil {
			return nil, liteErr
		}
		if liteChanged {
			body = liteBody
		}
	}

	policyModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if policyModel == "" {
		policyModel = reqModel
	}
	attributes, attributeErr := p.ModelAttributes(ctx, policyModel)
	if attributeErr != nil {
		return nil, attributeErr
	}
	updatedBody, policyErr := p.ApplyFastPass(ctx, policyModel, body)
	if policyErr != nil {
		return nil, policyErr
	}
	body = updatedBody

	// Broad intent includes retained image evidence for state and billing;
	// explicit intent checks the prepared request and selected model before the
	// group permission decision.
	imageIntent := p.ImageIntent(reqModel, canonicalImageIntentBody, policyModel, body, attemptImageIntentInvalidated) || attributes.Attributes.OutputOnly("image")
	explicitImageIntent := attributes.Attributes.OutputOnly("image") || p.ExplicitImageIntent(policyModel, body)
	if explicitImageIntent && !p.ImageAllowed() {
		p.FeatureDenied()
		p.Reject(403, "permission_error", p.ImagePermissionMessage(), "")
		return nil, errors.New("image generation disabled for group")
	}
	imageBillingModel := ""
	imageSizeTier := ""
	imageInputSize := ""
	if imageIntent {
		var imageCfgErr error
		imageCfg, imageCfgErr := p.ImageBilling(body, reqModel)
		if imageCfgErr != nil {
			return nil, imageCfgErr
		}
		imageBillingModel = imageCfg.Model
		imageSizeTier = imageCfg.SizeTier
		imageInputSize = imageCfg.InputSize
	}

	p.Log(
		"[OpenAI auto passthrough] Selected automatic passthrough: provider=%d name=%s type=%s model=%s stream=%v",
		profile.ID,
		profile.Name,
		profile.Type,
		reqModel,
		reqStream,
	)
	if reqStream {
		p.WarnTimeoutHeaders(ctx)
	}

	token, err := p.AccessToken(ctx)
	if err != nil {
		return nil, err
	}

	p.PrepareTransportPass()
	p.MarkPassthrough()

	agentTaskRecoveryTried := false
	compactModelFallbackRetried := false
	rejectedFieldRetryState := p.RetryState(body)
	var resp *http.Response
	var usage *wire.ForwardUsage

	responseID := ""
	imageCount := 0
	imageFrameCount := 0
	var deliveryErr error
	var imageOutputSizes []string
	for {
		// Every Compact recovery reenters this loop. Apply the replacement model's
		// instruction rules once, even if the first model left the field absent.
		if profile.UsesCodex && compactModelFallbackRetried && instructionModel != upstreamPassthroughModel {
			retryBody, instructionErr := prepareInstructions(body, upstreamPassthroughModel)
			if instructionErr != nil {
				return nil, instructionErr
			}
			body = retryBody
			instructionModel = upstreamPassthroughModel
		}
		actualModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
		if actualModel == "" {
			actualModel = reqModel
		}
		p.UpstreamModelObserved(actualModel)
		upstreamCtx, releaseUpstreamCtx := p.UpstreamContext(ctx)
		upstreamReq, buildErr := p.BuildPass(upstreamCtx, body, token)
		releaseUpstreamCtx()
		if buildErr != nil {
			return nil, buildErr
		}

		upstreamStart := time.Now()
		resp, err = p.SendPass(upstreamReq)
		p.Latency(time.Since(upstreamStart))
		if err != nil {
			// Transport failures request outer failover; the shared handler also unschedules persistent faults.
			return nil, p.TransportErrorPass(ctx, err)
		}
		if resp.StatusCode >= 400 {
			// Capture failure evidence and close its network body before recovery; a
			// replay reader keeps the same bytes available for later policy and final
			// error presentation.
			probeBody := p.ReadErrorBody(resp)
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(probeBody))
			// Remember adopted rejected-field payloads before another exchange,
			// preventing repeated normalization from replaying bytes already seen by
			// this attempt's recovery state.
			if retryBody, reason, changed, retryErr := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(resp.StatusCode, body, probeBody); retryErr != nil {
				return nil, fmt.Errorf("normalize passthrough rejected Responses field retry body: %w", retryErr)
			} else if changed && rejectedFieldRetryState.Allow(retryBody) {
				body = retryBody
				p.Log("[OpenAI] Retrying passthrough request after %s (provider: %s)", reason, profile.Name)
				continue
			}
			// Spend the separate agent-task budget before replay so repeated task
			// rejection can proceed to Compact or final failure handling on a later
			// response.
			if !agentTaskRecoveryTried && p.IsAgentIdentity(ctx) && p.InvalidAgentTask(resp.StatusCode, probeBody) {
				agentTaskRecoveryTried = true
				if recoveryErr := p.RecoverAgentTask(ctx); recoveryErr != nil {
					return nil, fmt.Errorf("agent identity task recovery failed: %w", recoveryErr)
				}
				continue
			}
			upstreamMsg := p.ErrorMessage(probeBody)
			// A captured Compact HTTP failure can replace the body and vendor model
			// once; record the adopted model before restarting exchange with the spent-
			// budget flag.
			if retryBody, fallbackModel, retry := p.CompactRetry(requestedModel, body, resp.StatusCode, upstreamMsg, probeBody, compactModelFallbackRetried); retry {
				p.CompactObserved(resp, probeBody, upstreamMsg)
				fromModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
				body = retryBody
				upstreamPassthroughModel = fallbackModel
				compactModelFallbackRetried = true
				p.UpstreamModelObserved(fallbackModel)
				p.Log(
					"[OpenAI passthrough] Retrying explicit compact request once with fallback model (provider: %s, from: %s, to: %s, upstream_code: %s)",
					profile.Name, fromModel, fallbackModel, p.ErrorCode(probeBody),
				)
				continue
			}

			// Check capacity and transient API key failures before committing passthrough output
			// so the outer handler can select another provider.
			// Reuse the body already read for recovery checks.
			if p.ShouldFailover(resp.StatusCode, probeBody) {
				return nil, p.FailoverError(ctx, resp, body, probeBody)
			}
			return nil, p.ErrorResponsePass(ctx, resp, body, probeBody)
		}

		p.WrapResponseBody(resp)

		if reqStream {
			result, handleErr := openai.ReadPassthroughStreaming(ctx, resp, upstream.NewOutputContext(p.Sink()), p.ResponseOptions(ctx), startTime, reqModel, upstreamPassthroughModel)
			if handleErr != nil && !errors.Is(handleErr, wire.ErrGIFResultInvalid) {
				// The installed Compact signal helper closes a rejected response when it
				// adopts recovery, releasing its body before this loop constructs the
				// replacement exchange.
				if retryBody, fallbackModel, retry := p.CompactFromSignal(requestedModel, body, handleErr, compactModelFallbackRetried, resp); retry {
					body = retryBody
					upstreamPassthroughModel = fallbackModel
					compactModelFallbackRetried = true
					continue
				}
				// An unrecovered Compact reader failure becomes HTTP-like evidence after
				// closing the original body, allowing ordinary passthrough failover
				// classification or final error presentation.
				if signal, ok := p.CompactSignal(handleErr); ok {
					_ = resp.Body.Close()
					compactResp, compactBody := p.CompactErrorResponse(resp, signal)
					if p.ShouldFailover(compactResp.StatusCode, compactBody) {
						return nil, p.FailoverError(ctx, compactResp, body, compactBody)
					}
					return nil, p.ErrorResponsePass(ctx, compactResp, body, compactBody)
				}
				_ = resp.Body.Close()
				return nil, handleErr
			}
			deliveryErr = handleErr
			usage = result.Usage

			responseID = strings.TrimSpace(result.ResponseID)
			imageCount = result.ImageCount
			imageFrameCount = result.ImageFrameCount
			imageOutputSizes = result.ImageOutputSizes
		} else {
			result, handleErr := openai.ReadPassthroughNonStreaming(ctx, resp, p.Sink(), p.ResponseOptions(ctx), reqModel, upstreamPassthroughModel)
			if handleErr != nil && !errors.Is(handleErr, wire.ErrGIFResultInvalid) {
				// The installed Compact signal helper closes a rejected response when it
				// adopts recovery, releasing its body before this loop constructs the
				// replacement exchange.
				if retryBody, fallbackModel, retry := p.CompactFromSignal(requestedModel, body, handleErr, compactModelFallbackRetried, resp); retry {
					body = retryBody
					upstreamPassthroughModel = fallbackModel
					compactModelFallbackRetried = true
					continue
				}
				// An unrecovered Compact reader failure becomes HTTP-like evidence after
				// closing the original body, allowing ordinary passthrough failover
				// classification or final error presentation.
				if signal, ok := p.CompactSignal(handleErr); ok {
					_ = resp.Body.Close()
					compactResp, compactBody := p.CompactErrorResponse(resp, signal)
					if p.ShouldFailover(compactResp.StatusCode, compactBody) {
						return nil, p.FailoverError(ctx, compactResp, body, compactBody)
					}
					return nil, p.ErrorResponsePass(ctx, compactResp, body, compactBody)
				}
				_ = resp.Body.Close()
				return nil, handleErr
			}
			deliveryErr = handleErr
			usage = result.Usage
			responseID = strings.TrimSpace(result.ResponseID)
			imageCount = result.ImageCount
			imageFrameCount = result.ImageFrameCount
			imageOutputSizes = result.ImageOutputSizes
		}
		break
	}
	defer func() { _ = resp.Body.Close() }()
	serviceTier := p.ServiceTier(body)
	p.BindOwner(ctx, responseID)

	if !profile.Shadow {
		p.UpdateCodexUsage(ctx, resp.Header)
	}

	// Successful reading can leave token usage absent; return empty quantities
	// with response identity and timing so completion receives a consistent
	// observation shape.
	if usage == nil {
		usage = &wire.ForwardUsage{}
	}

	forwardResult := &Result{
		RequestID:                   resp.Header.Get("x-request-id"),
		Headers:                     resp.Header,
		ResponseID:                  responseID,
		Usage:                       *usage,
		Model:                       reqModel,
		UpstreamModel:               upstreamPassthroughModel,
		UpstreamResponseServiceTier: p.ObservedServiceTier(),
		ServiceTier:                 p.ResolvedServiceTier(serviceTier),
		ReasoningEffort:             reasoningEffort,
		Stream:                      reqStream,
		OpenAIWSMode:                false,
		Duration:                    time.Since(startTime),
		Timings:                     nil,
	}
	// Observed image output supplies the image billing identity and size evidence,
	// allowing completion to use media pricing for the returned generated-image
	// count.
	if imageCount > 0 {
		forwardResult.ImageCount = imageCount
		forwardResult.ImageFrameCount = imageFrameCount
		forwardResult.ImageSize = imageSizeTier
		forwardResult.ImageInputSize = imageInputSize
		forwardResult.ImageOutputSizes = imageOutputSizes
		forwardResult.BillingModel = imageBillingModel
	}
	return forwardResult, deliveryErr
}
