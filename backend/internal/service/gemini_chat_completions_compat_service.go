package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/apicompat"
	"github.com/BrandonVee/TokenRouter/internal/pkg/geminicli"
	"github.com/BrandonVee/TokenRouter/internal/pkg/logger"
	"github.com/BrandonVee/TokenRouter/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type geminiOpenAICompatProtocol int

const (
	geminiOpenAICompatChatCompletions geminiOpenAICompatProtocol = iota
	geminiOpenAICompatResponses
)

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
	var responsesReq apicompat.ResponsesRequest
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

	var ccReq apicompat.ChatCompletionsRequest
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

	var resp *http.Response
	for attempt := 1; attempt <= geminiMaxRetries; attempt++ {
		upstreamReq, idHeader, err := buildReq(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			return nil, s.writeGeminiOpenAICompatError(c, protocol, http.StatusBadGateway, "upstream_error", err.Error())
		}
		requestIDHeader = idHeader

		resp, err = s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
		if err != nil {
			safeErr := sanitizeUpstreamErrorMessage(err.Error())
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: 0,
				Kind:               "request_error",
				Message:            safeErr,
			})
			if attempt < geminiMaxRetries {
				logger.LegacyPrintf("service.gemini_chat_completions", "Gemini account %d: upstream request failed, retry %d/%d: %v", account.ID, attempt, geminiMaxRetries, err)
				sleepGeminiBackoff(attempt)
				continue
			}
			setOpsUpstreamError(c, 0, safeErr, "")
			return nil, s.writeGeminiOpenAICompatError(c, protocol, http.StatusBadGateway, "upstream_error", "Upstream request failed after retries: "+safeErr)
		}

		if matched, rebuilt := s.checkErrorPolicyInLoop(ctx, account, resp, mappedModel); matched {
			resp = rebuilt
			break
		} else {
			resp = rebuilt
		}

		if resp.StatusCode >= 400 && s.shouldRetryGeminiUpstreamError(account, resp.StatusCode) {
			respBody := s.readUpstreamErrorBody(resp)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusForbidden && isGeminiInsufficientScope(resp.Header, respBody) {
				resp = &http.Response{
					StatusCode: resp.StatusCode,
					Header:     resp.Header.Clone(),
					Body:       io.NopCloser(bytes.NewReader(respBody)),
				}
				break
			}
			if resp.StatusCode == http.StatusTooManyRequests {
				s.handleGeminiUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
			}
			if attempt < geminiMaxRetries {
				upstreamReqID := resp.Header.Get(requestIDHeader)
				if upstreamReqID == "" {
					upstreamReqID = resp.Header.Get("x-goog-request-id")
				}
				upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
				upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					Platform:           account.Platform,
					AccountID:          account.ID,
					AccountName:        account.Name,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  upstreamReqID,
					Kind:               "retry",
					Message:            upstreamMsg,
				})
				logger.LegacyPrintf("service.gemini_chat_completions", "Gemini account %d: upstream status %d, retry %d/%d", account.ID, resp.StatusCode, attempt, geminiMaxRetries)
				sleepGeminiBackoff(attempt)
				continue
			}
			resp = &http.Response{
				StatusCode: resp.StatusCode,
				Header:     resp.Header.Clone(),
				Body:       io.NopCloser(bytes.NewReader(respBody)),
			}
			break
		}

		break
	}
	defer func() { _ = resp.Body.Close() }()

	requestID := resp.Header.Get(requestIDHeader)
	if requestID == "" {
		requestID = resp.Header.Get("x-goog-request-id")
	}
	if requestID != "" {
		c.Header("x-request-id", requestID)
	}

	var reasoningEffort *string
	if protocol == geminiOpenAICompatResponses {
		reasoningEffort = ExtractResponsesReasoningEffortFromBody(originalBody)
	} else {
		reasoningEffort = extractCCReasoningEffortFromBody(originalBody)
	}
	// 国产模型没有显式 effort 档位时，thinking 启用后补默认展示值。
	reasoningEffort = ApplyThinkingEnabledFallback(reasoningEffort, originalBody, mappedModel)

	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)
		decision := s.applyGeminiUpstreamErrorPolicy(ctx, account, resp.StatusCode, resp.Header, respBody, mappedModel)
		evBody := unwrapIfNeeded(account.Type == AccountTypeOAuth, respBody)
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

	var usage *ClaudeUsage
	var firstTokenMs *int
	var responseBody []byte
	if clientStream {
		streamRes, err := s.handleOpenAICompatStreamingResponseFromGemini(c, resp, startTime, originalModel, account.Type == AccountTypeOAuth, includeUsage, protocol, clientToolMapping)
		if err != nil {
			return nil, err
		}
		usage = streamRes.usage
		firstTokenMs = streamRes.firstTokenMs
		responseBody = streamRes.responseBody
	} else if useUpstreamStream {
		collected, usageObj, err := collectGeminiSSE(resp.Body, account.Type == AccountTypeOAuth)
		if err != nil {
			return nil, s.writeGeminiOpenAICompatError(c, protocol, http.StatusBadGateway, "upstream_error", "Failed to read upstream stream")
		}
		collectedBytes, _ := json.Marshal(collected)
		if protocol == geminiOpenAICompatResponses {
			responsesResp, usageObj2, convertErr := geminiResponseToResponses(collected, originalModel, collectedBytes, usageObj)
			if convertErr != nil {
				return nil, s.writeGeminiOpenAICompatError(c, protocol, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
			}
			responseBody, err = s.writeGeminiResponsesResponse(c, resp, responsesResp, clientToolMapping)
			if err != nil {
				return nil, err
			}
			usage = usageObj2
		} else {
			chatResp, usageObj2, convertErr := geminiResponseToChatCompletions(collected, originalModel, collectedBytes, usageObj)
			if convertErr != nil {
				return nil, s.writeGeminiOpenAICompatError(c, protocol, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
			}
			if responseBytes, marshalErr := json.Marshal(chatResp); marshalErr == nil {
				responseBody = cloneDataSharingRequestBody(responseBytes)
				c.Data(http.StatusOK, "application/json; charset=utf-8", responseBytes)
			} else {
				c.JSON(http.StatusOK, chatResp)
			}
			usage = usageObj2
		}
	} else {
		var usageResp *ClaudeUsage
		var body []byte
		if protocol == geminiOpenAICompatResponses {
			usageResp, body, err = s.handleResponsesNonStreamingResponseFromGemini(c, resp, originalModel, account.Type == AccountTypeOAuth, clientToolMapping)
		} else {
			usageResp, body, err = s.handleChatCompletionsNonStreamingResponseFromGemini(c, resp, originalModel, account.Type == AccountTypeOAuth)
		}
		if err != nil {
			return nil, err
		}
		usage = usageResp
		responseBody = body
	}

	if usage == nil {
		usage = &ClaudeUsage{}
	}

	imageCount := 0
	imageInputSize := s.extractImageInputSize(geminiReq)
	imageSize := normalizeOpenAIImageSizeTier(imageInputSize)
	if isImageGenerationModel(originalModel) {
		imageCount = 1
	}

	return &ForwardResult{
		RequestID:        requestID,
		Usage:            *usage,
		Model:            originalModel,
		UpstreamModel:    mappedModel,
		Stream:           clientStream,
		Duration:         time.Since(startTime),
		FirstTokenMs:     firstTokenMs,
		ReasoningEffort:  reasoningEffort,
		ResponseBody:     cloneDataSharingRequestBody(responseBody),
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
	switch account.Type {
	case AccountTypeAPIKey:
		return func(ctx context.Context) (*http.Request, string, error) {
			apiKey := account.GetCredential("api_key")
			if strings.TrimSpace(apiKey) == "" {
				return nil, "", errors.New("gemini api_key not configured")
			}

			baseURL := account.GetGeminiBaseURL(geminicli.AIStudioBaseURL)
			normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
			if err != nil {
				return nil, "", err
			}

			action := "generateContent"
			if clientStream {
				action = "streamGenerateContent"
			}
			fullURL, err := buildGeminiAIStudioModelActionURL(normalizedBaseURL, mappedModel, action, clientStream)
			if err != nil {
				return nil, "", err
			}

			restGeminiReq := normalizeGeminiRequestForAIStudio(geminiReq)
			upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(restGeminiReq))
			if err != nil {
				return nil, "", err
			}
			upstreamReq.Header.Set("Content-Type", "application/json")
			upstreamReq.Header.Set("x-goog-api-key", apiKey)
			return upstreamReq, "x-request-id", nil
		}, "x-request-id"

	case AccountTypeOAuth:
		return func(ctx context.Context) (*http.Request, string, error) {
			if s.tokenProvider == nil {
				return nil, "", errors.New("gemini token provider not configured")
			}
			accessToken, err := s.tokenProvider.GetAccessToken(ctx, account)
			if err != nil {
				return nil, "", err
			}

			projectID := strings.TrimSpace(account.GetCredential("project_id"))
			action := "generateContent"
			if useUpstreamStream {
				action = "streamGenerateContent"
			}

			if projectID != "" {
				baseURL, err := s.validateUpstreamBaseURL(geminicli.GeminiCliBaseURL)
				if err != nil {
					return nil, "", err
				}
				fullURL := fmt.Sprintf("%s/v1internal:%s", strings.TrimRight(baseURL, "/"), action)
				if useUpstreamStream {
					fullURL += "?alt=sse"
				}

				var inner any
				if err := json.Unmarshal(geminiReq, &inner); err != nil {
					return nil, "", fmt.Errorf("failed to parse gemini request: %w", err)
				}
				wrappedBytes, _ := json.Marshal(map[string]any{
					"model":   mappedModel,
					"project": projectID,
					"request": inner,
				})

				upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(wrappedBytes))
				if err != nil {
					return nil, "", err
				}
				upstreamReq.Header.Set("Content-Type", "application/json")
				upstreamReq.Header.Set("Authorization", "Bearer "+accessToken)
				upstreamReq.Header.Set("User-Agent", geminicli.GeminiCLIUserAgent)
				return upstreamReq, "x-request-id", nil
			}

			baseURL := account.GetGeminiBaseURL(geminicli.AIStudioBaseURL)
			normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
			if err != nil {
				return nil, "", err
			}

			fullURL, err := buildGeminiAIStudioModelActionURL(normalizedBaseURL, mappedModel, action, useUpstreamStream)
			if err != nil {
				return nil, "", err
			}

			restGeminiReq := normalizeGeminiRequestForAIStudio(geminiReq)
			upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(restGeminiReq))
			if err != nil {
				return nil, "", err
			}
			upstreamReq.Header.Set("Content-Type", "application/json")
			upstreamReq.Header.Set("Authorization", "Bearer "+accessToken)
			return upstreamReq, "x-request-id", nil
		}, "x-request-id"

	case AccountTypeServiceAccount:
		return func(ctx context.Context) (*http.Request, string, error) {
			if s.tokenProvider == nil {
				return nil, "", errors.New("gemini token provider not configured")
			}
			accessToken, err := s.tokenProvider.GetAccessToken(ctx, account)
			if err != nil {
				return nil, "", err
			}

			action := "generateContent"
			if clientStream {
				action = "streamGenerateContent"
			}
			fullURL, err := buildVertexGeminiURL(account.VertexProjectID(), account.VertexLocation(mappedModel), mappedModel, action, clientStream)
			if err != nil {
				return nil, "", err
			}

			restGeminiReq := normalizeGeminiRequestForAIStudio(geminiReq)
			upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(restGeminiReq))
			if err != nil {
				return nil, "", err
			}
			upstreamReq.Header.Set("Content-Type", "application/json")
			upstreamReq.Header.Set("Authorization", "Bearer "+accessToken)
			return upstreamReq, "x-request-id", nil
		}, "x-request-id"

	default:
		return func(context.Context) (*http.Request, string, error) {
			return nil, "", fmt.Errorf("unsupported account type: %s", account.Type)
		}, "x-request-id"
	}
}

