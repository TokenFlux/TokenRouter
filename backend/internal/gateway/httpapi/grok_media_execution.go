package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"
	mediaprovider "github.com/TokenFlux/TokenRouter/internal/gateway/media/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// grok_media_execution.go executes Grok image and video requests and captures
// their observed media usage for completion.
// Grok image results and asynchronous video tasks produce different billing
// inputs. Image execution counts actual returned outputs; video creation
// retains the task ID and request pricing parameters with a zero video count.
// Status or content observation can later report a completed video while
// preserving its saved creation metadata.
//
// Body preparation precedes provider model mapping and capability resolution,
// and the original parsed request remains available for media dimensions.
// Video downloads first read task status, then acquire a content resource
// whose close ends the platform activity. Health classification occurs before
// final client error rules, and empty successful image output becomes a
// failover signal before normal response delivery.

// grokMediaUsageMetadata holds endpoint-specific observations used to build
// completion results.
type grokMediaUsageMetadata struct {
	ResponseID string              // upstream video task identity
	Usage      openai.ForwardUsage // observed token usage
	// Model and BillingModel are filled by accepted video-status billing data.
	// Nonempty values replace the request-derived identities in ForwardGrokMedia's
	// status result; other endpoints retain their request identities.
	Model            string   // status-observed result model
	BillingModel     string   // status-resolved billing model
	ImageCount       int      // returned image output count
	ImageFrameCount  int      // encoded output frame billing units
	ImageSize        string   // image billing size tier
	ImageInputSize   string   // requested image dimensions
	ImageOutputSizes []string // observed output image sizes
	// VideoCount stays zero for task creation and becomes positive only when
	// a status response supplies completed-video billing evidence to this adapter.
	VideoCount           int
	VideoResolution      string // video billing resolution
	VideoDurationSeconds int    // video duration seconds
	VideoState           string // observed video task state
}

// ExtractGrokVideoBillingFromStatusBody delegates completed-video extraction
// and returns its billing result, or nil when completion evidence is absent.
// Status model and duration take precedence when supplied; saved creation data
// provides resolution and missing billing identities.
func ExtractGrokVideoBillingFromStatusBody(statusBody []byte, pending *gatewaymedia.GrokVideoPendingBilling, requestID string) *forwardcore.OpenAIResult {
	value := gatewaymedia.ExtractGrokVideoBillingFromStatusBody(statusBody, pending, requestID, extractGrokMediaVideoRequestID(statusBody))
	if value == nil {
		return nil
	}
	return &forwardcore.OpenAIResult{ResponseID: value.ResponseID, Model: value.Model, BillingModel: value.BillingModel, UpstreamModel: value.UpstreamModel, VideoCount: value.VideoCount, VideoResolution: value.VideoResolution, VideoDurationSeconds: value.VideoDurationSeconds}
}

