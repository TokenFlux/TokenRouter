package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tokenestimate"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ForwardResponsesInputTokens 转发 OpenAI 原生 POST /responses/input_tokens。
// 不支持该预检端点的账号使用本地估算，避免把已知不兼容请求发送到上游。
func (s *OpenAIGatewayService) ForwardResponsesInputTokens(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
) error {
	if account == nil {
		writeOpenAIResponsesInputTokensError(c, http.StatusServiceUnavailable, "api_error", "No available OpenAI accounts")
		return fmt.Errorf("responses input_tokens: missing account")
	}

	prepared, err := prepareNativeOpenAIInputTokensCountRequest(body, account)
	if err != nil {
		writeOpenAIResponsesInputTokensError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return err
	}

	if shouldEstimateOpenAIInputTokensLocally(account) {
		writeOpenAIResponsesInputTokensFallback(c, account, prepared, 0, "local_account")
		return nil
	}

	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		writeOpenAIResponsesInputTokensError(c, http.StatusBadGateway, "upstream_error", "Failed to get access token")
		return fmt.Errorf("responses input_tokens: get access token: %w", err)
	}

	upstreamBody, err := wirejson.Marshal(prepared.Request)
	if err != nil {
		writeOpenAIResponsesInputTokensError(c, http.StatusInternalServerError, "api_error", "Failed to build request")
		return fmt.Errorf("responses input_tokens: marshal request: %w", err)
	}
	upstreamReq, err := s.buildInputTokensUpstreamRequest(ctx, c, account, upstreamBody, token)
	if err != nil {
		writeOpenAIResponsesInputTokensError(c, http.StatusInternalServerError, "api_error", "Failed to build request")
		return fmt.Errorf("responses input_tokens: build request: %w", err)
	}
	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	if s.httpUpstream == nil {
		writeOpenAIResponsesInputTokensError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
		return fmt.Errorf("responses input_tokens: upstream client is unavailable")
	}
	return openai.CountNativeInputTokens(upstreamReq, openai.NativeInputTokensOptions{
		Enter: s.nativeAttemptActivity,
		Do: func(req *http.Request) (*http.Response, error) {
			return s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
		},
		TransportError: func(err error) error {
			safeErr := logredact.SanitizeUpstreamQueries(err.Error())
			gatewayhttp.SetOpsUpstreamError(c, 0, safeErr, "")
			writeOpenAIResponsesInputTokensError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			return fmt.Errorf("responses input_tokens: upstream request failed: %s", safeErr)
		},
		ReadBody: s.readResponsesInputTokensBody,
		HTTPError: func(resp *http.Response, respBody []byte) error {
			if resp.StatusCode == http.StatusNotFound || (account.Type == capability.AccountTypeOAuth && isOpenAIOAuthInputTokensUnsupported(resp.StatusCode, respBody)) {
				writeOpenAIResponsesInputTokensFallback(c, account, prepared, resp.StatusCode, "upstream_unsupported")
				return nil
			}
			return s.handleResponsesInputTokensUpstreamError(ctx, c, account, prepared, resp, respBody)
		},
		WriteError: func(status int, kind, message string) { writeOpenAIResponsesInputTokensError(c, status, kind, message) },
	}, gatewayhttp.ResponseSink{Writer: c.Writer})
}

func prepareNativeOpenAIInputTokensCountRequest(body []byte, account *Account) (*openAIInputTokensCountPrepared, error) {
	var req tokenestimate.Request
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("parse responses input_tokens request: %w", err)
	}
	originalModel := strings.TrimSpace(req.Model)
	if originalModel == "" {
		return nil, fmt.Errorf("parse responses input_tokens request: model is required")
	}
	billingModel := resolveOpenAIForwardModel(account, originalModel, "")
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	req.Model = upstreamModel
	return &openAIInputTokensCountPrepared{
		Request:         req,
		OriginalModel:   originalModel,
		NormalizedModel: originalModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
	}, nil
}

