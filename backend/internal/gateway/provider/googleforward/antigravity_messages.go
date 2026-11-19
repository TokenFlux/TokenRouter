package googleforward

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	googlewire "github.com/TokenFlux/TokenRouter/internal/protocol/google"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// antigravity_messages.go converts Messages requests for Antigravity and
// returns Claude responses with execution metadata.
// Antigravity Messages resolves the thinking-qualified final model before
// converting Claude input into v1internal Gemini bytes. The selected model is
// retained as completion upstream metadata while the response uses the original
// client model name and requested output mode.
//
// Both buffered and streaming clients use the upstream streaming action. The
// shared retry service runs before Claude signature or budget recovery, and
// provider-switch signals are translated for the outer gateway. Prompt-too-long
// errors retain typed body and request identity before ordinary health
// handling; project configuration and qualifying status errors can request
// replay before response presentation. The platform executor closes the
// selected successful response after synchronous adaptation.

// Forward executes one Claude request through a selected Antigravity OAuth
// provider and returns Messages completion metadata. Final model selection
// includes thinking state before whitelist and capability checks.
//
// Both client modes consume the streaming upstream action, with output
// adaptation collecting a buffered reply when requested. The shared provider
// retry service precedes signature and budget recovery. Prompt-length failures
// return typed evidence, provider switches retain sticky billing hints, and
// other HTTP errors use health observation and client presentation before
// completion data is returned.
// @project-doc docs/interfaces/antigravity_upstream.md#antigravity_native_execution
func (s *Antigravity) Forward(ctx context.Context, output Output, provider *gatewayprovider.ExecutionProvider, body []byte, isStickySession bool) (*forwardcore.MessagesResult, error) {
	c := &attempt{Output: output}

	startTime := time.Now()

	sessionID := c.GetHeader("session_id")
	prefix := logPrefix(sessionID, provider.Record.Name)

	var claudeReq protocolanthropic.ClaudeRequest
	if err := json.Unmarshal(body, &claudeReq); err != nil {
		return nil, c.ClaudeError(http.StatusBadRequest, "invalid_request_error", "Invalid request body")
	}
	if strings.TrimSpace(claudeReq.Model) == "" {
		return nil, c.ClaudeError(http.StatusBadRequest, "invalid_request_error", "Missing model")
	}

	originalModel := claudeReq.Model
	// Resolve the final whitelist model with this request thinking flag so
	// capability preparation, upstream identity, and admission refer to the
	// same selected model variant.
	thinkingEnabled := claudeReq.Thinking != nil && (claudeReq.Thinking.Type == "enabled" || claudeReq.Thinking.Type == "adaptive")
	modelCtx := requeststate.WithThinkingEnabled(ctx, thinkingEnabled)
	mappedModel := gatewayprovider.ExecutionModelPolicy(provider).FinalAntigravityModel(modelCtx, claudeReq.Model)
	if mappedModel == "" {
		c.FeatureDenied()
		return nil, c.ClaudeError(http.StatusForbidden, "permission_error", fmt.Sprintf("model %s not in whitelist", claudeReq.Model))
	}
	billingModel := mappedModel

	if s.Tokens == nil {
		return nil, c.ClaudeError(http.StatusBadGateway, "api_error", "Antigravity token provider not configured")
	}
	accessToken, err := gatewayprovider.ExecutionToken(ctx, s.Tokens, provider)
	if err != nil {
		return nil, &forwardcore.UpstreamFailoverError{
			StatusCode: http.StatusBadGateway,

			ResponseBody: []byte(`{"error":{"type":"authentication_error","message":"Failed to get upstream access token"},"type":"error"}`),
		}
	}

	projectID, err := resolveAntigravityProjectID(provider)
	if err != nil {
		_ = c.ClaudeError(http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}

	proxyURL := ""
	if provider.Record.ProxyID != nil && provider.Record.Proxy != nil {
		proxyURL = provider.Record.Proxy.URL()
	}

	// Resolve transform controls for the selected final model before
	// converting the Claude body, then send the Antigravity identity patch
	// along with that prepared request.
	transformOpts := s.getClaudeTransformOptions(ctx)
	attributes, attributesErr := gatewayprovider.ResolveModelAttributes(ctx, provider, mappedModel, s.Attributes, "gemini_generate_content")
	if attributesErr != nil {
		return nil, attributesErr
	}
	transformOpts.Capabilities = &attributes
	transformOpts.Thinking, err = gatewayprovider.GeminiThinking(attributes, body)
	if err != nil {
		return nil, c.ClaudeError(http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	transformOpts.ThinkingResolved = true
	// This forwarding path enables identity injection after reading
	// transform settings so its prepared Antigravity request includes the
	// platform identity patch for every call.
	transformOpts.EnableIdentityPatch = true

	geminiBody, err := antigravity.TransformClaudeToGeminiWithOptions(&claudeReq, projectID, mappedModel, transformOpts)
	if err != nil {
		return nil, c.ClaudeError(http.StatusBadRequest, "invalid_request_error", "Invalid request")
	}

	// Both client modes consume the v1internal streaming action; response
	// adaptation emits Claude events for streaming callers and collects
	// those events for a buffered reply.
	action := "streamGenerateContent"

	retry, params := s.antigravityRetryAdapter(antigravityRetryLoopParams{
		ctx:             ctx,
		prefix:          prefix,
		provider:        provider,
		proxyURL:        proxyURL,
		accessToken:     accessToken,
		action:          action,
		body:            geminiBody,
		c:               c,
		httpUpstream:    s.Transport,
		providerRepo:    s.Store,
		handleError:     s.handleUpstreamError,
		requestedModel:  originalModel,
		isStickySession: isStickySession, // The caller determines whether this request is sticky.
		groupID:         0,               // The caller clears sticky state because this entry has no group ID.
		sessionHash:     "",              // The caller owns the session binding.
	})
	target := &antigravity.Target{
		ProviderID: provider.Record.ID,
		Model:      billingModel,
		Mode:       antigravity.ModeClaudeResponse,
		StartedAt:  startTime,
		Response:   s.antigravityResponseAdapter(c).Options,
		Enter:      s.Enter,
	}
	target.Exchange = func(context.Context) (*http.Response, error) {
		result, err := retry.AntigravityRetryLoop(params)
		if err != nil {
			// Translate the platform provider-switch signal into
			// the gateway error type so the outer selection loop
			// receives the sticky cache-billing hint for another
			// attempt.
			if switchErr, ok := antigravity.IsAntigravityProviderSwitchError(err); ok {
				return nil, &forwardcore.UpstreamFailoverError{
					StatusCode: http.StatusServiceUnavailable,

					ForceCacheBilling: switchErr.IsStickySession,
				}
			}
			// Inspect the active inbound request after retry
			// failure so client disconnection uses its own
			// presentation error before the generic
			// exhausted-upstream failure is chosen.
			if c.RequestContext().Err() != nil {
				return nil, c.ClaudeError(http.StatusBadGateway, "client_disconnected", "Client disconnected before upstream response")
			}
			return nil, c.ClaudeError(http.StatusBadGateway, "upstream_error", "Upstream request failed after retries")
		}
		options := antigravity.ClaudeRecoveryOptions{
			Retry: func(body []byte) (*http.Response, error) {
				next := params
				next.Body = body
				value, err := retry.AntigravityRetryLoop(next)
				if err != nil {
					return nil, err
				}
				return value.Resp, nil
			},
			SignatureEnabled:   s.Gateway.IsSignatureRectifierEnabled,
			BudgetEnabled:      s.Gateway.IsBudgetRectifierEnabled,
			TransformOptions:   func(context.Context) antigravity.TransformOptions { return transformOpts },
			LogConfig:          s.getLogConfig,
			ErrorDetail:        s.getUpstreamErrorDetail,
			ReadErrorBody:      s.readUpstreamErrorBody,
			Observe:            retry.Options.Observe,
			IsBudgetConstraint: anthropic.IsThinkingBudgetConstraintError,
			BudgetTokens:       anthropic.BudgetRectifyBudgetTokens,
			MinMaxTokens:       anthropic.BudgetRectifyMinMaxTokens,
			MaxTokens:          anthropic.BudgetRectifyMaxTokens,
			TruncateForLog:     logredact.TruncateLine,
			TruncateString:     logredact.TruncateUTF8,
		}
		return antigravity.RecoverClaude(ctx, antigravity.ClaudeRecoveryInput{
			ProviderID:     provider.Record.ID,
			ProviderName:   provider.Record.Name,
			Prefix:         prefix,
			ProjectID:      projectID,
			Model:          mappedModel,
			Request:        claudeReq,
			InitialOptions: transformOpts,
		}, result.Resp, options), nil
	}
	target.BeforeResponse = func(ctx context.Context, resp *http.Response) (bool, error) {
		if resp.StatusCode < 400 {
			return false, nil
		}
		respBody := s.readUpstreamErrorBody(resp)
		if resp.StatusCode >= 400 {
			// Prompt-length failure returns captured bytes and
			// request identity in its typed error before ordinary
			// health handling, allowing the caller to select its
			// configured prompt fallback.
			if resp.StatusCode == http.StatusBadRequest && antigravity.IsPromptTooLongError(respBody) {
				upstreamMsg := strings.TrimSpace(googlewire.ExtractPlatformMessage(respBody))
				upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
				upstreamDetail := s.getUpstreamErrorDetail(respBody)
				logBody, maxBytes := s.getLogConfig()
				if logBody {
					logging.LegacyPrintf("service.antigravity_gateway", "%s status=400 prompt_too_long=true upstream_message=%q request_id=%s body=%s", prefix, upstreamMsg, resp.Header.Get("x-request-id"), logredact.TruncateLine(respBody, maxBytes))
				}
				c.Observe(ops.OpsUpstreamErrorEvent{
					Platform:           provider.Record.Platform,
					ProviderID:         provider.Record.ID,
					ProviderName:       provider.Record.Name,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  resp.Header.Get("x-request-id"),
					Kind:               "prompt_too_long",
					Message:            upstreamMsg,
					Detail:             upstreamDetail,
				})
				return true, &antigravity.PromptTooLongError{
					StatusCode: resp.StatusCode,

					RequestID: resp.Header.Get("x-request-id"),

					Body: respBody,
				}
			}

			s.handleUpstreamError(ctx, prefix, provider, resp.StatusCode, resp.Header, respBody, originalModel, 0, "", isStickySession)

			// Recognized project configuration status 400 returns
			// replay permission after health observation, leaving
			// the outer provider loop to retry or choose another
			// selected target.
			if resp.StatusCode == http.StatusBadRequest {
				msg := strings.ToLower(strings.TrimSpace(googlewire.ExtractPlatformMessage(respBody)))
				if upstream.IsGoogleProjectConfigError(msg) {
					upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(googlewire.ExtractPlatformMessage(respBody)))
					upstreamDetail := s.getUpstreamErrorDetail(respBody)
					logging.LegacyPrintf("gateway.antigravity", "%s status=400 google_config_error failover=true upstream_message=%q provider=%d", prefix, upstreamMsg, provider.Record.ID)
					c.Observe(ops.OpsUpstreamErrorEvent{
						Platform:           provider.Record.Platform,
						ProviderID:         provider.Record.ID,
						ProviderName:       provider.Record.Name,
						UpstreamStatusCode: resp.StatusCode,
						UpstreamRequestID:  resp.Header.Get("x-request-id"),
						Kind:               "failover",
						Message:            upstreamMsg,
						Detail:             upstreamDetail,
					})
					return true, &forwardcore.UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameProvider: true}
				}
			}

			if s.shouldFailoverUpstreamError(resp.StatusCode) {
				upstreamMsg := strings.TrimSpace(googlewire.ExtractPlatformMessage(respBody))
				upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
				upstreamDetail := s.getUpstreamErrorDetail(respBody)
				c.Observe(ops.OpsUpstreamErrorEvent{
					Platform:           provider.Record.Platform,
					ProviderID:         provider.Record.ID,
					ProviderName:       provider.Record.Name,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  resp.Header.Get("x-request-id"),
					Kind:               "failover",
					Message:            upstreamMsg,
					Detail:             upstreamDetail,
				})
				return true, &forwardcore.UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody}
			}

			return true, c.MappedClaudeError(provider, resp.StatusCode, resp.Header.Get("x-request-id"), respBody)
		}
		return false, nil
	}
	target.OutputError = func(err error) {
		kind := "stream_collect_error"
		if claudeReq.Stream {
			kind = "stream_error"
		}
		logging.LegacyPrintf("service.antigravity_gateway", "%s status=%s error=%v", prefix, kind, err)
	}
	result, err := (antigravity.Executor{}).Execute(ctx, upstream.AttemptInput{
		Protocol:      protocol.ProtocolAnthropicMessages,
		Body:          geminiBody,
		ResponseModel: originalModel,
		Stream:        claudeReq.Stream,
		Target:        target,
	}, c.Sink())
	if err != nil {
		return nil, err
	}
	return &forwardcore.MessagesResult{
		RequestID:             result.RequestID,
		UpstreamHeaders:       result.UpstreamHeaders,
		UpstreamResponseModel: result.UpstreamResponseModel,
		Usage:                 result.Usage,
		Model:                 originalModel,
		UpstreamModel:         billingModel,
		Stream:                claudeReq.Stream,
		Duration:              result.Duration,
		Timings:               result.Timings,
		ClientDisconnect:      result.ClientDisconnect,
	}, nil
}
