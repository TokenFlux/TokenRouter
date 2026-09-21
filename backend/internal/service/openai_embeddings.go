package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"

	mediaprovider "github.com/TokenFlux/TokenRouter/internal/gateway/media/provider"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	s09openai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
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
) (*forwardcore.OpenAIResult, error) {
	startTime := time.Now()

	originalModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if originalModel == "" {
		gatewayhttp.WriteEmbeddingsError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}

	billingModel := resolveOpenAIForwardModel(account, originalModel, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	gatewayhttp.SetOpsUpstreamModel(c, upstreamModel)
	upstreamBody := body
	if upstreamModel != originalModel {
		upstreamBody = s09openai.ReplaceModelInBody(body, upstreamModel)
	}

	logging.L().Debug("openai embeddings: forwarding",
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
	target := &mediaprovider.EmbeddingsOptions{

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
			safeErr := logredact.SanitizeUpstreamQueries(err.Error())
			gatewayhttp.SetOpsUpstreamError(c, 0, safeErr, "")
			gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{

				Platform: account.Platform,

				AccountID: account.ID,

				AccountName: account.Name,

				UpstreamStatusCode: 0,

				Kind: "request_error",

				Message: safeErr,
			})
			gatewayhttp.WriteEmbeddingsError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			return fmt.Errorf("upstream request failed: %s", safeErr)
		},

		ReadErrorBody: s.readUpstreamErrorBody,

		HTTPError: func(resp *http.Response, respBody []byte) error {
			upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
			var decision UpstreamErrorDecision
			return gatewaymedia.ResolveEmbeddingFailure(resp.StatusCode, gatewaymedia.EmbeddingFailurePorts{
				InvalidRequest: func() bool { return openai.IsOpenAIClientInvalidRequestError(resp.StatusCode, upstreamMsg, respBody) },
				ApplyPolicy: func() {
					if account.Platform == capability.PlatformGrok {
						decision = s.applyGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, upstreamModel)
					} else {
						decision = s.applyOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, upstreamModel)
					}
				},
				Generic: func() bool { return decision.ShouldReturnGenericError() },
				Failover: func() bool {
					defaultFailover := s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody)
					if account.Platform == capability.PlatformGrok {
						defaultFailover = s.shouldFailoverGrokUpstreamError(resp.StatusCode, respBody)
					}
					return decision.ShouldFailover(account, resp.StatusCode, defaultFailover)
				},
				RecordFailover: func() {
					upstreamDetail := ""
					if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
						maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
						if maxBytes <= 0 {
							maxBytes = 2048
						}
						upstreamDetail = logredact.TruncateUTF8(string(respBody), maxBytes)
					}
					gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{

						Platform: account.Platform,

						AccountID: account.ID,

						AccountName: account.Name,

						UpstreamStatusCode: resp.StatusCode,

						UpstreamRequestID: resp.Header.Get("x-request-id"),

						Kind: "failover",

						Message: upstreamMsg,

						Detail: upstreamDetail,
					})
				},
				NewFailover: func() error {
					shouldDisable := s.handleOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, upstreamModel)
					retryableOnSameAccount := !shouldDisable && account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode)
					if account.IsOpenAIOAuth() && resp.StatusCode == http.StatusTooManyRequests {
						return s.newOpenAIAccountFailoverError(account, resp.StatusCode, resp.Header, respBody, upstreamMsg, shouldDisable, retryableOnSameAccount)
					}
					if isOpenAIHTTPUpstreamAccessStateError(resp.StatusCode, upstreamMsg, respBody) {
						return newOpenAIUpstreamFailoverError(resp.StatusCode, resp.Header, respBody, upstreamMsg, retryableOnSameAccount)
					}
					return &forwardcore.UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameAccount: retryableOnSameAccount}
				},
				Forward: func() { gatewayhttp.WriteEmbeddingsUpstreamResponse(c, resp, respBody, s.responseHeaderFilter) },
				Write: func(response gatewaymedia.ErrorResponse) {
					gatewayhttp.WriteEmbeddingsError(c, response.Status, response.Type, response.Message)
				},
			})
		},

		ReadBody: func(reader io.Reader) ([]byte, error) {
			return ReadUpstreamResponseBody(reader, s.cfg, c, openAITooLargeError)
		},

		ReadFailure: func(err error) error {
			if !errors.Is(err, ErrUpstreamResponseBodyTooLarge) {
				gatewayhttp.WriteEmbeddingsError(c, http.StatusBadGateway, "api_error", "Failed to read upstream response")
			}
			return fmt.Errorf("read upstream body: %w", err)
		},

		WriteHeaders: func(output, input http.Header) {
			provider.WriteFilteredHeaders(output, input, s.responseHeaderFilter)
		},
	}
	result, err := (mediaprovider.Embeddings{Options: *target}).Execute(ctx, upstream.AttemptInput{
		Protocol:      protocol.ProtocolEmbeddings,
		Body:          upstreamBody,
		ResponseModel: originalModel,
	}, gatewayhttp.ResponseSink{Writer: c.Writer})
	if err != nil {
		return nil, err
	}
	return &forwardcore.OpenAIResult{

		RequestID: result.RequestID,

		UpstreamHeaders: result.UpstreamHeaders,

		Usage: s09openai.ForwardUsage{
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

func buildOpenAIEmbeddingsURL(base string) string {
	return httpclient.BuildOpenAIEndpointURL(base, "/v1/embeddings")
}
