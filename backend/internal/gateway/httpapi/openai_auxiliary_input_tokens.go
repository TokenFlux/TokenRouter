package httpapi

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tokenestimate"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ForwardResponsesInputTokens 转发 OpenAI 原生 POST /responses/input_tokens。
// 不支持该预检端点的账号使用本地估算，避免把已知不兼容请求发送到上游。
func (s *OpenAIAuxiliary) ForwardResponsesInputTokens(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
) error {
	if account == nil {
		writeOpenAIResponsesInputTokensError(c, http.StatusServiceUnavailable, "api_error", "No available OpenAI accounts")
		return fmt.Errorf("responses input_tokens: missing account")
	}

	prepared, err := gatewayprovider.PrepareNativeInputTokens(body, account)
	if err != nil {
		writeOpenAIResponsesInputTokensError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return err
	}

	if gatewayprovider.EstimateInputTokensLocally(account) {
		writeOpenAIResponsesInputTokensFallback(c, account, prepared, 0, "local_account")
		return nil
	}

	token, _, err := s.Requests.Credentials.Resolve(ctx, gatewayprovider.ExecutionRecord(account))
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
	if account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}
	if s.Requests.Transport == nil {
		writeOpenAIResponsesInputTokensError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
		return fmt.Errorf("responses input_tokens: upstream client is unavailable")
	}
	return openai.CountNativeInputTokens(upstreamReq, openai.NativeInputTokensOptions{
		Enter: s.Enter,
		Do: func(req *http.Request) (*http.Response, error) {
			return s.Requests.Transport.Do(req, proxyURL, account.Record.ID, account.Record.Concurrency)
		},
		TransportError: func(err error) error {
			safeErr := logredact.SanitizeUpstreamQueries(err.Error())
			SetOpsUpstreamError(c, 0, safeErr, "")
			writeOpenAIResponsesInputTokensError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			return fmt.Errorf("responses input_tokens: upstream request failed: %s", safeErr)
		},
		ReadBody: s.readResponsesInputTokensBody,
		HTTPError: func(resp *http.Response, respBody []byte) error {
			if resp.StatusCode == http.StatusNotFound || (account.Record.Type == capability.AccountTypeOAuth && isOpenAIOAuthInputTokensUnsupported(resp.StatusCode, respBody)) {
				writeOpenAIResponsesInputTokensFallback(c, account, prepared, resp.StatusCode, "upstream_unsupported")
				return nil
			}
			return s.handleResponsesInputTokensUpstreamError(ctx, c, account, prepared, resp, respBody)
		},
		WriteError: func(status int, kind, message string) { writeOpenAIResponsesInputTokensError(c, status, kind, message) },
	}, ResponseSink{Writer: c.Writer})
}

func writeOpenAIResponsesInputTokensFallback(c *gin.Context, account *gatewayprovider.ExecutionAccount, prepared *gatewayprovider.InputTokensPrepared, statusCode int, reason string) {
	estimated := openAIInputTokensFallbackMinimum
	if prepared != nil {
		if got, err := tokenestimate.Responses(prepared.Request); err == nil && got > 0 {
			estimated = got
		}
	}
	accountID := int64(0)
	model := ""
	if account != nil {
		accountID = account.Record.ID
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

func (s *OpenAIAuxiliary) readResponsesInputTokensBody(resp *http.Response) ([]byte, error) {
	body := s.Output.ReadErrorBody(resp)
	if len(body) == 0 {
		return nil, fmt.Errorf("responses input_tokens: empty upstream response")
	}
	return body, nil
}

func (s *OpenAIAuxiliary) handleResponsesInputTokensUpstreamError(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	prepared *gatewayprovider.InputTokensPrepared,
	resp *http.Response,
	body []byte,
) error {
	upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(body)))
	var decision accountcore.UpstreamErrorDecision
	if account.Record.Platform == capability.PlatformGrok {
		decision = gatewayprovider.ApplyGrokExecutionHealth(ctx, s.Output.GrokHealth, account, resp.StatusCode, resp.Header, body, "", prepared.UpstreamModel)
	} else {
		decision = gatewayprovider.ApplyOpenAIResponseHealth(ctx, s.Output.Health, account, resp.StatusCode, resp.Header, body, false, prepared.UpstreamModel)
	}
	if decision.ShouldReturnGenericError() {
		writeOpenAIResponsesInputTokensError(c, http.StatusInternalServerError, "upstream_error", "Upstream gateway error")
		return fmt.Errorf("responses input_tokens: upstream error %d (custom policy)", resp.StatusCode)
	}
	defaultFailover := gatewayprovider.ShouldFailoverOpenAIResponse(resp.StatusCode, upstreamMsg, body)
	if account.Record.Platform == capability.PlatformGrok {
		defaultFailover = gatewayprovider.ShouldFailoverGrokResponse(resp.StatusCode, body)
	}
	if decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode, defaultFailover) {
		return &forwardcore.UpstreamFailoverError{
			StatusCode:             resp.StatusCode,
			ResponseBody:           body,
			ResponseHeaders:        resp.Header.Clone(),
			RetryableOnSameAccount: decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode),
		}
	}
	SetOpsUpstreamError(c, resp.StatusCode, upstreamMsg, "")
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