// ForwardGrokMedia executes a selected Grok media endpoint and returns
// observations for request completion. The provider is required and must be
// Grok; video-content endpoints take their dedicated status-and-download path.
// Other requests are prepared, normalized and mapped before transport, with
// capability resolution applied to body-bearing requests.
//
// The original parsed model and media dimensions stay available beside
// provider-mapped billing and upstream identities. Images require actual
// output before client delivery; video creation saves task parameters with
// zero billable video count, while status observations can supply completed
// units. Provider health precedes error presentation, and the delegated
// platform executor owns response cleanup for this synchronous attempt.
func (s *GrokExecutor) ForwardGrokMedia(
	ctx context.Context,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	endpoint grok.GrokMediaEndpoint,
	requestID string,
	body []byte,
	contentType string,
) (*forwardcore.OpenAIResult, error) {
	startTime := time.Now()
	if provider == nil {
		return nil, fmt.Errorf("grok provider is required")
	}
	if provider.Record.Platform != capability.PlatformGrok {
		return nil, fmt.Errorf("provider platform %s is not supported for grok media", provider.Record.Platform)
	}

	token, _, err := s.Credentials.Resolve(ctx, RequestCredentialBudget(c), CredentialObserver{Context: c}, provider)
	if err != nil {
		return nil, err
	}
	if endpoint == grok.GrokMediaEndpointVideoContent {
		return s.forwardGrokMediaVideoContent(ctx, c, provider, token, requestID, startTime)
	}
	targetURL, err := s.Routes.Media(provider, endpoint, requestID)
	if err != nil {
		return nil, err
	}

	// Endpoint preparation can change body representation before model lookup;
	// retain the paired Content-Type so subsequent normalization reads the same request.
	body, contentType, err = prepareGrokMediaForwardBody(endpoint, body, contentType)
	if err != nil {
		return nil, err
	}
	body, contentType, err = normalizeGrokMediaForwardBody(endpoint, body, contentType)
	if err != nil {
		return nil, err
	}
	requestInfo := gatewayprovider.GrokMediaCodec().ParseGrokMediaRequest(contentType, body)
	billingModel := requestInfo.Model
	upstreamModel := billingModel
	if endpoint.RequiresRequestBody() {
		if mappedModel := strings.TrimSpace(gatewayprovider.ExecutionModelPolicy(provider).Mapped(requestInfo.Model)); mappedModel != "" {
			billingModel = mappedModel
		}
		upstreamModel = gatewayprovider.ExecutionModelPolicy(provider).NormalizeOpenAI(billingModel)
		// Rewrite only the model field after provider mapping; requestInfo retains
		// the original dimensions and model needed to describe the returned media result.
		if upstreamModel != requestInfo.Model {
			body, contentType, err = gatewayprovider.GrokMediaCodec().RewriteGrokMediaRequestModel(body, contentType, upstreamModel)
			if err != nil {
				return nil, fmt.Errorf("rewrite grok media provider mapped model: %w", err)
			}
		}
		resolved, err := gatewayprovider.ResolveModelAttributes(ctx, provider, upstreamModel, s.Attributes)
		if err != nil {
			return nil, err
		}
		if endpoint == grok.GrokMediaEndpointImagesGenerations || endpoint == grok.GrokMediaEndpointImagesEdits {
			if err := routing.RequireImageOutput(resolved.Attributes); err != nil {
				return nil, err
			}
		}
		provider.Capabilities = &resolved
		modeltrace.RegisterStage(ctx, upstreamModel)
	}
	body, contentType, err = sanitizeGrokMediaForwardBody(endpoint, body, contentType)
	if err != nil {
		return nil, err
	}

	// Retain request values and server-abort state while detaching client deadlines,
	// giving this platform exchange the same context policy as Grok text and Voice.
	upstreamCtx, releaseUpstreamCtx := gatewayprovider.DetachUpstreamContext(ctx)
	defer releaseUpstreamCtx()
	var cliHeaders func(http.Header)
	if provider.View().IsGrokOAuth() && isGrokCLIProxyTarget(targetURL) {
		cliHeaders = grok.ApplyCLIHeaders
	}
	req, err := grok.BuildMediaRequest(upstreamCtx, endpoint, targetURL, token, contentType, body, cliHeaders, gatewayprovider.BindExecutionHeaders(provider))
	if err != nil {
		return nil, err
	}
	proxyURL := ""
	if provider.Record.ProxyID != nil && provider.Record.Proxy != nil {
		proxyURL = provider.Record.Proxy.URL()
	}
	handled := false
	var handledResult *forwardcore.OpenAIResult
	target := &mediaprovider.GrokMediaOptions{
		ProviderID: provider.Record.ID,
		Endpoint:   endpoint,
		Request:    req,
		StartedAt:  startTime,
		Enter:      s.Enter,
		Do: func(req *http.Request) (*http.Response, error) {
			return s.Transport.Do(req, proxyURL, provider.Record.ID, provider.Record.Concurrency)
		},
		AfterExchange: func(elapsed time.Duration, err error) error {
			// Ops stores whole milliseconds, discarding submillisecond exchange timing.
			SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, elapsed.Milliseconds())
			if err != nil {
				return s.Failure.Handle(ctx, c, provider, err, false)
			}
			return nil
		},
		BeforeResponse: func(resp *http.Response) (bool, error) {
			if resp.StatusCode >= 400 {
				handled = true
				var err error
				handledResult, err = s.handleGrokMediaErrorResponse(ctx, resp, c, provider, requeststate.FirstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")), upstreamModel)
				return true, err
			}
			s.Health.ObserveResponse(ctx, provider.View(), resp.Header, resp.StatusCode, requestInfo.Model)
			return false, nil
		},
		ReadBody: func(reader io.Reader) ([]byte, error) {
			return ReadUpstreamResponseBody(reader, s.Output.Options.ReadLimit, c, OpenAIResponseTooLarge)
		},
		CountImages: openai.CountOpenAIResponseImageOutputsFromJSONBytes,
		TransformBody: func(data []byte) []byte {
			if endpoint == grok.GrokMediaEndpointVideoStatus {
				return rewriteGrokMediaVideoContentURLs(data, requestID, GrokMediaContentProxyURL(c, requestID))
			}
			return data
		},
		CopyHeaders: func(dst, src http.Header) {
			WriteOpenAIPassthroughResponseHeaders(dst, src, s.Output.Headers)
		},
	}
	var sink upstream.OutputSink
	if c != nil {
		sink = ResponseSink{Writer: c.Writer}
	}
	protocols := map[grok.GrokMediaEndpoint]protocol.ProtocolID{
		grok.GrokMediaEndpointImagesGenerations: protocol.ProtocolImagesGenerations,
		grok.GrokMediaEndpointImagesEdits:       protocol.ProtocolImagesEdits,
		grok.GrokMediaEndpointVideosGenerations: protocol.ProtocolVideosGenerations,
		grok.GrokMediaEndpointVideosEdits:       protocol.ProtocolVideosEdits,
		grok.GrokMediaEndpointVideosExtensions:  protocol.ProtocolVideosExtensions,
		grok.GrokMediaEndpointVideoStatus:       protocol.ProtocolVideosGenerations,
	}
	result, err := (mediaprovider.GrokMedia{Options: *target}).Execute(upstreamCtx, upstream.AttemptInput{Protocol: protocols[endpoint], Body: body, ResponseModel: requestInfo.Model}, sink)
	if handled {
		return handledResult, err
	}
	if err != nil {
		// An empty HTTP success has supplied no image units or client body; retain
		// its response evidence as failover data so another provider can satisfy generation.
		if missing, ok := errors.AsType[*grok.MissingImageOutput](err); ok {
			SetOpsUpstreamError(c, http.StatusBadGateway, missing.Error(), logredact.TruncateUTF8(string(missing.Body), 512))
			return nil, &forwardcore.UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: missing.Body, ResponseHeaders: missing.Headers}
		}
		return nil, err
	}
	respBody := result.MediaBody

	usage := grokMediaUsageFromResponse(endpoint, requestInfo, respBody)
	resultModel := requestInfo.Model
	resultBillingModel := billingModel
	if endpoint == grok.GrokMediaEndpointVideoStatus {
		// Status queries have no request-body model; billable status data supplies the result models.
		if m := strings.TrimSpace(usage.Model); m != "" {
			resultModel = m
		}
		if m := strings.TrimSpace(usage.BillingModel); m != "" {
			resultBillingModel = m
		}
	}
	return &forwardcore.OpenAIResult{
		RequestAttempt: result.RequestAttempt,
		RequestID:      result.RequestID,

		UpstreamHeaders: result.UpstreamHeaders,

		ResponseID: usage.ResponseID,

		Usage: usage.Usage,

		Model: resultModel,

		BillingModel: resultBillingModel,

		UpstreamModel: upstreamModel,

		ResponseHeaders: result.UpstreamHeaders.Clone(),

		Duration: time.Since(startTime),

		ImageCount:      usage.ImageCount,
		ImageFrameCount: usage.ImageFrameCount,

		ImageSize: usage.ImageSize,

		ImageInputSize: usage.ImageInputSize,

		ImageOutputSizes: usage.ImageOutputSizes,

		VideoCount: usage.VideoCount,
		VideoState: usage.VideoState,

		VideoResolution: usage.VideoResolution,

		VideoDurationSeconds: usage.VideoDurationSeconds,
	}, nil
}

