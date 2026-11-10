package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	openaiexecution "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_responses_execution.go dispatches Responses attempts and maintains
// request representations during encrypted recovery.
// A Responses attempt separates prelude dispatch from full native HTTP
// transformation, so raw Chat, Grok, Anthropic and passthrough routes retain
// their own execution behavior. The native branch keeps original client,
// billing and upstream model values alongside a byte view and optional decoded
// request map.
//
// Ciphertext recovery can change input after model policy has already run. The
// decoder first applies pending byte patches, invalidates a cached map when
// current bytes change and decodes the resulting view again. Lineage uses a
// session identity derived before encrypted items are stripped, so later
// rejection marking refers to the same prepared entry that a repeated request
// can present. HTTP exchange callbacks carry one token, fingerprint and route
// preparation through the shared runner's local recovery and response-reading
// phases.

// OpenAIResponsesExecutor combines selected-attempt request, output, text and
// lineage services for Responses forwarding. App assembly shares these
// dependencies with other protocol executors; each Forward call owns its
// byte/decoded preparation state.
type OpenAIResponsesExecutor struct {
	Requests *OpenAIRequests       // shared upstream request builder
	Output   *OpenAIResponseOutput // shared Responses output bindings
	Text     *OpenAITextExecutor   // shared compatible text executor
	Grok     *GrokExecutor         // shared Grok Responses executor
	// shared encrypted-history rejection tracker
	Lineage *OpenAIEncryptedLineage
	// Responses image injection policy
	ImageBridge *gatewayprovider.ResponseImagePolicy
}

