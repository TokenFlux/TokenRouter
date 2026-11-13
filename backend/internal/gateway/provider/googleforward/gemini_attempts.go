package googleforward

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressadapter "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
)

// gemini_attempts.go executes Gemini Messages and native actions and assembles
// their response observations.
// Gemini Messages and native actions use one selected-provider exchange while
// keeping the client model and upstream model separate. Project-bearing OAuth
// generation can request an upstream stream for a buffered client, and native
// counting can return an estimate when the OAuth token lacks the required
// scope.
//
// Temporary error rules can be applied inside the retry loop before final
// presentation. An internal response marker carries that completed decision so
// final policy handling does not repeat its persistence step. Captured error
// bytes are rebuilt for later inspection, while successful response handling
// combines upstream usage with request-size and attempt image observations.
// Local counting estimates return empty usage, distinguishing them from
// observed upstream tokens.

// These values bound provider retry count, carry applied temporary policy
// between error stages, and identify compatibility request and presentation
// values.
const (
	geminiMaxRetries = 5 // same-provider retry count cap

	// maxRelayRetryDelay caps a relay's Retry-After wait before the next
	// same-provider attempt; it matches the OpenAI 429 retry cap.
	maxRelayRetryDelay = 8 * time.Second

	geminiAppliedTempPolicyHeader = "X-TokenRouter-Internal-Temp-Policy-Applied" // applied temporary-policy marker

	// Missing functionCall thought signatures receive this sentinel through the
	// shared bridge so converted tool history can use the compatibility request
	// representation.
	// Protocol reference: https://ai.google.dev/gemini-api/docs/thought-signatures
	geminiDummyThoughtSignature = "skip_thought_signature_validator"

	geminiCustomCodeSkippedClientMessage = "Upstream gateway error" // custom-code miss client message
)

// geminiFinalFailure carries the final classification and optional outer retry
// signal for a failed Gemini exchange.
type geminiFinalFailure struct {
	policy providercore.ErrorPolicyResult // selected provider error policy
	// googleConfigError records the raw Google project classification. Protocol
	// callers use it only for their existing project-error diagnostic log.
	googleConfigError bool
	// failover is nil for terminal presentation; otherwise its headers are owned
	// by this outcome and its initial body borrows the inspected response bytes.
	failover *forwardcore.UpstreamFailoverError
}

// readUpstreamErrorBody returns available diagnostic bytes up to the error
// inspection cap. Missing bodies return nil and reading errors are discarded;
// callers retain responsibility for closing or rebuilding the response.
func (s *Gemini) readUpstreamErrorBody(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	// Keep policy and presentation diagnostics within a default 512 KiB
	// prefix.
	limit := int64(512 << 10)
	if s != nil && s.Options.Configured && s.Options.LogErrorBody && s.Options.LogErrorBodyMaxBytes > int(limit) {
		limit = int64(s.Options.LogErrorBodyMaxBytes)
	}
	// Retain the bounded prefix even after a source read failure because
	// policy and client presentation still classify the available upstream
	// error evidence.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, limit))
	return body
}

// validateUpstreamBaseURL normalizes a provider endpoint under the configured
// URL policy. Configured disabled allowlisting uses format validation;
// otherwise the allowed-host policy is required and failures return wrapped URL
// errors.
func (s *Gemini) validateUpstreamBaseURL(raw string) (string, error) {
	if s.Options.Configured && !s.Options.URLAllowlistEnabled {
		normalized, err := egress.ValidateURLFormat(raw, s.Options.AllowInsecureHTTP)
		if err != nil {
			return "", fmt.Errorf("invalid base_url: %w", err)
		}
		return normalized, nil
	}
	normalized, err := egress.ValidateHTTPSURL(raw, egress.ValidationOptions{
		AllowedHosts: s.Options.AllowedHosts,

		RequireAllowlist: true,

		AllowPrivate: s.Options.AllowPrivateHosts,
	})
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	return normalized, nil
}

