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
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

type antigravityCompatProtocol uint8

const (
	antigravityCompatChatCompletions antigravityCompatProtocol = iota
	antigravityCompatResponses
)

type antigravityCompatRequest struct {
	protocol          antigravityCompatProtocol
	originalBody      []byte
	claudeBody        []byte
	originalModel     string
	clientStream      bool
	includeUsage      bool
	startTime         time.Time
	reasoningEffort   *string
	clientToolMapping protocolbridge.ResponsesClientToolMapping
}

type antigravityCompatUpstreamCall struct {
	request      antigravityCompatRequest
	ctx          context.Context
	billingModel string
	prefix       string
	proxyURL     string
	accessToken  string
	geminiBody   []byte
}

// ForwardAsChatCompletions 使用 Antigravity 原生 OAuth 账号转发 Chat Completions 请求。
func (s *Antigravity) ForwardAsChatCompletions(
	ctx context.Context,
	output Output,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	_ *requeststate.ParsedRequest,
) (*protocolforward.MessagesResult, error) {
	c := &attempt{Output: output}

	if err := s.validateAntigravityCompatAccount(c, account); err != nil {
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
	antigravity.PreserveChatCompletionTokenLimit(&request, claudeRequest)
	claudeRequest.Stream = request.Stream
	claudeBody, err := json.Marshal(claudeRequest)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}
	claudeBody = prepareAntigravityCompatTools(c, claudeBody)

	return s.forwardAntigravityCompat(ctx, c, account, antigravityCompatRequest{

		protocol: antigravityCompatChatCompletions,

		originalBody: body,

		claudeBody: claudeBody,

		originalModel: request.Model,

		clientStream: request.Stream,

		includeUsage: request.StreamOptions != nil && request.StreamOptions.IncludeUsage,

		startTime: time.Now(),

		reasoningEffort: protocolforward.ExtractEffort(body, true, capability.NormalizeRecordedOpenAIEffortForModel),
	})
}

