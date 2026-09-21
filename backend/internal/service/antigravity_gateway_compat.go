package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	protocolforward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"

	"github.com/gin-gonic/gin"
)

type antigravityCompatProtocol uint8

const (
	antigravityCompatChatCompletions antigravityCompatProtocol = iota
	antigravityCompatResponses
)

const (
	// AntigravityCredentialRejectedClientMessage 是可安全返回给客户端的认证修复提示。
	AntigravityCredentialRejectedClientMessage = "Antigravity rejected the OAuth credential after refresh; reauthorize the account and verify project_id"
	// AntigravityCredentialRejectedReason 标识上游拒绝已刷新 OAuth 凭据。
	AntigravityCredentialRejectedReason protocolforward.GatewayFailureReason = "antigravity_oauth_credential_rejected"
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
func (s *AntigravityGatewayService) ForwardAsChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	_ *requeststate.ParsedRequest,
) (*protocolforward.MessagesResult, error) {
	if err := s.validateAntigravityCompatAccount(c, account); err != nil {
		return nil, err
	}

	var request protocolopenai.ChatCompletionsRequest
	if json.Unmarshal(body, &request) != nil {
		return nil, s.writeAntigravityCompatError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
	}
	if strings.TrimSpace(request.Model) == "" {
		return nil, s.writeAntigravityCompatError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
	}

	responsesRequest, err := protocolbridge.ChatCompletionsToResponses(&request, protocolforward.ConversionOptionsForModel(request.Model))
	if err != nil {
		return nil, s.writeAntigravityCompatError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	claudeRequest, err := protocolbridge.ResponsesToAnthropicRequest(responsesRequest)
	if err != nil {
		return nil, s.writeAntigravityCompatError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	antigravity.PreserveChatCompletionTokenLimit(&request, claudeRequest)
	claudeRequest.Stream = request.Stream
	claudeBody, err := json.Marshal(claudeRequest)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}
	claudeBody = prepareAntigravityCompatTools(c, claudeBody)

	return s.forwardAntigravityCompat(ctx, c, account, antigravityCompatRequest{
		protocol:        antigravityCompatChatCompletions,
		originalBody:    body,
		claudeBody:      claudeBody,
		originalModel:   request.Model,
		clientStream:    request.Stream,
		includeUsage:    request.StreamOptions != nil && request.StreamOptions.IncludeUsage,
		startTime:       time.Now(),
		reasoningEffort: extractCCReasoningEffortFromBody(body),
	})
}

// ForwardAsResponses 使用 Antigravity 原生 OAuth 账号转发 Responses 请求。
func (s *AntigravityGatewayService) ForwardAsResponses(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	_ *requeststate.ParsedRequest,
) (*protocolforward.MessagesResult, error) {
	if err := s.validateAntigravityCompatAccount(c, account); err != nil {
		return nil, err
	}

	adaptedBody, clientToolMapping, err := protocolforward.AdaptResponsesClientToolsForAnthropic(body)
	if err != nil {
		return nil, s.writeAntigravityCompatError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}

	var request protocolopenai.ResponsesRequest
	if json.Unmarshal(adaptedBody, &request) != nil {
		return nil, s.writeAntigravityCompatError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
	}
	if strings.TrimSpace(request.Model) == "" {
		return nil, s.writeAntigravityCompatError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
	}

	claudeRequest, err := protocolbridge.ResponsesToAnthropicRequest(&request)
	if err != nil {
		return nil, s.writeAntigravityCompatError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	claudeRequest.Stream = request.Stream
	claudeBody, err := json.Marshal(claudeRequest)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}
	claudeBody = prepareAntigravityCompatTools(c, claudeBody)

	return s.forwardAntigravityCompat(ctx, c, account, antigravityCompatRequest{
		protocol:          antigravityCompatResponses,
		originalBody:      body,
		claudeBody:        claudeBody,
		originalModel:     request.Model,
		clientStream:      request.Stream,
		startTime:         time.Now(),
		reasoningEffort:   ExtractResponsesReasoningEffortFromBody(body),
		clientToolMapping: clientToolMapping,
	})
}

func (s *AntigravityGatewayService) validateAntigravityCompatAccount(c *gin.Context, account *Account) error {
	if account != nil && account.Platform == capability.PlatformAntigravity && account.Type == capability.AccountTypeOAuth {
		return nil
	}
	return s.writeAntigravityCompatError(
		c,
		http.StatusBadRequest,
		"invalid_request_error",
		"native OAuth account required for antigravity compatibility mode",
	)
}

// prepareAntigravityCompatTools 保留 fork 的工具名混淆与缓存断点语义，并刷新回程映射。
func prepareAntigravityCompatTools(c *gin.Context, body []byte) []byte {
	rewrite := anthropic.BuildToolNameRewriteFromBody(body)
	if c != nil {
		// failover 可能复用同一个 gin.Context，因此即使本次不改名也要清除旧映射。
		c.Set(toolNameRewriteKey, rewrite)
	}
	return anthropic.ApplyToolNameRewriteToBody(body, rewrite)
}

func (s *AntigravityGatewayService) forwardAntigravityCompat(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	request antigravityCompatRequest,
) (*protocolforward.MessagesResult, error) {
	call, err := s.prepareAntigravityCompatCall(ctx, c, account, request)
	if err != nil {
		return nil, err
	}

	retry, params := s.antigravityRetryAdapter(antigravityRetryLoopParams{
		ctx:             call.ctx,
		prefix:          call.prefix,
		account:         account,
		proxyURL:        call.proxyURL,
		accessToken:     call.accessToken,
		action:          "streamGenerateContent",
		body:            call.geminiBody,
		c:               c,
		httpUpstream:    s.httpUpstream,
		settingService:  s.settingService,
		accountRepo:     s.accountRepo,
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
	target := &antigravity.Target{AccountID: account.ID, Model: call.billingModel, Mode: mode, StartedAt: request.startTime, IncludeUsage: request.includeUsage, ClientTools: request.clientToolMapping, Response: s.antigravityResponseAdapter(c).Options, Enter: s.nativeAttemptActivity,
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
	result, err := (antigravity.Executor{}).Execute(call.ctx, upstream.AttemptInput{Protocol: wireProtocol, Body: call.geminiBody, ResponseModel: request.originalModel, Stream: request.clientStream, Target: target}, gatewayhttp.ResponseSink{Writer: c.Writer})
	if err != nil {
		return nil, err
	}
	return &protocolforward.MessagesResult{RequestID: result.RequestID, UpstreamHeaders: result.UpstreamHeaders, Usage: result.Usage, Model: request.originalModel, UpstreamModel: call.billingModel, Stream: request.clientStream, Duration: result.Duration, FirstTokenMs: result.FirstTokenMs, ReasoningEffort: request.reasoningEffort, ClientDisconnect: result.ClientDisconnect}, nil
}

func (s *AntigravityGatewayService) prepareAntigravityCompatCall(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	request antigravityCompatRequest,
) (*antigravityCompatUpstreamCall, error) {
	var claudeRequest protocolanthropic.ClaudeRequest
	if json.Unmarshal(request.claudeBody, &claudeRequest) != nil {
		return nil, s.writeAntigravityCompatError(c, http.StatusBadRequest, "invalid_request_error", "Invalid request body")
	}

	thinkingEnabled := claudeRequest.Thinking != nil &&
		(claudeRequest.Thinking.Type == "enabled" || claudeRequest.Thinking.Type == "adaptive")
	modelCtx := requeststate.WithThinkingEnabled(ctx, thinkingEnabled)
	mappedModel := resolveFinalAntigravityModelKey(modelCtx, account, request.originalModel)
	if mappedModel == "" {
		gatewayhttp.MarkOpsClientBusinessLimited(c, gatewayhttp.OpsClientBusinessLimitedReasonLocalFeatureGate)
		message := fmt.Sprintf("model %s not in whitelist", request.originalModel)
		return nil, s.writeAntigravityCompatError(c, http.StatusForbidden, "permission_error", message)
	}
	if s.tokenProvider == nil {
		return nil, s.writeAntigravityCompatError(c, http.StatusBadGateway, "api_error", "Antigravity token provider not configured")
	}
	accessToken, err := accountToken(ctx, s.tokenProvider, account)
	if err != nil {
		return nil, &protocolforward.UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: []byte(`{"error":{"type":"authentication_error","message":"Failed to get upstream access token"},"type":"error"}`),
		}
	}

	projectID, err := resolveAntigravityProjectID(account)
	if err != nil {
		_ = s.writeAntigravityCompatError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}
	geminiBody, err := s.buildAntigravityCompatGeminiBody(modelCtx, request.claudeBody, &claudeRequest, projectID, mappedModel)
	if err != nil {
		return nil, s.writeAntigravityCompatError(c, http.StatusBadRequest, "invalid_request_error", "Invalid request")
	}

	request.reasoningEffort = ApplyThinkingEnabledFallback(request.reasoningEffort, request.originalBody, mappedModel)
	return &antigravityCompatUpstreamCall{
		request:      request,
		ctx:          modelCtx,
		billingModel: mappedModel,
		prefix:       logPrefix(getSessionID(c), account.Name),
		proxyURL:     antigravityCompatProxyURL(account),
		accessToken:  accessToken,
		geminiBody:   geminiBody,
	}, nil
}

func (s *AntigravityGatewayService) buildAntigravityCompatGeminiBody(
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

func antigravityCompatProxyURL(account *Account) string {
	if account.ProxyID == nil || account.Proxy == nil {
		return ""
	}
	return account.Proxy.URL()
}

func (s *AntigravityGatewayService) handleAntigravityCompatTransportError(c *gin.Context, err error) error {
	if switchErr, ok := antigravity.IsAntigravityAccountSwitchError(err); ok {
		return &protocolforward.UpstreamFailoverError{
			StatusCode:        http.StatusServiceUnavailable,
			ForceCacheBilling: switchErr.IsStickySession,
		}
	}
	if c.Request.Context().Err() != nil {
		return s.writeAntigravityCompatError(c, http.StatusBadGateway, "client_disconnected", "Client disconnected before upstream response")
	}
	return s.writeAntigravityCompatError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed after retries")
}

func (s *AntigravityGatewayService) handleAntigravityCompatHTTPError(
	ctx context.Context,
	c *gin.Context,
	account *Account,
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
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "failover",
			Message:            message,
			Detail:             s.getUpstreamErrorDetail(body),
		}
		if resp.StatusCode == http.StatusUnauthorized {
			event.Stage = string(protocolforward.GatewayFailureStageAccountAuth)
			event.Scope = string(protocolforward.GatewayFailureScopeAccount)
			event.Reason = string(AntigravityCredentialRejectedReason)
			gatewayhttp.AppendOpsUpstreamError(c, event)
			return antigravityCredentialRejectedError(resp, body)
		}
		gatewayhttp.AppendOpsUpstreamError(c, event)
		return &protocolforward.UpstreamFailoverError{
			StatusCode:      resp.StatusCode,
			ResponseBody:    body,
			ResponseHeaders: resp.Header.Clone(),
		}
	}
	return s.writeMappedAntigravityCompatError(c, account, resp.StatusCode, resp.Header.Get("x-request-id"), body)
}

func antigravityCredentialRejectedError(resp *http.Response, body []byte) *protocolforward.UpstreamFailoverError {
	return &protocolforward.UpstreamFailoverError{
		StatusCode:      resp.StatusCode,
		ResponseBody:    body,
		ResponseHeaders: resp.Header.Clone(),
		Stage:           protocolforward.GatewayFailureStageAccountAuth,
		Scope:           protocolforward.GatewayFailureScopeAccount,
		Reason:          AntigravityCredentialRejectedReason, NextAccountAction: protocolforward.NextAccountRetry, ClientStatusCode: http.StatusBadGateway,
		ClientMessage: AntigravityCredentialRejectedClientMessage,
	}
}

func (s *AntigravityGatewayService) writeAntigravityCompatError(
	c *gin.Context,
	status int,
	errType string,
	message string,
) error {
	gatewayhttp.MarkResponseCommitted(c)
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
			"param":   nil,
			"code":    nil,
		},
	})
	return errors.New(message)
}

