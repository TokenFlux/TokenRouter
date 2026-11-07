package httpapi

import (
	"context"
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
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_images_execution.go dispatches image requests by credential type and
// executes native API-key image exchanges.
// API Key image requests use the native Images endpoint while OAuth and
// setup-token requests use a Responses image tool. Each branch resolves the
// routed image spelling and final provider model separately before building
// the selected protocol request, allowing output and completion to retain
// their respective model observations.
//
// Image exchanges are detached from client cancellation so completed image
// and token evidence can survive a disconnected downstream. Native execution
// owns response-body cleanup and activity release; the HTTP adapter owns
// failure policy and response presentation. Successful eligible transports
// can use requested image count when no positive output count was observed,
// while failed native retention requires positive completed-image evidence. URL
// backfill occurs after response usage and dimensions are observed, keeping
// downloaded delivery bytes from redefining those measurements.
// Streaming attempts share the HTTP SSE controller through header waits and
// buffered JSON reads; response writers take over after those heartbeats stop.

const (
	// openAIImagesGenerationsURL and openAIImagesEditsURL identify default Images
	// operations; OpenAIRequests.ImagesURL can instead use the configured provider base.
	openAIImagesGenerationsURL = "https://api.openai.com/v1/images/generations"
	openAIImagesEditsURL       = "https://api.openai.com/v1/images/edits"
)

// OpenAIImagesExecutor binds one selected image exchange with HTTP output and
// failure policy.
type OpenAIImagesExecutor struct {
	Requests *OpenAIRequests       // shared credentials and request transport
	Output   *OpenAIResponseOutput // bound image readers and HTTP policy
	// Cooldown optionally writes confirmed image-tool unavailability. A nil
	// cooldown service is accepted by Apply and leaves provider state unchanged.
	Cooldown *provideradapter.ImageToolCooldown
	// Enter optionally tracks this exchange in application activity. Accepted
	// cleanup runs after native response-body handling, including HTTP error replay.
	Enter func() (func(), error)
}

// ForwardImages dispatches one parsed image attempt by exact provider type. A
// nil parsed request returns an error; callers supply a usable executor, HTTP
// context and selected provider. API Key uses direct Images, while OAuth and
// setup-token use the Responses image branch. The dispatch currently calls
// branch methods without forwarding its variadic TLS match arguments.
func (s *OpenAIImagesExecutor) ForwardImages(
	ctx context.Context,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	body []byte,
	parsed *gatewaymedia.ImageRequest,
	groupMappedModel string,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*forwardcore.OpenAIResult, error) {
	if parsed == nil {
		return nil, fmt.Errorf("parsed images request is required")
	}
	oauth, err := gatewaymedia.ImageExecutionPath(provider.Record.Type)
	if err != nil {
		return nil, err
	}
	if oauth {
		return s.forwardOpenAIImagesOAuth(ctx, c, provider, parsed, groupMappedModel)
	}
	return s.forwardOpenAIImagesAPIKey(ctx, c, provider, body, parsed, groupMappedModel)
}

// forwardOpenAIImagesAPIKey executes direct Images for the selected target
// and returns retained media observations. Group mapping can replace the
// parsed model before provider mapping, and both routed and final upstream
// spellings are validated as image-family names. The forwarding body is
// rebuilt with that final model and the selected endpoint's mode capability,
// retaining its JSON or multipart encoding and the independent client mode.
//
// Detachment removes client cancellation and deadlines before credential
// resolution and exchange while retaining any server-abort signal. Its returned
// release hook is a no-op. Native execution closes the response body before
// releasing activity. HTTP rejection uses health and failover policy or final image
// error presentation. Other outcomes retain image count through ImageOutcome,
// including requested-count substitution on its eligible successful paths and
// positive native completed-image evidence after an error.
func (s *OpenAIImagesExecutor) forwardOpenAIImagesAPIKey(
	ctx context.Context,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	body []byte,
	parsed *gatewaymedia.ImageRequest,
	groupMappedModel string,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*forwardcore.OpenAIResult, error) {
	startTime := time.Now()
	requestModel, upstreamModel, err := gatewaymedia.ResolveImageModels(parsed.Model, groupMappedModel, "", func(model string) string {
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
	SetOpsUpstreamModel(c, upstreamModel)
	logging.LegacyPrintf(
		"service.openai_gateway",
		"[OpenAI] Images request routing request_model=%s upstream_model=%s endpoint=%s provider_type=%s",
		strings.TrimSpace(parsed.Model),
		upstreamModel,
		parsed.Endpoint,
		provider.Record.Type,
	)
	protocolID := protocol.ProtocolImagesGenerations
	if parsed.IsEdits() {
		protocolID = protocol.ProtocolImagesEdits
	}
	mode, err := gatewayprovider.ExecutionProtocolRecord(provider).ImageStreamMode(protocolID)
	if err != nil {
		return nil, err
	}
	forwardBody, forwardContentType, err := upstream.RewriteImageRequest(body, parsed.ContentType, upstreamModel, mode, parsed.Stream)
	if err != nil {
		return nil, err
	}
	if parsed.OutputFormat == "gif" {
		forwardBody, forwardContentType, err = upstream.RewriteImageGIFOptions(forwardBody, forwardContentType, *parsed.FrameCount, *parsed.FrameDurationMS)
		if err != nil {
			return nil, err
		}
	}
	// Continue the selected exchange after client departure so an image and its
	// observed tokens can still reach completion. A retained server-abort signal can
	// stop this work; header waits also use the transport's configured budget.
	upstreamCtx, releaseUpstreamCtx := gatewayprovider.DetachUpstreamContext(ctx)
	defer releaseUpstreamCtx()

	token, _, err := s.Requests.Credentials.Resolve(upstreamCtx, gatewayprovider.ExecutionRecord(provider))
	if err != nil {
		return nil, err
	}
	upstreamReq, err := s.buildOpenAIImagesRequest(upstreamCtx, c, provider, forwardBody, forwardContentType, token, parsed.Endpoint, tlsRouterMatch...)
	if err != nil {
		return nil, err
	}

	// Request headers describe the selected upstream representation, independently
	// of the client-facing response mode retained by the original parsed request.
	if mode.UpstreamStream(parsed.Stream) {
		upstreamReq.Header.Set("Accept", "text/event-stream")
	} else {
		upstreamReq.Header.Set("Accept", "application/json")
	}
	proxyURL := ""
	if provider.Record.ProxyID != nil && provider.Record.Proxy != nil {
		proxyURL = provider.Record.Proxy.URL()
	}

	options := s.Output.ImageOptions(c)
	options.ExpectedGIF = parsed.OutputFormat == "gif"
	options.ExpectedImages = parsed.N
	download := s.imageBackfillOptions(provider)
	options.FetchImage = func(rawURL string) (string, error) {
		// URL expansion can outlast an ingress idle timeout. This download owns
		// output waiting exclusively; stop its heartbeat before event delivery resumes.
		if parsed.Stream && options.StartKeepalive != nil {
			stop := options.StartKeepalive()
			defer stop()
		}
		return download.FetchBase64(upstreamCtx, rawURL)
	}
	// Native nonstream reading extracts usage and dimensions before invoking this
	// delivery conversion, so downloaded image bytes cannot replace the observed billing data.
	options.Backfill = func(body []byte) []byte { return s.backfillOpenAIImagesB64JSON(upstreamCtx, provider, parsed, body) }
	var legacyHTTPResult *forwardcore.OpenAIResult
	httpFailure := false
	target := &mediaprovider.ImagesOptions{
		ProviderID: provider.Record.ID,
		OAuth:      false,
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
			if resp != nil {
				options.Logf("[OpenAI] Images mode provider_id=%d client_stream=%t upstream_stream=%t response_stream=%t conversion=%t", provider.Record.ID, parsed.Stream, mode.UpstreamStream(parsed.Stream), upstreamopenai.IsEventStreamResponse(resp.Header), parsed.Stream != upstreamopenai.IsEventStreamResponse(resp.Header))
			}
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
			// Native execution has closed the acquired body and installed redacted replay
			// bytes. Preserve this HTTP failure path separately from successful image counting.
			httpFailure = true
			upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
			shouldDisable := false
			// Apply policy only after HTTP failure qualifies for failover. Keep
			// access-state errors typed so the outer media attempt can select recovery
			// from the rejection rather than count it as image output.
			_, err := gatewaymedia.ResolveImageFailure(gatewaymedia.ImageFailurePorts{
				Failover: func() bool {
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
					shouldDisable = s.Output.ApplyHTTPFailure(upstreamCtx, resp, provider, respBody, upstreamModel).StopScheduling
					return false
				},
				NewFailover: func() error {
					retryableOnSameProvider := !shouldDisable && provider.View().IsPoolMode() && provider.View().IsPoolModeRetryableStatus(resp.StatusCode)
					if provider.View().IsOpenAIOAuthLike() && resp.StatusCode == http.StatusTooManyRequests {
						return (gatewayprovider.OpenAIFailoverPolicy{Health: s.Output.Health}).NewProviderFailure(provider, resp.StatusCode, resp.Header, respBody, upstreamMsg, shouldDisable, retryableOnSameProvider)
					}
					if gatewayprovider.IsOpenAIHTTPUpstreamAccessStateError(resp.StatusCode, upstreamMsg, respBody) {
						return gatewayprovider.NewOpenAIUpstreamFailure(resp.StatusCode, resp.Header, respBody, upstreamMsg, retryableOnSameProvider)
					}
					return &forwardcore.UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameProvider: retryableOnSameProvider}
				},
				Handle: func() error {
					var failure error
					legacyHTTPResult, failure = s.handleOpenAIImagesErrorResponse(upstreamCtx, resp, c, provider, forwardBody, upstreamModel)
					return failure
				},
			})
			return err
		},
	}
	result, err := (mediaprovider.Images{Options: *target}).Execute(upstreamCtx, upstream.AttemptInput{Protocol: protocolID, ResponseModel: requestModel, Stream: parsed.Stream}, ResponseSink{Writer: c.Writer})
	if httpFailure {
		return legacyHTTPResult, err
	}
	// Requested-count substitution belongs to eligible successful transport paths.
	// Failed attempts need positive streamed image evidence before retaining a result.
	imageCount, retain := gatewaymedia.ImageOutcome(parsed.Stream, false, upstreamopenai.IsEventStreamResponse(result.UpstreamHeaders), parsed.N, result.ObservedImages, err)
	if !retain {
		return nil, err
	}
	return gatewayprovider.ImagesForwardResult(result, parsed, imageCount), err
}

// buildOpenAIImagesRequest creates an authenticated Images POST for the
// selected endpoint and provider base URL. Invalid target resolution returns
// before request construction. Native building applies allowed client
// headers, User-Agent and enabled provider overrides while retaining the
// supplied body content type.
func (s *OpenAIImagesExecutor) buildOpenAIImagesRequest(
	ctx context.Context,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	body []byte,
	contentType string,
	token string,
	endpoint string,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*http.Request, error) {
	targetURL, err := s.Requests.ImagesURL(provider, endpoint)
	if err != nil {
		return nil, err
	}

	options := s.Requests.ResponseOptions(ctx, c, provider, token, targetURL, false, tlsRouterMatch...)
	options.AllowHeader = AllowOpenAIPassthroughHeader
	return upstreamopenai.BuildImagesRequest(ctx, body, contentType, options)
}
