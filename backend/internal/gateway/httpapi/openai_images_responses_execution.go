package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"
	mediaprovider "github.com/TokenFlux/TokenRouter/internal/gateway/media/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_images_responses_execution.go executes OAuth image requests through
// Responses and classifies image delivery failures.
// OAuth image generation uses a provider-selected Responses main model and a
// separately resolved image-tool model. Native conversion carries parsed
// prompts and image inputs into that tool request, then returns Images JSON or
// image-specific SSE events while preserving generation count, dimensions and
// token observations.
//
// An HTTP rejection can recover an invalid Agent Identity task once before
// ordinary health policy or final presentation. A read failure after HTTP
// success has a separate decision: heartbeat-adjusted output determines
// whether retry can still occur. Structured image-tool unavailability may
// cool the provider's image scope, while missing images inferred from model
// text leave that scope available. Provider retry classification remains
// separate from already delivered output and positive partial-image
// retention.

// buildOpenAIImagesResponsesRequest delegates parsed image input to native
// Responses conversion with the supplied tool and main models. Missing parsed
// input, either prompt or main model, or required editing images returns a
// validation error before an upstream request is built.
func buildOpenAIImagesResponsesRequest(parsed *gatewaymedia.ImageRequest, toolModel, mainModel string) ([]byte, error) {
	return openai.BuildOpenAIImagesResponsesRequest(gatewaymedia.NativeImageRequest(parsed), toolModel, mainModel)
}

// handleOpenAIImagesErrorResponse presents or classifies an HTTP image
// failure using replayable response bytes. It records sanitized message
// diagnostics and optional bounded body detail before the shared
// response-failure resolver chooses Cyber, health, failover, configured
// rewrite or native error output.
//
// Cyber classification presents warning output before returning its warning
// identity. Other paths apply current provider policy before generic
// response, failover and display rewriting; the policy decision retained by
// callbacks supplies those later choices. Callers provide usable response,
// selected provider and HTTP output dependencies. The optional first
// requested model identifies model-specific policy, and native execution
// closes the replay body after this synchronous handling.
func (s *OpenAIImagesExecutor) handleOpenAIImagesErrorResponse(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	requestBody []byte,
	requestedModel ...string,
) (*forwardcore.OpenAIResult, error) {
	body := s.Output.ReadErrorBody(resp)

	upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(body)))
	upstreamDetail := ""
	if s.Output.Options.LogUpstreamErrorBody {
		maxBytes := s.Output.Options.LogUpstreamErrorBodyMaxBytes
		// Use the diagnostic default when no positive body-detail cap is
		// configured. UTF-8-safe truncation bounds Ops detail while retaining the
		// replay body for failure policy.
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = logredact.TruncateUTF8(string(body), maxBytes)
	}
	SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	LogOpenAIInstructionsRequiredDebug(ctx, c, provider, resp.StatusCode, upstreamMsg, requestBody, body)

	if s.Output.Options.LogUpstreamErrorBody {
		logging.LegacyPrintf("service.openai_gateway",
			"OpenAI images upstream error %d (provider=%d platform=%s type=%s): %s",
			resp.StatusCode,
			provider.Record.ID,
			provider.Record.Platform,
			provider.Record.Type,
			logredact.TruncateLine(body, s.Output.Options.LogUpstreamErrorBodyMaxBytes),
		)
	}

	var decision providercore.UpstreamErrorDecision
	// Keep warning presentation ahead of health and recovery choices; the
	// resolver orders these callbacks so configured display rewriting cannot
	// hide a selected failover decision.
	return nil, gatewaymedia.ResolveImageResponseFailure(resp.StatusCode, upstreamMsg, gatewaymedia.ImageResponseFailurePorts{
		CyberMessage: func() (string, bool) {
			if !gatewayprovider.IsOpenAICyberWarningPayload(body, upstreamMsg) {
				return "", false
			}
			return gatewayprovider.ExtractOpenAICyberWarningMessage(body, upstreamMsg), true
		},
		Observe: func(kind, message string) {
			AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
				Platform:           provider.Record.Platform,
				ProviderID:         provider.Record.ID,
				ProviderName:       provider.Record.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				Kind:               kind,
				Message:            message,
				Detail:             upstreamDetail,
			}, telemetry.RequestAttemptFromResponse(resp))
		},
		Write: func(response gatewaymedia.ErrorResponse) error {
			upErr := &openai.OpenAIImagesUpstreamError{StatusCode: response.Status, ErrorType: response.Type, Message: response.Message, UpstreamRequestID: strings.TrimSpace(resp.Header.Get("x-request-id"))}
			WriteOpenAIImagesUpstreamErrorResponse(c, upErr)
			return upErr
		},
		WrapCyber: func(cause error) error {
			return gatewayprovider.WrapOpenAIUpstreamWarningIfCyber(resp.StatusCode, body, gatewayprovider.ExtractOpenAICyberWarningMessage(body, upstreamMsg), cause)
		},
		ApplyPolicy: func() {
			model := ""
			if len(requestedModel) > 0 {
				model = strings.TrimSpace(requestedModel[0])
			}
			decision = gatewayprovider.ApplyOpenAIResponseHealth(ctx, s.Output.Health, provider, resp.StatusCode, resp.Header, body, false, model)
		},
		Generic: func() bool { return decision.ShouldReturnGenericError() },
		Failover: func() bool {
			return decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode, gatewayprovider.ShouldFailoverOpenAIResponse(resp.StatusCode, upstreamMsg, body))
		},
		NewFailover: func() error {
			return &forwardcore.UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: body, RetryableOnSameProvider: decision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode)}
		},
		Rewrite: func() (gatewaymedia.ErrorResponse, bool) {
			status, typ, message, matched := ApplyErrorPassthroughRule(c, provider.Record.Platform, resp.StatusCode, body, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			return gatewaymedia.ErrorResponse{Status: status, Type: typ, Message: logredact.SanitizeUpstreamQueries(message)}, matched
		},
		DefaultResponse: func() error {
			upErr := openai.OpenAIImagesUpstreamErrorFromHTTP(resp.StatusCode, resp.Header, body)
			WriteOpenAIImagesUpstreamErrorResponse(c, upErr)
			return upErr
		},
	})
}

