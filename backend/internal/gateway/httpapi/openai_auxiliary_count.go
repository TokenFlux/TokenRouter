package httpapi

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"context"
	"fmt"
	"net/http"
	"strings"

	protocolforward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tokenestimate"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const openAIInputTokensFallbackMinimum = 1

// 兼容旧上游计数准备结构，算法由网关估算模块唯一持有。

// ForwardCountTokensAsAnthropic 将 Anthropic /v1/messages/count_tokens 桥接到
// OpenAI POST /v1/responses/input_tokens，并返回 Anthropic 兼容结果。
func (s *OpenAIAuxiliary) ForwardCountTokensAsAnthropic(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	defaultMappedModel string,
) error {
	if account == nil {
		writeAnthropicCountTokensError(c, http.StatusServiceUnavailable, "api_error", "No available OpenAI accounts")
		return fmt.Errorf("count_tokens: missing account")
	}

	// 三家国产供应商的兼容层都没有可依赖的 count_tokens 端点；无论账号使用
	// Chat、Anthropic 还是 Responses 上游协议，都只做本地估算且不改变账号状态。
	if account.View().IsCNProvider() {
		estimated, err := tokenestimate.Anthropic(body)
		if err != nil {
			writeAnthropicCountTokensError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
			return fmt.Errorf("count_tokens: estimate cn provider input tokens: %w", err)
		}
		logging.L().Debug("openai count_tokens: cn provider local estimate",
			zap.Int64("account_id", account.Record.ID),
			zap.Int("estimated_input_tokens", estimated),
		)
		c.JSON(http.StatusOK, gin.H{
			"input_tokens": estimated,
		})
		return nil
	}

	prepared, err := gatewayprovider.PrepareAnthropicInputTokens(body, account, defaultMappedModel)
	if err != nil {
		writeAnthropicCountTokensError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return err
	}

	upstreamBody, err := wirejson.Marshal(prepared.Request)
	if err != nil {
		writeAnthropicCountTokensError(c, http.StatusInternalServerError, "api_error", "Failed to build request")
		return fmt.Errorf("marshal openai input_tokens body: %w", err)
	}

	logging.L().Debug("openai count_tokens: model mapping applied",
		zap.Int64("account_id", account.Record.ID),
		zap.String("original_model", prepared.OriginalModel),
		zap.String("normalized_model", prepared.NormalizedModel),
		zap.String("billing_model", prepared.BillingModel),
		zap.String("upstream_model", prepared.UpstreamModel),
	)

	token, _, err := s.Requests.Credentials.Resolve(ctx, gatewayprovider.ExecutionRecord(account))
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
	if account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}
	return openai.CountInputTokens(upstreamReq, openai.InputTokensOptions{
		Enter: s.Enter,
		Do: func(req *http.Request) (*http.Response, error) {
			return s.Requests.Transport.Do(req, proxyURL, account.Record.ID, account.Record.Concurrency)
		},
		TransportError: func(err error) error {

			safeErr := logredact.SanitizeUpstreamQueries(err.Error())
			SetOpsUpstreamError(c, 0, safeErr, "")
			AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
				Platform:           account.Record.Platform,
				AccountID:          account.Record.ID,
				AccountName:        account.Record.Name,
				UpstreamStatusCode: 0,
				Kind:               "request_error",
				Message:            safeErr,
			})
			writeAnthropicCountTokensError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			return fmt.Errorf("openai input_tokens upstream request failed: %s", safeErr)

		},
		HTTPError: func(resp *http.Response, respBody []byte) error {

			upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
			if account.Record.Type == capability.AccountTypeOAuth && isOpenAIOAuthInputTokensUnsupported(resp.StatusCode, respBody) {
				writeOpenAIOAuthInputTokensFallback(c, account, prepared, resp.StatusCode)
				return nil
			}
			if isOpenAIInputTokensUnsupported(resp.StatusCode, respBody) {
				writeAnthropicCountTokensError(c, http.StatusNotFound, "not_found_error", "Token counting is not supported by upstream")
				return nil
			}
			var decision accountcore.UpstreamErrorDecision
			if account.Record.Platform == capability.PlatformGrok {
				decision = gatewayprovider.ApplyGrokExecutionHealth(ctx, s.Output.GrokHealth, account, resp.StatusCode, resp.Header, respBody, "", prepared.UpstreamModel)
			} else {
				decision = gatewayprovider.ApplyOpenAIResponseHealth(ctx, s.Output.Health, account, resp.StatusCode, resp.Header, respBody, false, prepared.UpstreamModel)
			}
			if decision.ShouldReturnGenericError() {
				writeAnthropicCountTokensError(c, http.StatusInternalServerError, "upstream_error", "Upstream gateway error")
				return fmt.Errorf("input_tokens upstream error: %d (not in custom error codes)", resp.StatusCode)
			}
			defaultFailover := gatewayprovider.ShouldFailoverOpenAIResponse(resp.StatusCode, upstreamMsg, respBody)
			if account.Record.Platform == capability.PlatformGrok {
				defaultFailover = gatewayprovider.ShouldFailoverGrokResponse(resp.StatusCode, respBody)
			}
			if decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode, defaultFailover) {
				return &protocolforward.UpstreamFailoverError{
					StatusCode:             resp.StatusCode,
					ResponseBody:           respBody,
					ResponseHeaders:        resp.Header.Clone(),
					RetryableOnSameAccount: decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode),
				}
			}

			upstreamDetail := ""
			if s.Output.Options.LogUpstreamErrorBody {
				maxBytes := s.Output.Options.LogUpstreamErrorBodyMaxBytes
				if maxBytes <= 0 {
					maxBytes = 2048
				}
				upstreamDetail = logredact.TruncateUTF8(string(respBody), maxBytes)
			}
			SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
			AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
				Platform:           account.Record.Platform,
				AccountID:          account.Record.ID,
				AccountName:        account.Record.Name,
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
	}, ResponseSink{Writer: c.Writer})
}