// ForwardAsResponses 使用 Antigravity 原生 OAuth 账号转发 Responses 请求。
func (s *Antigravity) ForwardAsResponses(
	ctx context.Context,
	output Output,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	_ *requeststate.ParsedRequest,
) (*protocolforward.MessagesResult, error) {
	c := &attempt{Output: output}

	if err := s.validateAntigravityCompatAccount(c, account); err != nil {
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

	return s.forwardAntigravityCompat(ctx, c, account, antigravityCompatRequest{

		protocol: antigravityCompatResponses,

		originalBody: body,

		claudeBody: claudeBody,

		originalModel: request.Model,

		clientStream: request.Stream,

		startTime: time.Now(),

		reasoningEffort: protocolforward.ExtractEffort(body, false, capability.NormalizeRecordedOpenAIEffortForModel),

		clientToolMapping: clientToolMapping,
	})
}

func (s *Antigravity) validateAntigravityCompatAccount(c *attempt, account *gatewayprovider.ExecutionAccount) error {
	if account != nil && account.Record.Platform == capability.PlatformAntigravity && account.Record.Type == capability.AccountTypeOAuth {
		return nil
	}
	return c.AntigravityCompatError(http.StatusBadRequest,
		"invalid_request_error",
		"native OAuth account required for antigravity compatibility mode",
	)
}

// prepareAntigravityCompatTools 保留 fork 的工具名混淆与缓存断点语义，并刷新回程映射。
func prepareAntigravityCompatTools(c *attempt, body []byte) []byte {
	rewrite := anthropic.BuildToolNameRewriteFromBody(body)
	if c != nil {
		// 每次尝试覆盖自己的工具映射，本次不改名时也不能保留先前映射。
		c.ToolNames = rewrite
	}
	return anthropic.ApplyToolNameRewriteToBody(body, rewrite)
}

func (s *Antigravity) forwardAntigravityCompat(
	ctx context.Context,
	c *attempt,
	account *gatewayprovider.ExecutionAccount,
	request antigravityCompatRequest,
) (*protocolforward.MessagesResult, error) {
	call, err := s.prepareAntigravityCompatCall(ctx, c, account, request)
	if err != nil {
		return nil, err
	}

	retry, params := s.antigravityRetryAdapter(antigravityRetryLoopParams{

		ctx: call.ctx,

		prefix: call.prefix,

		account: account,

		proxyURL: call.proxyURL,

		accessToken: call.accessToken,

		action: "streamGenerateContent",

		body: call.geminiBody,

		c: c,

		httpUpstream: s.Transport,

		accountRepo: s.Store,

		handleError: s.handleUpstreamError,

		requestedModel: request.originalModel,

		isStickySession: false,

		groupID: 0,

		sessionHash: "",
	})
	mode := antigravity.ModeChatResponse
	wireProtocol := protocol.ProtocolOpenAIChatCompletions
	if request.protocol == antigravityCompatResponses {
		mode = antigravity.ModeResponsesResponse
		wireProtocol = protocol.ProtocolOpenAIResponses
	}
	target := &antigravity.Target{
		AccountID:    account.Record.ID,
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
				return true, s.handleAntigravityCompatHTTPError(ctx, c, account, call, resp)
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
		RequestID:        result.RequestID,
		UpstreamHeaders:  result.UpstreamHeaders,
		Usage:            result.Usage,
		Model:            request.originalModel,
		UpstreamModel:    call.billingModel,
		Stream:           request.clientStream,
		Duration:         result.Duration,
		FirstTokenMs:     result.FirstTokenMs,
		ReasoningEffort:  request.reasoningEffort,
		ClientDisconnect: result.ClientDisconnect,
	}, nil
}

func (s *Antigravity) prepareAntigravityCompatCall(
	ctx context.Context,
	c *attempt,
	account *gatewayprovider.ExecutionAccount,
	request antigravityCompatRequest,
) (*antigravityCompatUpstreamCall, error) {
	var claudeRequest protocolanthropic.ClaudeRequest
	if json.Unmarshal(request.claudeBody, &claudeRequest) != nil {
		return nil, c.AntigravityCompatError(http.StatusBadRequest, "invalid_request_error", "Invalid request body")
	}

	thinkingEnabled := claudeRequest.Thinking != nil &&
		(claudeRequest.Thinking.Type == "enabled" || claudeRequest.Thinking.Type == "adaptive")
	modelCtx := requeststate.WithThinkingEnabled(ctx, thinkingEnabled)
	mappedModel := gatewayprovider.ExecutionModelPolicy(account).FinalAntigravityModel(modelCtx, request.originalModel)
	if mappedModel == "" {
		c.FeatureDenied()
		message := fmt.Sprintf("model %s not in whitelist", request.originalModel)
		return nil, c.AntigravityCompatError(http.StatusForbidden, "permission_error", message)
	}
	if s.Tokens == nil {
		return nil, c.AntigravityCompatError(http.StatusBadGateway, "api_error", "Antigravity token provider not configured")
	}
	accessToken, err := gatewayprovider.ExecutionToken(ctx, s.Tokens, account)
	if err != nil {
		return nil, &protocolforward.UpstreamFailoverError{

			StatusCode: http.StatusBadGateway,

			ResponseBody: []byte(`{"error":{"type":"authentication_error","message":"Failed to get upstream access token"},"type":"error"}`),
		}
	}

	projectID, err := resolveAntigravityProjectID(account)
	if err != nil {
		_ = c.AntigravityCompatError(http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}
	geminiBody, err := s.buildAntigravityCompatGeminiBody(modelCtx, request.claudeBody, &claudeRequest, projectID, mappedModel)
	if err != nil {
		return nil, c.AntigravityCompatError(http.StatusBadRequest, "invalid_request_error", "Invalid request")
	}

	request.reasoningEffort = gatewayprovider.ApplyThinkingEnabledFallback(request.reasoningEffort, request.originalBody, mappedModel)
	return &antigravityCompatUpstreamCall{

		request: request,

		ctx: modelCtx,

		billingModel: mappedModel,

		prefix: logPrefix(c.GetHeader("session_id"), account.Record.Name),

		proxyURL: antigravityCompatProxyURL(account),

		accessToken: accessToken,

		geminiBody: geminiBody,
	}, nil
}

func (s *Antigravity) buildAntigravityCompatGeminiBody(
	ctx context.Context,
	claudeBody []byte,
	claudeRequest *protocolanthropic.ClaudeRequest,
	projectID string,
	mappedModel string,
) ([]byte, error) {
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
		if cleaned, cleanErr := antigravity.CleanGeminiRequest(body); cleanErr == nil {
			body = cleaned
		}
		return antigravity.WrapV1InternalRequest(projectID, mappedModel, body)
	}

	options := s.getClaudeTransformOptions(ctx)
	options.EnableIdentityPatch = true
	return antigravity.TransformClaudeToGeminiWithOptions(claudeRequest, projectID, mappedModel, options)
}

func antigravityCompatProxyURL(account *gatewayprovider.ExecutionAccount) string {
	if account.Record.ProxyID == nil || account.Record.Proxy == nil {
		return ""
	}
	return account.Record.Proxy.URL()
}

func (s *Antigravity) handleAntigravityCompatTransportError(c *attempt, err error) error {
	if switchErr, ok := antigravity.IsAntigravityAccountSwitchError(err); ok {
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

func (s *Antigravity) handleAntigravityCompatHTTPError(
	ctx context.Context,
	c *attempt,
	account *gatewayprovider.ExecutionAccount,
	call *antigravityCompatUpstreamCall,
	resp *http.Response,
) error {
	body := s.readUpstreamErrorBody(resp)
	s.handleUpstreamError(
		ctx,
		call.prefix,
		account,
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

			Platform: account.Record.Platform,

			AccountID: account.Record.ID,

			AccountName: account.Record.Name,

			UpstreamStatusCode: resp.StatusCode,

			UpstreamRequestID: resp.Header.Get("x-request-id"),

			Kind: "failover",

			Message: message,

			Detail: s.getUpstreamErrorDetail(body),
		}
		if resp.StatusCode == http.StatusUnauthorized {
			event.Stage = string(protocolforward.GatewayFailureStageAccountAuth)
			event.Scope = string(protocolforward.GatewayFailureScopeAccount)
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
	return c.MappedAntigravityCompatError(account, resp.StatusCode, resp.Header.Get("x-request-id"), body)
}

func antigravityCredentialRejectedError(resp *http.Response, body []byte) *protocolforward.UpstreamFailoverError {
	return &protocolforward.UpstreamFailoverError{

		StatusCode: resp.StatusCode,

		ResponseBody: body,

		ResponseHeaders: resp.Header.Clone(),

		Stage: protocolforward.GatewayFailureStageAccountAuth,

		Scope: protocolforward.GatewayFailureScopeAccount,

		Reason:            protocolforward.AntigravityCredentialRejectedReason,
		NextAccountAction: protocolforward.NextAccountRetry,
		ClientStatusCode:  http.StatusBadGateway,

		ClientMessage: protocolforward.AntigravityCredentialRejectedClientMessage,
	}
}