// openAIImagesStreamPrefix returns the event prefix used for image generation
// or editing. A nil parsed request uses the generation prefix; callers reuse
// the result across partial and completed client SSE events.
func openAIImagesStreamPrefix(parsed *gatewaymedia.ImageRequest) string {
	return openai.OpenAIImagesStreamPrefix(gatewaymedia.NativeImageRequest(parsed))
}

// forwardOpenAIImagesOAuth builds and consumes one image-tool Responses
// exchange for the selected target. Routed image and provider model names are
// validated before attribute resolution, and missing routing input can use
// the native image fallback. The main Responses model is separate from that
// resolved image tool.
//
// The upstream context is detached before credentials and request
// construction so client departure can leave billable image evidence
// available. Detachment retains any server-abort signal and supplies a no-op
// release hook. Native execution owns acquired-body and activity cleanup. A
// matched invalid Agent Identity task can request one guarded recursive
// attempt after recovery, rebuilding from parsed input; the recursive call
// currently uses default TLS match arguments. HTTP failures retain their own
// result path, while other outcomes pass observed image count and error to
// ImageOutcome before returning media fields.
func (s *OpenAIImagesExecutor) forwardOpenAIImagesOAuth(
	ctx context.Context,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	parsed *gatewaymedia.ImageRequest,
	groupMappedModel string,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*forwardcore.OpenAIResult, error) {
	startTime := time.Now()
	requestModel, upstreamModel, err := gatewaymedia.ResolveImageModels(parsed.Model, groupMappedModel, "gpt-image-2", func(model string) string {
		return gatewayprovider.ExecutionModelPolicy(provider).OpenAIUpstream(model, false)
	})
	if err != nil {
		return nil, err
	}
	if err := s.resolveModelAttributes(ctx, provider, upstreamModel); err != nil {
		return nil, err
	}
	if parsed.OutputFormat == "gif" {
		operation := protocol.ProtocolImagesGenerations
		if parsed.IsEdits() {
			operation = protocol.ProtocolImagesEdits
		}
		if err := gatewayprovider.RequireImageGIF(provider, operation, provider.Capabilities.Attributes); err != nil {
			return nil, err
		}
	}
	logging.LegacyPrintf(
		"service.openai_gateway",
		"[OpenAI] Images request routing request_model=%s upstream_model=%s endpoint=%s provider_type=%s uploads=%d",
		requestModel,
		upstreamModel,
		parsed.Endpoint,
		provider.Record.Type,
		len(parsed.Uploads),
	)
	// Retain execution after client departure so native reading can finish observed
	// image and usage evidence. A supplied server-abort signal still permits
	// application shutdown to cancel this work.
	upstreamCtx, releaseUpstreamCtx := gatewayprovider.DetachUpstreamContext(ctx)
	defer releaseUpstreamCtx()

	token, _, err := s.Requests.Credentials.Resolve(upstreamCtx, gatewayprovider.ExecutionRecord(provider))
	if err != nil {
		return nil, err
	}

	mainModel := gatewayprovider.ExecutionProtocolRecord(provider).CodexImageResponsesModel()
	responsesBody, err := buildOpenAIImagesResponsesRequest(parsed, upstreamModel, mainModel)
	if err != nil {
		return nil, err
	}
	// Mark the already-built image request before generic Responses preparation,
	// retaining its dedicated image-tool construction choices through shared request building.
	upstreamCtx = openai.WithOpenAIImagesSelfBuiltRequest(upstreamCtx)
	upstreamReq, err := s.Requests.Build(upstreamCtx, c, provider, responsesBody, token, true, parsed.StickySessionSeed(), false, tlsRouterMatch...)
	if err != nil {
		return nil, err
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Accept", "text/event-stream")
	upstreamReq.Header.Set("OpenAI-Beta", "responses=experimental")

	proxyURL := ""
	if provider.Record.ProxyID != nil && provider.Record.Proxy != nil {
		proxyURL = provider.Record.Proxy.URL()
	}

	options := s.Output.ImageOptions(c)
	options.ExpectedGIF = parsed.OutputFormat == "gif"
	var legacyHTTPResult *forwardcore.OpenAIResult
	httpFailure := false
	retryAgent := false
	target := &mediaprovider.ImagesOptions{
		ProviderID: provider.Record.ID,
		OAuth:      true,
		Model:      upstreamModel,
		StartedAt:  startTime,

		Request: upstreamReq,
		Options: options,
		Enter:   s.Enter,

		ResponseFormat: parsed.ResponseFormat,
		StreamPrefix:   openAIImagesStreamPrefix(parsed),

		Do: func(req *http.Request) (*http.Response, error) {
			upstreamStart := time.Now()
			resp, err := s.Requests.Transport.DoWithTLS(req, proxyURL, provider.Record.ID, provider.Record.Concurrency, s.Requests.TLSProfile(provider, tlsRouterMatch...))
			// Ops upstream timing uses whole milliseconds measured around this exchange.
			SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
			return resp, err
		},

		TransportError: func(err error) error {
			safeErr := logredact.SanitizeUpstreamQueries(err.Error())
			SetOpsUpstreamError(c, 0, safeErr, "")
			AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
				Platform:           provider.Record.Platform,
				ProviderID:         provider.Record.ID,
				ProviderName:       provider.Record.Name,
				UpstreamStatusCode: 0,
				UpstreamURL:        logredact.SafeUpstreamURL(upstreamReq.URL.String()),
				Kind:               "request_error",
				Message:            safeErr,
			})
			return fmt.Errorf("upstream request failed: %s", safeErr)
		},

		ReadErrorBody: s.Output.ReadErrorBody,

		RedactErrorBody: func(body []byte) []byte { return s.Requests.Identity.Redact(upstreamCtx, provider, body) },

		HTTPError: func(resp *http.Response, respBody []byte) error {
			httpFailure = true
			// A rejected driver is configuration failure before image execution. End this
			// request without cooling the image model or repeating generation on another provider.
			if driverErr := openai.OpenAIImagesMainModelError(resp.StatusCode, respBody, mainModel, upstreamModel); driverErr != nil {
				driverErr.UpstreamRequestID = resp.Header.Get("x-request-id")
				SetOpsUpstreamError(c, resp.StatusCode, driverErr.Message, "")
				status, kind, message, _ := ApplyErrorPassthroughRule(
					c, provider.Record.Platform, resp.StatusCode, respBody,
					driverErr.StatusCode, driverErr.ErrorType, driverErr.Message,
				)
				// Rewrite a delivery copy so administrator status overrides cannot change
				// the retry classification retained by the outer image attempt loop.
				clientError := *driverErr
				clientError.StatusCode = status
				clientError.ErrorType = kind
				clientError.Message = logredact.SanitizeUpstreamQueries(message)
				WriteOpenAIImagesUpstreamErrorResponse(c, &clientError)
				return driverErr
			}
			upstreamMsg := ""
			var decision providercore.UpstreamErrorDecision
			// Preserve HTTP rejection as its own outcome while the resolver first
			// tries identity recovery, then applies provider policy or final error
			// output without counting generated images.
			retry, err := gatewaymedia.ResolveImageFailure(gatewaymedia.ImageFailurePorts{
				Recover: func() (bool, error) {
					// Try task recovery before provider failure policy for this identity-specific
					// rejection, using the request flag to keep recursive recovery bounded to once.
					if requeststate.AgentTaskRecoveryTried(ctx) || !s.Requests.Identity.UsesAgentIdentity(ctx, provider) || !openai.IsAgentTaskInvalidHTTPResponse(resp.StatusCode, respBody) {
						return false, nil
					}
					return true, s.Requests.Identity.Recover(ctx, provider, provider.View().GetCredential("task_id"))
				},
				Failover: func() bool {
					upstreamMsg = logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
					return gatewayprovider.ShouldFailoverOpenAIResponse(resp.StatusCode, upstreamMsg, respBody)
				},
				Observe: func() {
					AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
						Platform: provider.Record.Platform,

						ProviderID: provider.Record.ID,

						ProviderName: provider.Record.Name,

						UpstreamStatusCode: resp.StatusCode,

						UpstreamRequestID: resp.Header.Get("x-request-id"),

						UpstreamURL: logredact.SafeUpstreamURL(upstreamReq.URL.String()),

						Kind: "failover",

						Message: upstreamMsg,
					}, telemetry.RequestAttemptFromResponse(resp))
				},
				ApplyPolicy: func() bool {
					decision = s.Output.ApplyHTTPFailure(upstreamCtx, resp, provider, respBody, requestModel)
					return decision.ShouldReturnGenericError()
				},
				NewFailover: func() error {
					retryableOnSameProvider := decision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode)
					if provider.View().IsOpenAIOAuthLike() && resp.StatusCode == http.StatusTooManyRequests {
						return (gatewayprovider.OpenAIFailoverPolicy{Health: s.Output.Health}).NewProviderFailure(
							provider,
							resp.StatusCode,
							resp.Header,
							respBody,
							upstreamMsg,
							false,
							retryableOnSameProvider,
						)
					}
					return &forwardcore.UpstreamFailoverError{
						StatusCode: resp.StatusCode,

						ResponseBody: respBody,

						RetryableOnSameProvider: retryableOnSameProvider,
					}
				},
				Handle: func() error {
					var failure error
					legacyHTTPResult, failure = s.handleOpenAIImagesErrorResponse(upstreamCtx, resp, c, provider, responsesBody, requestModel)
					return failure
				},
			})
			retryAgent = retry
			return err
		},

		ResponseError: func(resp *http.Response, before int, err error) error {
			return s.handleOpenAIImagesOAuthResponseError(upstreamCtx, c, provider, requestModel, logredact.SafeUpstreamURL(upstreamReq.URL.String()), resp, before, err)
		},
	}
	protocolID := protocol.ProtocolImagesGenerations
	if parsed.IsEdits() {
		protocolID = protocol.ProtocolImagesEdits
	}
	result, err := (mediaprovider.Images{Options: *target}).Execute(upstreamCtx, upstream.AttemptInput{Protocol: protocolID, ResponseModel: requestModel, Stream: parsed.Stream}, ResponseSink{Writer: c.Writer})
	// Native cleanup has finished before rebuilding after matched identity recovery.
	// Mark the source context so another invalid task cannot recurse through recovery.
	if retryAgent {
		return s.forwardOpenAIImagesOAuth(requeststate.WithAgentTaskRecovery(ctx), c, provider, parsed, groupMappedModel)
	}
	if httpFailure {
		return legacyHTTPResult, err
	}
	imageCount, retain := gatewaymedia.ImageOutcome(parsed.Stream, true, openai.IsEventStreamResponse(result.UpstreamHeaders), parsed.N, result.ObservedImages, err)
	if !retain {
		return nil, err
	}
	return gatewayprovider.ImagesForwardResult(result, parsed, imageCount), err
}

