package googleforward

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	protocolforward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// antigravity_openai.go converts Chat and Responses requests for Antigravity
// and restores client output identities.
// Antigravity OpenAI compatibility retains original client bytes beside an
// intermediate Anthropic request. Final model family chooses native Gemini
// conversion or Claude transformation, and final-route attributes read the
// original controls when that representation is supplied.
//
// Chat preserves its positive token limit after bridge conversion, while
// Responses keeps a client tool restoration map for namespaces. Every attempt
// replaces its tool rewrite map before response handling. Both client protocols
// use the streaming v1internal action with synchronous output adaptation. HTTP
// authorization failure carries a provider-scoped credential reason and safe
// client status in the gateway failover error; the prepared call retains a
// request copy separately from the entry request used for returned effort
// metadata.

// These modes select the Antigravity response adapter and client wire protocol
// used after compatible request conversion and exchange.
const (
	antigravityCompatChatCompletions antigravityCompatProtocol = iota // Chat completion response mode
	antigravityCompatResponses                                        // OpenAI Responses output mode
)

// antigravityModelAttributes supplies optional final-route attributes and
// original request controls.
type antigravityModelAttributes struct {
	// Resolution is optional; absent input leaves capability application to
	// the selected conversion defaults used by the platform body builder.
	Resolution *modelrules.Resolution // final route attribute resolution
	// Controls defaults to the Anthropic body when nil; a supplied slice
	// retains the original client protocol controls for final attribute
	// application.
	Controls []byte // original client control bytes
}

// antigravityCompatProtocol identifies the client format restored after
// Antigravity execution.
type antigravityCompatProtocol uint8

// antigravityCompatRequest retains client identity, intermediate input, and
// response restoration choices.
type antigravityCompatRequest struct {
	// originalBody supplies client control syntax while claudeBody carries
	// intermediate Anthropic input. originalModel and clientStream retain
	// client identity and mode as the selected route builds different
	// upstream bytes.
	//
	// selected client output protocol
	protocol      antigravityCompatProtocol
	originalBody  []byte    // original client request bytes
	claudeBody    []byte    // converted Anthropic request bytes
	originalModel string    // logical client model identifier
	clientStream  bool      // client streaming response flag
	includeUsage  bool      // Chat usage chunk permission
	startTime     time.Time // compatibility attempt start time
	// reasoningEffort is nil when client extraction finds no value;
	// preparation can replace this pointer in its copied request before
	// building the call.
	reasoningEffort *string // recorded client effort value
	// clientToolMapping accompanies Responses output; synchronous conversion
	// uses it to restore flattened client tool names and namespaces while the
	// executor emits protocol events.
	//
	// Responses tool restoration mapping
	clientToolMapping protocolbridge.ResponsesClientToolMapping
}

// antigravityCompatUpstreamCall holds prepared credentials and body for one
// provider exchange.
type antigravityCompatUpstreamCall struct {
	// request is a value copy with any prepared effort fallback; its other
	// reference-valued members remain shared with the entry request during
	// execution.
	//
	// prepared compatibility request value
	request antigravityCompatRequest
	// thinking-qualified request execution context
	ctx          context.Context
	billingModel string // selected final upstream model
	prefix       string // provider attempt log prefix
	proxyURL     string // selected transport proxy URL
	accessToken  string // resolved OAuth access token
	geminiBody   []byte // prepared v1internal request bytes
}