func (s *OpenAIAuxiliary) buildInputTokensUpstreamRequest(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	token string,
) (*http.Request, error) {
	targetURL := openaiPlatformAPIInputTokensURL
	if account.Record.Type == capability.AccountTypeAPIKey {
		if baseURL := gatewayprovider.ExecutionProtocolTarget(account).GetOpenAIBaseURL(); strings.TrimSpace(baseURL) != "" {
			validatedURL, err := s.Requests.ValidateBaseURL(baseURL)
			if err != nil {
				return nil, err
			}
			targetURL = httpclient.BuildOpenAIResponsesInputTokensURL(validatedURL)
		}
	}

	options := s.Requests.ResponseOptions(ctx, c, account, token, targetURL, false)
	options.ForwardHeaders = func() http.Header {
		if c == nil || c.Request == nil {
			return nil
		}
		return c.Request.Header
	}
	return openai.BuildInputTokensRequest(ctx, body, options, account.View().GetOpenAIUserAgent)
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
	msg := strings.ToLower(strings.TrimSpace(upstream.ExtractErrorMessage(body)))
	return strings.Contains(msg, "input_tokens") && strings.Contains(msg, "not found")
}

func writeOpenAIOAuthInputTokensFallback(c *gin.Context, account *gatewayprovider.ExecutionAccount, prepared *gatewayprovider.InputTokensPrepared, statusCode int) {
	estimated := openAIInputTokensFallbackMinimum
	if got, err := tokenestimate.Responses(prepared.Request); err == nil {
		if got > 0 {
			estimated = got
		}
		logging.L().Info("openai count_tokens: oauth fallback to local tiktoken estimate",
			zap.Int64("account_id", account.Record.ID),
			zap.Int("upstream_status", statusCode),
			zap.Int("estimated_input_tokens", estimated),
			zap.String("upstream_model", prepared.UpstreamModel),
		)
	} else {
		logging.L().Warn("openai count_tokens: oauth local tiktoken fallback failed, using minimum estimate",
			zap.Int64("account_id", account.Record.ID),
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
	msg := strings.ToLower(strings.TrimSpace(upstream.ExtractErrorMessage(body)))
	code := strings.ToLower(strings.TrimSpace(upstream.ExtractErrorCode(body)))

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
	if statusCode == http.StatusForbidden && upstream.IsHTMLResponse(body) {
		return true
	}

	return strings.Contains(msg, "input_tokens") &&
		(strings.Contains(msg, "not found") ||
			strings.Contains(msg, "not supported") ||
			strings.Contains(msg, "unsupported"))
}