// Forward prepares and executes one selected Responses provider attempt,
// returning observations and failure for outer handling. Callers retain the
// live HTTP context and app-bound services during synchronous output. Prelude
// dispatch can delegate directly to raw Chat, Grok, native Anthropic or
// passthrough before native HTTP transformation runs.
//
// The native branch retains model identities and prepared byte/decoded views
// through ciphertext recovery. Pending patches invalidate cached decoded state
// before another map is used, and changed retry bytes also force a fresh
// request view. Lineage identity is derived before encrypted-history stripping
// and retained for marking rejected digests. The shared HTTP runner owns
// response-body cleanup, local recovery and reading; the outer text loop
// decides provider switching and completion. Deferred model capture uses the
// returned result even when failure accompanies partial observations.
func (s *OpenAIResponsesExecutor) Forward(ctx context.Context, c *gin.Context, provider *gatewayprovider.ExecutionProvider, body []byte) (out *forwardcore.OpenAIResult, failure error) {
	defer func() { captureResponseModel(c, out) }()
	var routeErr error
	provider, routeErr = gatewayprovider.ProviderForProtocolAttempt(ctx, provider)
	if routeErr != nil {
		return nil, routeErr
	}
	profile := openAIForwardProfile(provider)
	prepared, err := openaiexecution.PreparePrelude(ctx, body, profile, openAIForwardPreludeAdapter{s: s, c: c, provider: provider, ctx: ctx})
	if err != nil {
		return nil, err
	}
	body = prepared.Body
	startTime := prepared.StartedAt
	canonicalImageIntentBody := prepared.CanonicalImageIntentBody
	tlsRouterMatch := prepared.TLS
	originalBody := prepared.OriginalBody
	requestView := prepared.View
	reqModel, reqStream, promptCacheKey := requestView.Model, requestView.Stream, requestView.PromptCacheKey
	originalModel := reqModel
	isCodexCLI := prepared.CodexCLI
	switch prepared.Route {
	case openaiexecution.DispatchRawChat:
		return s.Text.ResponsesViaRawChat(ctx, c, provider, body, tlsRouterMatch)
	case openaiexecution.DispatchGrok:
		return s.Grok.ForwardResponses(ctx, c, provider, body, originalModel, reqStream, startTime)
	case openaiexecution.DispatchAnthropic:
		return s.Text.NativeResponses(ctx, c, provider, body, reqModel)
	case openaiexecution.DispatchPassthrough:
		return s.Text.Passthrough(ctx, c, provider, originalBody, canonicalImageIntentBody, reqModel, prepared.ImageIntentInvalidated, prepared.ReasoningEffort, reqStream, startTime, tlsRouterMatch)
	}

	transformed, err := openaiexecution.TransformRequest(ctx, prepared, profile, openAIForwardTransformAdapter{openAIForwardPreludeAdapter{s: s, c: c, provider: provider, ctx: ctx}})
	if err != nil {
		return nil, err
	}
	body = transformed.Body
	requestView = transformed.View
	reqModel = transformed.Model
	promptCacheKey = transformed.PromptCacheKey
	requestedModel, billingModel, upstreamModel := transformed.RequestedModel, transformed.BillingModel, transformed.UpstreamModel
	imageIntent := transformed.ImageIntent
	fingerprintIDs := transformed.Fingerprint
	reqBody := transformed.Decoded
	// HTTP ciphertext recovery reuses model/policy preparation. Apply pending byte
	// patches before decoding so the mutable map describes the body selected for this retry.
	ensureReqBody := func() (map[string]any, error) {
		// A patched byte view supersedes its earlier decoded map; drop that cache
		// before a retry decoder can trim history from stale request content.
		if requestView.HasPatches() {
			patched, err := requestView.ApplyPatches()
			if err != nil {
				return nil, err
			}
			body = patched
			requestView = requeststate.NewOpenAIRequestView(body)
			reqBody = nil
		}
		if reqBody != nil {
			return reqBody, nil
		}
		decoded, err := requestView.Decode()
		if err != nil {
			return nil, err
		}
		reqBody = decoded
		return reqBody, nil
	}
	// Known rejected ciphertext is stripped using the session key from this
	// prepared entry, before stripping or recovery changes its bytes. Retain that
	// identity so later rejection marking matches another request presenting the same entry.
	lineageGroupID := OpenAIResponseGroupID(c)
	lineageEntryBody := body
	lineageSessionHash := ""
	if stateStore := s.Lineage.Store; stateStore != nil && stateStore.HasAnySessionInvalidEncryptedContent() {
		lineageSessionHash = GenerateOpenAISessionHash(c, body)
		if invalidDigests := stateStore.GetSessionInvalidEncryptedContentDigests(lineageGroupID, lineageSessionHash); len(invalidDigests) > 0 {
			strippedBody, strippedCount := s.Lineage.Strip(
				body, invalidDigests, "invalid_encrypted_lineage_strip", provider.Record.ID, 0,
			)
			// Adopt successful lineage stripping and retire the decoded cache; later
			// body validation or recovery must inspect the current stripped byte view.
			if strippedCount > 0 {
				body = strippedBody
				requestView = requeststate.NewOpenAIRequestView(body)
				reqBody = nil
			}
		}
	}
	imageBillingModel := ""
	imageSizeTier := ""
	imageInputSize := ""
	if imageIntent {
		imageCfg, imageCfgErr := gatewayprovider.ResolveResponsesImageBilling(ctx, provider, body, s.Text.modelAttributes())
		if imageCfgErr != nil {
			WriteOpenAIModelAttributeError(c, imageCfgErr, c.Writer.Written())
			return nil, imageCfgErr
		}
		imageBillingModel = imageCfg.Model
		imageSizeTier = imageCfg.SizeTier
		imageInputSize = imageCfg.InputSize
	}

	token, _, err := s.Requests.Credentials.Resolve(ctx, gatewayprovider.ExecutionRecord(provider))
	if err != nil {
		return nil, err
	}
	SetOpsUpstreamModel(c, upstreamModel)

	reasoningEffort := requeststate.ExtractOpenAIReasoningEffortFromBody(body)
	// The mapped model guides enabled-thinking display effort after request
	// transformation; this usage fallback does not add another outgoing reasoning control.
	reasoningEffort = gatewayprovider.ApplyThinkingEnabledFallback(reasoningEffort, body, reqModel)
	reasoningEffortValue := ""
	if reasoningEffort != nil {
		reasoningEffortValue = *reasoningEffort
	}
	firstOutputTimeout := time.Duration(0)
	if reqStream && provider.Record.Platform == capability.PlatformOpenAI {
		firstOutputTimeout = s.Output.FirstOutputTimeout(reasoningEffortValue)
	}

	input := openaiexecution.HTTPInput{
		Body: body, LineageEntryBody: lineageEntryBody, ProviderID: provider.Record.ID, ProviderName: provider.Record.Name, Platform: provider.Record.Platform,
		RequestedModel: requestedModel, OriginalModel: originalModel, BillingModel: billingModel, UpstreamModel: upstreamModel,
		ReasoningEffort: reasoningEffort, ReasoningEffortValue: reasoningEffortValue,
		ImageBillingModel: imageBillingModel, ImageSizeTier: imageSizeTier, ImageInputSize: imageInputSize,
		ImageMainModelAdapted: transformed.ImageMainModelAdapted,
		DefaultInstructions:   transformed.DefaultInstructions,
		Stream:                reqStream, OAuth: provider.View().IsOAuth(), Shadow: provider.View().IsShadow(), Grok: provider.View().IsGrok(), StartedAt: startTime,
	}
	exchange := openai.HTTPExchangeOptions{
		StartedAt:          startTime,
		FirstOutputTimeout: firstOutputTimeout,
		RequestContext:     gatewayprovider.DetachUpstreamContext,
		Build: func(ctx context.Context, body []byte) (*http.Request, error) {
			return s.Requests.Build(ctx, c, provider, body, token, reqStream, promptCacheKey, isCodexCLI, tlsRouterMatch)
		},
		ApplyHeaders: func(headers http.Header) { openai.ApplyCodexFingerprintHeaders(headers, fingerprintIDs) },
		Do: func(request *http.Request) (*http.Response, error) {
			resetResponseModel(c)
			proxyURL := ""
			if provider.Record.ProxyID != nil && provider.Record.Proxy != nil {
				proxyURL = provider.Record.Proxy.URL()
			}
			return s.Requests.Transport.DoWithTLS(request, proxyURL, provider.Record.ID, provider.Record.Concurrency, s.Requests.TLSProfile(provider, tlsRouterMatch))
		},
		Latency: func(elapsed time.Duration) {
			// Ops latency stores whole milliseconds from this exchange duration.
			SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, elapsed.Milliseconds())
		},
		HeaderTimeout: func() error {
			return s.Output.FirstOutputFailure(ctx, c, provider, startTime, originalModel, reasoningEffortValue, firstOutputTimeout, "response_headers", nil)
		},
		TransportError: func(err error) error { return s.Requests.Failure.Handle(ctx, c, provider, err, false) },
	}
	options := s.nativeForwardHTTPOptions(ctx, c, provider, input, exchange,
		func(current []byte) ([]byte, bool, error) {
			return prepareOpenAIHTTPEncryptedRetry(current, func(current []byte) (map[string]any, error) {
				// Local recovery may supply new bytes after prior preparation. Invalidate
				// both view and map before trimming so the repair uses that current request.
				if !bytes.Equal(body, current) {
					body = current
					requestView = requeststate.NewOpenAIRequestView(body)
					reqBody = nil
				}
				return ensureReqBody()
			})
		},
		func(entry []byte) {
			digests := openai.CollectOpenAIEncryptedContentDigestsRaw(entry)
			if len(digests) == 0 {
				return
			}
			// A request with no prior rejected lineage still needs its entry identity
			// before these newly rejected digests are published for later session lookup.
			if lineageSessionHash == "" {
				lineageSessionHash = GenerateOpenAISessionHash(c, entry)
			}
			s.Lineage.Mark(lineageGroupID, lineageSessionHash, digests)
		},
	)
	// The HTTP runner clears this explicit map reference after a successful
	// response and before reading output; byte/view state remains available for later recovery decoding.
	options.ReleaseDecodedRequest = func() { reqBody = nil }
	result, err := openaiexecution.RunHTTP(ctx, input, options)
	return openaiexecution.ToForwardResult(result), err
}

// shouldForwardOpenAIResponsesViaRawChatCompletions delegates raw-Chat routing
// policy for the selected attempt provider.
func shouldForwardOpenAIResponsesViaRawChatCompletions(provider *gatewayprovider.ExecutionProvider) bool {
	return gatewayprovider.ExecutionModelPolicy(provider).RawChat()
}