// forwardGrokMediaVideoContent opens task status and its validated content
// resource, then streams safe content headers and bytes. The request owner
// supplies the bound provider and task identity; the acquired resource is
// closed after output to release its platform activity.
//
// Range input is carried to the content request, and the provider layer
// distinguishes authenticated relay paths from signed vendor URLs. A completed
// status can also supply video units even when this call was for download. The
// returned result carries those observations; saved creation metadata and
// settlement claiming remain in the media completion owner. The status and
// download exchanges retain separate dispatch references. Opening the download
// completes status processing; the final return records the download outcome,
// including body-read and downstream-write failures after successful headers.
func (s *GrokExecutor) forwardGrokMediaVideoContent(
	ctx context.Context,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	token, requestID string,
	startTime time.Time,
) (result *forwardcore.OpenAIResult, failure error) {
	statusURL, err := s.Routes.Media(provider, grok.GrokMediaEndpointVideoStatus, requestID)
	if err != nil {
		return nil, err
	}

	// Retain request values and server-abort state while detaching client deadlines,
	// giving this platform exchange the same context policy as Grok text and Voice.
	upstreamCtx, releaseUpstreamCtx := gatewayprovider.DetachUpstreamContext(ctx)
	defer releaseUpstreamCtx()
	proxyURL := ""
	if provider.Record.ProxyID != nil && provider.Record.Proxy != nil {
		proxyURL = provider.Record.Proxy.URL()
	}
	rangeHeader := ""
	if c != nil {
		rangeHeader = c.GetHeader("Range")
	}
	handled := false
	var handledResult *forwardcore.OpenAIResult
	var dispatch telemetry.RequestAttemptRef
	defer func() { telemetry.ObserveRequestResult(ctx, dispatch, failure) }()
	resource, err := mediaprovider.OpenVideoContent(upstreamCtx, mediaprovider.VideoContentOptions{
		StatusURL: statusURL,
		RequestID: requestID,
		Token:     token,
		Range:     rangeHeader,
		Context:   upstream.WithHTTPUpstreamRedirectsDisabled,
		ContentURL: func() (string, error) {
			return s.Routes.Media(provider, grok.GrokMediaEndpointVideoContent, requestID)
		},
		ApplyHeaders: func(headers http.Header, target string) {
			if provider.View().IsGrokOAuth() && isGrokCLIProxyTarget(target) {
				grok.ApplyCLIHeaders(headers)
			}
			provideradapter.ApplyProviderHeaderOverrides(gatewayprovider.ExecutionProtocolRecord(provider), headers)
		},
		Do: func(req *http.Request) (*http.Response, error) {
			// A second exchange starts only after status reading and URL validation
			// succeed. Finish that status dispatch before observing the content response.
			telemetry.ObserveRequestResult(ctx, dispatch, nil)
			resp, err := s.Transport.Do(req, proxyURL, provider.Record.ID, provider.Record.Concurrency)
			dispatch = telemetry.RequestAttemptFromResponse(resp)
			return resp, err
		},
		ReadStatus: func(reader io.Reader) ([]byte, error) {
			return ReadUpstreamResponseBody(reader, s.Output.Options.ReadLimit, c, OpenAIResponseTooLarge)
		},
		Latency: func(elapsed time.Duration) {
			// Ops stores whole milliseconds, discarding submillisecond exchange timing.
			SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, elapsed.Milliseconds())
		},
		TransportError: func(err error) error { return s.Failure.Handle(ctx, c, provider, err, false) },
		HTTPError: func(resp *http.Response, id string) error {
			handled = true
			var err error
			handledResult, err = s.handleGrokMediaErrorResponse(ctx, resp, c, provider, id, "")
			return err
		},
		Enter: s.Enter,
	})
	if handled {
		return handledResult, err
	}
	if err != nil {
		return nil, err
	}
	// This content resource also carries the attempt activity release, so close
	// it after forwarding succeeds or fails to finish both body and activity ownership.
	defer func() { _ = resource.Close() }()
	contentResp := &http.Response{StatusCode: resource.StatusCode, ContentLength: resource.ContentLength, Header: resource.Headers, Body: resource}
	contentRequestID := resource.RequestID
	statusBody := resource.StatusBody

	s.Health.ObserveResponse(ctx, provider.View(), contentResp.Header, contentResp.StatusCode, "")
	if err := WriteGrokMediaContentResponse(c, contentResp, func() { MarkResponseCommitted(c) }); err != nil {
		return nil, err
	}
	// Content download also observes completion: done with video.url yields billing units.
	// The handler merges pending billing data and claims settlement as it does for status polling.
	result = &forwardcore.OpenAIResult{
		RequestAttempt: dispatch,
		RequestID:      contentRequestID,
		VideoState:     gatewaymedia.GrokVideoRequestState(statusBody),

		UpstreamHeaders: contentResp.Header,

		ResponseHeaders: contentResp.Header.Clone(),

		Duration: time.Since(startTime),
	}
	if billed := ExtractGrokVideoBillingFromStatusBody(statusBody, nil, requestID); billed != nil {
		result.ResponseID = requeststate.FirstNonEmpty(billed.ResponseID, strings.TrimSpace(requestID))
		result.Model = billed.Model
		result.BillingModel = billed.BillingModel
		result.UpstreamModel = billed.UpstreamModel
		result.VideoCount = billed.VideoCount
		result.VideoResolution = billed.VideoResolution
		result.VideoDurationSeconds = billed.VideoDurationSeconds
	}
	return result, nil
}

