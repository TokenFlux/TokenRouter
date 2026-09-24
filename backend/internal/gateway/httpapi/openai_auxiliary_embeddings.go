package httpapi

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

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
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	mediaprovider "github.com/TokenFlux/TokenRouter/internal/gateway/media/provider"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	s09openai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

func (s *OpenAIAuxiliary) ForwardEmbeddings(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	defaultMappedModel string,
) (*forwardcore.OpenAIResult, error) {
	startTime := time.Now()

	originalModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if originalModel == "" {
		WriteEmbeddingsError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}

	billingModel := gatewayprovider.ExecutionModelPolicy(account).ForwardModel(originalModel, defaultMappedModel)
	upstreamModel := gatewayprovider.ExecutionModelPolicy(account).NormalizeOpenAI(billingModel)
	SetOpsUpstreamModel(c, upstreamModel)
	upstreamBody := body
	if upstreamModel != originalModel {
		upstreamBody = s09openai.ReplaceModelInBody(body, upstreamModel)
	}

	logging.L().Debug("openai embeddings: forwarding",
		zap.Int64("account_id", account.Record.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
	)

	apiKey := strings.TrimSpace(account.View().GetOpenAIProtocolAPIKey())
	if apiKey == "" {
		return nil, fmt.Errorf("account %d missing api_key", account.Record.ID)
	}
	// 协议感知：Anthropic 协议账号的凭证 base_url 指向 /anthropic 端点，
	// embeddings 需使用 OpenAI 格式 base。
	baseURL := gatewayprovider.ExecutionProtocolTarget(account).GetOpenAIFormatBaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	validatedURL, err := s.Requests.ValidateBaseURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base_url: %w", err)
	}
	targetURL := buildOpenAIEmbeddingsURL(validatedURL)

	proxyURL := ""
	if account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}
	forwardHeaders := make(http.Header)
	for key, values := range c.Request.Header {
		if AllowOpenAIRawChatHeader(strings.ToLower(key)) {
			forwardHeaders[key] = append([]string(nil), values...)
		}
	}
	target := &mediaprovider.EmbeddingsOptions{

		AccountID: account.Record.ID,

		Model: upstreamModel,

		URL: targetURL,

		Token: apiKey,

		ForwardHeaders: forwardHeaders,

		UserAgent: account.View().GetOpenAIUserAgent(),

		ApplyHeaders: gatewayprovider.BindExecutionHeaders(account),

		RequestContext: gatewayprovider.DetachUpstreamContext,

		Enter: s.Enter,

		StartedAt: startTime,

		Do: func(request *http.Request) (*http.Response, error) {
			return s.Requests.Transport.Do(request, proxyURL, account.Record.ID, account.Record.Concurrency)
		},

		TransportError: func(err error) error {
			safeErr := logredact.SanitizeUpstreamQueries(err.Error())
			SetOpsUpstreamError(c, 0, safeErr, "")
			AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{

				Platform: account.Record.Platform,

				AccountID: account.Record.ID,

				AccountName: account.Record.Name,

				UpstreamStatusCode: 0,

				Kind: "request_error",

				Message: safeErr,
			})
			WriteEmbeddingsError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			return fmt.Errorf("upstream request failed: %s", safeErr)
		},

		ReadErrorBody: s.Output.ReadErrorBody,

		HTTPError: func(resp *http.Response, respBody []byte) error {
			upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
			var decision accountcore.UpstreamErrorDecision
			return gatewaymedia.ResolveEmbeddingFailure(resp.StatusCode, gatewaymedia.EmbeddingFailurePorts{
				InvalidRequest: func() bool { return openai.IsOpenAIClientInvalidRequestError(resp.StatusCode, upstreamMsg, respBody) },
				ApplyPolicy: func() {
					if account.Record.Platform == capability.PlatformGrok {
						decision = gatewayprovider.ApplyGrokExecutionHealth(ctx, s.Output.GrokHealth, account, resp.StatusCode, resp.Header, respBody, "", upstreamModel)
					} else {
						decision = gatewayprovider.ApplyOpenAIResponseHealth(ctx, s.Output.Health, account, resp.StatusCode, resp.Header, respBody, false, upstreamModel)
					}
				},
				Generic: func() bool { return decision.ShouldReturnGenericError() },
				Failover: func() bool {
					defaultFailover := gatewayprovider.ShouldFailoverOpenAIResponse(resp.StatusCode, upstreamMsg, respBody)
					if account.Record.Platform == capability.PlatformGrok {
						defaultFailover = gatewayprovider.ShouldFailoverGrokResponse(resp.StatusCode, respBody)
					}
					return decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode, defaultFailover)
				},
				RecordFailover: func() {
					upstreamDetail := ""
					if s.Output.Options.LogUpstreamErrorBody {
						maxBytes := s.Output.Options.LogUpstreamErrorBodyMaxBytes
						if maxBytes <= 0 {
							maxBytes = 2048
						}
						upstreamDetail = logredact.TruncateUTF8(string(respBody), maxBytes)
					}
					AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{

						Platform: account.Record.Platform,

						AccountID: account.Record.ID,

						AccountName: account.Record.Name,

						UpstreamStatusCode: resp.StatusCode,

						UpstreamRequestID: resp.Header.Get("x-request-id"),

						Kind: "failover",

						Message: upstreamMsg,

						Detail: upstreamDetail,
					})
				},
				NewFailover: func() error {
					shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(ctx, s.Output.Health, account, resp.StatusCode, resp.Header, respBody, false, upstreamModel).StopScheduling
					retryableOnSameAccount := !shouldDisable && account.View().IsPoolMode() && account.View().IsPoolModeRetryableStatus(resp.StatusCode)
					if account.View().IsOpenAIOAuth() && resp.StatusCode == http.StatusTooManyRequests {
						return (gatewayprovider.OpenAIFailoverPolicy{Health: s.Output.Health}).NewAccountFailure(account, resp.StatusCode, resp.Header, respBody, upstreamMsg, shouldDisable, retryableOnSameAccount)
					}
					if gatewayprovider.IsOpenAIHTTPUpstreamAccessStateError(resp.StatusCode, upstreamMsg, respBody) {
						return gatewayprovider.NewOpenAIUpstreamFailure(resp.StatusCode, resp.Header, respBody, upstreamMsg, retryableOnSameAccount)
					}
					return &forwardcore.UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameAccount: retryableOnSameAccount}
				},
				Forward: func() { WriteEmbeddingsUpstreamResponse(c, resp, respBody, s.Output.Headers) },
				Write: func(response gatewaymedia.ErrorResponse) {
					WriteEmbeddingsError(c, response.Status, response.Type, response.Message)
				},
			})
		},

		ReadBody: func(reader io.Reader) ([]byte, error) {
			return ReadUpstreamResponseBody(reader, s.Output.Options.ReadLimit, c, OpenAIResponseTooLarge)
		},

		ReadFailure: func(err error) error {
			if !errors.Is(err, httpclient.ErrResponseBodyTooLarge) {
				WriteEmbeddingsError(c, http.StatusBadGateway, "api_error", "Failed to read upstream response")
			}
			return fmt.Errorf("read upstream body: %w", err)
		},

		WriteHeaders: func(output, input http.Header) {
			provider.WriteFilteredHeaders(output, input, s.Output.Headers)
		},
	}
	result, err := (mediaprovider.Embeddings{Options: *target}).Execute(ctx, upstream.AttemptInput{
		Protocol:      protocol.ProtocolEmbeddings,
		Body:          upstreamBody,
		ResponseModel: originalModel,
	}, ResponseSink{Writer: c.Writer})
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