// ForwardAsChatCompletions converts one Chat body into a selected Antigravity
// OAuth attempt. It validates provider kind and input model before creating
// intermediate Responses and Anthropic forms.
//
// The positive client token limit is reapplied after bridge conversion, and
// tool rewriting records the response map for this attempt. Original client
// bytes, model, stream mode, and include_usage choice accompany execution. The
// parsed-request parameter is unused because this path derives its preparation
// state from the supplied body.
func (s *Antigravity) ForwardAsChatCompletions(
	ctx context.Context,
	output Output,
	provider *gatewayprovider.ExecutionProvider,
	body []byte,
	_ *requeststate.ParsedRequest,
) (*protocolforward.MessagesResult, error) {
	ctx = requeststate.WithClientProtocol(ctx, protocol.ProtocolOpenAIChatCompletions)
	c := &attempt{Output: output}

	if err := s.validateAntigravityCompatProvider(c, provider); err != nil {
		return nil, err
	}

	var request protocolopenai.ChatCompletionsRequest
	if json.Unmarshal(body, &request) != nil {
		return nil, c.AntigravityCompatError(http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
	}
	if strings.TrimSpace(request.Model) == "" {
		return nil, c.AntigravityCompatError(http.StatusBadRequest, "invalid_request_error", "model is required")
	}

	responsesRequest, err := protocolbridge.ChatCompletionsToResponses(&request, protocolforward.ConversionOptionsForModel(request.Model))
	if err != nil {
		return nil, c.AntigravityCompatError(http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	claudeRequest, err := protocolbridge.ResponsesToAnthropicRequest(responsesRequest)
	if err != nil {
		return nil, c.AntigravityCompatError(http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	// Reapply the positive Chat completion limit after bridge conversion so
	// its precedence and platform ceiling remain the upstream max-token
	// input, including small requested limits.
	antigravity.PreserveChatCompletionTokenLimit(&request, claudeRequest)
	claudeRequest.Stream = request.Stream
	claudeBody, err := json.Marshal(claudeRequest)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}
	claudeBody = prepareAntigravityCompatTools(c, claudeBody)

	return s.forwardAntigravityCompat(ctx, c, provider, antigravityCompatRequest{
		protocol:        antigravityCompatChatCompletions,
		originalBody:    body,
		claudeBody:      claudeBody,
		originalModel:   request.Model,
		clientStream:    request.Stream,
		includeUsage:    request.StreamOptions != nil && request.StreamOptions.IncludeUsage,
		startTime:       time.Now(),
		reasoningEffort: protocolforward.ExtractEffort(body, true, capability.NormalizeRecordedOpenAIEffortForModel),
	})
}

// ForwardAsResponses converts one Responses body into a selected Antigravity
// OAuth attempt. It adapts client tools before typed conversion and retains
// that mapping for namespace restoration.
//
// Original client bytes remain alongside the Anthropic body for final-model
// control resolution. Model and stream choice remain logical client metadata as
// the mapped provider route prepares the upstream request. The parsed-request
// parameter is unused; execution and response handling use the supplied body
// and fresh attempt state.
func (s *Antigravity) ForwardAsResponses(
	ctx context.Context,
	output Output,
	provider *gatewayprovider.ExecutionProvider,
	body []byte,
	_ *requeststate.ParsedRequest,
) (*protocolforward.MessagesResult, error) {
	ctx = requeststate.WithClientProtocol(ctx, protocol.ProtocolOpenAIResponses)
	c := &attempt{Output: output}

	if err := s.validateAntigravityCompatProvider(c, provider); err != nil {
		return nil, err
	}

	adaptedBody, clientToolMapping, err := protocolforward.AdaptResponsesClientToolsForAnthropic(body)
	if err != nil {
		return nil, c.AntigravityCompatError(http.StatusBadRequest, "invalid_request_error", err.Error())
	}

	var request protocolopenai.ResponsesRequest
	if json.Unmarshal(adaptedBody, &request) != nil {
		return nil, c.AntigravityCompatError(http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
	}
	if strings.TrimSpace(request.Model) == "" {
		return nil, c.AntigravityCompatError(http.StatusBadRequest, "invalid_request_error", "model is required")
	}

	claudeRequest, err := protocolbridge.ResponsesToAnthropicRequest(&request)
	if err != nil {
		return nil, c.AntigravityCompatError(http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	claudeRequest.Stream = request.Stream
	claudeBody, err := json.Marshal(claudeRequest)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}
	claudeBody = prepareAntigravityCompatTools(c, claudeBody)

	return s.forwardAntigravityCompat(ctx, c, provider, antigravityCompatRequest{
		protocol:          antigravityCompatResponses,
		originalBody:      body,
		claudeBody:        claudeBody,
		originalModel:     request.Model,
		clientStream:      request.Stream,
		startTime:         time.Now(),
		reasoningEffort:   protocolforward.ExtractEffort(body, false, capability.NormalizeRecordedOpenAIEffortForModel),
		clientToolMapping: clientToolMapping,
	})
}

// validateAntigravityCompatProvider admits a nonnil Antigravity OAuth target
// before conversion. Other targets receive a local compatible status-400 error
// through the current output adapter, ending this preparation path.
func (s *Antigravity) validateAntigravityCompatProvider(c *attempt, provider *gatewayprovider.ExecutionProvider) error {
	if provider != nil && provider.Record.Platform == capability.PlatformAntigravity && provider.Record.Type == capability.ProviderTypeOAuth {
		return nil
	}
	return c.AntigravityCompatError(http.StatusBadRequest,
		"invalid_request_error",
		"native OAuth provider required for antigravity compatibility mode",
	)
}

// prepareAntigravityCompatTools rewrites client tool names and replaces the
// attempt response map. A nil attempt still returns delegated rewritten bytes;
// a nil rewrite clears any map retained by a supplied attempt.
func prepareAntigravityCompatTools(c *attempt, body []byte) []byte {
	rewrite := anthropic.BuildToolNameRewriteFromBody(body)
	if c != nil {
		// Assign the current rewrite result even when it is nil so a
		// reused attempt output cannot restore names with a mapping
		// from an earlier body.
		c.ToolNames = rewrite
	}
	return anthropic.ApplyToolNameRewriteToBody(body, rewrite)
}

// forwardAntigravityCompat executes the prepared call using the requested Chat
// or Responses output mode. The executor owns final response closure; client
// identity and mode come from the entry request, while the prepared call
// supplies the thinking context, credential, upstream body, and final model.
//
// Successful result effort comes from this function's request parameter.
// prepareAntigravityCompatCall stores its effort fallback in call.request, a
// separate value copy, so assigning that fallback does not update the effort
// pointer used here. Error observation reads call.request's original model,
// while completion retains the entry model beside the final upstream model.
func (s *Antigravity) forwardAntigravityCompat(
	ctx context.Context,
	c *attempt,
	provider *gatewayprovider.ExecutionProvider,
	request antigravityCompatRequest,
) (*protocolforward.MessagesResult, error) {
	call, err := s.prepareAntigravityCompatCall(ctx, c, provider, request)
	if err != nil {
		return nil, err
	}

	retry, params := s.antigravityRetryAdapter(antigravityRetryLoopParams{
		ctx:             call.ctx,
		prefix:          call.prefix,
		provider:        provider,
		proxyURL:        call.proxyURL,
		accessToken:     call.accessToken,
		action:          "streamGenerateContent",
		body:            call.geminiBody,
		c:               c,
		httpUpstream:    s.Transport,
		providerRepo:    s.Store,
		handleError:     s.handleUpstreamError,
		requestedModel:  request.originalModel,
		isStickySession: false,
		groupID:         0,
		sessionHash:     "",
	})
	mode := antigravity.ModeChatResponse
	wireProtocol := protocol.ProtocolOpenAIChatCompletions
	if request.protocol == antigravityCompatResponses {
		mode = antigravity.ModeResponsesResponse
		wireProtocol = protocol.ProtocolOpenAIResponses
	}
	target := &antigravity.Target{
		ProviderID:   provider.Record.ID,
		Model:        call.billingModel,
		Mode:         mode,
		StartedAt:    request.startTime,
		IncludeUsage: request.includeUsage,
		ClientTools:  request.clientToolMapping,
		Response:     s.antigravityResponseAdapter(c).Options,
		Enter:        s.Enter,

		Exchange: func(context.Context) (*http.Response, error) {
			result, err := retry.AntigravityRetryLoop(params)
			if err != nil {
				return nil, s.handleAntigravityCompatTransportError(c, err)
			}
			return result.Resp, nil
		},

		BeforeResponse: func(ctx context.Context, resp *http.Response) (bool, error) {
			if resp.StatusCode >= http.StatusBadRequest {
				return true, s.handleAntigravityCompatHTTPError(ctx, c, provider, call, resp)
			}
			return false, nil
		},
	}
	result, err := (antigravity.Executor{}).Execute(call.ctx, upstream.AttemptInput{
		Protocol:      wireProtocol,
		Body:          call.geminiBody,
		ResponseModel: request.originalModel,
		Stream:        request.clientStream,
		Target:        target,
	}, c.Sink())
	if err != nil {
		return nil, err
	}
	return &protocolforward.MessagesResult{
		RequestID:             result.RequestID,
		UpstreamHeaders:       result.UpstreamHeaders,
		UpstreamResponseModel: result.UpstreamResponseModel,
		Usage:                 result.Usage,
		Model:                 request.originalModel,
		UpstreamModel:         call.billingModel,
		Stream:                request.clientStream,
		Duration:              result.Duration,
		Timings:               result.Timings,
		ReasoningEffort:       request.reasoningEffort,
		ClientDisconnect:      result.ClientDisconnect,
	}, nil
}

// prepareAntigravityCompatCall resolves a thinking-qualified final model and
// prepares selected-provider credentials and bytes. Missing whitelist
// admission, token service, or project data returns the corresponding local or
// failover error.
//
// Original client control bytes accompany the intermediate Claude request when
// final attributes are applied. The returned call stores a request copy
// containing any prepared effort fallback, alongside the thinking context and
// selected billing model. The caller retains its separate entry request value,
// including the effort used by its result assembly.
func (s *Antigravity) prepareAntigravityCompatCall(
	ctx context.Context,
	c *attempt,
	provider *gatewayprovider.ExecutionProvider,
	request antigravityCompatRequest,
) (*antigravityCompatUpstreamCall, error) {
	var claudeRequest protocolanthropic.ClaudeRequest
	if json.Unmarshal(request.claudeBody, &claudeRequest) != nil {
		return nil, c.AntigravityCompatError(http.StatusBadRequest, "invalid_request_error", "Invalid request body")
	}

	thinkingEnabled := claudeRequest.Thinking != nil &&
		(claudeRequest.Thinking.Type == "enabled" || claudeRequest.Thinking.Type == "adaptive")
	modelCtx := requeststate.WithThinkingEnabled(ctx, thinkingEnabled)
	mappedModel := gatewayprovider.ExecutionModelPolicy(provider).FinalAntigravityModel(modelCtx, request.originalModel)
	if mappedModel == "" {
		c.FeatureDenied()
		message := fmt.Sprintf("model %s not in whitelist", request.originalModel)
		return nil, c.AntigravityCompatError(http.StatusForbidden, "permission_error", message)
	}
	if s.Tokens == nil {
		return nil, c.AntigravityCompatError(http.StatusBadGateway, "api_error", "Antigravity token provider not configured")
	}
	accessToken, err := gatewayprovider.ExecutionToken(ctx, s.Tokens, provider)
	if err != nil {
		return nil, &protocolforward.UpstreamFailoverError{
			StatusCode: http.StatusBadGateway,

			ResponseBody: []byte(`{"error":{"type":"authentication_error","message":"Failed to get upstream access token"},"type":"error"}`),
		}
	}

	projectID, err := resolveAntigravityProjectID(provider)
	if err != nil {
		_ = c.AntigravityCompatError(http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}
	attributes, attributesErr := gatewayprovider.ResolveModelAttributes(modelCtx, provider, mappedModel, s.Attributes, "gemini_generate_content")
	if attributesErr != nil {
		return nil, attributesErr
	}
	geminiBody, err := s.buildAntigravityCompatGeminiBody(modelCtx, request.claudeBody, &claudeRequest, projectID, mappedModel, antigravityModelAttributes{Resolution: &attributes, Controls: request.originalBody})
	if err != nil {
		return nil, c.AntigravityCompatError(http.StatusBadRequest, "invalid_request_error", "Invalid request")
	}
	if gatewayprovider.ExecutionModelPolicy(provider).ShouldStripWireEffort(modelCtx, request.originalModel) {
		geminiBody = requeststate.StripWireReasoningEffort(geminiBody, protocol.ProtocolGeminiGenerateContent)
	}

	request.reasoningEffort = gatewayprovider.ApplyThinkingEnabledFallback(request.reasoningEffort, request.originalBody, mappedModel)
	return &antigravityCompatUpstreamCall{
		request:      request,
		ctx:          modelCtx,
		billingModel: mappedModel,
		prefix:       logPrefix(c.GetHeader("session_id"), provider.Record.Name),
		proxyURL:     antigravityCompatProxyURL(provider),
		accessToken:  accessToken,
		geminiBody:   geminiBody,
	}, nil
}

// buildAntigravityCompatGeminiBody chooses native Gemini or Claude
// transformation from the final mapped model family. The first optional
// attribute input supplies resolution and controls, with Claude bytes as the
// nil-control default.
//
// Gemini input receives mixed-tool compatibility, signature completion,
// identity injection, and accepted schema cleanup before final attribute
// application and v1internal wrapping. Claude-family input uses transform
// options with identity enabled and resolved thinking when supplied. These
// branches retain the selected project/model envelope while applying controls
// from the input representation chosen by the caller.
func (s *Antigravity) buildAntigravityCompatGeminiBody(
	ctx context.Context,
	claudeBody []byte,
	claudeRequest *protocolanthropic.ClaudeRequest,
	projectID string,
	mappedModel string,
	inputs ...antigravityModelAttributes,
) ([]byte, error) {
	var resolved *modelrules.Resolution
	controls := claudeBody
	if len(inputs) > 0 {
		resolved = inputs[0].Resolution
		if inputs[0].Controls != nil {
			controls = inputs[0].Controls
		}
	}

	if strings.HasPrefix(strings.ToLower(mappedModel), "gemini-") {
		body, err := convertClaudeMessagesToGeminiGenerateContent(claudeBody)
		if err != nil {
			return nil, err
		}
		body, err = antigravity.EnableMixedGeminiToolInvocations(body)
		if err != nil {
			return nil, err
		}
		body = ensureGeminiFunctionCallThoughtSignatures(body)
		body, err = antigravity.InjectIdentityPatchToGeminiRequest(body)
		if err != nil {
			return nil, err
		}
		// Accept schema cleanup when it succeeds; a cleanup error
		// leaves the current converted bytes available for attribute
		// application and upstream request validation.
		if cleaned, cleanErr := antigravity.CleanGeminiRequest(body); cleanErr == nil {
			body = cleaned
		}
		if resolved != nil {
			body, err = applyGeminiModelParameters(body, controls, *resolved, true)
			if err != nil {
				return nil, err
			}
		}

		return antigravity.WrapV1InternalRequest(projectID, mappedModel, body)
	}

	options := s.getClaudeTransformOptions(ctx)
	if resolved != nil {
		options.Capabilities = resolved
		var err error
		options.Thinking, err = gatewayprovider.GeminiThinking(*resolved, controls)
		if err != nil {
			return nil, err
		}
		options.ThinkingResolved = true
	}
	options.EnableIdentityPatch = true
	return antigravity.TransformClaudeToGeminiWithOptions(claudeRequest, projectID, mappedModel, options)
}

// antigravityCompatProxyURL returns the attached provider proxy URL when both
// references exist. Otherwise it returns empty for direct transport; callers
// provide a selected provider before preparing the compatible call.
func antigravityCompatProxyURL(provider *gatewayprovider.ExecutionProvider) string {
	if provider.Record.ProxyID == nil || provider.Record.Proxy == nil {
		return ""
	}
	return provider.Record.Proxy.URL()
}

// handleAntigravityCompatTransportError translates provider-switch signals with
// their sticky billing hint. Other retry failures use inbound request
// cancellation to select client-disconnected or exhausted-upstream compatible
// error presentation.
func (s *Antigravity) handleAntigravityCompatTransportError(c *attempt, err error) error {
	if switchErr, ok := antigravity.IsAntigravityProviderSwitchError(err); ok {
		return &protocolforward.UpstreamFailoverError{
			StatusCode: http.StatusServiceUnavailable,

			ForceCacheBilling: switchErr.IsStickySession,
		}
	}
	if c.RequestContext().Err() != nil {
		return c.AntigravityCompatError(http.StatusBadGateway, "client_disconnected", "Client disconnected before upstream response")
	}
	return c.AntigravityCompatError(http.StatusBadGateway, "upstream_error", "Upstream request failed after retries")
}

// handleAntigravityCompatHTTPError observes provider health before selecting
// failover or compatible presentation. Qualifying status 401 supplies
// credential-failure metadata and safe client output; other qualifying statuses
// retain cloned response headers in failover.
func (s *Antigravity) handleAntigravityCompatHTTPError(
	ctx context.Context,
	c *attempt,
	provider *gatewayprovider.ExecutionProvider,
	call *antigravityCompatUpstreamCall,
	resp *http.Response,
) error {
	body := s.readUpstreamErrorBody(resp)
	s.handleUpstreamError(
		ctx,
		call.prefix,
		provider,
		resp.StatusCode,
		resp.Header,
		body,
		call.request.originalModel,
		0,
		"",
		false,
	)
	if s.shouldFailoverUpstreamError(resp.StatusCode) {
		message := logredact.SanitizeUpstreamQueries(strings.TrimSpace(google.ExtractPlatformMessage(body)))
		event := ops.OpsUpstreamErrorEvent{
			Platform:           provider.Record.Platform,
			ProviderID:         provider.Record.ID,
			ProviderName:       provider.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "failover",
			Message:            message,
			Detail:             s.getUpstreamErrorDetail(body),
		}
		if resp.StatusCode == http.StatusUnauthorized {
			event.Stage = string(protocolforward.GatewayFailureStageProviderAuth)
			event.Scope = string(protocolforward.GatewayFailureScopeProvider)
			event.Reason = string(protocolforward.AntigravityCredentialRejectedReason)
			c.Observe(event)
			return antigravityCredentialRejectedError(resp, body)
		}
		c.Observe(event)
		return &protocolforward.UpstreamFailoverError{
			StatusCode: resp.StatusCode,

			ResponseBody: body,

			ResponseHeaders: resp.Header.Clone(),
		}
	}
	return c.MappedAntigravityCompatError(provider, resp.StatusCode, resp.Header.Get("x-request-id"), body)
}

// antigravityCredentialRejectedError returns provider-auth failure metadata
// with cloned headers and shared diagnostic bytes. Outer handling receives
// next-provider replay, safe status 502, and the credential-rejection client
// message instead of raw authorization text.
func antigravityCredentialRejectedError(resp *http.Response, body []byte) *protocolforward.UpstreamFailoverError {
	return &protocolforward.UpstreamFailoverError{
		StatusCode:         resp.StatusCode,
		ResponseBody:       body,
		ResponseHeaders:    resp.Header.Clone(),
		Stage:              protocolforward.GatewayFailureStageProviderAuth,
		Scope:              protocolforward.GatewayFailureScopeProvider,
		Reason:             protocolforward.AntigravityCredentialRejectedReason,
		NextProviderAction: protocolforward.NextProviderRetry,
		ClientStatusCode:   http.StatusBadGateway,
		ClientMessage:      protocolforward.AntigravityCredentialRejectedClientMessage,
	}
}