func shouldEstimateOpenAIInputTokensLocally(account *Account) bool {
	if account == nil || account.IsGrok() || account.IsCNProvider() || account.Type == capability.AccountTypeUpstream {
		return true
	}
	if account.Type != capability.AccountTypeAPIKey {
		return false
	}
	baseURL := strings.TrimSpace(account.GetCredential("base_url"))
	if baseURL == "" {
		return false
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return true
	}
	return !strings.EqualFold(parsed.Hostname(), "api.openai.com")
}

func writeOpenAIResponsesInputTokensFallback(c *gin.Context, account *Account, prepared *openAIInputTokensCountPrepared, statusCode int, reason string) {
	estimated := openAIInputTokensFallbackMinimum
	if prepared != nil {
		if got, err := tokenestimate.Responses(prepared.Request); err == nil && got > 0 {
			estimated = got
		}
	}
	accountID := int64(0)
	model := ""
	if account != nil {
		accountID = account.ID
	}
	if prepared != nil {
		model = prepared.UpstreamModel
	}
	logging.L().Info("openai responses input_tokens: local estimate fallback",
		zap.Int64("account_id", accountID),
		zap.Int("upstream_status", statusCode),
		zap.Int("estimated_input_tokens", estimated),
		zap.String("upstream_model", model),
		zap.String("reason", reason),
	)
	c.JSON(http.StatusOK, gin.H{
		"object":       "response.input_tokens",
		"input_tokens": estimated,
	})
}

func writeOpenAIResponsesInputTokensError(c *gin.Context, status int, errType, message string) {
	c.JSON(status, gin.H{"error": gin.H{"type": errType, "message": message}})
}

func (s *OpenAIGatewayService) readResponsesInputTokensBody(resp *http.Response) ([]byte, error) {
	body := s.readUpstreamErrorBody(resp)
	if len(body) == 0 {
		return nil, fmt.Errorf("responses input_tokens: empty upstream response")
	}
	return body, nil
}

func (s *OpenAIGatewayService) handleResponsesInputTokensUpstreamError(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	prepared *openAIInputTokensCountPrepared,
	resp *http.Response,
	body []byte,
) error {
	upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(body)))
	var decision UpstreamErrorDecision
	if account.Platform == capability.PlatformGrok {
		decision = s.applyGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, prepared.UpstreamModel)
	} else {
		decision = s.applyOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, prepared.UpstreamModel)
	}
	if decision.ShouldReturnGenericError() {
		writeOpenAIResponsesInputTokensError(c, http.StatusInternalServerError, "upstream_error", "Upstream gateway error")
		return fmt.Errorf("responses input_tokens: upstream error %d (custom policy)", resp.StatusCode)
	}
	defaultFailover := s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, body)
	if account.Platform == capability.PlatformGrok {
		defaultFailover = s.shouldFailoverGrokUpstreamError(resp.StatusCode, body)
	}
	if decision.ShouldFailover(account, resp.StatusCode, defaultFailover) {
		return &forwardcore.UpstreamFailoverError{
			StatusCode:             resp.StatusCode,
			ResponseBody:           body,
			ResponseHeaders:        resp.Header.Clone(),
			RetryableOnSameAccount: decision.RetryableOnSameAccount(account, resp.StatusCode),
		}
	}
	gatewayhttp.SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, "")
	message := "Upstream request failed"
	if resp.StatusCode == http.StatusTooManyRequests {
		message = "Rate limit exceeded"
	} else if resp.StatusCode >= 500 {
		message = "Upstream service temporarily unavailable"
	}
	writeOpenAIResponsesInputTokensError(c, resp.StatusCode, "upstream_error", message)
	if upstreamMsg == "" {
		return fmt.Errorf("responses input_tokens: upstream error %d", resp.StatusCode)
	}
	return fmt.Errorf("responses input_tokens: upstream error %d message=%s", resp.StatusCode, upstreamMsg)
}