func (s *GeminiMessagesCompatService) handleChatCompletionsNonStreamingResponseFromGemini(
	c *gin.Context,
	resp *http.Response,
	originalModel string,
	isOAuth bool,
) (*ClaudeUsage, []byte, error) {
	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, nil, err
	}
	if isOAuth {
		if unwrappedBody, uwErr := unwrapGeminiResponse(respBody); uwErr == nil {
			respBody = unwrappedBody
		}
	}

	var geminiResp map[string]any
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		return nil, nil, s.writeChatCompletionsError(c, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
	}

	chatResp, usage, err := geminiResponseToChatCompletions(geminiResp, originalModel, respBody, nil)
	if err != nil {
		return nil, nil, s.writeChatCompletionsError(c, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
	}

	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	body, err := json.Marshal(chatResp)
	if err != nil {
		return nil, nil, err
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
	return usage, cloneDataSharingRequestBody(body), nil
}

func geminiResponseToChatCompletions(
	geminiResp map[string]any,
	originalModel string,
	rawData []byte,
	usageOverride *ClaudeUsage,
) (*apicompat.ChatCompletionsResponse, *ClaudeUsage, error) {
	responsesResp, usage, err := geminiResponseToResponses(geminiResp, originalModel, rawData, usageOverride)
	if err != nil {
		return nil, nil, err
	}
	return apicompat.ResponsesToChatCompletions(responsesResp, originalModel), usage, nil
}

// geminiResponseToResponses 统一完成 Gemini -> Anthropic -> Responses 的响应转换。
func geminiResponseToResponses(
	geminiResp map[string]any,
	originalModel string,
	rawData []byte,
	usageOverride *ClaudeUsage,
) (*apicompat.ResponsesResponse, *ClaudeUsage, error) {
	claudeRespMap, usage := convertGeminiToClaudeMessage(geminiResp, originalModel, rawData, true)
	if usageOverride != nil && (usageOverride.InputTokens > 0 || usageOverride.OutputTokens > 0 || usageOverride.CacheReadInputTokens > 0) {
		usage = usageOverride
		if usageMap, ok := claudeRespMap["usage"].(map[string]any); ok {
			usageMap["input_tokens"] = usage.InputTokens
			usageMap["output_tokens"] = usage.OutputTokens
			usageMap["cache_read_input_tokens"] = usage.CacheReadInputTokens
		}
	}

	claudeBytes, err := json.Marshal(claudeRespMap)
	if err != nil {
		return nil, nil, err
	}
	var anthropicResp apicompat.AnthropicResponse
	if err := json.Unmarshal(claudeBytes, &anthropicResp); err != nil {
		return nil, nil, err
	}
	responsesResp := apicompat.AnthropicToResponsesResponse(&anthropicResp)
	responsesResp.Model = originalModel
	return responsesResp, usage, nil
}

func (s *GeminiMessagesCompatService) handleResponsesNonStreamingResponseFromGemini(
	c *gin.Context,
	resp *http.Response,
	originalModel string,
	isOAuth bool,
	clientToolMapping apicompat.ResponsesClientToolMapping,
) (*ClaudeUsage, []byte, error) {
	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, nil, err
	}
	if isOAuth {
		if unwrappedBody, unwrapErr := unwrapGeminiResponse(respBody); unwrapErr == nil {
			respBody = unwrappedBody
		}
	}

	var geminiResp map[string]any
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		return nil, nil, s.writeGeminiOpenAICompatError(c, geminiOpenAICompatResponses, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
	}
	responsesResp, usage, err := geminiResponseToResponses(geminiResp, originalModel, respBody, nil)
	if err != nil {
		return nil, nil, s.writeGeminiOpenAICompatError(c, geminiOpenAICompatResponses, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
	}
	body, err := s.writeGeminiResponsesResponse(c, resp, responsesResp, clientToolMapping)
	return usage, body, err
}

func (s *GeminiMessagesCompatService) writeGeminiResponsesResponse(
	c *gin.Context,
	resp *http.Response,
	responsesResp *apicompat.ResponsesResponse,
	clientToolMapping apicompat.ResponsesClientToolMapping,
) ([]byte, error) {
	if resp != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	body, err := json.Marshal(responsesResp)
	if err != nil {
		return nil, err
	}
	body = reverseToolNamesIfPresent(c, body)
	body, _, err = apicompat.RestoreResponsesClientToolPayload(body, clientToolMapping)
	if err != nil {
		return nil, fmt.Errorf("restore responses client tools: %w", err)
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
	return cloneDataSharingRequestBody(body), nil
}

func (s *GeminiMessagesCompatService) handleOpenAICompatStreamingResponseFromGemini(
	c *gin.Context,
	resp *http.Response,
	startTime time.Time,
	originalModel string,
	isOAuth bool,
	includeUsage bool,
	protocol geminiOpenAICompatProtocol,
	clientToolMapping apicompat.ResponsesClientToolMapping,
) (*geminiStreamResult, error) {
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	anthState := apicompat.NewAnthropicEventToResponsesState()
	anthState.Model = originalModel
	ccState := apicompat.NewResponsesEventToChatState()
	ccState.Model = originalModel
	ccState.IncludeUsage = includeUsage
	clientToolRestorer := apicompat.NewResponsesClientToolStreamRestorer(clientToolMapping)

	var usage ClaudeUsage
	var firstTokenMs *int
	firstChunk := true
	streamAccumulator := newOpenAIChatCompletionsStreamAccumulator(originalModel)

	writeChatChunk := func(chunk apicompat.ChatCompletionsChunk) bool {
		payload, err := json.Marshal(chunk)
		if err != nil {
			return false
		}
		observeOpenAIChatStreamPayload(streamAccumulator, payload, openAIUsageFromClaudeUsage(usage))
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", payload); err != nil {
			return true
		}
		return false
	}
	writeResponsesEvent := func(event apicompat.ResponsesStreamEvent) bool {
		payload, err := json.Marshal(event)
		if err != nil {
			return false
		}
		payload = reverseToolNamesIfPresent(c, payload)
		payloads, _, err := clientToolRestorer.RestoreEvent(payload)
		if err != nil {
			return false
		}
		for _, restored := range payloads {
			eventType := gjson.GetBytes(restored, "type").String()
			if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventType, restored); err != nil {
				return true
			}
		}
		return false
	}

	resultSnapshot := func() *geminiStreamResult {
		var responseBody []byte
		if protocol == geminiOpenAICompatChatCompletions {
			responseBody = streamAccumulator.ResponseBody(openAIUsageFromClaudeUsage(usage))
		}
		return &geminiStreamResult{
			usage:        &usage,
			firstTokenMs: firstTokenMs,
			responseBody: responseBody,
		}
	}

	emitAnthropicEvent := func(evt *apicompat.AnthropicStreamEvent) bool {
		responsesEvents := apicompat.AnthropicEventToResponsesEvents(evt, anthState)
		for _, resEvt := range responsesEvents {
			if protocol == geminiOpenAICompatResponses {
				if disconnected := writeResponsesEvent(resEvt); disconnected {
					return true
				}
				continue
			}
			chunks := apicompat.ResponsesEventToChatChunks(&resEvt, ccState)
			for _, chunk := range chunks {
				if disconnected := writeChatChunk(chunk); disconnected {
					return true
				}
			}
		}
		flusher.Flush()
		return false
	}

	messageID := generateAnthropicMsgID()
	if emitAnthropicEvent(&apicompat.AnthropicStreamEvent{
		Type: "message_start",
		Message: &apicompat.AnthropicResponse{
			ID:         messageID,
			Type:       "message",
			Role:       "assistant",
			Model:      originalModel,
			Content:    []apicompat.AnthropicContentBlock{},
			StopReason: nil, // 序列化为 JSON null。
			Usage:      apicompat.AnthropicUsage{},
		},
	}) {
		return resultSnapshot(), nil
	}

	finishReason := ""
	sawToolUse := false
	nextBlockIndex := 0
	openBlockIndex := -1
	openBlockType := ""
	seenText := ""
	seenThinking := ""
	openToolIndex := -1
	openToolName := ""
	seenToolJSON := ""

	closeOpenBlock := func() bool {
		if openBlockIndex < 0 {
			return false
		}
		disconnected := emitAnthropicEvent(&apicompat.AnthropicStreamEvent{Type: "content_block_stop"})
		openBlockIndex = -1
		openBlockType = ""
		return disconnected
	}
	closeOpenTool := func() bool {
		if openToolIndex < 0 {
			return false
		}
		disconnected := emitAnthropicEvent(&apicompat.AnthropicStreamEvent{Type: "content_block_stop"})
		openToolIndex = -1
		openToolName = ""
		seenToolJSON = ""
		return disconnected
	}

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(trimmed, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				if payload != "" && payload != "[DONE]" {
					rawBytes := []byte(payload)
					if isOAuth {
						if innerBytes, uwErr := unwrapGeminiResponse(rawBytes); uwErr == nil {
							rawBytes = innerBytes
						}
					}

					var geminiResp map[string]any
					if err := json.Unmarshal(rawBytes, &geminiResp); err == nil {
						if firstChunk {
							firstChunk = false
							ms := int(time.Since(startTime).Milliseconds())
							firstTokenMs = &ms
						}
						if fr := extractGeminiFinishReason(geminiResp); fr != "" {
							finishReason = fr
						}
						if u := extractGeminiUsage(rawBytes); u != nil {
							usage = *u
						}

						for _, part := range extractGeminiParts(geminiResp) {
							if text, ok := part["text"].(string); ok && text != "" {
								if openToolIndex >= 0 {
									if closeOpenTool() {
										return resultSnapshot(), nil
									}
								}
								thought, _ := part["thought"].(bool)
								blockType := "text"
								deltaType := "text_delta"
								seen := seenText
								if thought {
									blockType = "thinking"
									deltaType = "thinking_delta"
									seen = seenThinking
								}
								delta, newSeen := computeGeminiTextDelta(seen, text)
								if thought {
									seenThinking = newSeen
								} else {
									seenText = newSeen
								}
								if delta == "" {
									continue
								}
								if openBlockType != blockType {
									if closeOpenBlock() {
										return resultSnapshot(), nil
									}
									idx := nextBlockIndex
									nextBlockIndex++
									openBlockIndex = idx
									openBlockType = blockType
									contentBlock := &apicompat.AnthropicContentBlock{Type: "text", Text: ""}
									if thought {
										contentBlock = &apicompat.AnthropicContentBlock{Type: "thinking", Thinking: ""}
									}
									if emitAnthropicEvent(&apicompat.AnthropicStreamEvent{
										Type:         "content_block_start",
										Index:        &idx,
										ContentBlock: contentBlock,
									}) {
										return resultSnapshot(), nil
									}
								}
								deltaEvent := &apicompat.AnthropicDelta{Type: deltaType, Text: delta}
								if thought {
									deltaEvent.Text = ""
									deltaEvent.Thinking = delta
								}
								if emitAnthropicEvent(&apicompat.AnthropicStreamEvent{
									Type:  "content_block_delta",
									Delta: deltaEvent,
								}) {
									return resultSnapshot(), nil
								}
								continue
							}

							if fc, ok := part["functionCall"].(map[string]any); ok && fc != nil {
								name, _ := fc["name"].(string)
								if strings.TrimSpace(name) == "" {
									name = "tool"
								}
								if closeOpenBlock() {
									return resultSnapshot(), nil
								}
								if openToolIndex >= 0 && openToolName != name {
									if closeOpenTool() {
										return resultSnapshot(), nil
									}
								}
								if openToolIndex < 0 {
									idx := nextBlockIndex
									nextBlockIndex++
									openToolIndex = idx
									openToolName = name
									sawToolUse = true
									if emitAnthropicEvent(&apicompat.AnthropicStreamEvent{
										Type:  "content_block_start",
										Index: &idx,
										ContentBlock: &apicompat.AnthropicContentBlock{
											Type:  "tool_use",
											ID:    "toolu_" + randomHex(8),
											Name:  name,
											Input: json.RawMessage(`{}`),
										},
									}) {
										return resultSnapshot(), nil
									}
								}

								argsJSONText := "{}"
								switch v := fc["args"].(type) {
								case nil:
								case string:
									if strings.TrimSpace(v) != "" {
										argsJSONText = v
									}
								default:
									if b, err := json.Marshal(v); err == nil && len(b) > 0 {
										argsJSONText = string(b)
									}
								}
								delta, newSeen := computeGeminiTextDelta(seenToolJSON, argsJSONText)
								seenToolJSON = newSeen
								if delta != "" {
									if emitAnthropicEvent(&apicompat.AnthropicStreamEvent{
										Type: "content_block_delta",
										Delta: &apicompat.AnthropicDelta{
											Type:        "input_json_delta",
											PartialJSON: delta,
										},
									}) {
										return resultSnapshot(), nil
									}
								}
							}
						}
					}
				}
			}
		}

		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("stream read error: %w", err)
		}
	}

	if closeOpenBlock() {
		return resultSnapshot(), nil
	}
	if closeOpenTool() {
		return resultSnapshot(), nil
	}

	stopReason := mapGeminiFinishReasonToClaudeStopReason(finishReason)
	if sawToolUse {
		stopReason = "tool_use"
	}
	anthState.InputTokens = usage.InputTokens
	anthState.CacheReadInputTokens = usage.CacheReadInputTokens
	if emitAnthropicEvent(&apicompat.AnthropicStreamEvent{
		Type: "message_delta",
		Delta: &apicompat.AnthropicDelta{
			Type:       "message_delta",
			StopReason: stopReason,
		},
		Usage: &apicompat.AnthropicUsage{
			InputTokens:          usage.InputTokens,
			OutputTokens:         usage.OutputTokens,
			CacheReadInputTokens: usage.CacheReadInputTokens,
		},
	}) {
		return resultSnapshot(), nil
	}
	if emitAnthropicEvent(&apicompat.AnthropicStreamEvent{Type: "message_stop"}) {
		return resultSnapshot(), nil
	}

	for _, resEvt := range apicompat.FinalizeAnthropicResponsesStream(anthState) {
		if protocol == geminiOpenAICompatResponses {
			if disconnected := writeResponsesEvent(resEvt); disconnected {
				return resultSnapshot(), nil
			}
			continue
		}
		chunks := apicompat.ResponsesEventToChatChunks(&resEvt, ccState)
		for _, chunk := range chunks {
			if disconnected := writeChatChunk(chunk); disconnected {
				return resultSnapshot(), nil
			}
		}
	}
	if protocol == geminiOpenAICompatChatCompletions {
		for _, chunk := range apicompat.FinalizeResponsesChatStream(ccState) {
			if disconnected := writeChatChunk(chunk); disconnected {
				return resultSnapshot(), nil
			}
		}
		_, _ = io.WriteString(c.Writer, "data: [DONE]\n\n")
	}
	flusher.Flush()

	return resultSnapshot(), nil
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