// shouldCoolOpenAIImagesToolForError reports whether an image error lacks the
// model-text synthesis marker. The unavailable-tool caller checks its code first
// and treats an unmarked error as upstream capability evidence; nil and marked
// errors skip cooldown.
func shouldCoolOpenAIImagesToolForError(upstreamErr *openai.OpenAIImagesUpstreamError) bool {
	return upstreamErr != nil && !upstreamErr.SynthesizedFromModelText
}

// handleOpenAIImagesOAuthResponseError applies selected-provider policy to
// errors observed after successful HTTP exchange. Callers supply the
// heartbeat-adjusted pre-read size; comparison with current real output
// determines whether classified recovery can still return a failover error.
// Unrecognized errors retain their original value.
//
// Classified body-transport failures synthesize a 502 diagnostic envelope and
// clone response headers before cleanup. The bound image reader leaves
// HTTP request cancellation, cancellation errors and response-size failures
// unclassified.
// Typed image errors use their own status and request ID, with header fallback
// when that identity is absent.
//
// Structured unavailable-tool errors may write image-scope cooldown;
// text-derived verdicts skip that write. Actual output change prevents
// another attempt, and remaining retryable errors retain the current health
// decision and same-provider policy.
func (s *OpenAIImagesExecutor) handleOpenAIImagesOAuthResponseError(
	ctx context.Context,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	requestedModel string,
	upstreamURL string,
	resp *http.Response,
	writerSizeBeforeResponse int,
	err error,
) error {
	responseWritten := c != nil && c.Writer != nil && HeartbeatAdjustedWrittenSize(c) != writerSizeBeforeResponse
	if code, message, ok := openai.OpenAIUpstreamStreamReadErrorDetails(err); ok {
		// A classified body failure supplies status-502 recovery evidence separately
		// from HTTP success. Clone headers before response cleanup for later diagnostics.
		headers := http.Header(nil)
		requestID := ""
		statusCode := http.StatusBadGateway
		if resp != nil {
			headers = resp.Header.Clone()
			requestID = strings.TrimSpace(resp.Header.Get("x-request-id"))
		}
		responseBody := []byte(fmt.Sprintf(`{"error":{"type":"upstream_error","code":%q,"message":%q}}`, code, message))
		decision := gatewayprovider.ApplyOpenAIResponseHealth(ctx, s.Output.Health, provider, statusCode, headers, responseBody, false, requestedModel)
		retryable := decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(provider), statusCode, true)
		kind := "http_error"
		if retryable {
			kind = "failover"
			if responseWritten {
				kind = "retry_exhausted_failover"
			}
		}
		AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
			Platform: provider.Record.Platform,

			ProviderID: provider.Record.ID,

			ProviderName: provider.Record.Name,

			UpstreamStatusCode: statusCode,

			UpstreamRequestID: requestID,

			UpstreamURL: upstreamURL,

			Kind: kind,

			Message: message,
		}, telemetry.RequestAttemptFromResponse(resp))
		if !retryable || responseWritten {
			return err
		}
		return &forwardcore.UpstreamFailoverError{
			StatusCode: statusCode,

			ResponseBody: responseBody,

			ResponseHeaders: headers,

			RetryableOnSameProvider: decision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(provider), statusCode),
		}
	}

	var upstreamErr *openai.OpenAIImagesUpstreamError
	if !errors.As(err, &upstreamErr) {
		return err
	}

	requestID := strings.TrimSpace(upstreamErr.UpstreamRequestID)
	headers := http.Header(nil)
	if resp != nil {
		headers = resp.Header.Clone()
		// Use the HTTP request ID only when the typed image error supplies none,
		// preserving event-level identity when both sources are present.
		if requestID == "" {
			requestID = strings.TrimSpace(resp.Header.Get("x-request-id"))
		}
	}
	responseBody := openai.OpenAIImagesUpstreamErrorResponseBody(upstreamErr)
	decision := gatewayprovider.ApplyOpenAIResponseHealth(ctx, s.Output.Health, provider, upstreamErr.StatusCode, headers, responseBody, false, requestedModel)
	retryable := decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(provider), upstreamErr.StatusCode, openai.IsOpenAIImagesRetryableUpstreamError(upstreamErr))
	kind := "http_error"
	if retryable {
		kind = "failover"
		if responseWritten {
			kind = "retry_exhausted_failover"
		}
	}
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform: provider.Record.Platform,

		ProviderID: provider.Record.ID,

		ProviderName: provider.Record.Name,

		UpstreamStatusCode: upstreamErr.StatusCode,

		UpstreamRequestID: requestID,

		UpstreamURL: upstreamURL,

		Kind: kind,

		Message: upstreamErr.ClientMessage(),
	}, telemetry.RequestAttemptFromResponse(resp))

	// The unavailable-tool code selects image-specific policy. Cooldown needs
	// structured evidence; model-text inference still permits another provider before output.
	if upstreamErr.Code == "image_generation_unavailable" {
		if shouldCoolOpenAIImagesToolForError(upstreamErr) {
			s.Cooldown.Apply(ctx, gatewayprovider.ExecutionRecord(provider))
		}
		if responseWritten {
			return err
		}
		return (gatewayprovider.OpenAIFailoverPolicy{Health: s.Output.Health}).NewProviderFailure(
			provider,
			upstreamErr.StatusCode,
			headers,
			responseBody,
			upstreamErr.ClientMessage(),
			false,
			false,
		)
	}
	if !retryable || responseWritten {
		return err
	}
	// Reapply health policy for the remaining retryable image errors before
	// building the final failure, so same-provider permission reflects this
	// branch's stop-scheduling decision.
	shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(ctx, s.Output.Health, provider, upstreamErr.StatusCode, headers, responseBody, false, requestedModel).StopScheduling
	return (gatewayprovider.OpenAIFailoverPolicy{Health: s.Output.Health}).NewProviderFailure(
		provider,
		upstreamErr.StatusCode,
		headers,
		responseBody,
		upstreamErr.ClientMessage(),
		shouldDisable,
		!shouldDisable && provider.View().IsPoolMode() && provider.View().IsPoolModeRetryableStatus(upstreamErr.StatusCode),
	)
}