// isGrokCLIProxyTarget reports whether the URL uses the Grok CLI proxy target.
func isGrokCLIProxyTarget(rawURL string) bool {
	return gatewayprovider.GrokMediaCodec().IsGrokCLIProxyTarget(rawURL)
}

// prepareGrokMediaForwardBody prepares image-edit references and can convert
// multipart input to JSON. Other endpoints and unrecognized body
// representations pass through with their Content-Type; any codec error is
// returned before forwarding.
func prepareGrokMediaForwardBody(endpoint grok.GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	return gatewayprovider.GrokMediaCodec().PrepareGrokMediaForwardBody(endpoint, body, contentType)
}

// normalizeGrokMediaForwardBody canonicalizes legacy image_url references into
// url for supported valid JSON image-edit and video-generation bodies. Model
// IDs remain supplied; invalid JSON or body-free endpoints pass through with
// their Content-Type.
func normalizeGrokMediaForwardBody(endpoint grok.GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	return gatewayprovider.GrokMediaCodec().NormalizeGrokMediaForwardBody(endpoint, body, contentType)
}

// sanitizeGrokMediaForwardBody applies image geometry cleanup after provider
// model resolution. Invalid JSON and body-free or other endpoints pass
// through, while geometry errors return to the caller with transport still
// unstarted.
func sanitizeGrokMediaForwardBody(endpoint grok.GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	return gatewayprovider.GrokMediaCodec().SanitizeGrokMediaForwardBody(endpoint, body, contentType)
}