// Forward converts one selected-provider Messages request into Gemini and
// returns separate client and wire model metadata. It resolves model attributes
// before conversion controls and builds fresh attempt image state.
//
// Project-bearing OAuth can use an upstream stream for a buffered client.
// Signature recovery retains the original Claude body for cleanup and
// reconversion, while later policy and HTTP presentation classify the selected
// response before output. Successful completion combines actual token usage
// with request image-size hints and per-attempt image count selection.
// @project-doc docs/interfaces/gemini_upstream.md#gemini_native_execution
func (s *Gemini) Forward(ctx context.Context, output Output, provider *gatewayprovider.ExecutionProvider, body []byte) (*forwardcore.MessagesResult, error) {
	c := &attempt{Output: output}

	c.Images = 0
	startTime := time.Now()

	var req struct {
		Model  string `json:"model"`  // client request model identifier
		Stream bool   `json:"stream"` // client streaming response flag
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("parse request: %w", err)
	}
	if strings.TrimSpace(req.Model) == "" {
		return nil, fmt.Errorf("missing model")
	}

	originalModel := req.Model
	// Resolve provider mapping before conversion capabilities and request
	// planning so OAuth, API key, and service-account attempts send the
	// same selected wire model.
	mappedModel := gatewayprovider.ExecutionModelPolicy(provider).UpstreamModel(ctx, req.Model)
	toolScope := session.GeminiToolScope{APIKeyID: c.APIKeyID(), ProviderID: provider.Record.ID, Model: mappedModel}

	geminiReq, err := bridge.NativeConvertClaudeMessagesToGeminiGenerateContent(s.geminiToolOptions(toolScope), body)
	if err != nil {
		return nil, c.ClaudeError(http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	attributes, err := gatewayprovider.ResolveModelAttributes(ctx, provider, mappedModel, s.Attributes, "gemini_generate_content")
	if err != nil {
		return nil, err
	}
	geminiReq, err = applyGeminiModelParameters(geminiReq, body, attributes, true)
	if err != nil {
		return nil, c.ClaudeError(http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	originalClaudeBody := body

	proxyURL := ""
	if provider.Record.ProxyID != nil && provider.Record.Proxy != nil {
		proxyURL = provider.Record.Proxy.URL()
	}

	requestIDHeader := "x-request-id"
	switch provider.Record.Type {
	case capability.ProviderTypeAPIKey, capability.ProviderTypeOAuth, capability.ProviderTypeServiceAccount:
	default:
		return nil, fmt.Errorf("unsupported provider type: %s", provider.Record.Type)
	}
	useUpstreamStream := req.Stream
	// Project-bearing OAuth can collect upstream events for a buffered
	// Messages client, so the request plan tracks client and upstream
	// stream choices separately.
	if provider.Record.Type == capability.ProviderTypeOAuth && !req.Stream && strings.TrimSpace(provider.View().GetCredential("project_id")) != "" {
		useUpstreamStream = true
	}
	plan := s.geminiRequestPlan(provider, mappedModel, "", false, req.Stream, useUpstreamStream, false, c.GetHeader)
	buildReq := func(ctx context.Context) (*http.Request, string, error) {
		return gemininative.BuildRequest(ctx, geminiReq, plan)
	}

	options := s.geminiExchangeOptions(c, ctx, provider, mappedModel, geminiExchangeMessages, gemininative.OpenAICompatChatCompletions)
	options.Build = buildReq
	options.RequestIDHeader = requestIDHeader
	options.Do = func(req *http.Request) (*http.Response, error) {
		return s.Transport.Do(req, proxyURL, provider.Record.ID, provider.Record.Concurrency)
	}
	options.FilterThinking = func() []byte { return gatewayprovider.FilterThinkingBlocksForRetry(originalClaudeBody, originalModel) }
	options.FilterTools = func() []byte {
		return gatewayprovider.FilterSignatureSensitiveBlocksForRetry(originalClaudeBody, originalModel)
	}
	options.ConvertRetry = func(stripped []byte) ([]byte, error) {
		// A provider rejected the original signature. Rebuild with the
		// compatibility marker so the retry does not send that signature again.
		converted, err := convertClaudeMessagesToGeminiGenerateContent(stripped)
		if err != nil {
			return nil, err
		}
		converted, err = applyGeminiModelParameters(converted, stripped, attributes, false)
		// After signature cleanup and reconversion succeed, disable
		// thinking when the resolved toggle permits it so recovery
		// sends the stripped history with compatible controls.
		if err == nil && attributes.Attributes.HasReasoningOption("toggle") {
			converted, err = sjson.SetBytes(converted, "generationConfig.thinkingConfig", bridge.GeminiThinkingConfig{BudgetSet: true})
		}
		return converted, err
	}
	options.ReplaceBody = func(value []byte) { geminiReq = value }
	var requestID string
	var compatibilityResult *forwardcore.MessagesResult
	stopped := false
	target := &gemininative.Target{
		ProviderID:     provider.Record.ID,
		Model:          mappedModel,
		Mode:           gemininative.MessagesResponse,
		Exchange:       options,
		Response:       s.geminiResponseAdapter(c, toolScope).Options,
		StartedAt:      startTime,
		UpstreamStream: useUpstreamStream,
		OAuth:          provider.Record.Type == capability.ProviderTypeOAuth,
		Enter:          s.Enter,
	}
	target.BeforeResponse = func(ctx context.Context, resp *http.Response, requestIDHeader string) (bool, error) {
		var callbackErr error
		compatibilityResult, callbackErr = func() (*forwardcore.MessagesResult, error) {
			if resp.StatusCode >= 400 {
				respBody := s.readUpstreamErrorBody(resp)
				outcome := s.buildGeminiFailureOutcome(ctx, provider, resp, respBody, mappedModel)
				upstreamReqID := resp.Header.Get(requestIDHeader)
				if upstreamReqID == "" {
					upstreamReqID = resp.Header.Get("x-goog-request-id")
				}
				if outcome.failover != nil {
					upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
					c.Observe(ops.OpsUpstreamErrorEvent{
						Platform:           provider.Record.Platform,
						ProviderID:         provider.Record.ID,
						ProviderName:       provider.Record.Name,
						UpstreamStatusCode: resp.StatusCode,
						UpstreamRequestID:  upstreamReqID,
						Kind:               "failover",
						Message:            upstreamMsg,
						Detail:             s.upstreamErrorDetail(respBody),
					})
					if outcome.googleConfigError {
						log.Printf("[Gemini] status=400 google_config_error failover=true upstream_message=%q provider=%d", upstreamMsg, provider.Record.ID)
					}
					return nil, outcome.failover
				}
				if outcome.policy == providercore.ErrorPolicyCustomSkipped {
					return nil, c.GeminiCustomCodeSkippedError(provider, resp.StatusCode, upstreamReqID, respBody, func() {
						_ = c.ClaudeError(http.StatusInternalServerError, "api_error", geminiCustomCodeSkippedClientMessage)
					})
				}
				return nil, c.GeminiMappedError(provider, resp.StatusCode, upstreamReqID, respBody)
			}

			requestID = resp.Header.Get(requestIDHeader)
			if requestID == "" {
				requestID = resp.Header.Get("x-goog-request-id")
			}

			return nil, nil
		}()
		stopped = resp.StatusCode >= 400 || callbackErr != nil || compatibilityResult != nil
		return stopped, callbackErr
	}
	result, executeErr := (gemininative.Executor{}).Execute(ctx, upstream.AttemptInput{
		Protocol:      protocolcore.ProtocolAnthropicMessages,
		Body:          body,
		Stream:        req.Stream,
		ResponseModel: originalModel,
		Target:        target,
	}, c.Sink())
	if stopped {
		return compatibilityResult, executeErr
	}
	if executeErr != nil {
		executeErr = s.streamFailure(ctx, c, provider, result, executeErr, func(status int, body []byte) error {
			return c.GeminiMappedError(provider, status, result.RequestID, body)
		})
		if !result.HasUsage && !result.Served {
			return nil, executeErr
		}
	}
	requestID = result.RequestID
	usage := &result.Usage

	// Combine request size hints with the attempt observation after
	// reading; completion uses observed image counts independently of model
	// naming or declared output capability.
	imageInputSize := s.extractImageInputSize(body)
	imageSize := media.NormalizeImageSizeTier(imageInputSize)
	imageCount := c.imageCount(originalModel, mappedModel)

	return &forwardcore.MessagesResult{
		RequestID:             requestID,
		UpstreamHeaders:       result.UpstreamHeaders,
		UpstreamResponseModel: result.UpstreamResponseModel,
		Usage:                 *usage,
		Model:                 originalModel,
		UpstreamModel:         mappedModel,
		Stream:                req.Stream,
		Duration:              result.Duration,
		Timings:               result.Timings,
		ClientDisconnect:      result.ClientDisconnect,
		ImageCount:            imageCount,
		ImageSize:             imageSize,
		ImageInputSize:        imageInputSize,
	}, executeErr
}

// ForwardNative executes generateContent, streamGenerateContent, or countTokens
// for a selected provider. Missing model, action, or body receives a local
// Google error, and native part/signature cleanup precedes attribute
// preparation.
//
// Project-bearing OAuth generation can use an upstream stream independently of
// client mode. Insufficient OAuth counting scope selects a local estimate with
// empty result usage, before custom error policy handling. Other HTTP failures
// use provider policy and protocol presentation, while successful generation
// returns separate logical model, wire model, token usage, and image
// observations.
func (s *Gemini) ForwardNative(ctx context.Context, output Output, provider *gatewayprovider.ExecutionProvider, originalModel string, action string, stream bool, body []byte) (*forwardcore.MessagesResult, error) {
	c := &attempt{Output: output}

	c.Images = 0
	startTime := time.Now()

	if strings.TrimSpace(originalModel) == "" {
		return nil, c.GoogleError(http.StatusBadRequest, "Missing model in URL")
	}
	if strings.TrimSpace(action) == "" {
		return nil, c.GoogleError(http.StatusBadRequest, "Missing action in URL")
	}
	if len(body) == 0 {
		return nil, c.GoogleError(http.StatusBadRequest, "Request body is empty")
	}

	// Use filtered native parts when JSON cleanup succeeds; failed cleanup
	// retains the supplied body so its remaining validation stays with
	// subsequent request preparation.
	if filteredBody, err := gemini.FilterEmptyParts(body); err == nil {
		body = filteredBody
	}

	switch action {
	case "generateContent", "streamGenerateContent", "countTokens":
	default:
		return nil, c.GoogleError(http.StatusNotFound, "Unsupported action: "+action)
	}

	// Supply the bridge compatibility signature for missing functionCall
	// history before model attribute preparation, leaving existing
	// signature values available to platform handling.
	body = ensureGeminiFunctionCallThoughtSignatures(body)

	// Resolve the group-mapped model through provider mappings for every
	// credential type.
	mappedModel := gatewayprovider.ExecutionModelPolicy(provider).UpstreamModel(ctx, originalModel)
	attributes, err := gatewayprovider.ResolveModelAttributes(ctx, provider, mappedModel, s.Attributes, "gemini_generate_content")
	if err != nil {
		return nil, err
	}
	body, err = applyGeminiModelParameters(body, body, attributes, false)
	if err != nil {
		return nil, err
	}

	proxyURL := ""
	if provider.Record.ProxyID != nil && provider.Record.Proxy != nil {
		proxyURL = provider.Record.Proxy.URL()
	}

	useUpstreamStream := stream
	upstreamAction := action
	if provider.Record.Type == capability.ProviderTypeOAuth && !stream && action == "generateContent" && strings.TrimSpace(provider.View().GetCredential("project_id")) != "" {
		// Project-bearing OAuth generation uses an upstream stream
		// collected into the client response; keep the original client
		// mode separate from this request action selection.
		useUpstreamStream = true
		upstreamAction = "streamGenerateContent"
	}
	forceAIStudio := action == "countTokens"

	requestIDHeader := "x-request-id"
	switch provider.Record.Type {
	case capability.ProviderTypeAPIKey, capability.ProviderTypeOAuth, capability.ProviderTypeServiceAccount:
	default:
		return nil, c.GoogleError(http.StatusBadGateway, "Unsupported provider type: "+provider.Record.Type)
	}
	plan := s.geminiRequestPlan(provider, mappedModel, upstreamAction, true, stream, useUpstreamStream, forceAIStudio, c.GetHeader)
	buildReq := func(ctx context.Context) (*http.Request, string, error) {
		return gemininative.BuildRequest(ctx, body, plan)
	}

	options := s.geminiExchangeOptions(c, ctx, provider, mappedModel, geminiExchangeNative, gemininative.OpenAICompatChatCompletions)
	options.Build = buildReq
	options.RequestIDHeader = requestIDHeader
	options.Do = func(req *http.Request) (*http.Response, error) {
		return s.Transport.Do(req, proxyURL, provider.Record.ID, provider.Record.Concurrency)
	}
	options.CountFallback = action == "countTokens" && isGoogleAccount(provider)
	options.EstimateCount = func() int { return gemininative.EstimateGeminiCountTokens(body) }
	var requestID string
	var compatibilityResult *forwardcore.MessagesResult
	stopped := false
	target := &gemininative.Target{
		ProviderID:     provider.Record.ID,
		Model:          mappedModel,
		Mode:           gemininative.NativeResponse,
		Exchange:       options,
		Response:       s.geminiResponseAdapter(c, session.GeminiToolScope{}).Options,
		StartedAt:      startTime,
		UpstreamStream: useUpstreamStream,
		OAuth:          provider.Record.Type == capability.ProviderTypeOAuth,
		Enter:          s.Enter,
	}
	target.BeforeResponse = func(ctx context.Context, resp *http.Response, requestIDHeader string) (bool, error) {
		var callbackErr error
		compatibilityResult, callbackErr = func() (*forwardcore.MessagesResult, error) {
			requestID = resp.Header.Get(requestIDHeader)
			if requestID == "" {
				requestID = resp.Header.Get("x-goog-request-id")
			}

			isOAuth := provider.Record.Type == capability.ProviderTypeOAuth

			if resp.StatusCode >= 400 {
				respBody := s.readUpstreamErrorBody(resp)
				// Insufficient OAuth counting scope selects a
				// local estimate before error policy, and
				// returned usage stays empty so this
				// compatibility response does not imply an
				// upstream token observation.
				if action == "countTokens" && isOAuth && gemininative.IsGeminiInsufficientScope(resp.Header, respBody) {
					estimated := gemininative.EstimateGeminiCountTokens(body)
					if requestID != "" {
						c.Header("x-request-id", requestID)
					}
					c.Count(estimated)
					return &forwardcore.MessagesResult{
						RequestID:       requestID,
						UpstreamHeaders: resp.Header,
						Usage:           upstream.TokenUsage{},
						Model:           originalModel,
						UpstreamModel:   mappedModel,
						Stream:          false,
						Duration:        time.Since(startTime),
					}, nil
				}

				outcome := s.buildGeminiFailureOutcome(ctx, provider, resp, respBody, mappedModel)
				if outcome.failover != nil {
					// Native ordinary failures expose the OAuth response object to outer
					// failover; skipped policy keeps the original bytes for diagnostics.
					if outcome.policy != providercore.ErrorPolicyCustomSkipped && outcome.policy != providercore.ErrorPolicyPoolBypassed {
						outcome.failover.ResponseBody = gemininative.UnwrapIfNeeded(isOAuth, respBody)
					}
					evBody := outcome.failover.ResponseBody
					upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(evBody)))
					c.Observe(ops.OpsUpstreamErrorEvent{
						Platform:           provider.Record.Platform,
						ProviderID:         provider.Record.ID,
						ProviderName:       provider.Record.Name,
						UpstreamStatusCode: resp.StatusCode,
						UpstreamRequestID:  requestID,
						Kind:               "failover",
						Message:            upstreamMsg,
						Detail:             s.upstreamErrorDetail(evBody),
					})
					if outcome.googleConfigError {
						log.Printf("[Gemini] status=400 google_config_error failover=true upstream_message=%q provider=%d", upstreamMsg, provider.Record.ID)
					}
					return nil, outcome.failover
				}
				if outcome.policy == providercore.ErrorPolicyCustomSkipped {
					return nil, c.GeminiCustomCodeSkippedError(provider, resp.StatusCode, requestID, respBody, func() {
						_ = c.GoogleError(http.StatusInternalServerError, geminiCustomCodeSkippedClientMessage)
					})
				}
				return nil, c.GeminiNativeUpstreamError(provider, resp, respBody, requestID, isOAuth)
			}

			return nil, nil
		}()
		stopped = resp.StatusCode >= 400 || callbackErr != nil || compatibilityResult != nil
		return stopped, callbackErr
	}
	result, executeErr := (gemininative.Executor{}).Execute(ctx, upstream.AttemptInput{
		Protocol:      protocolcore.ProtocolGeminiGenerateContent,
		Body:          body,
		Stream:        stream,
		ResponseModel: originalModel,
		Target:        target,
	}, c.Sink())
	if stopped {
		return compatibilityResult, executeErr
	}
	if executeErr != nil {
		executeErr = s.streamFailure(ctx, c, provider, result, executeErr, func(status int, body []byte) error {
			return c.GeminiNativeUpstreamError(provider, &http.Response{StatusCode: status, Header: result.UpstreamHeaders}, body, result.RequestID, false)
		})
		if !result.HasUsage && !result.Served {
			return nil, executeErr
		}
	}
	if result.EstimatedTokenCount != nil {
		return &forwardcore.MessagesResult{
			Usage:         upstream.TokenUsage{},
			Model:         originalModel,
			UpstreamModel: mappedModel,
			Stream:        false,
			Duration:      result.Duration,
		}, nil
	}
	requestID = result.RequestID
	usage := &result.Usage

	// Combine request size hints with the attempt observation after
	// reading; completion uses observed image counts independently of model
	// naming or declared output capability.
	imageInputSize := s.extractImageInputSize(body)
	imageSize := media.NormalizeImageSizeTier(imageInputSize)
	imageCount := c.imageCount(originalModel, mappedModel)

	return &forwardcore.MessagesResult{
		RequestID:             requestID,
		UpstreamHeaders:       result.UpstreamHeaders,
		UpstreamResponseModel: result.UpstreamResponseModel,
		Usage:                 *usage,
		Model:                 originalModel,
		UpstreamModel:         mappedModel,
		Stream:                result.Stream,
		Duration:              result.Duration,
		Timings:               result.Timings,
		ClientDisconnect:      result.ClientDisconnect,
		ImageCount:            imageCount,
		ImageSize:             imageSize,
		ImageInputSize:        imageInputSize,
	}, executeErr
}

// checkErrorPolicyInLoop inspects failures before another Gemini retry and
// returns a readable response body afterward. Success statuses and missing
// Health return the original response unchanged. Providers other than Google
// accounts also keep ordinary responses unchanged unless pool or custom-code
// policy needs evaluation; their final shared health observer orders
// Retry-After before temporary rules.
//
// For inspected failures, captured bytes replace the closed source reader and
// headers are cloned into the replacement response. A temporary rule adds the
// internal applied marker so final handling can reuse its persistence decision.
// Any selected policy stops this platform retry loop, leaving outer provider
// and pool replay with the handler budget.
func (s *Gemini) checkErrorPolicyInLoop(
	ctx context.Context, provider *gatewayprovider.ExecutionProvider, resp *http.Response, mappedModel string,
) (matched bool, rebuilt *http.Response) {
	if resp.StatusCode < 400 || s.Health == nil || (!isGoogleAccount(provider) && !provider.View().IsPoolMode() && !provider.View().IsCustomErrorCodesEnabled()) {
		return false, resp
	}
	// Retain the inspected prefix before closing the network reader, then
	// clone headers and rebuild the body so final error handling can read
	// that evidence.
	body := s.readUpstreamErrorBody(resp)
	_ = resp.Body.Close()
	rebuilt = &http.Response{
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
	policy := s.Health.CheckErrorPolicy(ctx, gatewayprovider.ExecutionRecord(provider), gatewayprovider.HealthObservationFromContext(ctx, resp.StatusCode, nil, body, []string{mappedModel}))
	if policy == providercore.ErrorPolicyTempUnscheduled {
		// Carry the applied temporary-policy marker on the rebuilt
		// response so final error handling recognizes the persisted
		// decision before attempting the same state update again.
		rebuilt.Header.Set(geminiAppliedTempPolicyHeader, "1")
	}
	// Any selected policy ends this platform retry loop, allowing the outer
	// handler budget to decide pool replay after the response is returned.
	return policy != providercore.ErrorPolicyNone, rebuilt
}

// shouldRetryGeminiUpstreamError selects quota and server retries plus Code
// Assist status 403. OAuth records with missing oauth_type and a project ID
// select legacy Code Assist mode for that decision.
func (s *Gemini) shouldRetryGeminiUpstreamError(provider *gatewayprovider.ExecutionProvider, statusCode int) bool {
	switch statusCode {
	case 429, 500, 502, 503, 504, 529:
		return true
	case 403:
		// Restrict status-403 retries to Code Assist credentials,
		// including the legacy project-bearing form, so other OAuth
		// modes proceed to final provider policy.
		if provider == nil || provider.Record.Type != capability.ProviderTypeOAuth {
			return false
		}
		oauthType := strings.ToLower(strings.TrimSpace(provider.View().GetCredential("oauth_type")))
		if oauthType == "" && strings.TrimSpace(provider.View().GetCredential("project_id")) != "" {
			// A populated project_id with no oauth_type selects
			// legacy Code Assist mode, keeping historical records
			// eligible for the same status-403 retry branch.
			oauthType = "code_assist"
		}
		return oauthType == "code_assist"
	default:
		return false
	}
}

// buildGeminiFailureOutcome applies final provider policy and constructs retry
// metadata from the raw upstream response. Callers run native counting fallback
// first and retain responsibility for request-ID headers, diagnostic events,
// and protocol rendering. A nil failover leaves the policy result for terminal presentation.
//
// Pool bypass and custom-code misses use ordinary failover statuses without the
// Google project exception, and their replay permission follows pool settings.
// Other decisions combine project classification with provider health evidence.
// Headers are cloned after policy consumes its internal marker; the body remains
// borrowed so each protocol can select its existing OAuth presentation.
func (s *Gemini) buildGeminiFailureOutcome(
	ctx context.Context,
	provider *gatewayprovider.ExecutionProvider,
	resp *http.Response,
	body []byte,
	mappedModel string,
) geminiFinalFailure {
	decision := s.applyGeminiUpstreamErrorPolicy(ctx, provider, resp.StatusCode, resp.Header, body, mappedModel)
	outcome := geminiFinalFailure{policy: decision.Policy}
	var retryableOnSameProvider bool
	if decision.Policy == providercore.ErrorPolicyCustomSkipped || decision.Policy == providercore.ErrorPolicyPoolBypassed {
		// Skipped health policy retains server/authentication failover while pool
		// configuration alone determines whether the outer handler can replay it.
		if !s.shouldFailoverGeminiUpstreamError(resp.StatusCode) {
			return outcome
		}
		retryableOnSameProvider = provider.View().IsPoolMode() && provider.View().IsPoolModeRetryableStatus(resp.StatusCode)
	} else {
		message := strings.ToLower(strings.TrimSpace(upstream.ExtractErrorMessage(body)))
		outcome.googleConfigError = isGoogleAccount(provider) && resp.StatusCode == http.StatusBadRequest && upstream.IsGoogleProjectConfigError(message)
		defaultFailover := outcome.googleConfigError || s.shouldFailoverGeminiUpstreamError(resp.StatusCode)
		if !decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode, defaultFailover) {
			return outcome
		}
		retryableOnSameProvider = decision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode)
	}
	outcome.failover = &forwardcore.UpstreamFailoverError{
		Platform:                provider.Record.Platform,
		StatusCode:              resp.StatusCode,
		ResponseHeaders:         resp.Header.Clone(),
		SameProviderRetryDelay:  relayRetryDelay(provider, resp.Header),
		ResponseBody:            body,
		RetryableOnSameProvider: retryableOnSameProvider,
	}
	return outcome
}

// shouldFailoverGeminiUpstreamError recognizes authentication, quota, and
// server statuses for provider switching.
func (s *Gemini) shouldFailoverGeminiUpstreamError(statusCode int) bool {
	switch statusCode {
	case 401, 403, 429, 529:
		return true
	default:
		return statusCode >= 500
	}
}

// upstreamErrorDetail returns a bounded UTF-8 error prefix under enabled
// configured logging. Nil runtime or disabled logging returns empty, and
// nonpositive configured limits select the 2048-byte diagnostic cap.
func (s *Gemini) upstreamErrorDetail(body []byte) string {
	if s == nil || !s.Options.Configured || !s.Options.LogErrorBody {
		return ""
	}
	maxBytes := s.Options.LogErrorBodyMaxBytes
	if maxBytes <= 0 {
		maxBytes = 2048
	}
	return logredact.TruncateUTF8(string(body), maxBytes)
}

// ForwardAIStudioGET reads one selected-provider model resource through the
// native model reader. Nil provider returns an error; transport and token
// callbacks use current provider inputs, and response headers follow the
// injected filter.
func (s *Gemini) ForwardAIStudioGET(ctx context.Context, provider *gatewayprovider.ExecutionProvider, path string) (*gemininative.HTTPResult, error) {
	if provider == nil {
		return nil, errors.New("provider is nil")
	}
	options := gemininative.ModelGetOptions{
		Mode:        gemininative.CredentialMode(provider.Record.Type),
		BaseURL:     func() string { return provider.View().GetGeminiBaseURL(geminicli.AIStudioBaseURL) },
		APIKey:      func() string { return provider.View().GetCredential("api_key") },
		ValidateURL: s.validateUpstreamBaseURL,
		Enter:       s.Enter,
		Do: func(req *http.Request) (*http.Response, error) {
			proxy := ""
			if provider.Record.ProxyID != nil && provider.Record.Proxy != nil {
				proxy = provider.Record.Proxy.URL()
			}
			return s.Transport.Do(req, proxy, provider.Record.ID, provider.Record.Concurrency)
		},
		FilterHeaders: func(header http.Header) http.Header {
			return egressadapter.FilterHeaders(header, s.HeaderFilter)
		},
	}
	if s.Tokens != nil {
		options.Token = func(ctx context.Context) (string, error) {
			return gatewayprovider.ExecutionToken(ctx, s.Tokens, provider)
		}
	}
	return gemininative.ReadAIStudioModel(ctx, path, options)
}

// applyGeminiUpstreamErrorPolicy selects explicit policy before the default
// Gemini observer. An applied temporary marker is consumed first and returns
// the persisted scheduling decision from the retry loop.
//
// Fresh custom matches and temporary rules stop scheduling; custom skips and
// pool bypass return directly. Only the remaining path invokes default health
// observation, so pool quota handling does not add a local default cooldown.
// The returned decision remains paired with the selected provider policy for
// outer retry and presentation choices.
func (s *Gemini) applyGeminiUpstreamErrorPolicy(
	ctx context.Context,
	provider *gatewayprovider.ExecutionProvider,
	statusCode int,
	headers http.Header,
	body []byte,
	mappedModel string,
) providercore.UpstreamErrorDecision {
	decision := providercore.ErrorDecisionWithoutPersistence(gatewayprovider.ExecutionErrorPolicy(provider), statusCode)
	if s == nil || provider == nil {
		return decision
	}
	// Consume the internal applied marker before normal policy evaluation
	// so a temporary rule already persisted in the exchange loop is
	// returned without another update.
	if headers != nil && headers.Get(geminiAppliedTempPolicyHeader) == "1" {
		headers.Del(geminiAppliedTempPolicyHeader)
		decision.Policy = providercore.ErrorPolicyTempUnscheduled
		decision.StopScheduling = true
		return decision
	}
	if !isGoogleAccount(provider) && s.Health != nil {
		// Shared provider health owns quota and retry evidence for providers that
		// only borrow the Gemini encoding. Return its decision directly so Google
		// quota observation cannot write a second deadline for this attempt.
		return s.Health.ApplyUpstreamError(ctx, gatewayprovider.ExecutionRecord(provider), gatewayprovider.HealthObservationFromContext(ctx, statusCode, headers, body, nil))
	}
	if s.Health != nil {
		decision.Policy = s.Health.ApplyExplicitErrorPolicy(ctx, gatewayprovider.ExecutionRecord(provider), gatewayprovider.HealthObservationFromContext(ctx, statusCode, nil, body, []string{mappedModel}))
		decision.StopScheduling = decision.Policy == providercore.ErrorPolicyCustomMatched || decision.Policy == providercore.ErrorPolicyTempUnscheduled
	}
	switch decision.Policy {
	case providercore.ErrorPolicyCustomMatched, providercore.ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case providercore.ErrorPolicyCustomSkipped, providercore.ErrorPolicyPoolBypassed:
		return decision
	}
	s.observeHealth(ctx, provider, statusCode, headers, body)
	return decision
}

// ensureGeminiFunctionCallThoughtSignatures delegates missing function-call
// signature compatibility to the native bridge. It supplies the configured
// sentinel while the bridge owns JSON parsing and returned byte-slice handling.
func ensureGeminiFunctionCallThoughtSignatures(body []byte) []byte {
	return bridge.NativeEnsureGeminiFunctionCallThoughtSignatures(bridge.NativeGeminiOptions{DummyThoughtSignature: geminiDummyThoughtSignature}, body)
}

// convertClaudeMessagesToGeminiGenerateContent delegates Messages conversion
// using native Gemini bridge options. The compatibility signature is supplied
// to the converter, and conversion errors return before an upstream request is
// planned.
func convertClaudeMessagesToGeminiGenerateContent(body []byte) ([]byte, error) {
	return bridge.NativeConvertClaudeMessagesToGeminiGenerateContent(bridge.NativeGeminiOptions{DummyThoughtSignature: geminiDummyThoughtSignature}, body)
}

// extractImageInputSize returns the trimmed camelCase request image-size hint.
// Malformed JSON or absent generation/image configuration returns empty,
// leaving billing-tier defaults to the caller normalizer after extraction.
func (s *Gemini) extractImageInputSize(body []byte) string {
	var req struct {
		// GenerationConfig is optional in the JSON view; absent
		// configuration leaves image-size extraction empty for the
		// caller normalizer to interpret.
		GenerationConfig *struct { // request generation option object
			// ImageConfig is optional within GenerationConfig; a
			// missing object leaves the image-size hint absent
			// while other generation options remain irrelevant to
			// extraction.
			ImageConfig *struct { // request image option object
				ImageSize string `json:"imageSize"` // request image-size hint
			} `json:"imageConfig"`
		} `json:"generationConfig"`
	}
	// Treat malformed JSON as an absent size hint so the caller
	// billing-tier normalizer can apply its default after extraction.
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}

	if req.GenerationConfig != nil && req.GenerationConfig.ImageConfig != nil {
		return strings.TrimSpace(req.GenerationConfig.ImageConfig.ImageSize)
	}

	return ""
}

