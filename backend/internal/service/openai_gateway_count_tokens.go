package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/tokenestimate"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apicompat"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const openAIInputTokensFallbackMinimum = 1

// 兼容旧上游计数准备结构，算法由网关估算模块唯一持有。
type openAIInputTokensCountRequest = tokenestimate.Request

type openAIInputTokensCountPrepared struct {
	Request         openAIInputTokensCountRequest
	OriginalModel   string
	NormalizedModel string
	BillingModel    string
	UpstreamModel   string
}

// EstimateGrokCountTokens 在本地估算 Anthropic 兼容的 count_tokens 请求。Grok 没有
// 兼容的 token 计数端点，因此该路径不选择账号、不读取凭据，也不调用上游。
func EstimateGrokCountTokens(body []byte) (int, error) {
	return estimateAnthropicCountTokensLocally(body)
}

func estimateAnthropicCountTokensLocally(body []byte) (int, error) {
	return tokenestimate.Anthropic(body)
}

// ForwardCountTokensAsAnthropic 将 Anthropic /v1/messages/count_tokens 桥接到
// OpenAI POST /v1/responses/input_tokens，并返回 Anthropic 兼容结果。
func (s *OpenAIGatewayService) ForwardCountTokensAsAnthropic(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) error {
	if account == nil {
		writeAnthropicCountTokensError(c, http.StatusServiceUnavailable, "api_error", "No available OpenAI accounts")
		return fmt.Errorf("count_tokens: missing account")
	}

	// 三家国产供应商的兼容层都没有可依赖的 count_tokens 端点；无论账号使用
	// Chat、Anthropic 还是 Responses 上游协议，都只做本地估算且不改变账号状态。
	if account.IsCNProvider() {
		estimated, err := estimateAnthropicCountTokensLocally(body)
		if err != nil {
			writeAnthropicCountTokensError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
			return fmt.Errorf("count_tokens: estimate cn provider input tokens: %w", err)
		}
		logger.L().Debug("openai count_tokens: cn provider local estimate",
			zap.Int64("account_id", account.ID),
			zap.Int("estimated_input_tokens", estimated),
		)
		c.JSON(http.StatusOK, gin.H{
			"input_tokens": estimated,
		})
		return nil
	}

	prepared, err := prepareOpenAIInputTokensCountRequest(body, account, defaultMappedModel)
	if err != nil {
		writeAnthropicCountTokensError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return err
	}

	upstreamBody, err := marshalOpenAIUpstreamJSON(prepared.Request)
	if err != nil {
		writeAnthropicCountTokensError(c, http.StatusInternalServerError, "api_error", "Failed to build request")
		return fmt.Errorf("marshal openai input_tokens body: %w", err)
	}

	logger.L().Debug("openai count_tokens: model mapping applied",
		zap.Int64("account_id", account.ID),
		zap.String("original_model", prepared.OriginalModel),
		zap.String("normalized_model", prepared.NormalizedModel),
		zap.String("billing_model", prepared.BillingModel),
		zap.String("upstream_model", prepared.UpstreamModel),
	)

	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		writeAnthropicCountTokensError(c, http.StatusBadGateway, "upstream_error", "Failed to get access token")
		return fmt.Errorf("get access token: %w", err)
	}

	upstreamReq, err := s.buildInputTokensUpstreamRequest(ctx, c, account, upstreamBody, token)
	if err != nil {
		writeAnthropicCountTokensError(c, http.StatusInternalServerError, "api_error", "Failed to build request")
		return fmt.Errorf("build input_tokens request: %w", err)
	}

	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	return nativeopenai.CountInputTokens(upstreamReq, nativeopenai.InputTokensOptions{
		Enter: s.nativeAttemptActivity,
		Do: func(req *http.Request) (*http.Response, error) {
			return s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
		},
		TransportError: func(err error) error {

			safeErr := sanitizeUpstreamErrorMessage(err.Error())
			setOpsUpstreamError(c, 0, safeErr, "")
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: 0,
				Kind:               "request_error",
				Message:            safeErr,
			})
			writeAnthropicCountTokensError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			return fmt.Errorf("openai input_tokens upstream request failed: %s", safeErr)

		},
		HTTPError: func(resp *http.Response, respBody []byte) error {

			upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
			if account.Type == AccountTypeOAuth && isOpenAIOAuthInputTokensUnsupported(resp.StatusCode, respBody) {
				writeOpenAIOAuthInputTokensFallback(c, account, prepared, resp.StatusCode)
				return nil
			}
			if isOpenAIInputTokensUnsupported(resp.StatusCode, respBody) {
				writeAnthropicCountTokensError(c, http.StatusNotFound, "not_found_error", "Token counting is not supported by upstream")
				return nil
			}
			var decision UpstreamErrorDecision
			if account.Platform == PlatformGrok {
				decision = s.applyGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, prepared.UpstreamModel)
			} else {
				decision = s.applyOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, prepared.UpstreamModel)
			}
			if decision.ShouldReturnGenericError() {
				writeAnthropicCountTokensError(c, http.StatusInternalServerError, "upstream_error", "Upstream gateway error")
				return fmt.Errorf("input_tokens upstream error: %d (not in custom error codes)", resp.StatusCode)
			}
			defaultFailover := s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody)
			if account.Platform == PlatformGrok {
				defaultFailover = s.shouldFailoverGrokUpstreamError(resp.StatusCode, respBody)
			}
			if decision.ShouldFailover(account, resp.StatusCode, defaultFailover) {
				return &UpstreamFailoverError{
					StatusCode:             resp.StatusCode,
					ResponseBody:           respBody,
					ResponseHeaders:        resp.Header.Clone(),
					RetryableOnSameAccount: decision.RetryableOnSameAccount(account, resp.StatusCode),
				}
			}

			upstreamDetail := ""
			if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
				maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
				if maxBytes <= 0 {
					maxBytes = 2048
				}
				upstreamDetail = truncateString(string(respBody), maxBytes)
			}
			setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				Kind:               "request_error",
				Message:            upstreamMsg,
				Detail:             upstreamDetail,
			})

			errMsg := "Upstream request failed"
			switch resp.StatusCode {
			case http.StatusTooManyRequests:
				errMsg = "Rate limit exceeded"
			case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, 529:
				errMsg = "Upstream service temporarily unavailable"
			}
			writeAnthropicCountTokensError(c, resp.StatusCode, "upstream_error", errMsg)
			if upstreamMsg == "" {
				return fmt.Errorf("input_tokens upstream error: %d", resp.StatusCode)
			}
			return fmt.Errorf("input_tokens upstream error: %d message=%s", resp.StatusCode, upstreamMsg)

		},
		WriteError: func(status int, kind, message string) { writeAnthropicCountTokensError(c, status, kind, message) },
	}, gatewayhttp.ResponseSink{Writer: c.Writer})
}

