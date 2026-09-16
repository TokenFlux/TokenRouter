package service

import (
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apicompat"
	upstream "github.com/TokenFlux/TokenRouter/internal/upstream"

	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/gin-gonic/gin"
)

type geminiOpenAICompatProtocol = gemininative.OpenAICompatProtocol

const geminiOpenAICompatChatCompletions = gemininative.OpenAICompatChatCompletions
const geminiOpenAICompatResponses = gemininative.OpenAICompatResponses

// ForwardAsResponses 使用 Gemini 账号承接 OpenAI Responses 请求。
// 请求、重试和错误策略与 Chat Completions 共用同一套 Gemini 上游执行器。
func (s *GeminiMessagesCompatService) ForwardAsResponses(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	_ *ParsedRequest,
) (*ForwardResult, error) {
	startTime := time.Now()

	adaptedBody, clientToolMapping, err := adaptResponsesClientToolsForAnthropic(body)
	if err != nil {
		return nil, s.writeGeminiOpenAICompatError(c, geminiOpenAICompatResponses, http.StatusBadRequest, "invalid_request_error", "Failed to adapt client tools")
	}
	var responsesReq protocolopenai.ResponsesRequest
	if err := json.Unmarshal(adaptedBody, &responsesReq); err != nil {
		return nil, s.writeGeminiOpenAICompatError(c, geminiOpenAICompatResponses, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
	}
	if strings.TrimSpace(responsesReq.Model) == "" {
		return nil, s.writeGeminiOpenAICompatError(c, geminiOpenAICompatResponses, http.StatusBadRequest, "invalid_request_error", "model is required")
	}

	anthropicReq, err := apicompat.ResponsesToAnthropicRequest(&responsesReq)
	if err != nil {
		return nil, s.writeGeminiOpenAICompatError(c, geminiOpenAICompatResponses, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	anthropicReq.Stream = responsesReq.Stream
	claudeBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal responses compat request: %w", err)
	}

	return s.forwardClaudeBodyAsOpenAICompat(
		ctx,
		c,
		account,
		claudeBody,
		responsesReq.Model,
		responsesReq.Stream,
		false,
		startTime,
		body,
		geminiOpenAICompatResponses,
		clientToolMapping,
	)
}

// ForwardAsChatCompletions 使用 Gemini 账号承接 OpenAI Chat Completions 请求。
// 客户端侧保持 Chat Completions 响应格式，上游请求走 Gemini 原生端点。
func (s *GeminiMessagesCompatService) ForwardAsChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
) (*ForwardResult, error) {
	startTime := time.Now()

	var ccReq protocolopenai.ChatCompletionsRequest
	if err := json.Unmarshal(body, &ccReq); err != nil {
		return nil, s.writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
	}
	if strings.TrimSpace(ccReq.Model) == "" {
		return nil, s.writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
	}

	originalModel := ccReq.Model
	clientStream := ccReq.Stream
	includeUsage := ccReq.StreamOptions != nil && ccReq.StreamOptions.IncludeUsage

	responsesReq, err := apicompat.ChatCompletionsToResponses(&ccReq)
	if err != nil {
		return nil, s.writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}

	anthropicReq, err := apicompat.ResponsesToAnthropicRequest(responsesReq)
	if err != nil {
		return nil, s.writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	anthropicReq.Stream = clientStream

	claudeBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal chat completions compat request: %w", err)
	}

	return s.forwardClaudeBodyAsOpenAICompat(
		ctx,
		c,
		account,
		claudeBody,
		originalModel,
		clientStream,
		includeUsage,
		startTime,
		body,
		geminiOpenAICompatChatCompletions,
		apicompat.ResponsesClientToolMapping{},
	)
}

func (s *GeminiMessagesCompatService) forwardClaudeBodyAsOpenAICompat(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	claudeBody []byte,
	originalModel string,
	clientStream bool,
	includeUsage bool,
	startTime time.Time,
	originalBody []byte,
	protocol geminiOpenAICompatProtocol,
	clientToolMapping apicompat.ResponsesClientToolMapping,
) (*ForwardResult, error) {
	var req struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(claudeBody, &req); err != nil {
		return nil, s.writeGeminiOpenAICompatError(c, protocol, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
	}
	if strings.TrimSpace(req.Model) == "" {
		return nil, s.writeGeminiOpenAICompatError(c, protocol, http.StatusBadRequest, "invalid_request_error", "model is required")
	}

	// 两种 OpenAI 兼容入口都遵循 C -> U，OAuth 账号不能绕过账号映射。
	mappedModel := resolveAccountMappedModelForForward(account, req.Model)

	geminiReq, err := convertClaudeMessagesToGeminiGenerateContent(claudeBody)
	if err != nil {
		return nil, s.writeGeminiOpenAICompatError(c, protocol, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	geminiReq = ensureGeminiFunctionCallThoughtSignatures(geminiReq)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	useUpstreamStream := clientStream
	if account.Type == AccountTypeOAuth && !clientStream && strings.TrimSpace(account.GetCredential("project_id")) != "" {
		useUpstreamStream = true
	}

	buildReq, requestIDHeader := s.buildGeminiChatCompletionsUpstreamRequestFunc(
		account,
		mappedModel,
		geminiReq,
		clientStream,
		useUpstreamStream,
	)

	options := s.geminiExchangeOptions(c, ctx, account, mappedModel, geminiExchangeOpenAI, protocol)
	options.Build = buildReq
	options.RequestIDHeader = requestIDHeader
	options.Do = func(req *http.Request) (*http.Response, error) {
		return s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	}
	var requestID string
	var compatibilityResult *ForwardResult
	stopped := false
	var reasoningEffort *string
	clientProtocol := protocolcore.ProtocolOpenAIChatCompletions
	if protocol == geminiOpenAICompatResponses {
		clientProtocol = protocolcore.ProtocolOpenAIResponses
	}
	target := &gemininative.Target{AccountID: account.ID, Model: mappedModel, Mode: gemininative.OpenAIResponse, Exchange: options, Response: s.geminiResponseAdapter(c).Options, StartedAt: startTime, UpstreamStream: useUpstreamStream, OAuth: account.Type == AccountTypeOAuth, Enter: s.nativeAttemptActivity}
	target.OpenAIProtocol = protocol
	target.IncludeUsage = includeUsage
	target.ClientTools = clientToolMapping
	target.BeforeResponse = func(ctx context.Context, resp *http.Response, requestIDHeader string) (bool, error) {
		var callbackErr error
		compatibilityResult, callbackErr = func() (*ForwardResult, error) {

			requestID = resp.Header.Get(requestIDHeader)
			if requestID == "" {
				requestID = resp.Header.Get("x-goog-request-id")
			}
			if requestID != "" {
				c.Header("x-request-id", requestID)
			}

			if protocol == geminiOpenAICompatResponses {
				reasoningEffort = ExtractResponsesReasoningEffortFromBody(originalBody, mappedModel)
			} else {
				reasoningEffort = extractCCReasoningEffortFromBody(originalBody, mappedModel)
			}
			// 国产模型没有显式 effort 档位时，thinking 启用后补默认展示值。
			reasoningEffort = ApplyThinkingEnabledFallback(reasoningEffort, originalBody, mappedModel)

			if resp.StatusCode >= 400 {
				respBody := s.readUpstreamErrorBody(resp)
				decision := s.applyGeminiUpstreamErrorPolicy(ctx, account, resp.StatusCode, resp.Header, respBody, mappedModel)
				evBody := unwrapIfNeeded(account.Type == AccountTypeOAuth, respBody)
				if decision.Policy == ErrorPolicyCustomSkipped || decision.Policy == ErrorPolicyPoolBypassed {
					if failoverErr := s.skippedErrorPolicyFailoverError(c, account, resp.StatusCode, respBody, requestID); failoverErr != nil {
						return nil, failoverErr
					}
					if decision.Policy == ErrorPolicyCustomSkipped {
						return nil, s.writeGeminiCustomCodeSkippedError(c, account, resp.StatusCode, requestID, respBody, func() {
							_ = s.writeChatCompletionsError(c, http.StatusInternalServerError, "api_error", geminiCustomCodeSkippedClientMessage)
						})
					}
					return nil, s.writeGeminiOpenAICompatMappedError(c, account, resp.StatusCode, requestID, evBody, protocol)
				}
				if decision.ShouldReturnGenericError() {
					genericBody := []byte(`{"error":{"message":"Upstream gateway error"}}`)
					return nil, s.writeGeminiOpenAICompatMappedError(c, account, http.StatusInternalServerError, requestID, genericBody, protocol)
				}

				msg400 := strings.ToLower(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
				googleConfigError := resp.StatusCode == http.StatusBadRequest && isGoogleProjectConfigError(msg400)
				if decision.ShouldFailover(account, resp.StatusCode, googleConfigError || s.shouldFailoverGeminiUpstreamError(resp.StatusCode)) {
					upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(evBody)))
					appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
						Platform:           account.Platform,
						AccountID:          account.ID,
						AccountName:        account.Name,
						UpstreamStatusCode: resp.StatusCode,
						UpstreamRequestID:  requestID,
						Kind:               "failover",
						Message:            upstreamMsg,
					})
					return nil, &UpstreamFailoverError{
						StatusCode:             resp.StatusCode,
						ResponseBody:           evBody,
						RetryableOnSameAccount: decision.RetryableOnSameAccount(account, resp.StatusCode),
					}
				}

				return nil, s.writeGeminiOpenAICompatMappedError(c, account, resp.StatusCode, requestID, evBody, protocol)
			}

			return nil, nil
		}()
		stopped = resp.StatusCode >= 400 || callbackErr != nil || compatibilityResult != nil
		return stopped, callbackErr
	}
	result, executeErr := (gemininative.Executor{}).Execute(ctx, upstream.AttemptInput{Protocol: clientProtocol, Body: geminiReq, Stream: clientStream, ResponseModel: originalModel, Target: target}, gatewayhttp.ResponseSink{Writer: c.Writer})
	if stopped {
		return compatibilityResult, executeErr
	}
	if executeErr != nil {
		return nil, executeErr
	}
	requestID = result.RequestID
	usage := &result.Usage
	firstTokenMs := result.FirstTokenMs

	imageCount := 0
	imageInputSize := s.extractImageInputSize(geminiReq)
	imageSize := normalizeOpenAIImageSizeTier(imageInputSize)
	if isImageGenerationModel(originalModel) {
		imageCount = 1
	}

	return &ForwardResult{
		RequestID:        requestID,
		UpstreamHeaders:  result.UpstreamHeaders,
		Usage:            *usage,
		Model:            originalModel,
		UpstreamModel:    mappedModel,
		Stream:           clientStream,
		Duration:         result.Duration,
		FirstTokenMs:     firstTokenMs,
		ReasoningEffort:  reasoningEffort,
		ImageCount:       imageCount,
		ImageSize:        imageSize,
		ImageInputSize:   imageInputSize,
		ClientDisconnect: false,
	}, nil
}

func (s *GeminiMessagesCompatService) buildGeminiChatCompletionsUpstreamRequestFunc(
	account *Account,
	mappedModel string,
	geminiReq []byte,
	clientStream bool,
	useUpstreamStream bool,
) (func(context.Context) (*http.Request, string, error), string) {
	plan := s.geminiRequestPlan(account, mappedModel, "", false, clientStream, useUpstreamStream, false)
	return func(ctx context.Context) (*http.Request, string, error) {
		return gemininative.BuildRequest(ctx, geminiReq, plan)
	}, "x-request-id"
}

func geminiResponseToChatCompletions(geminiResp map[string]any, originalModel string, rawData []byte, usageOverride *ClaudeUsage) (*protocolopenai.ChatCompletionsResponse, *ClaudeUsage, error) {
	return gemininative.GeminiResponseToChatCompletions(geminiResp, originalModel, rawData, usageOverride)
}

func (s *GeminiMessagesCompatService) writeGeminiOpenAICompatMappedError(
	c *gin.Context,
	account *Account,
	upstreamStatus int,
	upstreamRequestID string,
	body []byte,
	protocol geminiOpenAICompatProtocol,
) error {
	upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	setOpsUpstreamError(c, upstreamStatus, upstreamMsg, "")
	if account != nil {
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: upstreamStatus,
			UpstreamRequestID:  upstreamRequestID,
			Kind:               "http_error",
			Message:            upstreamMsg,
		})
	}

	if status, errType, errMsg, matched := applyErrorPassthroughRule(
		c,
		PlatformGemini,
		upstreamStatus,
		body,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	); matched {
		return s.writeGeminiOpenAICompatError(c, protocol, status, errType, errMsg)
	}

	statusCode := http.StatusBadGateway
	errType := "upstream_error"
	errMsg := "Upstream request failed"
	if mapped := mapGeminiErrorBodyToClaudeError(body); mapped != nil {
		if mapped.Type != "" {
			errType = mapped.Type
		}
		if mapped.Message != "" {
			errMsg = mapped.Message
		}
		if mapped.StatusCode > 0 {
			statusCode = mapped.StatusCode
		}
	}

	switch upstreamStatus {
	case http.StatusBadRequest:
		if statusCode == http.StatusBadGateway {
			statusCode = http.StatusBadRequest
		}
		if errType == "upstream_error" {
			errType = "invalid_request_error"
		}
		if errMsg == "Upstream request failed" {
			errMsg = "Invalid request"
		}
	case http.StatusNotFound:
		statusCode = http.StatusNotFound
		if errType == "upstream_error" {
			errType = "not_found_error"
		}
		if errMsg == "Upstream request failed" {
			errMsg = "Resource not found"
		}
	case http.StatusTooManyRequests:
		statusCode = http.StatusTooManyRequests
		if errType == "upstream_error" {
			errType = "rate_limit_error"
		}
		if errMsg == "Upstream request failed" {
			errMsg = "Upstream rate limit exceeded, please retry later"
		}
	case 529:
		statusCode = http.StatusServiceUnavailable
		if errType == "upstream_error" {
			errType = "overloaded_error"
		}
		if errMsg == "Upstream request failed" {
			errMsg = "Upstream service overloaded, please retry later"
		}
	}

	if upstreamMsg != "" && errMsg == "Upstream request failed" {
		errMsg = upstreamMsg
	}
	// 池模式的 4xx 不会切换账号，客户端需要看到上游给出的具体校验原因；
	// 普通账号仍保留兼容层的通用错误文案。
	if account != nil && account.IsPoolMode() && upstreamStatus >= http.StatusBadRequest && upstreamMsg != "" {
		errMsg = upstreamMsg
	}
	return s.writeGeminiOpenAICompatError(c, protocol, statusCode, errType, errMsg)
}

// writeGeminiOpenAICompatError 按客户端入口输出对应的 OpenAI 错误格式。
func (s *GeminiMessagesCompatService) writeGeminiOpenAICompatError(
	c *gin.Context,
	protocol geminiOpenAICompatProtocol,
	status int,
	errType string,
	message string,
) error {
	if protocol == geminiOpenAICompatResponses {
		writeResponsesError(c, status, errType, message)
		return fmt.Errorf("%s", message)
	}
	return s.writeChatCompletionsError(c, status, errType, message)
}

func (s *GeminiMessagesCompatService) writeChatCompletionsError(c *gin.Context, status int, errType, message string) error {
	c.JSON(status, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
	return fmt.Errorf("%s", message)
}