// observeHealth delegates default Gemini error observation using the selected
// execution record. When the observer returns updated data, credential and
// extra references are assigned back into the target for later attempt
// decisions.
func (s *Gemini) observeHealth(ctx context.Context, target *gatewayprovider.ExecutionProvider, status int, headers http.Header, body []byte) {
	record := gatewayprovider.ExecutionRecord(target)
	observation := gatewayprovider.HealthObservationFromContext(ctx, status, headers, body, nil)
	// Google quota classification applies only to Google accounts. A relay that
	// borrows the Gemini encoding records Retry-After and administrator rules
	// through shared provider health instead.
	if target != nil && !isGoogleAccount(target) {
		if s.Health != nil {
			s.Health.ApplyUpstreamError(ctx, record, observation)
		}
		return
	}
	updated := s.Errors.Observe(ctx, record, status, headers, body, observation)
	if target != nil && updated != nil {
		target.Record.Credentials, target.Record.Extra = updated.Credentials, updated.Extra
	}
}

// isGoogleAccount reports whether the target is a Google Gemini account, whose
// project-configuration failover, count fallback, quota observation, and local
// retry rules apply. A compatible relay that routes a model through Gemini
// encoding borrows only the wire format and uses shared provider policy.
func isGoogleAccount(value *gatewayprovider.ExecutionProvider) bool {
	return value.Record.Platform == capability.PlatformGemini
}

// relayRetryDelay returns the same-provider wait a relay requests through
// Retry-After, capped at maxRelayRetryDelay so one response cannot hold a
// pooled request open for the provider's whole cooldown. Google accounts and
// responses without a future deadline return zero, keeping shared retry timing.
func relayRetryDelay(value *gatewayprovider.ExecutionProvider, headers http.Header) time.Duration {
	if value == nil || isGoogleAccount(value) {
		return 0
	}
	now := time.Now()
	reset := upstream.RetryAfterResetTime(headers, now)
	if reset == nil {
		return 0
	}
	return min(reset.Sub(now), maxRelayRetryDelay)
}