func prepareOpenAIInputTokensCountRequest(
	body []byte,
	account *Account,
	defaultMappedModel string,
) (*openAIInputTokensCountPrepared, error) {
	var anthropicReq protocolanthropic.AnthropicRequest
	if err := json.Unmarshal(body, &anthropicReq); err != nil {
		return nil, fmt.Errorf("parse anthropic count_tokens request: %w", err)
	}

	originalModel := anthropicReq.Model
	applyOpenAICompatModelNormalization(&anthropicReq)
	normalizedModel := anthropicReq.Model
	billingModel := resolveOpenAIForwardModel(account, normalizedModel, strings.TrimSpace(defaultMappedModel))
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)

	responsesReq, err := apicompat.AnthropicToResponses(&anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("convert anthropic request to responses: %w", err)
	}

	return &openAIInputTokensCountPrepared{
		Request: openAIInputTokensCountRequest{
			Model:        upstreamModel,
			Instructions: responsesReq.Instructions,
			Input:        responsesReq.Input,
			Tools:        responsesReq.Tools,
			ToolChoice:   responsesReq.ToolChoice,
		},
		OriginalModel:   originalModel,
		NormalizedModel: normalizedModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
	}, nil
}

func (s *OpenAIGatewayService) buildInputTokensUpstreamRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	token string,
) (*http.Request, error) {
	targetURL := openaiPlatformAPIInputTokensURL
	if account.Type == AccountTypeAPIKey {
		if baseURL := account.GetOpenAIBaseURL(); strings.TrimSpace(baseURL) != "" {
			validatedURL, err := s.validateUpstreamBaseURL(baseURL)
			if err != nil {
				return nil, err
			}
			targetURL = buildOpenAIResponsesInputTokensURL(validatedURL)
		}
	}

	options := s.nativeResponsesRequestOptions(ctx, c, account, token, targetURL, false)
	options.ForwardHeaders = func() http.Header {
		if c == nil || c.Request == nil {
			return nil
		}
		return c.Request.Header
	}
	return nativeopenai.BuildInputTokensRequest(ctx, body, options, account.GetOpenAIUserAgent)
}