// grokMediaUsageFromResponse combines token observations with endpoint-specific
// media billing metadata. Image endpoints count outputs and retain
// requested dimensions. Video creation retains task identity, resolution and
// duration with no completed-video count.
//
// Status endpoints separately preserve terminal-state observations and invoke
// completed-video extraction. Only accepted completion evidence fills result
// models, billable count and duration; other states still reach task
// bookkeeping with zero video units. Missing token usage remains its zero
// observation while media-specific evidence supplies the applicable pricing
// dimensions.
func grokMediaUsageFromResponse(endpoint grok.GrokMediaEndpoint, requestInfo grok.GrokMediaRequestInfo, responseBody []byte) grokMediaUsageMetadata {
	// Missing token usage leaves zero counters; the availability flag is discarded
	// because image and video completion can still use their independently observed units.
	usage, _ := openai.ExtractOpenAIUsageFromJSONBytes(responseBody)
	meta := grokMediaUsageMetadata{Usage: usage}
	switch endpoint {
	case grok.GrokMediaEndpointImagesGenerations, grok.GrokMediaEndpointImagesEdits:
		meta.ImageCount = openai.CountOpenAIResponseImageOutputsFromJSONBytes(responseBody)
		meta.ImageSize = requestInfo.SizeTier
		meta.ImageInputSize = requestInfo.Size
		meta.ImageOutputSizes = openai.CollectOpenAIResponseImageOutputSizesFromJSONBytes(responseBody)
	case grok.GrokMediaEndpointVideosGenerations, grok.GrokMediaEndpointVideosEdits, grok.GrokMediaEndpointVideosExtensions:
		// Creation accepts asynchronous work. Retain task identity and pricing
		// parameters while leaving the video count zero until status confirms completion.
		meta.ResponseID = extractGrokMediaVideoRequestID(responseBody)
		meta.VideoResolution = requestInfo.Resolution
		meta.VideoDurationSeconds = requestInfo.DurationSeconds
	case grok.GrokMediaEndpointVideoStatus:
		meta.VideoState = gatewaymedia.GrokVideoRequestState(responseBody)
		// Terminal state still reaches task bookkeeping, but billing units require
		// the extraction helper's done status and nonempty video URL evidence.
		if billed := ExtractGrokVideoBillingFromStatusBody(responseBody, nil, ""); billed != nil {
			meta.ResponseID = billed.ResponseID
			meta.Model = billed.Model
			meta.BillingModel = billed.BillingModel
			meta.VideoCount = billed.VideoCount
			meta.VideoResolution = billed.VideoResolution
			meta.VideoDurationSeconds = billed.VideoDurationSeconds
		}
	}
	return meta
}