func (s *AntigravityGatewayService) writeMappedAntigravityCompatError(
	c *gin.Context,
	account *Account,
	upstreamStatus int,
	upstreamRequestID string,
	body []byte,
) error {
	gatewayhttp.MarkResponseCommitted(c)
	message := logredact.SanitizeUpstreamQueries(strings.TrimSpace(google.ExtractPlatformMessage(body)))
	gatewayhttp.SetOpsUpstreamError(c, upstreamStatus, message, s.getUpstreamErrorDetail(body))
	gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: upstreamStatus,
		UpstreamRequestID:  upstreamRequestID,
		Kind:               "http_error",
		Message:            message,
	})
	c.JSON(protocolforward.MapStatus(upstreamStatus), gin.H{
		"error": gin.H{
			"message": antigravity.GetPassthroughOrDefault(message, "Upstream request failed"),
			"type":    "upstream_error",
			"param":   nil,
			"code":    nil,
		},
	})
	return fmt.Errorf("upstream error: %d %s", upstreamStatus, message)
}

func (s *AntigravityGatewayService) mapAntigravityCompatCollectionError(c *gin.Context, err error) error {
	var failoverError *protocolforward.UpstreamFailoverError
	if errors.As(err, &failoverError) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if strings.Contains(err.Error(), "stream data interval timeout") {
		return s.writeAntigravityCompatError(c, http.StatusBadGateway, "upstream_timeout", "Upstream stream data interval timeout")
	}
	if errors.Is(err, bufio.ErrTooLong) {
		return s.writeAntigravityCompatError(c, http.StatusBadGateway, "response_too_large", "Upstream response line too long")
	}
	return s.writeAntigravityCompatError(c, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
}