func writeAnthropicCountTokensError(c *gin.Context, status int, errType, message string) {
	c.JSON(status, gin.H{
		"type": "error",
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

func isOpenAIInputTokensUnsupported(statusCode int, body []byte) bool {
	if statusCode != http.StatusNotFound {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	return strings.Contains(msg, "input_tokens") && strings.Contains(msg, "not found")
}

func writeOpenAIOAuthInputTokensFallback(c *gin.Context, account *Account, prepared *openAIInputTokensCountPrepared, statusCode int) {
	estimated := openAIInputTokensFallbackMinimum
	if got, err := estimateOpenAIInputTokens(prepared.Request); err == nil {
		if got > 0 {
			estimated = got
		}
		logger.L().Info("openai count_tokens: oauth fallback to local tiktoken estimate",
			zap.Int64("account_id", account.ID),
			zap.Int("upstream_status", statusCode),
			zap.Int("estimated_input_tokens", estimated),
			zap.String("upstream_model", prepared.UpstreamModel),
		)
	} else {
		logger.L().Warn("openai count_tokens: oauth local tiktoken fallback failed, using minimum estimate",
			zap.Int64("account_id", account.ID),
			zap.Int("upstream_status", statusCode),
			zap.Int("estimated_input_tokens", estimated),
			zap.String("upstream_model", prepared.UpstreamModel),
			zap.Error(err),
		)
	}

	c.JSON(http.StatusOK, gin.H{
		"input_tokens": estimated,
	})
}

func isOpenAIOAuthInputTokensUnsupported(statusCode int, body []byte) bool {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
	default:
		return false
	}

	bodyLower := strings.ToLower(string(body))
	msg := strings.ToLower(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	code := strings.ToLower(strings.TrimSpace(extractUpstreamErrorCode(body)))

	if code == "missing_scope" ||
		strings.Contains(bodyLower, "api.responses.write") ||
		strings.Contains(bodyLower, "missing scopes") ||
		strings.Contains(bodyLower, "insufficient_scope") {
		return true
	}

	if statusCode == http.StatusNotFound && isOpenAIInputTokensUnsupported(statusCode, body) {
		return true
	}

	// 上游代理可能在请求到达 API 前拦截 OAuth 平台端点，并返回没有结构化错误的 HTML 403 页面。
	// 该响应属于端点不可用，count_tokens 应回退本地估算且不能影响账号健康状态。
	if statusCode == http.StatusForbidden && isHTMLResponse(body) {
		return true
	}

	return strings.Contains(msg, "input_tokens") &&
		(strings.Contains(msg, "not found") ||
			strings.Contains(msg, "not supported") ||
			strings.Contains(msg, "unsupported"))
}

func isHTMLResponse(body []byte) bool {
	trimmed := strings.TrimSpace(strings.ToLower(string(body)))
	return strings.HasPrefix(trimmed, "<!doctype html") ||
		strings.HasPrefix(trimmed, "<html")
}

func estimateOpenAIInputTokens(req openAIInputTokensCountRequest) (int, error) {
	return tokenestimate.Responses(req)
}