// extractGrokMediaVideoRequestID delegates extraction of supported upstream
// video task identifiers.
func extractGrokMediaVideoRequestID(body []byte) string {
	return gatewayprovider.GrokMediaCodec().ExtractGrokMediaVideoRequestID(body)
}

// handleGrokMediaErrorResponse applies provider health, records sanitized
// diagnostics and delegates media failure selection. The response and provider
// are required; classification controls retry metadata before final
// administrator rules can rewrite client presentation.
func (s *GrokExecutor) handleGrokMediaErrorResponse(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	requestIDHeader string,
	requestedModel string,
) (*forwardcore.OpenAIResult, error) {
	body := s.Output.ReadErrorBody(resp)
	// Apply health policy before final client rules so rewritten presentation cannot
	// alter the pool observation, cooldown or retry decision derived from this response.
	decision := gatewayprovider.ApplyGrokExecutionHealth(ctx, s.Health, provider, resp.StatusCode, resp.Header, body, "", requestedModel)
	upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(body)))
	if upstreamMsg == "" {
		upstreamMsg = fmt.Sprintf("xAI upstream returned status %d", resp.StatusCode)
	}

	upstreamDetail := ""
	if s.Output.Options.LogUpstreamErrorBody {
		maxBytes := s.Output.Options.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		// Bound diagnostic retention while the full response body still determines
		// health classification, client-rule matching and returned failover evidence.
		upstreamDetail = logredact.TruncateUTF8(string(body), maxBytes)
	}
	SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	return nil, gatewaymedia.ResolveGrokFailure(resp.StatusCode, upstreamMsg, gatewaymedia.GrokFailurePorts{
		ContentRejection: func() (bool, string) {
			if !grok.IsGrokContentPolicyRejection(resp.StatusCode, body) {
				return false, ""
			}
			return true, gatewayprovider.GrokContentPolicyClientMessage(body)
		},
		Generic: decision.ShouldReturnGenericError,
		Failover: func() bool {
			return decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode, gatewayprovider.ShouldFailoverGrokResponse(resp.StatusCode, body))
		},
		Retry: func() gatewaymedia.GrokRetry {
			retryable, delay, deadline, maximum := gatewayprovider.GrokSameProviderRetryMetadata(provider, resp.StatusCode, body)
			return gatewaymedia.GrokRetry{Retryable: retryable, PolicyRetryable: decision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode), Delay: delay, Deadline: deadline, Maximum: maximum}
		},
		Observe: func(kind, message string) {
			AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
				Platform:           provider.Record.Platform,
				ProviderID:         provider.Record.ID,
				ProviderName:       provider.Record.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  requestIDHeader,
				Kind:               kind,
				Message:            message,
				Detail:             upstreamDetail,
			}, telemetry.RequestAttemptFromResponse(resp))
		},
		Rewrite: func() (gatewaymedia.ErrorResponse, bool) {
			status, typ, message, matched := ApplyErrorPassthroughRule(c, provider.Record.Platform, resp.StatusCode, body, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			return gatewaymedia.ErrorResponse{Status: status, Type: typ, Message: message}, matched
		},
		Write: func(response gatewaymedia.ErrorResponse) {
			MarkResponseCommitted(c)
			WriteGrokMediaErrorResponse(c, response.Status, response.Type, response.Message)
		},
		NewFailover: func(retry gatewaymedia.GrokRetry, transient bool) error {
			return &forwardcore.UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: body, ResponseHeaders: resp.Header.Clone(), RetryableOnSameProvider: retry.Retryable || retry.PolicyRetryable, RequestScopedTransient: transient, SameProviderRetryDelay: retry.Delay, SameProviderRetryDeadline: retry.Deadline, SameProviderRetryMax: retry.Maximum}
		},
	})
}

// rewriteGrokMediaVideoContentURLs delegates same-task content URL replacement
// for client status output. Recognized protected or signed media URLs become
// the supplied proxy path, while unrelated values remain in the response
// bytes.
func rewriteGrokMediaVideoContentURLs(body []byte, requestID, proxyURL string) []byte {
	return gatewaymedia.RewriteVideoContentURLs(body, requestID, proxyURL, gatewayprovider.GrokMediaCodec().IsGrokMediaVideoContentURL)
}
