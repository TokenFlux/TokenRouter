package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	s09openai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

func (s *OpenAIGatewayService) ForwardEmbeddings(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()

	originalModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if originalModel == "" {
		writeOpenAIEmbeddingsError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}

	billingModel := resolveOpenAIForwardModel(account, originalModel, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	SetOpsUpstreamModel(c, upstreamModel)
	upstreamBody := body
	if upstreamModel != originalModel {
		upstreamBody = ReplaceModelInBody(body, upstreamModel)
	}

	logger.L().Debug("openai embeddings: forwarding",
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
	)

	apiKey := strings.TrimSpace(account.GetOpenAIProtocolAPIKey())
	if apiKey == "" {
		return nil, fmt.Errorf("account %d missing api_key", account.ID)
	}
	// 协议感知：Anthropic 协议账号的凭证 base_url 指向 /anthropic 端点，
	// embeddings 需使用 OpenAI 格式 base。
	baseURL := account.GetOpenAIFormatBaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	validatedURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base_url: %w", err)
	}
	targetURL := buildOpenAIEmbeddingsURL(validatedURL)

	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	forwardHeaders := make(http.Header)
	for key, values := range c.Request.Header {
		if openaiCCRawAllowedHeaders[strings.ToLower(key)] {
			forwardHeaders[key] = append([]string(nil), values...)
		}
	}
	target := &nativeopenai.EmbeddingsTarget{

		AccountID: account.ID,

		Model: upstreamModel,

		URL: targetURL,

		Token: apiKey,

		ForwardHeaders: forwardHeaders,

		UserAgent: account.GetOpenAIUserAgent(),

		ApplyHeaders: account.ApplyHeaderOverrides,

		RequestContext: detachUpstreamContext,

		Enter: s.nativeAttemptActivity,

		StartedAt: startTime,

		Do: func(request *http.Request) (*http.Response, error) {
			return s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
		},

		TransportError: func(err error) error {
			safeErr := sanitizeUpstreamErrorMessage(err.Error())
			setOpsUpstreamError(c, 0, safeErr, "")
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{

				Platform: account.Platform,

				AccountID: account.ID,

				AccountName: account.Name,

				UpstreamStatusCode: 0,

				Kind: "request_error",

				Message: safeErr,
			})
			writeOpenAIEmbeddingsError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			return fmt.Errorf("upstream request failed: %s", safeErr)
		},

		ReadErrorBody: s.readUpstreamErrorBody,

		HTTPError: func(resp *http.Response, respBody []byte) error {

			upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
			upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
			if isOpenAIClientInvalidRequestError(resp.StatusCode, upstreamMsg, respBody) {
				writeOpenAIEmbeddingsUpstreamResponse(c, resp, respBody, s.responseHeaderFilter)
				return fmt.Errorf("upstream invalid request: %d", resp.StatusCode)
			}
			var decision UpstreamErrorDecision
			if account.Platform == PlatformGrok {
				decision = s.applyGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, upstreamModel)
			} else {
				decision = s.applyOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, upstreamModel)
			}
			if decision.ShouldReturnGenericError() {
				writeOpenAIEmbeddingsError(c, http.StatusInternalServerError, "upstream_error", "Upstream gateway error")
				return fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
			}
			defaultFailover := s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody)
			if account.Platform == PlatformGrok {
				defaultFailover = s.shouldFailoverGrokUpstreamError(resp.StatusCode, respBody)
			}
			if decision.ShouldFailover(account, resp.StatusCode, defaultFailover) {
				upstreamDetail := ""
				if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
					maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
					if maxBytes <= 0 {
						maxBytes = 2048
					}
					upstreamDetail = truncateString(string(respBody), maxBytes)
				}
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{

					Platform: account.Platform,

					AccountID: account.ID,

					AccountName: account.Name,

					UpstreamStatusCode: resp.StatusCode,

					UpstreamRequestID: resp.Header.Get("x-request-id"),

					Kind: "failover",

					Message: upstreamMsg,

					Detail: upstreamDetail,
				})
				shouldDisable := s.handleOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, upstreamModel)
				retryableOnSameAccount := !shouldDisable && account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode)
				if account.IsOpenAIOAuth() && resp.StatusCode == http.StatusTooManyRequests {
					return s.newOpenAIAccountFailoverError(account, resp.StatusCode, resp.Header, respBody, upstreamMsg, shouldDisable, retryableOnSameAccount)
				}
				if isOpenAIHTTPUpstreamAccessStateError(resp.StatusCode, upstreamMsg, respBody) {
					return newOpenAIUpstreamFailoverError(resp.StatusCode, resp.Header, respBody, upstreamMsg, retryableOnSameAccount)
				}
				return &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameAccount: retryableOnSameAccount}
			}
			writeOpenAIEmbeddingsUpstreamResponse(c, resp, respBody, s.responseHeaderFilter)
			return fmt.Errorf("upstream returned status %d", resp.StatusCode)
		},

		ReadBody: func(reader io.Reader) ([]byte, error) {
			return ReadUpstreamResponseBody(reader, s.cfg, c, openAITooLargeError)
		},

		ReadFailure: func(err error) error {
			if !errors.Is(err, ErrUpstreamResponseBodyTooLarge) {
				writeOpenAIEmbeddingsError(c, http.StatusBadGateway, "api_error", "Failed to read upstream response")
			}
			return fmt.Errorf("read upstream body: %w", err)
		},

		WriteHeaders: func(output, input http.Header) {
			responseheaders.WriteFilteredHeaders(output, input, s.responseHeaderFilter)
		},
	}
	result, err := (nativeopenai.EmbeddingsExecutor{}).Execute(ctx, upstream.AttemptInput{
		Protocol:      protocol.ProtocolEmbeddings,
		Body:          upstreamBody,
		ResponseModel: originalModel,
		Target:        target,
	}, gatewayhttp.ResponseSink{Writer: c.Writer})
	if err != nil {
		return nil, err
	}
	return &OpenAIForwardResult{

		RequestID: result.RequestID,

		UpstreamHeaders: result.UpstreamHeaders,

		Usage: OpenAIUsage{
			InputTokens:              result.Usage.InputTokens,
			ImageInputTokens:         result.ImageInputTokens,
			OutputTokens:             result.Usage.OutputTokens,
			CacheReadInputTokens:     result.Usage.CacheReadInputTokens,
			CacheCreationInputTokens: result.Usage.CacheCreationInputTokens,
		},

		Model: originalModel,

		BillingModel: billingModel,

		UpstreamModel: upstreamModel,

		Stream: false,

		Duration: result.Duration,
	}, nil
}

func writeOpenAIEmbeddingsUpstreamResponse(c *gin.Context, resp *http.Response, body []byte, filter *responseheaders.CompiledHeaderFilter) {
	if c == nil || resp == nil {
		return
	}
	if c.Writer.Written() {
		return
	}
	if resp.Header != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, filter)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		c.Writer.Header().Set("Content-Type", ct)
	} else {
		c.Writer.Header().Set("Content-Type", "application/json")
	}
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = c.Writer.Write(body)
}

func writeOpenAIEmbeddingsError(c *gin.Context, statusCode int, errType, message string) {
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

func extractOpenAIEmbeddingsUsage(body []byte) OpenAIUsage {
	return s09openai.ExtractEmbeddingsUsage(body)
}

func buildOpenAIEmbeddingsURL(base string) string {
	return buildOpenAIEndpointURL(base, "/v1/embeddings")
}
